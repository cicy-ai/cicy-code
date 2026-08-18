package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("CICY_CLOUD_DISABLE_WS", "1")
	os.Exit(m.Run())
}

type recordingMessageAcker struct{ ids []string }

type lockedLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (a *recordingMessageAcker) Ack(id string) error {
	a.ids = append(a.ids, id)
	return nil
}

func TestCiCyCloudIdlePollBackoffAndReset(t *testing.T) {
	tr := &cicyCloudTransport{}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second}
	for i, expected := range want {
		if got := tr.nextIdlePollDelay(); got != expected {
			t.Fatalf("backoff %d = %s, want %s", i, got, expected)
		}
	}
	tr.resetIdlePollDelay()
	if got := tr.nextIdlePollDelay(); got != 2*time.Second {
		t.Fatalf("backoff after traffic = %s, want 2s", got)
	}
}

func TestCiCyCloudStreamReconnectBackoff(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	var delay time.Duration
	for i, expected := range want {
		delay = nextCiCyCloudStreamBackoff(delay)
		if delay != expected {
			t.Fatalf("backoff %d = %s, want %s", i, delay, expected)
		}
	}
}

func TestCiCyCloudWebSocketCarriesMessageAndAck(t *testing.T) {
	t.Setenv("CICY_CLOUD_DISABLE_WS", "0")
	withTempCicyRoot(t)
	withTestStore(t)
	if _, err := store.Exec(`INSERT INTO agent_config
		(pane_id,title,agent_type,role,default_model,use_custom_gateway,active)
		VALUES ('w-local:main.0','Local Agent','codex','worker','gpt-local',1,1)`); err != nil {
		t.Fatal(err)
	}
	connected := make(chan *websocket.Conn, 4)
	acked := make(chan string, 1)
	directories := make(chan M, 4)
	states := make(chan M, 8)
	var patchRequests atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/code/ws-ticket":
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
			_ = json.NewEncoder(w).Encode(M{"ticket": "signed", "wsUrl": wsURL})
		case "/ws":
			if r.URL.Query().Get("ticket") != "signed" {
				t.Errorf("ticket missing")
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			connected <- conn
			_ = conn.WriteJSON(M{"type": "message", "cursor": 42, "message": M{
				"id": "msg-websocket-12345678", "senderInstanceId": "code-remote-1234567890",
				"senderAgentId": "w-remote", "targetAgentId": "w-local", "kind": "user_message", "text": "over ws"}})
			for {
				var frame M
				if conn.ReadJSON(&frame) != nil {
					return
				}
				typeName, requestID := anyString(frame["type"]), anyString(frame["requestId"])
				if ids, ok := frame["ids"].([]interface{}); typeName == "ack" && ok && len(ids) == 1 {
					acked <- anyString(ids[0])
					_ = conn.WriteJSON(M{"type": "acked", "requestId": requestID})
				} else if typeName == "send" {
					_ = conn.WriteJSON(M{"type": "sent", "requestId": requestID, "id": "msg-ws-send-12345678", "cursor": 43})
				} else if typeName == "agent_state_publish" {
					states <- frame
					_ = conn.WriteJSON(M{"type": "agent_state_published", "requestId": requestID, "revision": 1})
				}
			}
		case "/api/code/agents":
			if r.Method == http.MethodPatch {
				patchRequests.Add(1)
				http.Error(w, "PATCH forbidden", http.StatusMethodNotAllowed)
				return
			}
			if r.Method != http.MethodPost {
				t.Errorf("unexpected agent directory method %s", r.Method)
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			var body M
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("directory decode: %v", err)
			}
			directories <- body
			_ = json.NewEncoder(w).Encode(M{"success": true})
		case "/api/code/messages/poll":
			_ = json.NewEncoder(w).Encode(M{"messages": []M{}})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)
	now := time.Now()
	tr := &cicyCloudTransport{token: "test", lastHeartbeat: now, lastPresence: now}
	defer tr.Close()
	tr.initStream()
	var firstConn *websocket.Conn
	select {
	case firstConn = <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket did not connect")
	}
	var directory, stateFrame M
	select {
	case directory = <-directories:
	case <-time.After(3 * time.Second):
		t.Fatal("agent directory was not synchronized")
	}
	select {
	case stateFrame = <-states:
	case <-time.After(3 * time.Second):
		t.Fatal("agent state was not published over websocket")
	}
	encodedDirectory, _ := json.Marshal(directory)
	for _, forbidden := range []string{"status", "model", "contextUsedPct", "context_used_pct", "cost", "online", "latestQuestion", "latestResponse"} {
		if bytes.Contains(encodedDirectory, []byte(`"`+forbidden+`"`)) {
			t.Fatalf("runtime field %q leaked into directory HTTP body: %s", forbidden, encodedDirectory)
		}
	}
	if anyString(stateFrame["type"]) != "agent_state_publish" || stateFrame["fullSnapshot"] != true {
		t.Fatalf("unexpected state frame: %#v", stateFrame)
	}
	encodedState, _ := json.Marshal(stateFrame)
	for _, required := range []string{`"agentId":"w-local"`, `"status":"idle"`, `"model":"gpt-local"`, `"online":true`} {
		if !bytes.Contains(encodedState, []byte(required)) {
			t.Fatalf("state frame missing %s: %s", required, encodedState)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		tr.streamConnMu.Lock()
		ready := tr.streamConn != nil
		tr.streamConnMu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("websocket was not installed on transport")
		}
		time.Sleep(time.Millisecond)
	}
	msgs, _, err := tr.Poll("")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "over ws" || msgs[0].TargetPaneID != "w-local" {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	if !strings.HasSuffix(msgs[0].Peer.ContextToken, "|ws") {
		t.Fatalf("websocket source not preserved: %q", msgs[0].Peer.ContextToken)
	}
	if err := tr.Ack(msgs[0].AckID); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-acked:
		if id != msgs[0].AckID {
			t.Fatalf("ack=%q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ack not sent over websocket")
	}
	sentID, transport, err := tr.sendCloudMessageWithTransport(M{
		"targetInstanceId": "code-target-1234567890123456", "targetAgentId": "w-102", "text": "hello", "kind": "user_message",
	})
	if err != nil || sentID != "msg-ws-send-12345678" || transport != "ws" {
		t.Fatalf("send = id %q transport %q err %v", sentID, transport, err)
	}
	tr.recordCloudReply(cicyCloudStreamMessage{ID: "msg-ws-reply-12345678", ReplyTo: sentID, Text: "answer", EnqueuedAtMS: 1234})
	state, ok := tr.cloudMessageState(sentID)
	if !ok || state.Transport != "ws" || state.Reply.ID != "msg-ws-reply-12345678" || state.ReceivedMS == 0 {
		t.Fatalf("local websocket message state = %#v, ok=%v", state, ok)
	}
	go tr.reportAgentState("w-local")
	select {
	case frame := <-states:
		if anyString(frame["type"]) != "agent_state_publish" || frame["fullSnapshot"] != true {
			t.Fatalf("state transition did not publish a full websocket snapshot: %#v", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("state transition was not published over websocket")
	}
	if got := patchRequests.Load(); got != 0 {
		t.Fatalf("agent runtime state made %d HTTP PATCH requests", got)
	}
	_ = firstConn.Close()
	select {
	case <-connected:
	case <-time.After(4 * time.Second):
		t.Fatal("websocket did not reconnect")
	}
	select {
	case <-directories:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not synchronize agent directory")
	}
	select {
	case frame := <-states:
		if frame["fullSnapshot"] != true {
			t.Fatalf("reconnect state was not a full snapshot: %#v", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not publish agent state over websocket")
	}
}

func TestCiCyCloudAgentRuntimeStateIncludesBoundedConversationPreview(t *testing.T) {
	question := strings.Repeat("问", 300)
	response := strings.Repeat("答", 300)
	state := cicyCloudAgentRuntimeState("w-1004", "fallback-model", M{
		"status": "thinking", "model": "gpt-live", "context_used_pct": 42, "context_window_size": 200000,
		"cost_credit": 0.125, "complete": false, "latest_question": question, "latest_response": response,
		"latest_response_type": "text", "updated_at": "2026-08-14T13:00:00Z",
		"latest_tool": M{"name": "exec_command", "input": strings.Repeat("参数", 100)},
	})
	if state["status"] != "thinking" || state["model"] != "gpt-live" || state["contextUsedPct"] != 42 || state["contextWindowSize"] != 200000 || state["cost"] != 0.125 || state["working"] != true {
		t.Fatalf("runtime metrics = %#v", state)
	}
	if got := anyString(state["latestQuestion"]); len(got) > 256 || !strings.HasSuffix(got, "…") {
		t.Fatalf("latest question = %d bytes without bounded ellipsis", len(got))
	}
	if got := anyString(state["latestResponse"]); len(got) > 256 || !strings.HasSuffix(got, "…") {
		t.Fatalf("latest response = %d bytes without bounded ellipsis", len(got))
	}
	if state["latestResponseType"] != "text" || state["latestResponseAt"] != "2026-08-14T13:00:00Z" || state["updatedAt"] != "2026-08-14T13:00:00Z" {
		t.Fatalf("conversation metadata = %#v", state)
	}
	tool, ok := state["latestTool"].(M)
	if !ok || tool["name"] != "exec_command" || len(anyString(tool["input"])) > 96 {
		t.Fatalf("latest tool = %#v", state["latestTool"])
	}
}

func TestCiCyCloudAgentRuntimeStatePreservesMetricPresence(t *testing.T) {
	tests := []struct {
		name           string
		metrics        M
		wantContext    interface{}
		wantWindow     interface{}
		wantCost       interface{}
		wantModel      string
		wantMetricKeys bool
	}{
		{name: "no metrics", metrics: nil, wantModel: "configured-model"},
		{name: "missing fields", metrics: M{}, wantModel: "configured-model"},
		{name: "nil fields", metrics: M{"context_used_pct": nil, "context_window_size": nil, "cost_credit": nil}, wantModel: "configured-model"},
		{name: "wrong field types", metrics: M{"context_used_pct": "0", "context_window_size": true, "cost_credit": "0"}, wantModel: "configured-model"},
		{name: "explicit numeric zero", metrics: M{"context_used_pct": 0, "context_window_size": 0, "cost_credit": float64(0)}, wantContext: 0, wantCost: float64(0), wantModel: "configured-model", wantMetricKeys: true},
		{name: "json numbers", metrics: M{"context_used_pct": json.Number("42"), "context_window_size": json.Number("200000"), "cost_credit": json.Number("0.004")}, wantContext: 42, wantWindow: 200000, wantCost: float64(0.004), wantModel: "configured-model", wantMetricKeys: true},
		{name: "runtime model wins", metrics: M{"model": "runtime-model"}, wantModel: "runtime-model"},
		{name: "empty runtime model falls back", metrics: M{"model": "  "}, wantModel: "configured-model"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cicyCloudAgentRuntimeState("w-1004", "configured-model", test.metrics)
			if state["model"] != test.wantModel {
				t.Fatalf("model = %#v, want %q", state["model"], test.wantModel)
			}
			context, hasContext := state["contextUsedPct"]
			window, hasWindow := state["contextWindowSize"]
			cost, hasCost := state["cost"]
			if context != test.wantContext || window != test.wantWindow || cost != test.wantCost {
				t.Fatalf("metrics = context(%v, %v) window(%v, %v) cost(%v, %v)", context, hasContext, window, hasWindow, cost, hasCost)
			}
			if test.wantMetricKeys != hasContext || test.wantMetricKeys != hasCost || (test.wantWindow != nil) != hasWindow {
				t.Fatalf("metric presence = context:%v window:%v cost:%v", hasContext, hasWindow, hasCost)
			}
		})
	}
}

func TestCiCyCloudAgentRuntimeStateRejectsNonFiniteMetrics(t *testing.T) {
	state := cicyCloudAgentRuntimeState("w-1004", "configured-model", M{
		"context_used_pct": math.NaN(), "context_window_size": math.Inf(1), "cost_credit": math.Inf(-1),
	})
	for _, key := range []string{"contextUsedPct", "contextWindowSize", "cost"} {
		if _, exists := state[key]; exists {
			t.Fatalf("non-finite %s was published: %#v", key, state[key])
		}
	}
	if _, err := json.Marshal(state); err != nil {
		t.Fatalf("runtime state is not JSON-safe: %v", err)
	}
}

func TestCiCyCloudAgentRuntimeStateFitsMaximumHubSnapshot(t *testing.T) {
	metrics := M{"status": "thinking", "model": "gpt-live", "complete": false,
		"latest_question": strings.Repeat("问", 300), "latest_response": strings.Repeat("答", 300),
		"latest_response_type": "thinking", "updated_at": "2026-08-14T13:00:00.123456789Z",
		"latest_tool": M{"name": strings.Repeat("工", 80), "input": strings.Repeat("参", 120)}}
	states := make([]M, 0, 500)
	for index := 0; index < 500; index++ {
		states = append(states, cicyCloudAgentRuntimeState(fmt.Sprintf("w-%04d", index), "gpt-live", metrics))
	}
	payload, err := json.Marshal(states)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 512<<10 {
		t.Fatalf("maximum state snapshot = %d bytes, exceeds 512 KiB", len(payload))
	}
}

func TestCiCyCloudWebSocketDialLogDoesNotLeakCredentials(t *testing.T) {
	t.Setenv("CICY_CLOUD_DISABLE_WS", "0")
	const token = "token-must-never-appear"
	const ticket = "ticket-must-never-appear"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/code/ws-ticket":
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
			_ = json.NewEncoder(w).Encode(M{"ticket": ticket, "wsUrl": wsURL})
		case "/ws":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	var logs lockedLogBuffer
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	tr := &cicyCloudTransport{accountID: 17, token: token}
	defer tr.Close()
	tr.initStream()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), "websocket dial failed") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := logs.String()
	if !strings.Contains(got, "account=17 status=401") {
		t.Fatalf("missing actionable dial diagnostics: %q", got)
	}
	for _, secret := range []string{token, ticket, "/ws?ticket=", server.URL} {
		if strings.Contains(got, secret) {
			t.Fatalf("websocket diagnostics leaked %q: %q", secret, got)
		}
	}
}

func TestCiCyCloudPresenceIntervalStaysInsideOfflineWindow(t *testing.T) {
	if cicyCloudHeartbeatInterval >= 90*time.Second {
		t.Fatalf("heartbeat interval %s must stay below the 90s offline window", cicyCloudHeartbeatInterval)
	}
	if cicyCloudPresenceInterval <= cicyCloudHeartbeatInterval {
		t.Fatalf("roster interval %s must be less frequent than heartbeat %s", cicyCloudPresenceInterval, cicyCloudHeartbeatInterval)
	}
}

func TestCiCyCloudTunnelReadyImmediatelyReportsTunnelURL(t *testing.T) {
	heartbeats := make(chan M, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/code/instances/heartbeat" {
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body M
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("heartbeat decode: %v", err)
		}
		heartbeats <- body
		_ = json.NewEncoder(w).Encode(M{"success": true})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	now := time.Now()
	tr := &cicyCloudTransport{token: "test", lastHeartbeat: now, lastPresence: now}
	url := "https://ready-now.trycloudflare.com"
	tr.reportTunnelReady(url)

	select {
	case body := <-heartbeats:
		if body["tunnelUrl"] != url {
			t.Fatalf("tunnelUrl = %#v, want %q", body["tunnelUrl"], url)
		}
	case <-time.After(time.Second):
		t.Fatal("tunnel-ready heartbeat was not sent immediately")
	}
}

func TestCiCyCloudErrorRetryDelayRemainsQuotaSafe(t *testing.T) {
	// Poll sleeps this adaptive delay before the generic IM loop adds its 3s
	// error delay. At the steady-state maximum, four continuously failing
	// instances stay below the free 100k Worker requests/day ceiling.
	requestsPerDay := 4 * int((24*time.Hour)/(cicyCloudPollMaxDelay+3*time.Second))
	if requestsPerDay >= 100_000 {
		t.Fatalf("error retry load = %d requests/day, must stay below 100000", requestsPerDay)
	}
}

func TestCiCyCloudPollDoesNotFeedAgentRepliesBackToAgent(t *testing.T) {
	terminalAcks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/code/messages/poll" {
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
		if r.URL.Query().Get("ack") == "msg-reply-12345678" {
			terminalAcks++
			_ = json.NewEncoder(w).Encode(M{"messages": []M{}})
			return
		}
		_ = json.NewEncoder(w).Encode(M{"messages": []M{
			{"id": "msg-user-12345678", "senderInstanceId": "code-source-1234567890123456", "kind": "user_message", "text": "hello"},
			{"id": "msg-reply-12345678", "senderInstanceId": "code-source-1234567890123456", "kind": "agent_reply", "text": "answer"},
		}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	now := time.Now()
	tr := &cicyCloudTransport{token: "test", lastHeartbeat: now, lastPresence: now}
	msgs, cursor, err := tr.Poll("")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Fatalf("agent replies must be acked without triggering an agent: %#v", msgs)
	}
	if msgs[0].Peer.ContextToken != "|msg-user-12345678|http" {
		t.Fatalf("original message id must be retained for reply correlation, got %q", msgs[0].Peer.ContextToken)
	}
	if msgs[0].AckID != "msg-user-12345678" {
		t.Fatalf("user message must carry its post-delivery ack id, got %q", msgs[0].AckID)
	}
	if cursor != "msg-user-12345678" || terminalAcks != 1 {
		t.Fatalf("user cursor=%q terminal acks=%d, want user-only cursor and one terminal ACK", cursor, terminalAcks)
	}
}

func TestCiCyCloudAckOnlyAfterLocalAgentAcceptsMessage(t *testing.T) {
	acker := &recordingMessageAcker{}
	msg := botMsg{AckID: "msg-user-12345678"}
	if err := ackDeliveredMessage(acker, msg, false); err != nil {
		t.Fatal(err)
	}
	if len(acker.ids) != 0 {
		t.Fatalf("failed local delivery must remain unacked for retry: %#v", acker.ids)
	}
	if err := ackDeliveredMessage(acker, msg, true); err != nil {
		t.Fatal(err)
	}
	if len(acker.ids) != 1 || acker.ids[0] != msg.AckID {
		t.Fatalf("successful local delivery must ack exactly once: %#v", acker.ids)
	}
}

func TestCiCyCloudRetryDoesNotAckOrDuplicateConfirmedDelivery(t *testing.T) {
	acker := &recordingMessageAcker{}
	msg := botMsg{AckID: "msg-retry-12345678"}
	before := agentTurnStartMarker{CurrentTurn: "turn-old", ReplyTurn: "turn-old"}

	// Enter produced no new current/reply turn: the Cloud row must remain
	// unacked so it can retry later.
	if started := agentTurnStartedSince(before, before); started {
		t.Fatal("unchanged turn marker must not confirm injection")
	} else if err := ackDeliveredMessage(acker, msg, started); err != nil {
		t.Fatal(err)
	}
	if len(acker.ids) != 0 {
		t.Fatalf("unstarted turn was acked: %#v", acker.ids)
	}

	// A later clean retry observes one new turn and produces exactly one ACK.
	after := agentTurnStartMarker{CurrentTurn: "turn-new", ReplyTurn: "turn-new"}
	if started := agentTurnStartedSince(before, after); !started {
		t.Fatal("new turn marker must confirm injection")
	} else if err := ackDeliveredMessage(acker, msg, started); err != nil {
		t.Fatal(err)
	}
	if len(acker.ids) != 1 || acker.ids[0] != msg.AckID {
		t.Fatalf("confirmed retry must ack exactly once: %#v", acker.ids)
	}
}

func TestCiCyCloudExactTargetBypassesGenericInboundToggle(t *testing.T) {
	acc := &imAccount{InboundToAgent: false}
	if imInboundEnabled(acc, false) {
		t.Fatal("ordinary IM messages must still respect inbound_to_agent=false")
	}
	if !imInboundEnabled(acc, true) {
		t.Fatal("authenticated Cloud exact-target messages must not be dropped by the generic IM toggle")
	}
}

func TestCiCyCloudAckUsesExplicitMessageID(t *testing.T) {
	var ack string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ack = r.URL.Query().Get("ack")
		_ = json.NewEncoder(w).Encode(M{"messages": []M{}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	now := time.Now()
	tr := &cicyCloudTransport{token: "test", lastHeartbeat: now, lastPresence: now}
	if err := tr.Ack("msg-user-12345678"); err != nil {
		t.Fatal(err)
	}
	if ack != "msg-user-12345678" {
		t.Fatalf("wrong ack id %q", ack)
	}
}

func TestCiCyCloudSendMarksOutputAsTerminalAgentReply(t *testing.T) {
	withTestStore(t)
	if _, err := store.Exec(`INSERT INTO cicy_cloud_inbox
		(message_id,account_id,target_pane_id,text,peer_chat_id,context_token,status)
		VALUES('msg-user-12345678',1,'w-1001:main.0','hello','code-source','w-1001|msg-user-12345678','running')`); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(M{"message": M{"id": "msg-reply-12345678"}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	tr := &cicyCloudTransport{token: "test"}
	if _, err := tr.Send(botPeer{ChatID: "code-target-1234567890123456", ContextToken: "w-1001|msg-user-12345678|http"}, "answer"); err != nil {
		t.Fatal(err)
	}
	if body["kind"] != "agent_reply" || body["hopCount"] != float64(1) {
		t.Fatalf("unsafe reply envelope: %#v", body)
	}
	if body["senderAgentId"] != "w-1001" || body["replyTo"] != "msg-user-12345678" {
		t.Fatalf("reply correlation was not preserved: %#v", body)
	}
	var status string
	if err := store.QueryRow(`SELECT status FROM cicy_cloud_inbox WHERE message_id='msg-user-12345678'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "replied" {
		t.Fatalf("successful agent_reply must complete durable inbox row, got %q", status)
	}
}

func TestCiCyCloudRPCReadsStructuredDataWithoutAgentDispatch(t *testing.T) {
	withTestStore(t)
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(M{"message": M{"id": "msg-rpc-reply-12345678"}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	tr := &cicyCloudTransport{token: "test"}
	if err := tr.handleRPCRequest("msg-rpc-12345678", "code-source-1234567890123456", "w-9", "w-102", `{"op":"msgs"}`, "ws"); err != nil {
		t.Fatal(err)
	}
	if body["kind"] != "rpc_reply" || body["replyTo"] != "msg-rpc-12345678" || body["hopCount"] != float64(1) {
		t.Fatalf("invalid rpc reply envelope: %#v", body)
	}
	if body["targetInstanceId"] != "code-source-1234567890123456" || body["targetAgentId"] != "w-9" || body["senderAgentId"] != "w-102" {
		t.Fatalf("rpc correlation lost: %#v", body)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body["text"].(string)), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["ok"] != true {
		t.Fatalf("structured rpc failed: %#v", envelope)
	}
}

func TestCiCyCloudRPCCancelInterruptsExactTerminalAgent(t *testing.T) {
	withTestStore(t)
	if _, err := store.Exec(`INSERT INTO agent_config
		(pane_id,title,agent_type,active) VALUES ('w-102:main.0','Terminal Agent','codex',1)`); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	tmuxLog := filepath.Join(t.TempDir(), "tmux.log")
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CICY_TEST_TMUX_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICY_TEST_TMUX_LOG", tmuxLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(M{"message": M{"id": "msg-rpc-cancel-reply-12345678"}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	tr := &cicyCloudTransport{token: "test"}
	if err := tr.handleRPCRequest("msg-rpc-cancel-12345678", "code-source-1234567890123456", "w-web", "w-102", `{"op":"cancel"}`, "ws"); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body["text"].(string)), &envelope); err != nil {
		t.Fatal(err)
	}
	data, _ := envelope["data"].(map[string]any)
	if envelope["ok"] != true || data["canceled"] != true || data["paneId"] != "w-102" {
		t.Fatalf("unexpected cancel reply: %#v", envelope)
	}
	command, err := os.ReadFile(tmuxLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(command)), "send-keys -t w-102:main.0 Escape"; got != want {
		t.Fatalf("tmux command = %q, want %q", got, want)
	}
}

func TestCancelAgentTurnFinalizesStaleHeadlessReply(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	const agentID = "w-cicy-cancel-stale"
	if _, err := store.Exec(`INSERT INTO agent_config
		(pane_id,title,agent_type,active) VALUES (?, 'Headless Agent', 'cicy', 1)`, agentID+":main.0"); err != nil {
		t.Fatal(err)
	}
	if err := aiGatewayWriteReplySnapshot(agentID, aiGatewayReplySnapshot{
		ConversationID: "conv-cancel", Status: "working", UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := cancelAgentTurnData(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if result["canceled"] != true || result["paneId"] != agentID {
		t.Fatalf("unexpected cancel result: %#v", result)
	}
	if reply := aiGatewayLoadReplySnapshot(agentID); reply.Status != "completed" {
		t.Fatalf("stale reply status = %q, want completed", reply.Status)
	}
}

func TestCancelAgentTurnCancelsInFlightHeadlessSession(t *testing.T) {
	withTestStore(t)
	const agentID = "w-cicy-cancel-live"
	if _, err := store.Exec(`INSERT INTO agent_config
		(pane_id,title,agent_type,active) VALUES (?, 'Live Headless Agent', 'cicy', 1)`, agentID+":main.0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &cicySession{pending: []string{"queued"}}
	session.setCancel(cancel)
	cicySessionsMu.Lock()
	cicySessions[agentID] = session
	cicySessionsMu.Unlock()
	t.Cleanup(func() {
		cicySessionsMu.Lock()
		delete(cicySessions, agentID)
		cicySessionsMu.Unlock()
	})

	result, err := cancelAgentTurnData(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if result["canceled"] != true || result["mode"] != "headless" {
		t.Fatalf("unexpected cancel result: %#v", result)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("in-flight context was not canceled")
	}
	session.qmu.Lock()
	defer session.qmu.Unlock()
	if len(session.pending) != 0 {
		t.Fatalf("queued inputs were not cleared: %#v", session.pending)
	}
}

func TestCancelAgentTurnRejectsUnknownAgent(t *testing.T) {
	withTestStore(t)
	if _, err := cancelAgentTurnData("w-missing-cancel"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestCiCyCloudRPCRepliesAndAcknowledgesExactlyOnce(t *testing.T) {
	withTestStore(t)
	var replies, acks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/code/messages/poll" && r.URL.Query().Get("ack") != "":
			acks++
			_ = json.NewEncoder(w).Encode(M{"messages": []M{}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/code/messages/poll":
			_ = json.NewEncoder(w).Encode(M{"messages": []M{{
				"id": "msg-rpc-12345678", "senderInstanceId": "code-source-1234567890123456",
				"senderAgentId": "w-9", "targetAgentId": "w-102", "kind": "rpc_request", "text": `{"op":"msgs"}`,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/code/messages":
			replies++
			_ = json.NewEncoder(w).Encode(M{"message": M{"id": "msg-rpc-reply-12345678"}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	now := time.Now()
	tr := &cicyCloudTransport{token: "test", lastHeartbeat: now, lastPresence: now}
	msgs, cursor, err := tr.Poll("")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 || cursor != "" || replies != 1 || acks != 1 {
		t.Fatalf("rpc must reply+ack once without agent dispatch: msgs=%#v cursor=%q replies=%d acks=%d", msgs, cursor, replies, acks)
	}
}

func TestCiCyCloudInboxPersistsOnceBeforeAck(t *testing.T) {
	withTestStore(t)
	msg := botMsg{
		AckID: "msg-durable-12345678",
		Text:  "hello",
		Peer: botPeer{
			ChatID:       "code-source-1234567890123456|w-99",
			ContextToken: "w-1001|msg-durable-12345678",
		},
	}
	if err := persistCiCyCloudInbound(7, msg, "w-1001", msg.Text); err != nil {
		t.Fatal(err)
	}
	if err := persistCiCyCloudInbound(7, msg, "w-1001", msg.Text); err != nil {
		t.Fatal(err)
	}
	var count, attempts int
	var status, senderAgent string
	if err := store.QueryRow(`SELECT COUNT(*),status,attempt_count,sender_agent_id FROM cicy_cloud_inbox WHERE message_id=?`, msg.AckID).
		Scan(&count, &status, &attempts, &senderAgent); err != nil {
		t.Fatal(err)
	}
	if count != 1 || status != "received" || attempts != 0 || senderAgent != "w-99" {
		t.Fatalf("unexpected durable inbox row count=%d status=%q attempts=%d sender=%q", count, status, attempts, senderAgent)
	}
}

func TestCiCyCloudInboxSerializesMessagesPerAgent(t *testing.T) {
	withTestStore(t)
	if _, err := store.Exec(`INSERT INTO cicy_cloud_inbox
		(message_id,account_id,target_pane_id,text,peer_chat_id,context_token,status)
		VALUES
		('msg-first-12345678',1,'w-102:main.0','first','source','w-102|msg-first-12345678','running'),
		('msg-second-12345678',1,'w-102:main.0','second','source','w-102|msg-second-12345678','received')`); err != nil {
		t.Fatal(err)
	}
	dispatchCiCyCloudInboxItem(cicyCloudInboxItem{
		MessageID: "msg-second-12345678", AccountID: 1, TargetPaneID: "w-102:main.0",
		Text: "second", PeerChatID: "source", ContextToken: "w-102|msg-second-12345678", Status: "received",
	})
	var status string
	if err := store.QueryRow(`SELECT status FROM cicy_cloud_inbox WHERE message_id='msg-second-12345678'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "received" {
		t.Fatalf("second message dispatched while first turn is active: %q", status)
	}
}
