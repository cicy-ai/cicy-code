package main

import (
	"log"
	"strings"
	"sync"
	"time"
)

// aiGatewayReplyHook is satisfied by both *tgReplyPushHook (legacy per-pane Telegram)
// and *imReplyPushHook (the generic IM-account hook). The AI-gateway audit session
// fans out streaming reply events / the final snapshot to every hook attached to a pane.
type aiGatewayReplyHook interface {
	handleEvents(events []aiGatewayReplyEvent)
	finalize(reply aiGatewayReplySnapshot)
}

const imReplyUpdateMinInterval = 1500 * time.Millisecond

// imReplyPushHook streams an agent's reply (thinking + answer text — never tool
// inputs/outputs) to a bound IM account. For editable transports (Telegram) it
// edits a single live message as the reply grows; for non-editable transports
// (WeChat) it sends one message on finalize.
type imReplyPushHook struct {
	accID     int64
	paneID    string
	transport botTransport
	canEdit   bool

	mu         sync.Mutex
	thinking   strings.Builder
	answer     strings.Builder
	lastPushAt time.Time
	timer      *time.Timer
	closed     bool
}

func newReplyHooksForPane(agentID string) []aiGatewayReplyHook {
	var hooks []aiGatewayReplyHook
	if h := newTGReplyPushHook(agentID); h != nil {
		hooks = append(hooks, h)
	}
	for _, acc := range imAccountsForPane(agentID) {
		tr := imTransportFor(acc.ID)
		if tr == nil {
			continue
		}
		if strings.TrimSpace(acc.BoundPaneID) == "" {
			continue
		}
		hooks = append(hooks, &imReplyPushHook{
			accID:     acc.ID,
			paneID:    normPaneID(agentID),
			transport: tr,
			canEdit:   tr.CanEdit(),
		})
		log.Printf("[im] reply hook attached account=%d pane=%s editable=%t", acc.ID, shortPaneID(agentID), tr.CanEdit())
	}
	return hooks
}

func (h *imReplyPushHook) handleEvents(events []aiGatewayReplyEvent) {
	if h == nil || len(events) == 0 {
		return
	}
	h.mu.Lock()
	changed := false
	for _, event := range events {
		if event.Kind != "sse" {
			continue // ignore tool_call / web_search — never push tool details
		}
		payload, _ := event.Payload.(map[string]interface{})
		streamKind, _ := payload["stream_kind"].(string)
		content, _ := payload["content"].(string)
		if content == "" {
			continue
		}
		switch streamKind {
		case "thinking":
			h.thinking.WriteString(content)
			changed = true
		case "answer":
			h.answer.WriteString(content)
			changed = true
		}
	}
	if changed && h.canEdit {
		h.schedulePushLocked(false)
	}
	h.mu.Unlock()
}

func (h *imReplyPushHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	if strings.TrimSpace(reply.Thinking) != "" {
		h.thinking.Reset()
		h.thinking.WriteString(reply.Thinking)
	}
	if strings.TrimSpace(reply.Answer) != "" {
		h.answer.Reset()
		h.answer.WriteString(reply.Answer)
	}
	h.schedulePushLocked(true)
	h.mu.Unlock()
}

func (h *imReplyPushHook) schedulePushLocked(force bool) {
	if force {
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
		go h.flush()
		return
	}
	wait := imReplyUpdateMinInterval - time.Since(h.lastPushAt)
	if wait <= 0 && h.timer == nil {
		go h.flush()
		return
	}
	if h.timer != nil {
		return
	}
	if wait < 0 {
		wait = 0
	}
	h.timer = time.AfterFunc(wait, func() {
		h.mu.Lock()
		h.timer = nil
		h.mu.Unlock()
		h.flush()
	})
}

func (h *imReplyPushHook) renderLocked() string {
	answer := strings.TrimSpace(h.answer.String())
	if h.closed && answer != "" {
		return answer
	}
	parts := []string{"Thinking..."}
	if thinking := strings.TrimSpace(h.thinking.String()); thinking != "" {
		parts = append(parts, "Thinking:\n"+thinking)
	}
	if answer != "" {
		parts = append(parts, "Reply:\n"+answer)
	}
	return strings.Join(parts, "\n\n")
}

func (h *imReplyPushHook) flush() {
	if h == nil {
		return
	}
	h.mu.Lock()
	text := imClampMessage(h.renderLocked())
	h.lastPushAt = time.Now()
	h.mu.Unlock()

	acc, _ := imGetAccount(h.accID)
	var peer botPeer
	if acc != nil {
		peer = imPeerForAccount(acc)
	}
	if peer.empty() {
		log.Printf("[im] reply flush skipped account=%d (no peer)", h.accID)
		return
	}
	st := imLiveStateFor(h.accID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastText == text {
		return
	}
	if h.canEdit && strings.TrimSpace(st.messageID) != "" {
		if err := h.transport.Edit(peer, st.messageID, text); err == nil {
			st.lastText = text
			return
		}
		st.messageID = ""
	}
	mid, err := h.transport.Send(peer, text)
	if err != nil {
		log.Printf("[im] reply send failed account=%d: %v", h.accID, err)
		return
	}
	st.messageID = mid
	st.lastText = text
}
