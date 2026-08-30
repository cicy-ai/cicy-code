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
