package main

import (
	"testing"
)

func resetPendingCallbacks() {
	pendingCallbackMu.Lock()
	pendingCallbacks = map[string][]pendingCallback{}
	pendingCallbackMu.Unlock()
}

func msgStatus(t *testing.T, id string) (status string, replied int) {
	t.Helper()
	if err := store.QueryRow(`SELECT status, replied FROM agent_messages WHERE id=?`, id).Scan(&status, &replied); err != nil {
		t.Fatalf("read message %s: %v", id, err)
	}
	return
}

// Acceptance #1: insert='sent' → markDone='done' + reply pointer written so it
// can JOIN to the real history_turns row.
func TestAgentMessageInsertAndMarkDone(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	if err := insertAgentMessage("m1", "w-20001", "w-20003", "📮 [w-20001] do X", true, "conv-A", "turn-A"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// from-side pointer persisted (#177).
	var fc, ft string
	if err := store.QueryRow(`SELECT from_conversation_id, from_turn_id FROM agent_messages WHERE id=?`, "m1").Scan(&fc, &ft); err != nil {
		t.Fatalf("read from pointer: %v", err)
	}
	if fc != "conv-A" || ft != "turn-A" {
		t.Fatalf("from pointer not stored: conv=%s turn=%s", fc, ft)
	}
	status, replied := msgStatus(t, "m1")
	if status != "sent" || replied != 0 {
		t.Fatalf("after insert: status=%s replied=%d, want sent/0", status, replied)
	}

	reply := aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-xyz", ConversationID: "conv-9", HistoryID: 42}
	got, err := markAgentMessageDone("m1", reply)
	if err != nil {
		t.Fatalf("markDone: %v", err)
	}
	if got {
		t.Fatalf("replied should be false (receiver never messaged back)")
	}
	var (
		st, conv, turn string
		hid            int64
	)
	if err := store.QueryRow(`SELECT status, reply_conversation_id, reply_turn_id, reply_history_id FROM agent_messages WHERE id=?`, "m1").
		Scan(&st, &conv, &turn, &hid); err != nil {
		t.Fatalf("read pointer: %v", err)
	}
	if st != "done" || conv != "conv-9" || turn != "turn-xyz" || hid != 42 {
		t.Fatalf("pointer not written: status=%s conv=%s turn=%s hid=%d", st, conv, turn, hid)
	}
}

// Addendum #176 acceptance #3: even a --notify send is suppressed when the
// receiver replied in-band (replied=1) → finalize only flips the DB, no push.
func TestAgentMessageNotifyRepliedSuppressesPush(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetPendingCallbacks()

	// A→B open message, sent with --notify.
	if err := insertAgentMessage("m2", "w-20001", "w-20003", "📮 [w-20001] do X", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// B messages A back during the turn → mark the open A→B row replied=1.
	// (This is exactly what handleSend's E-block calls.)
	markAgentMessagesReplied(normPaneID("w-20001"), normPaneID("w-20003"))
	if _, replied := msgStatus(t, "m2"); replied != 1 {
		t.Fatalf("markAgentMessagesReplied did not set replied=1")
	}

	// finalize on the suppressed row: must flip to done WITHOUT pushing — even
	// though notify=true, replied=1 wins (replyCallbackShouldPush → false), so it
	// returns before sendTextToPane.
	h := &replyCallbackHook{receiverPaneID: normPaneID("w-20003"), callbackTo: normPaneID("w-20001"), msgID: "m2", notify: true}
	pendingCallbackMu.Lock()
	pendingCallbacks[normPaneID("w-20003")] = []pendingCallback{{callbackTo: normPaneID("w-20001"), msgID: "m2", notify: true}}
	pendingCallbackMu.Unlock()
	h.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-2", ConversationID: "conv-2"})

	if status, _ := msgStatus(t, "m2"); status != "done" {
		t.Fatalf("suppressed row not marked done: status=%s", status)
	}
	// The entry must have been consumed (fired) even though no push happened.
	pendingCallbackMu.Lock()
	remaining := len(pendingCallbacks[normPaneID("w-20003")])
	pendingCallbackMu.Unlock()
	if remaining != 0 {
		t.Fatalf("suppressed finalize did not consume the pending entry: remaining=%d", remaining)
	}
}

// Acceptance #4: receiver fails → status='failed' + error stored, replied=0.
func TestAgentMessageMarkFailed(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	if err := insertAgentMessage("m4", "w-20001", "w-20003", "📮 [w-20001] do X", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := markAgentMessageFailed("m4", aiGatewayReplySnapshot{Status: "failed", TurnID: "turn-4"}, "boom"); err != nil {
		t.Fatalf("markFailed: %v", err)
	}
	var status, errMsg string
	if err := store.QueryRow(`SELECT status, error FROM agent_messages WHERE id=?`, "m4").Scan(&status, &errMsg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "failed" || errMsg != "boom" {
		t.Fatalf("failed row wrong: status=%s error=%s", status, errMsg)
	}
}

// Acceptance #6: --no-callback message records callback=0, stays 'sent', and is
// still queryable.
func TestAgentMessageNoCallbackStaysSent(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	if err := insertAgentMessage("m6", "w-20001", "w-20003", "📮 [w-20001] fyi", false, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var status string
	var callback int
	if err := store.QueryRow(`SELECT status, callback FROM agent_messages WHERE id=?`, "m6").Scan(&status, &callback); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "sent" || callback != 0 {
		t.Fatalf("no-callback row wrong: status=%s callback=%d", status, callback)
	}
	rows, err := queryAgentMessages(agentMessageFilter{Open: true})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "m6" {
		t.Fatalf("open query did not return the sent row: %+v", rows)
	}
}

// queryAgentMessages filters by from/to/status.
func TestQueryAgentMessagesFilters(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	_ = insertAgentMessage("a", "w-1", "w-2", "x", true, "", "")
	_ = insertAgentMessage("b", "w-1", "w-3", "y", true, "", "")
	_, _ = markAgentMessageDone("b", aiGatewayReplySnapshot{Status: "completed", TurnID: "t"})

	to2, _ := queryAgentMessages(agentMessageFilter{To: "w-2"})
	if len(to2) != 1 || to2[0].ID != "a" {
		t.Fatalf("to filter: %+v", to2)
	}
	done, _ := queryAgentMessages(agentMessageFilter{Status: "done"})
	if len(done) != 1 || done[0].ID != "b" {
		t.Fatalf("status filter: %+v", done)
	}
	from1, _ := queryAgentMessages(agentMessageFilter{From: "w-1"})
	if len(from1) != 2 {
		t.Fatalf("from filter: want 2, got %d", len(from1))
	}
}

// Acceptance #7: a message pointer resolves to the receiver's real turn content
// (q / answer / item_json) via the cross-DB Go JOIN.
func TestLookupHistoryTurnJoin(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	agentID := "w-20003"
	current := aiGatewayCurrentSnapshot{ConversationID: "conv-join"}
	reply := aiGatewayReplySnapshot{TurnID: "turn-join", ConversationID: "conv-join", Status: "completed"}
	record := testHistoryRecord("what is X?", "X is the answer", "2026-06-14T01:00:00Z", "2026-06-14T01:00:02Z", "claude-opus-4-8")
	if _, err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
		t.Fatalf("upsert history turn: %v", err)
	}

	turn, ok := lookupHistoryTurn(agentID, "conv-join", "turn-join")
	if !ok {
		t.Fatalf("lookupHistoryTurn miss")
	}
	if turn.Q != "what is X?" || turn.A != "X is the answer" {
		t.Fatalf("joined turn wrong: q=%q a=%q", turn.Q, turn.A)
	}

	// Missing pointer / unknown agent → graceful miss, no history.db created.
	if _, ok := lookupHistoryTurn("w-does-not-exist", "c", "t"); ok {
		t.Fatalf("lookup should miss for unknown agent")
	}
}

// #177: queryAgentMessages surfaces the from-side conv/turn pointer so callers
// can JOIN the initiator's history too (the from half of the causal link).
func TestQueryExposesFromPointer(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	if err := insertAgentMessage("mf", "w-1", "w-2", "x", true, "conv-init", "turn-init"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := queryAgentMessages(agentMessageFilter{From: "w-1"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("query: err=%v rows=%d", err, len(rows))
	}
	if rows[0].FromConversationID != "conv-init" || rows[0].FromTurnID != "turn-init" {
		t.Fatalf("from pointer not exposed: conv=%q turn=%q", rows[0].FromConversationID, rows[0].FromTurnID)
	}
}

func TestStampedSenderID(t *testing.T) {
	cases := map[string]string{
		"📮 [w-10029] hello":     "w-10029",
		"📮 [w-1001] multi word": "w-1001",
		"no stamp here":          "",
		"📮 broken":               "",
		"📨 [w-9] old marker":    "", // only the 📮 stamp is parsed
	}
	for in, want := range cases {
		if got := stampedSenderID(in); got != want {
			t.Errorf("stampedSenderID(%q) = %q, want %q", in, got, want)
		}
	}
}
