package main

import "testing"

// These tests pin the SEND TIMING of the "work done"/completion signal.
//
// The live, authoritative path is replyCallbackHook.finalize (gateway_reply_callback.go):
// a per-message callback that fires at the RECEIVER's real end-of-turn. The two
// timing gates that matter:
//   1. tool gate  — a completed reply that still carries ToolCalls is an
//      INTERMEDIATE tool round (tool_call → tool_result → … → answer), not the
//      user-visible end of the turn, so it must NOT fire.
//   2. turn anchor — a reply whose turn_id == the turn already in-flight when the
//      callback was registered (the receiver's boot/opening round) must NOT fire.
// A failure fires regardless of tool calls.

func setupPending(recv, cbTo, msgID string, notify bool) {
	pendingCallbackMu.Lock()
	pendingCallbacks[normPaneID(recv)] = []pendingCallback{{callbackTo: normPaneID(cbTo), msgID: msgID, notify: notify}}
	pendingCallbackMu.Unlock()
}

func pendingLen(recv string) int {
	pendingCallbackMu.Lock()
	defer pendingCallbackMu.Unlock()
	return len(pendingCallbacks[normPaneID(recv)])
}

func TestReplyCallbackFinalizeTiming(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetPendingCallbacks()

	const master, recv = "w-20001", "w-20003"

	// --- 1) intermediate tool round: completed + ToolCalls → must NOT fire ---
	if err := insertAgentMessage("mt", master, recv, "📮 do X", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	setupPending(recv, master, "mt", true)
	h := &replyCallbackHook{receiverPaneID: normPaneID(recv), callbackTo: normPaneID(master), msgID: "mt", notify: true}
	h.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-1", ToolCalls: []aiGatewayToolCall{{ToolName: "Bash"}}})
	if st, _ := msgStatus(t, "mt"); st != "sent" {
		t.Fatalf("[tool gate] intermediate round fired too early: status=%s want sent", st)
	}
	if n := pendingLen(recv); n != 1 {
		t.Fatalf("[tool gate] intermediate round consumed the pending entry (fired early): remaining=%d", n)
	}

	// --- 2) terminal answer round: completed + no tools → fires now ---
	h.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-1", ToolCalls: nil})
	if st, _ := msgStatus(t, "mt"); st != "done" {
		t.Fatalf("[tool gate] terminal round did not fire: status=%s want done", st)
	}
	if n := pendingLen(recv); n != 0 {
		t.Fatalf("[tool gate] terminal round did not consume pending: remaining=%d", n)
	}

	// --- 3) failure fires even mid tool round ---
	resetPendingCallbacks()
	if err := insertAgentMessage("mf", master, recv, "📮 do Y", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	setupPending(recv, master, "mf", true)
	hf := &replyCallbackHook{receiverPaneID: normPaneID(recv), callbackTo: normPaneID(master), msgID: "mf", notify: true}
	hf.finalize(aiGatewayReplySnapshot{Status: "failed", TurnID: "turn-2", ToolCalls: []aiGatewayToolCall{{ToolName: "Bash"}}, LastStopReason: "boom"})
	if st, _ := msgStatus(t, "mf"); st != "failed" {
		t.Fatalf("[failure] failed reply did not fire with tools present: status=%s want failed", st)
	}

	// --- 4) turn anchor: born turn does NOT fire; a new turn does ---
	resetPendingCallbacks()
	if err := insertAgentMessage("mb", master, recv, "📮 do Z", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	setupPending(recv, master, "mb", true)
	hb := &replyCallbackHook{receiverPaneID: normPaneID(recv), callbackTo: normPaneID(master), msgID: "mb", notify: true, bornTurnID: "turn-boot"}
	hb.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-boot"}) // same as born → skip
	if st, _ := msgStatus(t, "mb"); st != "sent" {
		t.Fatalf("[turn anchor] born turn fired: status=%s want sent", st)
	}
	if n := pendingLen(recv); n != 1 {
		t.Fatalf("[turn anchor] born turn consumed pending: remaining=%d", n)
	}
	hb.finalize(aiGatewayReplySnapshot{Status: "completed", TurnID: "turn-new"}) // new turn → fires
	if st, _ := msgStatus(t, "mb"); st != "done" {
		t.Fatalf("[turn anchor] new turn did not fire: status=%s want done", st)
	}
}

// TestWorkerCompletionTmuxPathUnwired characterizes the OTHER, legacy completion
// path (worker_completion.go → "[w-xxx]work done" tmux line). markWorkerPromptSubmitted
// tracks a pending prompt on every send to a worker pane, and completeWorkerPromptIfIdle
// is the intended consumer that fires the tmux line on a non-idle→idle transition.
// The function WORKS when called — but it is never registered as a hook (the only
// RegisterHook is the chatbus worker_idle one), so in production this path never
// runs and its pending entry is never drained.
func TestWorkerCompletionTmuxPathUnwired(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	paneID := normPaneID("w-20003") // w-2* ⇒ isWorkerPane
	workerCompletionState.mu.Lock()
	workerCompletionState.pending = map[string]workerPendingPrompt{}
	workerCompletionState.mu.Unlock()

	markWorkerPromptSubmitted(paneID, "please do the thing")
	short := shortPaneID(paneID)
	workerCompletionState.mu.Lock()
	_, tracked := workerCompletionState.pending[short]
	workerCompletionState.mu.Unlock()
	if !tracked {
		t.Fatalf("worker prompt was not tracked as pending")
	}

	// The consumer works when invoked directly: a non-idle→idle transition drains
	// the pending entry (and would emit the tmux line if a master were active).
	completeWorkerPromptIfIdle(paneID, paneSt{Status: sp("thinking")}, paneSt{Status: sp("idle")})
	workerCompletionState.mu.Lock()
	_, stillPending := workerCompletionState.pending[short]
	workerCompletionState.mu.Unlock()
	if stillPending {
		t.Fatalf("completeWorkerPromptIfIdle did not consume the pending entry when called directly")
	}

	// But re-tracking and firing hooks the way production does (triggerHooks) does
	// NOT drain it, because completeWorkerPromptIfIdle is not a registered hook.
	markWorkerPromptSubmitted(paneID, "another prompt")
	triggerHooks(paneID, paneSt{Status: sp("thinking")}, paneSt{Status: sp("idle")})
	// In the test binary the hook registry is empty (main() never ran), which is
	// precisely the point: completeWorkerPromptIfIdle is not registered anywhere,
	// so the production status-transition path leaves the entry pending.
	workerCompletionState.mu.Lock()
	_, leaked := workerCompletionState.pending[short]
	workerCompletionState.mu.Unlock()
	if !leaked {
		t.Fatalf("a registered hook unexpectedly drained the worker pending entry")
	}
}
