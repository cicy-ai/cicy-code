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
}
