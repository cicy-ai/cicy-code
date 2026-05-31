package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

// aiGatewayReplyHook is the interface the AI gateway audit session uses to
// fan out streaming reply events / the final snapshot to attached hooks.
// Currently only the cross-agent reply callback implements it.
type aiGatewayReplyHook interface {
	handleEvents(events []aiGatewayReplyEvent)
	onItems(items []map[string]interface{})
	finalize(reply aiGatewayReplySnapshot)
}

// Cross-agent reply callback: when agent A sends a message to agent B with
// callback=true, the CLI passes A's pane_id as callback_to. We queue A onto
// B's pending list. Each B request peeks the list (peekCallbackHooksForPane) and
// attaches one callback hook per entry WITHOUT consuming it. The entry is removed
// only when its hook fires — at B's final reply (status=completed with no
// tool_calls) or a failure — writing one line into A's pane:
// "[<B>] ✅ work done" or "[<B>] ❌ failed".

var (
	pendingCallbackMu sync.Mutex
	pendingCallbacks  = map[string][]string{} // paneID → list of callback_to pane_ids
)

func registerReplyCallback(paneID, callbackTo string) {
	paneID = normPaneID(paneID)
	callbackTo = normPaneID(callbackTo)
	if paneID == "" || callbackTo == "" || paneID == callbackTo {
		return
	}
	pendingCallbackMu.Lock()
	pendingCallbacks[paneID] = append(pendingCallbacks[paneID], callbackTo)
	pendingCallbackMu.Unlock()
	log.Printf("[reply-callback] registered pane=%s callback_to=%s", shortPaneID(paneID), shortPaneID(callbackTo))
}

// peekCallbackHooksForPane is called from newReplyHooksForPane at the start of
// each request. It returns one hook per callback queued against this pane WITHOUT
// consuming the queue — the entry is removed only when its hook actually fires
// (replyCallbackHook.finalize, on the final/ failed response).
//
// Peeking instead of draining-then-re-queuing-in-finalize fixes a race: a
// CLI turn is several gateway requests (tool_call → tool_result → … → answer).
// The old code drained the queue on every request and re-queued it in finalize
// when tool_calls were still in flight; over MITM each request is a fresh
// single-turn connection in its own goroutine and the CLI sends the next request
// the moment it sees the final SSE event — so the next request's drain often ran
// BEFORE the previous response's finalize re-queued, dropping the callback or
// firing it a turn late. Keeping the entry queued until it fires removes the race.
func peekCallbackHooksForPane(paneID string) []aiGatewayReplyHook {
	paneID = normPaneID(paneID)
	if paneID == "" {
		return nil
	}
	pendingCallbackMu.Lock()
	queue := append([]string(nil), pendingCallbacks[paneID]...)
	pendingCallbackMu.Unlock()
	if len(queue) == 0 {
		return nil
	}
	hooks := make([]aiGatewayReplyHook, 0, len(queue))
	for _, target := range queue {
		hooks = append(hooks, &replyCallbackHook{
			receiverPaneID: paneID,
			callbackTo:     target,
		})
	}
	return hooks
}

// removeOneCallbackEntry consumes a single matching callbackTo from the pane's
// pending list. Returns false when no matching entry remains (already fired by a
// concurrent hook) so the caller can avoid a double send.
func removeOneCallbackEntry(paneID, callbackTo string) bool {
	pendingCallbackMu.Lock()
	defer pendingCallbackMu.Unlock()
	q := pendingCallbacks[paneID]
	for i, t := range q {
		if t == callbackTo {
			pendingCallbacks[paneID] = append(q[:i:i], q[i+1:]...)
			if len(pendingCallbacks[paneID]) == 0 {
				delete(pendingCallbacks, paneID)
			}
			return true
		}
	}
	return false
}

type replyCallbackHook struct {
	receiverPaneID string
	callbackTo     string
	fired          bool
	mu             sync.Mutex
}

func (h *replyCallbackHook) handleEvents(_ []aiGatewayReplyEvent) {}
func (h *replyCallbackHook) onItems(_ []map[string]interface{})    {}
func (h *replyCallbackHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil {
		return
	}
	status := strings.TrimSpace(reply.Status)
	if status != "completed" && status != "failed" {
		return
	}
	// CLIs like claude/codex/opencode emit several gateway requests for a single
	// user turn: tool_call → tool_result → … → final answer. Each intermediate
	// request's response carries ToolCalls and finalizes with status=completed, so
	// it is NOT the user-visible end of the turn. Leave the callback queued (it was
	// only peeked, never consumed); the final request (no tool_calls) or a failure
	// fires it. This is what ties the callback to reply.json's real complete/failure.
	if status == "completed" && len(reply.ToolCalls) > 0 {
		return
	}

	h.mu.Lock()
	if h.fired {
		h.mu.Unlock()
		return
	}
	h.fired = true
	h.mu.Unlock()

	// Consume exactly this entry as we fire; if a concurrent hook already took it,
	// don't double-send.
	if !removeOneCallbackEntry(h.receiverPaneID, h.callbackTo) {
		return
	}

	short := shortPaneID(h.receiverPaneID)
	verdict := "✅ work done"
	if status == "failed" {
		verdict = "❌ failed"
	}
	notice := fmt.Sprintf("[%s] %s", short, verdict)
	if err := sendTextToPane(h.callbackTo, notice, true); err != nil {
		log.Printf("[reply-callback] send failed pane=%s -> %s: %v", short, shortPaneID(h.callbackTo), err)
	} else {
		log.Printf("[reply-callback] fired pane=%s -> %s status=%s", short, shortPaneID(h.callbackTo), status)
	}
}
