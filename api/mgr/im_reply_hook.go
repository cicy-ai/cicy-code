// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// aiGatewayReplyHook interface lives in gateway_reply_callback.go.
// *tgReplyPushHook (legacy per-pane Telegram) and *imReplyPushHook (the generic
// IM-account hook) both satisfy it alongside *replyCallbackHook.

const imReplyUpdateMinInterval = 1500 * time.Millisecond

// imReplyPushHook pushes an agent's reply to a bound IM account, one message
// per reply item: text goes verbatim (truncated), tool_use as a one-line
// "🔧 <name> + key arg" summary, tool failures as a "❌ <name> 失败" notice
// (see renderReplyItemForIM). Thinking is never pushed — it's model-facing
// scratch and would flood the chat.
type imReplyPushHook struct {
	accID     int64
	paneID    string
	transport botTransport
	peer      botPeer
	canEdit   bool

	mu         sync.Mutex
	thinking   strings.Builder
	answer     strings.Builder
	lastPushAt time.Time
	timer      *time.Timer
	closed     bool
}

// imTypingSession drives one "typing" indicator for a single (account, pane)
// across the WHOLE agent turn — from the moment the reply starts producing items
// until the turn reaches a terminal status (completed / failed). A Claude/Codex
// turn is many HTTP rounds (tool loop) with client-side tool-run gaps in between;
// the per-round reply hook is torn down and rebuilt each round, so typing must
// live ABOVE the hook or it flickers off every gap. The indicator follows the
// worker-status window: on while status is "active", off only at terminal.
type imTypingSession struct {
	stop chan struct{}
	once sync.Once
}

var imTypingSessions = struct {
	mu sync.Mutex
	m  map[string]*imTypingSession
}{m: map[string]*imTypingSession{}}

func imTypingKey(accID int64, pane string) string {
	return strconv.FormatInt(accID, 10) + "|" + normPaneID(pane)
}

const imTypingRenewInterval = 4 * time.Second // WeChat/TG typing auto-clears ~5s
const imTypingMaxLifetime = 12 * time.Minute  // backstop if a terminal finalize never arrives

// imTypingEnsure starts the typing loop for (account, pane) if not already
// running. Idempotent — safe to call on every reply item and every tool round.
func imTypingEnsure(accID int64, pane string, tr botTransport, peer botPeer) {
	if tr == nil || peer.empty() {
		return
	}
	key := imTypingKey(accID, pane)
	imTypingSessions.mu.Lock()
	if _, ok := imTypingSessions.m[key]; ok {
		imTypingSessions.mu.Unlock()
		return
	}
	s := &imTypingSession{stop: make(chan struct{})}
	imTypingSessions.m[key] = s
	imTypingSessions.mu.Unlock()
	imDebugf("[im] typing start account=%d pane=%s", accID, shortPaneID(pane))
	go imTypingRun(s, accID, pane, tr, peer)
}

// imTypingStop cancels the typing loop for (account, pane). Called when the turn
// reaches a terminal status. No-op if nothing is running.
func imTypingStop(accID int64, pane string) {
	key := imTypingKey(accID, pane)
	imTypingSessions.mu.Lock()
	s := imTypingSessions.m[key]
	delete(imTypingSessions.m, key)
	imTypingSessions.mu.Unlock()
	if s != nil {
		s.once.Do(func() { close(s.stop) })
		imDebugf("[im] typing stop account=%d pane=%s", accID, shortPaneID(pane))
	}
}

func imTypingRun(s *imTypingSession, accID int64, pane string, tr botTransport, peer botPeer) {
	// Fire immediately so the indicator shows the moment the reply starts.
	if err := tr.Typing(peer); err != nil {
		log.Printf("[im] typing send failed account=%d: %v", accID, err)
	}
	ticker := time.NewTicker(imTypingRenewInterval)
	defer ticker.Stop()
	safety := time.NewTimer(imTypingMaxLifetime)
	defer safety.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-safety.C:
			imDebugf("[im] typing safety-stop account=%d pane=%s", accID, shortPaneID(pane))
			imTypingStop(accID, pane)
			return
		case <-ticker.C:
			if err := tr.Typing(peer); err != nil {
				log.Printf("[im] typing renew failed account=%d: %v", accID, err)
			}
		}
	}
}

// imReplyItemID 取 reply.Items 中 cicy 序号字段（用于增量推送跟踪）。
// 字段名是 "id"（cicy 序号 1,2,3...）。tool_use 的原生 tool call ID 存在
// "tool_id" 字段（不是 "id"），避免冲突。
func imReplyItemID(item map[string]interface{}) int {
	switch v := item["id"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

// newReplyHooksForPane 创建当前 audit session 用的 reply hooks。
//   - isContinuation=true（tool 续传，同 turn 内续传 HTTP）：peek pending IM 列表，
//     不清空，让下一次续传 HTTP 还能拿到同一组 hook。
//   - isContinuation=false（新 user q）：drain pending IM 列表（拿完即清空），
//     让上次 turn 残留的 IM 绑定不会泄漏到下一个新 q。新 q 自己从 IM 发起时
//     imRegisterReplyPushForInbound 会重新 register。
func newReplyHooksForPane(agentID string, isContinuation bool) []aiGatewayReplyHook {
	var hooks []aiGatewayReplyHook
	hooks = append(hooks, peekCallbackHooksForPane(agentID)...)
	// Team knowledge memory-write hook — an independent peer of the reply
	// callback (its own enable flag, gated in finalize; never tied to audit).
	hooks = append(hooks, &memoryWriteHook{sourcePane: normPaneID(agentID)})
	// 注：老的 tgReplyPushHook（基于 agent_config.tg_token 字段）已废弃。
	// TG 走和 WeChat 一样的通用 imReplyPushHook 路径：
	//   imRegisterReplyPushForInbound (IM 进来时) → imPeek/Drain (这里) → attach hook
	// 这样保证只有真正从 IM 发起的对话才 push 回 IM；
	// 任何非 IM 来源（web UI / CLI / 其他 agent）即便 pane 绑了 IM 也不会"乱回复"。
	var imAccs map[int64]botPeer
	if isContinuation {
		imAccs = imPeekReplyPushAccountsForPane(agentID)
	} else {
		imAccs = imDrainReplyPushAccountsForPane(agentID)
		// drain 之后立刻把这些 acc 重新 register 回去，确保本 turn 内后续 tool 续传 HTTP
		// 仍能 attach 到同一组 hook。下一个新 q 来时再 drain 一次清掉。
		for accID, peer := range imAccs {
			imRegisterReplyPushForInbound(agentID, accID, peer)
		}
	}
	for accID, peer := range imAccs {
		acc, _ := imGetAccount(accID)
		if acc == nil {
			continue
		}
		tr := imTransportFor(accID)
		if tr == nil {
			continue
		}
		// Per-item 推送模式：TG / WeChat / 飞书都用同一种 hook，
		// 每个 reply item flush 时立即发送一条新消息，不做 streaming edit。
		// peer 用注册时记下的「发起会话」——多会话共用一个账号时回复各回各家；
		// 老数据没带 peer 时退回账号级 last peer。
		if peer.empty() {
			peer = imPeerForAccount(acc)
		}
		hooks = append(hooks, &imReplyPushHook{
			accID:     accID,
			paneID:    normPaneID(agentID),
			transport: tr,
			peer:      peer,
			canEdit:   tr.CanEdit(),
		})
		imDebugf("[im] reply hook attached account=%d pane=%s chat=%s transport=%s continuation=%t",
			accID, shortPaneID(agentID), peer.ChatID, tr.Kind(), isContinuation)
	}
	return hooks
}

func (h *imReplyPushHook) onItems(items []map[string]interface{}) {
	if h == nil || len(items) == 0 {
		return
	}
	// reply 开始输出即启动 typing loop（覆盖整个 turn：跨 tool 续传与客户端工具
	// 执行间隙,直到 finalize 看到终态才取消）。imTypingEnsure 幂等,每个 item /
	// 每个 tool 轮都可安全调用。
	imTypingEnsure(h.accID, h.paneID, h.transport, h.peer)
	// 每个 reply item 渲染一条 IM 消息立刻发送，不做 streaming edit。
	// 编辑式 transport（Telegram）和非编辑式（WeChat）行为保持一致。
	for _, item := range items {
		text := renderReplyItemForIM(item)
		if text == "" {
			continue
		}
		text = imClampMessage(text)
		// 回到发起这轮对话的会话(h.peer);兜底才用账号级 last peer。
		peer := h.peer
		if peer.empty() {
			acc, _ := imGetAccount(h.accID)
			if acc == nil {
				continue
			}
			peer = imPeerForAccount(acc)
		}
		if peer.empty() {
			continue
		}
		if _, err := imSendOutbound(imOutboundMessage{
			AccountID: h.accID,
			Transport: h.transport,
			Peer:      peer,
			Text:      text,
			Purpose:   imOutboundPurposeReply,
		}); err != nil {
			log.Printf("[im] reply per-item push failed account=%d type=%s err=%v", h.accID, item["type"], err)
			continue
		}
		imDebugf("[im] reply per-item push account=%d type=%s len=%d", h.accID, item["type"], len(text))
	}
}

// handleEvents 之前用于 streaming edit（同一条消息边收 SSE 边编辑）。
// 现在改成 per-item 推送（每个 item flush 一次 IM 消息），不再需要这条路径。
func (h *imReplyPushHook) handleEvents(_ []aiGatewayReplyEvent) {}

// finalize 之前在 HTTP 完成时 push 最终消息。
// 现在 per-item 模式下，最终消息已经在最后一个 item flush 时推过了，
// finalize 只清理状态。注意：**不**在这里 cancel pending push —— 一个 turn
// 可能包含多次 HTTP（tool 续传），cancel 应该等到整个 turn 结束（新 q 开始）才做。
func (h *imReplyPushHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	// finalize 每个 HTTP 轮都会触发。只有到达**终态**(completed/failed/idle…)
	// 才取消 typing;working/thinking/streaming/tool_use 等在途状态(包含一个
	// tool 续传轮刚结束、工具正在客户端跑的那一刻)继续保持 typing,直到整轮真正
	// 完成或失败。这与 worker 状态点同一个 busy 窗口语义。
	active := agentInspectorStatusIsActive(reply.Status)
	if !active {
		imTypingStop(h.accID, h.paneID)
	}
	imDebugf("[im] reply finalize account=%d turn=%s status=%s items=%d active=%t",
		h.accID, reply.TurnID, reply.Status, len(reply.Items), active)
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
	// Non-editable transports (WeChat) only send on finalize, and only the
	// answer text — never the "Thinking..." prefix or partial streams.
	if !h.canEdit && !h.closed {
		return
	}
	if !h.canEdit && strings.TrimSpace(h.answer.String()) == "" {
		imDebugf("[im] reply flush skipped account=%d (no answer for non-editable transport)", h.accID)
		return
	}
	h.mu.Lock()
	text := imClampMessage(h.renderLocked())
	h.lastPushAt = time.Now()
	h.mu.Unlock()

	peer := h.peer
	if peer.empty() {
		if acc, _ := imGetAccount(h.accID); acc != nil {
			peer = imPeerForAccount(acc)
		}
	}
	if peer.empty() {
		imDebugf("[im] reply flush skipped account=%d (no peer)", h.accID)
		return
	}
	st := imLiveStateFor(h.accID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastText == text {
		return
	}
	// Suppress rapid re-sends (tmux send retry loop can fire a second Enter
	// that triggers a garbage agent reply 2-5 s after the real one).
	if time.Since(st.lastSendTime) < imSendCooldown {
		imDebugf("[im] reply flush skipped account=%d (cooldown %v since last send)", h.accID, time.Since(st.lastSendTime).Round(time.Millisecond))
		return
	}
	if h.canEdit && strings.TrimSpace(st.messageID) != "" {
		if err := h.transport.Edit(peer, st.messageID, text); err == nil {
			st.lastText = text
			return
		}
		st.messageID = ""
	}
	res, err := imSendOutbound(imOutboundMessage{AccountID: h.accID, Transport: h.transport, Peer: peer, Text: text, Purpose: imOutboundPurposeReply})
	if err != nil {
		return
	}
	mid := res.MessageID
	st.messageID = mid
	st.lastText = text
	st.lastSendTime = time.Now()
}
