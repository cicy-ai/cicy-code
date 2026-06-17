package main

import "testing"

// The cross-agent reply callback must fire exactly once, at the receiver's final
// reply (no tool_calls) or failure — never get consumed early by an intermediate
// tool-continuation request. peek (don't consume) + remove-on-fire is what makes
// that race-free; this locks in those invariants.
func TestReplyCallbackPeekAndDeferral(t *testing.T) {
	pendingCallbackMu.Lock()
	pendingCallbacks = map[string][]pendingCallback{}
	pendingCallbackMu.Unlock()

	recv := normPaneID("w-20003")
	caller := normPaneID("w-20001")
	registerReplyCallback("w-20003", "w-20001", "", false, "")

	queueLen := func() int {
		pendingCallbackMu.Lock()
		defer pendingCallbackMu.Unlock()
		return len(pendingCallbacks[recv])
	}

	// peek returns a hook WITHOUT consuming the queue (so the next request still sees it).
	hooks := peekCallbackHooksForPane("w-20003")
	if len(hooks) != 1 {
		t.Fatalf("peek hooks = %d, want 1", len(hooks))
	}
	if queueLen() != 1 {
		t.Fatalf("peek consumed the queue: remaining=%d, want 1", queueLen())
	}

	// Intermediate response (tool_calls in flight) must NOT fire or consume.
	h := hooks[0].(*replyCallbackHook)
	h.finalize(aiGatewayReplySnapshot{Status: "completed", ToolCalls: []aiGatewayToolCall{{}}})
	if h.fired {
		t.Fatalf("hook fired on an intermediate tool_calls response")
	}
	if queueLen() != 1 {
		t.Fatalf("deferral consumed the queue: remaining=%d, want 1", queueLen())
	}

	// removeOneCallbackEntry consumes exactly one; a second call reports nothing left.
	if !removeOneCallbackEntry(recv, caller, "") {
		t.Fatalf("removeOneCallbackEntry should consume the entry")
	}
	if queueLen() != 0 {
		t.Fatalf("entry not removed: remaining=%d, want 0", queueLen())
	}
	if removeOneCallbackEntry(recv, caller, "") {
		t.Fatalf("second remove should report false (already consumed)")
	}
}

// turn_id anchoring: the callback must NOT fire on the turn that was already in
// flight at registration (the receiver's boot/opening round) — only a NEW turn
// (handling the message) fires. Fixes the --notify "boot round falsely reports
// done" bug.
func TestReplyCallbackBornTurnAnchor(t *testing.T) {
	pendingCallbackMu.Lock()
	pendingCallbacks = map[string][]pendingCallback{}
	pendingCallbackMu.Unlock()

	recv := normPaneID("w-21003")
	// notify=false so a fire is hermetic (no sendTextToPane); msgID="" so the
	// store update is skipped. We assert on queue consumption alone.
	registerReplyCallback("w-21003", "w-21001", "", false, "turn-X")

	hooks := peekCallbackHooksForPane("w-21003")
	if len(hooks) != 1 {
		t.Fatalf("peek hooks = %d, want 1", len(hooks))
	}
	h := hooks[0].(*replyCallbackHook)
	if h.bornTurnID != "turn-X" {
		t.Fatalf("bornTurnID not propagated to hook: %q", h.bornTurnID)
	}
	queueLen := func() int {
		pendingCallbackMu.Lock()
		defer pendingCallbackMu.Unlock()
		return len(pendingCallbacks[recv])
	}

	// Boot round (same turn id as registration) → must NOT fire or consume.
	h.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-X"})
	if h.fired {
		t.Fatalf("fired on the born (boot) turn")
	}
	if queueLen() != 1 {
		t.Fatalf("born-turn finalize consumed the queue: remaining=%d, want 1", queueLen())
	}

	// New turn (the one handling the message) → fires + consumes.
	h.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-Y"})
	if !h.fired {
		t.Fatalf("did not fire on a new turn")
	}
	if queueLen() != 0 {
		t.Fatalf("new-turn finalize did not consume: remaining=%d, want 0", queueLen())
	}
}

// Addendum #176: a work-done line is pushed ONLY for --notify sends whose
// receiver didn't already reply in-band. Default dispatch (no --notify) and any
// in-band-replied turn are DB-only. This locks the exact push matrix.
func TestReplyCallbackShouldPush(t *testing.T) {
	cases := []struct {
		notify, replied, want bool
	}{
		{notify: false, replied: false, want: false}, // default dispatch: DB-only
		{notify: false, replied: true, want: false},  // default + replied: DB-only
		{notify: true, replied: false, want: true},   // --notify, no in-band reply: push
		{notify: true, replied: true, want: false},   // --notify but replied in-band: suppressed
	}
	for _, c := range cases {
		if got := replyCallbackShouldPush(c.notify, c.replied); got != c.want {
			t.Errorf("shouldPush(notify=%v, replied=%v) = %v, want %v", c.notify, c.replied, got, c.want)
		}
	}
}

// Addendum #177 part 3: the push line reports only the message-status flip
// (not "work done"), carries the msg id, and degrades gracefully when id empty.
func TestReplyCallbackNotice(t *testing.T) {
	cases := []struct{ status, short, msgID, want string }{
		{"completed", "w-20003", "abcd1234", "🔔 [w-20003] msg abcd1234 → done"},
		{"failed", "w-20003", "abcd1234", "⚠️ [w-20003] msg abcd1234 → failed"},
		{"completed", "w-20003", "", "🔔 [w-20003] → done"},
		{"failed", "w-20003", "", "⚠️ [w-20003] → failed"},
	}
	for _, c := range cases {
		if got := replyCallbackNotice(c.status, c.short, c.msgID); got != c.want {
			t.Errorf("notice(%q,%q,%q) = %q, want %q", c.status, c.short, c.msgID, got, c.want)
		}
	}
}
