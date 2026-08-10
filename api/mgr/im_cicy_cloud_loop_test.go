package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("CICY_CLOUD_DISABLE_WS", "1")
	os.Exit(m.Run())
}

type recordingMessageAcker struct{ ids []string }

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
	connected := make(chan struct{}, 1)
	acked := make(chan string, 1)
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
			connected <- struct{}{}
			_ = conn.WriteJSON(M{"type": "message", "cursor": 42, "message": M{
				"id": "msg-websocket-12345678", "senderInstanceId": "code-remote-1234567890",
				"senderAgentId": "w-remote", "targetAgentId": "w-local", "kind": "user_message", "text": "over ws"}})
			for {
				var frame struct {
					Type, RequestID string
					IDs             []string `json:"ids"`
				}
				if conn.ReadJSON(&frame) != nil {
					return
				}
				if frame.Type == "ack" && len(frame.IDs) == 1 {
					acked <- frame.IDs[0]
					_ = conn.WriteJSON(M{"type": "acked", "requestId": frame.RequestID})
				}
			}
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
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket did not connect")
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
}

func TestCiCyCloudPresenceIntervalStaysInsideOfflineWindow(t *testing.T) {
	if cicyCloudHeartbeatInterval >= 90*time.Second {
		t.Fatalf("heartbeat interval %s must stay below the 90s offline window", cicyCloudHeartbeatInterval)
	}
	if cicyCloudPresenceInterval <= cicyCloudHeartbeatInterval {
		t.Fatalf("roster interval %s must be less frequent than heartbeat %s", cicyCloudPresenceInterval, cicyCloudHeartbeatInterval)
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
	if msgs[0].Peer.ContextToken != "|msg-user-12345678" {
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
	if _, err := tr.Send(botPeer{ChatID: "code-target-1234567890123456", ContextToken: "w-1001|msg-user-12345678"}, "answer"); err != nil {
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
	if err := tr.handleRPCRequest("msg-rpc-12345678", "code-source-1234567890123456", "w-9", "w-102", `{"op":"msgs"}`); err != nil {
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
