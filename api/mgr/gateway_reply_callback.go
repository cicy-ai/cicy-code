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
// tool_calls) or a failure. The DB row is always updated; a chat line is pushed
// to A's pane only for --notify sends whose receiver didn't reply in-band, and
// it reports just the message-status flip (not task success):
// "🔔 [<B>] msg <id> → done" or "⚠️ [<B>] msg <id> → failed".

// pendingCallback is one queued callback against a receiver pane: who to notify
// (callbackTo), which agent_messages row it tracks (msgID, may be empty for
// callers that don't record a message), and whether a chat-line wake-up should
// be pushed on completion (notify — only --notify sends opt in; the DB state is
// always updated regardless).
type pendingCallback struct {
	callbackTo string
	msgID      string
	notify     bool
	// bornTurnID is the receiver's in-flight turn_id at registration time (its
	// boot/opening or a prior turn). finalize ignores that exact turn so a
	// freshly-booted agent's opening round doesn't falsely fire "done" — only a
	// NEW turn (the one actually handling this message) fires. Empty = no
	// in-flight turn at registration → first terminal reply fires (old behavior).
	bornTurnID string
}

var (
	pendingCallbackMu sync.Mutex
	pendingCallbacks  = map[string][]pendingCallback{} // paneID → queued callbacks
)

func registerReplyCallback(paneID, callbackTo, msgID string, notify bool, bornTurnID string) {
	paneID = normPaneID(paneID)
	callbackTo = normPaneID(callbackTo)
	if paneID == "" || callbackTo == "" || paneID == callbackTo {
		return
	}
	pendingCallbackMu.Lock()
	pendingCallbacks[paneID] = append(pendingCallbacks[paneID], pendingCallback{callbackTo: callbackTo, msgID: msgID, notify: notify, bornTurnID: bornTurnID})
	pendingCallbackMu.Unlock()
	log.Printf("[reply-callback] registered pane=%s callback_to=%s msg=%s notify=%v born_turn=%s", shortPaneID(paneID), shortPaneID(callbackTo), msgID, notify, bornTurnID)
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
	queue := append([]pendingCallback(nil), pendingCallbacks[paneID]...)
	pendingCallbackMu.Unlock()
	if len(queue) == 0 {
		return nil
	}
	hooks := make([]aiGatewayReplyHook, 0, len(queue))
	for _, entry := range queue {
		hooks = append(hooks, &replyCallbackHook{
			receiverPaneID: paneID,
			callbackTo:     entry.callbackTo,
			msgID:          entry.msgID,
			notify:         entry.notify,
			bornTurnID:     entry.bornTurnID,
		})
	}
	return hooks
}

// removeOneCallbackEntry consumes a single entry matching (callbackTo, msgID)
// from the pane's pending list. Returns false when no matching entry remains
// (already fired by a concurrent hook) so the caller can avoid a double send.
func removeOneCallbackEntry(paneID, callbackTo, msgID string) bool {
	pendingCallbackMu.Lock()
	defer pendingCallbackMu.Unlock()
	q := pendingCallbacks[paneID]
	for i, t := range q {
		if t.callbackTo == callbackTo && t.msgID == msgID {
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
	msgID          string
	notify         bool
	bornTurnID     string
	fired          bool
	mu             sync.Mutex
}

// replyCallbackShouldPush decides whether finalize emits a work-done/failed chat
// line: only when the sender opted in (--notify) AND the receiver did not already
// reply in-band this turn. The DB state is updated regardless of this verdict.
func replyCallbackShouldPush(notify, replied bool) bool { return notify && !replied }

// replyCallbackNotice builds the wake-up line pushed to the sender. It reports
// only the message-status flip on the receiver's side — NOT "work done", which
// would falsely assert task success (the system only knows the receiver's turn
// reached a terminal state). short=shortPaneID(receiver); msgID may be empty
// (degrades to the no-id form). emoji-first so the outcome reads first.
func replyCallbackNotice(status, short, msgID string) string {
	emoji, verdict := "🔔", "done"
	if status == "failed" {
		emoji, verdict = "⚠️", "failed"
	}
	if strings.TrimSpace(msgID) != "" {
		return fmt.Sprintf("%s [%s] msg %s → %s", emoji, short, msgID, verdict)
	}
	return fmt.Sprintf("%s [%s] → %s", emoji, short, verdict)
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

	// turn_id anchoring: if this terminal reply is the SAME turn that was already
	// in flight when the callback was registered (the receiver's boot/opening or a
	// prior round), it is not the turn that handles this message — leave the entry
	// queued for the next turn and don't fire. Only a new turn_id fires. When
	// bornTurnID is empty (no in-flight turn at registration), behavior is unchanged.
	if h.bornTurnID != "" && strings.TrimSpace(reply.TurnID) == h.bornTurnID {
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
	if !removeOneCallbackEntry(h.receiverPaneID, h.callbackTo, h.msgID) {
		return
	}

	short := shortPaneID(h.receiverPaneID)

	// Update the agent_messages row to its terminal state and write the reply
	// pointer (turn_id/conversation_id/history_id) so the link is traceable.
	// `replied` reports whether the receiver already answered the sender in-band
	// this turn — the signal that lets us de-noise the chat below.
	replied := false
	if h.msgID != "" {
		var derr error
		if status == "failed" {
			emsg := strings.TrimSpace(reply.LastStopReason)
			if emsg == "" {
				emsg = "reply ended with status=failed"
			}
			replied, derr = markAgentMessageFailed(h.msgID, reply, emsg)
		} else {
			replied, derr = markAgentMessageDone(h.msgID, reply)
		}
		if derr != nil {
			log.Printf("[reply-callback] msg store update failed msg=%s: %v", h.msgID, derr)
		}
	}

	// Default: DB-only, no chat push. A work-done/failed line is pushed ONLY when
	// the sender opted in with --notify AND the receiver did NOT already answer
	// in-band this turn (replied=0). So: plain dispatch = zero chat noise; an
	// explicit --notify acts as a wake-up unless the receiver already replied.
	if !replyCallbackShouldPush(h.notify, replied) {
		log.Printf("[reply-callback] db-only pane=%s -> %s status=%s notify=%v replied=%v", short, shortPaneID(h.callbackTo), status, h.notify, replied)
		return
	}

	notice := replyCallbackNotice(status, short, h.msgID)
	if err := sendTextToPane(h.callbackTo, notice, true); err != nil {
		log.Printf("[reply-callback] send failed pane=%s -> %s: %v", short, shortPaneID(h.callbackTo), err)
	} else {
		log.Printf("[reply-callback] fired pane=%s -> %s status=%s", short, shortPaneID(h.callbackTo), status)
	}
}
