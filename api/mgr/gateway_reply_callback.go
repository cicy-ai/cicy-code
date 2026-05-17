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
	finalize(reply aiGatewayReplySnapshot)
}

// Cross-agent reply callback: when agent A sends a message to agent B with
// callback=true, the CLI passes A's pane_id as callback_to. We queue A onto
// B's pending list. Each time B starts a new turn, drainCallbackHooksForPane(B)
// drains the pending list and attaches one callback hook per entry. When B's
// reply finalizes (status=completed/failed), each hook fires once and writes
// one line into A's pane: "[<B>] work done" or "[<B>] failed".

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

// drainCallbackHooksForPane is called from newReplyHooksForPane at the start
// of each new turn. It consumes any callbacks queued against this pane and
// returns one hook per entry.
func drainCallbackHooksForPane(paneID string) []aiGatewayReplyHook {
	paneID = normPaneID(paneID)
	if paneID == "" {
		return nil
	}
	pendingCallbackMu.Lock()
	queue := pendingCallbacks[paneID]
	delete(pendingCallbacks, paneID)
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

type replyCallbackHook struct {
	receiverPaneID string
	callbackTo     string
	fired          bool
	mu             sync.Mutex
}

func (h *replyCallbackHook) handleEvents(_ []aiGatewayReplyEvent) {}

func (h *replyCallbackHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil {
		return
	}
	status := strings.TrimSpace(reply.Status)
	if status != "completed" && status != "failed" {
		return
	}
	h.mu.Lock()
	if h.fired {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	// CLIs like claude/codex/opencode emit several gateway requests for a single
	// user turn: tool_call → tool_result → tool_call → … → final answer. Each
	// intermediate request's response carries ToolCalls and finalizes with
	// status=completed, so naively firing on the first finalize delivers the
	// callback BEFORE the receiver actually answered. When tool_calls are still
	// outstanding, re-queue this callback so newReplyHooksForPane picks it up at
	// the start of the next request in the same turn. We only fire when the
	// final response has no remaining tool_calls (or when the turn failed).
	if status == "completed" && len(reply.ToolCalls) > 0 {
		pendingCallbackMu.Lock()
		pendingCallbacks[h.receiverPaneID] = append(pendingCallbacks[h.receiverPaneID], h.callbackTo)
		pendingCallbackMu.Unlock()
		log.Printf("[reply-callback] defer pane=%s -> %s (tool_calls=%d in flight)",
			shortPaneID(h.receiverPaneID), shortPaneID(h.callbackTo), len(reply.ToolCalls))
		return
	}

	h.mu.Lock()
	h.fired = true
	h.mu.Unlock()

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
