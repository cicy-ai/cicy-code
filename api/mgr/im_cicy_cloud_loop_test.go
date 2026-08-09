package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type recordingMessageAcker struct{ ids []string }

func (a *recordingMessageAcker) Ack(id string) error {
	a.ids = append(a.ids, id)
	return nil
}

func TestCiCyCloudPollDoesNotFeedAgentRepliesBackToAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/code/messages/poll" {
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(M{"messages": []M{
			{"id": "msg-user-12345678", "senderInstanceId": "code-source-1234567890123456", "kind": "user_message", "text": "hello"},
			{"id": "msg-reply-12345678", "senderInstanceId": "code-source-1234567890123456", "kind": "agent_reply", "text": "answer"},
		}})
	}))
	defer server.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", server.URL)

	tr := &cicyCloudTransport{token: "test", lastPresence: time.Now()}
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
	if cursor != "msg-user-12345678,msg-reply-12345678" {
		t.Fatalf("both messages must advance the ACK cursor, got %q", cursor)
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

	tr := &cicyCloudTransport{token: "test", lastPresence: time.Now()}
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
