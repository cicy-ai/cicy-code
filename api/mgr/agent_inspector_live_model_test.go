package main

import "testing"

// A non-gateway pane's reply.json is only written while the MITM audit is
// following the turn; once a turn ends in `failed` it stops being updated and
// keeps reporting whatever model was in flight at that moment. usage.jsonl gets
// one record per request regardless, so the header model for those panes has to
// come from the log — otherwise a pane that failed once shows a model from hours
// ago forever (w-101 sat on deepseek-v4-pro through 132 claude-opus-5 requests).
func TestAgentInspectorLiveModelPrefersUsageLogForNonGatewayPane(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	const nonGateway = "w-9101"
	const gateway = "w-9102"
	for paneID, useCustomGateway := range map[string]int{nonGateway: 0, gateway: 1} {
		if _, err := store.Exec(
			"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, use_custom_gateway) VALUES (?,?,?,?,?,?,?,?,?)",
			normPaneID(paneID), paneID, "/tmp/"+paneID, "", "{}", "worker", "", "claude", useCustomGateway,
		); err != nil {
			t.Fatalf("insert %s: %v", paneID, err)
		}
	}

	stale := aiGatewayReplySnapshot{Model: "deepseek-v4-pro"}
	for _, paneID := range []string{nonGateway, gateway} {
		aiGatewayAppendUsageLog(paneID, agentUsageLogRecord{
			TS:     "2026-08-30T16:16:11Z",
			Model:  "claude-opus-5",
			Status: "completed",
		})
	}

	if got := agentInspectorLiveModel(nonGateway, stale); got != "claude-opus-5" {
		t.Fatalf("non-gateway pane: want the logged model claude-opus-5, got %q", got)
	}
	if got := agentInspectorLiveModel(gateway, stale); got != "deepseek-v4-pro" {
		t.Fatalf("gateway pane: want the reply snapshot model deepseek-v4-pro, got %q", got)
	}
}

// With no usage log yet (a pane that has not served a request since it was
// created) the reply snapshot stays the answer — the fix must not blank the
// model out.
func TestAgentInspectorLiveModelFallsBackToReplyWithoutUsageLog(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	const paneID = "w-9103"
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, use_custom_gateway) VALUES (?,?,?,?,?,?,?,?,?)",
		normPaneID(paneID), paneID, "/tmp/"+paneID, "", "{}", "worker", "", "claude", 0,
	); err != nil {
		t.Fatalf("insert %s: %v", paneID, err)
	}

	if got := agentInspectorLiveModel(paneID, aiGatewayReplySnapshot{Model: "claude-opus-5"}); got != "claude-opus-5" {
		t.Fatalf("want claude-opus-5 from the reply snapshot, got %q", got)
	}
}

// Claude Code fires auxiliary calls (aux_kind "sidechain": Task subagents,
// post-turn housekeeping) on a cheaper model right after a turn, so the literal
// last usage line is often haiku. The live model — and the roster summary the
// hub / mobile read — must skip those and report the mainline model.
func TestAgentInspectorLiveModelSkipsAuxiliarySidechainRecords(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	const paneID = "w-9104"
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, use_custom_gateway) VALUES (?,?,?,?,?,?,?,?,?)",
		normPaneID(paneID), paneID, "/tmp/"+paneID, "", "{}", "worker", "", "claude", 0,
	); err != nil {
		t.Fatalf("insert %s: %v", paneID, err)
	}
	aiGatewayAppendUsageLog(paneID, agentUsageLogRecord{TS: "2026-09-08T03:02:05Z", Model: "claude-fable-5-1", Status: "completed", CostCredit: 0.3})
	aiGatewayAppendUsageLog(paneID, agentUsageLogRecord{TS: "2026-09-08T03:02:07Z", Model: "claude-haiku-4-5-20251001", Status: "completed", AuxKind: "sidechain", CostCredit: 0.01})

	if got := agentInspectorLiveModel(paneID, aiGatewayReplySnapshot{Model: "claude-fable-5-1"}); got != "claude-fable-5-1" {
		t.Fatalf("live model: want mainline claude-fable-5-1, got %q", got)
	}
	_, model, cost := agentUsageRuntimeSummary(paneID)
	if model == nil || *model != "claude-fable-5-1" {
		t.Fatalf("runtime summary model: want claude-fable-5-1, got %v", model)
	}
	// aux spend still bills to the pane
	if cost == nil || *cost < 0.31-1e-9 {
		t.Fatalf("runtime summary cost must include aux spend, got %v", cost)
	}
	// only aux records so far → no mainline model, fall back to the reply
	const auxOnly = "w-9105"
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, use_custom_gateway) VALUES (?,?,?,?,?,?,?,?,?)",
		normPaneID(auxOnly), auxOnly, "/tmp/"+auxOnly, "", "{}", "worker", "", "claude", 0,
	); err != nil {
		t.Fatalf("insert %s: %v", auxOnly, err)
	}
	aiGatewayAppendUsageLog(auxOnly, agentUsageLogRecord{TS: "2026-09-08T03:02:07Z", Model: "claude-haiku-4-5-20251001", Status: "completed", AuxKind: "sidechain"})
	if got := agentInspectorLiveModel(auxOnly, aiGatewayReplySnapshot{Model: "claude-opus-4-8"}); got != "claude-opus-4-8" {
		t.Fatalf("aux-only log: want the reply model claude-opus-4-8, got %q", got)
	}
}
