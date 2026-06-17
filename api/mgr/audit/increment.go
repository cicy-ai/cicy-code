package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// increment.go — incremental outbound scanning.
//
// The outbound request (current.json) carries the FULL conversation history and
// is re-sent every turn, so scanning the whole body re-matches the same content
// endlessly — the same secret floods the log forever, and history is rescanned
// for no gain. The root fix (not "stop scanning outbound", which throws away
// real detection of newly-sent secrets) is to scan ONLY the messages that are
// NEW this turn.
//
// Each message block is content-addressed (sha256 of its raw JSON). A per-agent
// set remembers which blocks were already scanned; IncrementalOutboundPayload
// returns a synthetic {"messages":[…]} body containing only the unseen blocks,
// so the scanner sees just the delta. A brand-new secret in a new message is
// still scanned and caught; an old secret sitting in history is scanned exactly
// once (the turn it first appeared) and never again.
//
// State is in-memory and process-scoped: after a restart each agent re-baselines
// once (one event over the then-current history), not a per-turn flood.

type outboundSeenState struct {
	mu     sync.Mutex
	perAgt map[string]map[string]struct{}
	order  []agentHash // FIFO for the global cap
	cap    int
}

type agentHash struct{ agent, hash string }

var outboundSeen = &outboundSeenState{
	perAgt: make(map[string]map[string]struct{}),
	cap:    200000,
}

// record marks (agent, hash) seen, enforcing the global FIFO cap. Caller holds
// the lock.
func (s *outboundSeenState) recordLocked(agent, hash string) {
	set := s.perAgt[agent]
	if set == nil {
		set = make(map[string]struct{})
		s.perAgt[agent] = set
	}
	if _, ok := set[hash]; ok {
		return
	}
	set[hash] = struct{}{}
	s.order = append(s.order, agentHash{agent, hash})
	if len(s.order) > s.cap {
		old := s.order[0]
		s.order = s.order[1:]
		if m := s.perAgt[old.agent]; m != nil {
			delete(m, old.hash)
		}
	}
}

// baselineIfFirst handles the FIRST time we ever see an agent (since process
// start): it marks every current message as seen WITHOUT scanning and returns
// true. This is the "never rescan history" guarantee across restarts — the
// accumulated conversation present at startup is treated as already-known, so a
// restart never re-flags the existing history; only messages that arrive AFTER
// we start watching get scanned. Returns false if the agent was already
// initialised (normal incremental path applies).
func (s *outboundSeenState) baselineIfFirst(agent string, hashes []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.perAgt[agent]; ok {
		return false
	}
	s.perAgt[agent] = make(map[string]struct{})
	for _, h := range hashes {
		s.recordLocked(agent, h)
	}
	return true
}

// newHashes returns, and records, the subset of hashes not seen before for the
// agent (normal incremental path, after baseline).
func (s *outboundSeenState) newHashes(agent string, hashes []string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := make(map[string]bool, len(hashes))
	set := s.perAgt[agent]
	for _, h := range hashes {
		if set != nil {
			if _, ok := set[h]; ok {
				continue
			}
		}
		fresh[h] = true
		s.recordLocked(agent, h)
		set = s.perAgt[agent]
	}
	return fresh
}

// IncrementalOutboundPayload reduces a full outbound request body to only the
// message blocks that are NEW for this agent. Returns:
//   - the synthetic delta body ({"messages":[…new…]}) when there is new content,
//   - nil when nothing is new (caller should skip submitting — no rescan),
//   - the original body unchanged when it can't be parsed as a messages request
//     (fail-open: better to scan once than miss a non-standard body).
func IncrementalOutboundPayload(agentID string, body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	// Anthropic /v1/messages -> "messages"; OpenAI Responses -> "input".
	raw, ok := req["messages"]
	if !ok {
		raw, ok = req["input"]
	}
	if !ok {
		return body
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil || len(arr) == 0 {
		return body
	}
	hashes := make([]string, len(arr))
	for i, m := range arr {
		sum := sha256.Sum256(m)
		hashes[i] = hex.EncodeToString(sum[:12])
	}
	// First time we see this agent: baseline the existing history as known and
	// scan nothing (never rescan accumulated history — including across restarts).
	if outboundSeen.baselineIfFirst(agentID, hashes) {
		return nil
	}
	fresh := outboundSeen.newHashes(agentID, hashes)
	if len(fresh) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(fresh))
	for i, m := range arr {
		if fresh[hashes[i]] {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	body2, err := json.Marshal(map[string]interface{}{"messages": out})
	if err != nil {
		return body
	}
	return body2
}

// OutboundUserPromptOnly keeps ONLY the user's authored prompt(s) (the "q") from a
// messages payload, dropping assistant messages and user tool_result blocks.
//
// 审"未发生": outbound audit scans only what the human/agent NEWLY sends from its
// own side — the user's prompt. The model's proposed tool_use is audited on the
// INBOUND reply (where it is 未发生 — not yet executed); the tool_use/tool_result
// carried back in continuation requests is 已发生 history, so re-scanning it on
// outbound would double-flag the same action. Returns nil when nothing
// user-authored remains. A non-"messages" body (e.g. Responses-API "input") is
// returned unchanged (best-effort; cicy/claude use "messages").
func OutboundUserPromptOnly(body []byte) []byte {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	raw, ok := req["messages"]
	if !ok {
		return body // not a messages-shaped body — leave as-is
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil || len(arr) == 0 {
		return nil
	}
	kept := make([]json.RawMessage, 0, len(arr))
	for _, m := range arr {
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(m, &msg) != nil {
			continue
		}
		if msg.Role != "user" {
			continue // assistant = model output (audited inbound) → drop
		}
		if userContentIsToolResultOnly(msg.Content) {
			continue // tool_result = 已发生 execution result → drop
		}
		kept = append(kept, m)
	}
	if len(kept) == 0 {
		return nil
	}
	out2, err := json.Marshal(map[string]interface{}{"messages": kept})
	if err != nil {
		return body
	}
	return out2
}

// userContentIsToolResultOnly reports whether a user message's content is made up
// ENTIRELY of tool_result blocks (no human-authored text/image). String content
// (a plain prompt) is never tool-result-only.
func userContentIsToolResultOnly(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false // string content (a plain prompt) — keep
	}
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}
