package main

import "testing"

// helper: find an unpaired tool_use (orphan) anywhere in the window.
func hasOrphanToolUse(msgs []M) bool {
	for i, m := range msgs {
		ids := dispatcherToolUseIDs(m)
		if len(ids) == 0 {
			continue
		}
		have := map[string]bool{}
		if i+1 < len(msgs) {
			have = dispatcherToolResultIDs(msgs[i+1])
		}
		for _, id := range ids {
			if !have[id] {
				return true
			}
		}
	}
	return false
}

// Reproduces the production brick: an assistant tool_use immediately followed by
// a new user STRING message (turn interrupted before the tool resolved). The
// balancer must pair it; the result window must be orphan-free.
func TestBalanceHealsMidHistoryOrphanToolUse(t *testing.T) {
	msgs := []M{
		{"role": "user", "content": "查一下成本"},
		{"role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": ""},
			map[string]interface{}{"type": "tool_use", "id": "call_orphan_1", "name": "todo_list", "input": map[string]interface{}{}},
		}},
		// interruption: a fresh user message, NOT a tool_result
		{"role": "user", "content": "📮 [w-10065] 新指令"},
	}
	if !hasOrphanToolUse(msgs) {
		t.Fatal("setup should contain an orphan tool_use")
	}
	out := dispatcherBalanceToolCalls(msgs)
	if hasOrphanToolUse(out) {
		t.Fatal("balancer left an orphan tool_use — provider would 400")
	}
	// The synthetic tool_result must be paired AND the original user text kept.
	paired := dispatcherToolResultIDs(out[2])
	if !paired["call_orphan_1"] {
		t.Error("synthetic tool_result for call_orphan_1 not in the following message")
	}
	var keptText bool
	dispatcherForEachBlock(out[2], func(bm map[string]interface{}, typ string) {
		if typ == "text" {
			if s, _ := bm["text"].(string); s == "📮 [w-10065] 新指令" {
				keptText = true
			}
		}
	})
	if !keptText {
		t.Error("original user text was dropped during merge")
	}
}

// Orphan tool_use as the LAST message (no following turn) → fresh user turn.
func TestBalanceHealsTrailingOrphanToolUse(t *testing.T) {
	msgs := []M{
		{"role": "user", "content": "go"},
		{"role": "assistant", "content": []M{
			{"type": "tool_use", "id": "call_tail", "name": "agent_list", "input": map[string]interface{}{}},
		}},
	}
	out := dispatcherBalanceToolCalls(msgs)
	if hasOrphanToolUse(out) {
		t.Fatal("trailing orphan tool_use not healed")
	}
	if len(out) != 3 || func() bool { r, _ := out[2]["role"].(string); return r != "user" }() {
		t.Fatalf("expected a fresh trailing user message, got %v", out)
	}
}

// A HEALTHY history (every tool_use already paired) must be returned unchanged
// in structure — no spurious injections.
func TestBalanceLeavesHealthyHistoryUntouched(t *testing.T) {
	msgs := []M{
		{"role": "user", "content": "查"},
		{"role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "tool_use", "id": "c1", "name": "todo_list", "input": map[string]interface{}{}},
		}},
		{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "c1", "content": "ok"},
		}},
		{"role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "好了"},
		}},
	}
	out := dispatcherBalanceToolCalls(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("healthy history length changed %d → %d (spurious injection)", len(msgs), len(out))
	}
	if hasOrphanToolUse(out) {
		t.Fatal("healthy history reported orphan")
	}
}

// Multiple tool_use in one assistant turn with a PARTIAL result set: only the
// missing id gets a synthetic result; the present one is left as-is.
func TestBalancePartialResultsOnlyFillsMissing(t *testing.T) {
	msgs := []M{
		{"role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "tool_use", "id": "a", "name": "x", "input": map[string]interface{}{}},
			map[string]interface{}{"type": "tool_use", "id": "b", "name": "y", "input": map[string]interface{}{}},
		}},
		{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "a", "content": "done"},
		}},
	}
	out := dispatcherBalanceToolCalls(msgs)
	if hasOrphanToolUse(out) {
		t.Fatal("partial-result orphan not healed")
	}
	res := dispatcherToolResultIDs(out[1])
	if !res["a"] || !res["b"] {
		t.Errorf("both a and b must be paired after balance, got %v", res)
	}
}
