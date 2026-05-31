package main

import "testing"

// The cross-agent reply callback must fire exactly once, at the receiver's final
// reply (no tool_calls) or failure — never get consumed early by an intermediate
// tool-continuation request. peek (don't consume) + remove-on-fire is what makes
// that race-free; this locks in those invariants.
func TestReplyCallbackPeekAndDeferral(t *testing.T) {
	pendingCallbackMu.Lock()
	pendingCallbacks = map[string][]string{}
	pendingCallbackMu.Unlock()

	recv := normPaneID("w-20003")
	caller := normPaneID("w-20001")
	registerReplyCallback("w-20003", "w-20001")

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
	if !removeOneCallbackEntry(recv, caller) {
		t.Fatalf("removeOneCallbackEntry should consume the entry")
	}
	if queueLen() != 0 {
		t.Fatalf("entry not removed: remaining=%d, want 0", queueLen())
	}
	if removeOneCallbackEntry(recv, caller) {
		t.Fatalf("second remove should report false (already consumed)")
	}
}
