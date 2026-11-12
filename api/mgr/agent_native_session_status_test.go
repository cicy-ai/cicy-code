// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"
	"time"
)

func seedKiroPane(t *testing.T, pane, ws string) {
	t.Helper()
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, active) VALUES (?,?,?,?,?,?,?,?,?,?,1)",
		pane+":main.0", pane, ws, "", "{}", "", "", "kiro-cli", true, true); err != nil {
		t.Fatal(err)
	}
}

// A kiro agent had no poll status at all; its card ran blind.
func TestNativeSessionLiteMetricsPublishesKiroStatus(t *testing.T) {
	withTestStore(t)
	kiroHome := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)
	kiroSessionIndex.Lock()
	kiroSessionIndex.ids = nil
	kiroSessionIndex.Unlock()
	dir := filepath.Join(kiroHome, "sessions", "cli")

	busyWS, idleWS := t.TempDir(), t.TempDir()
	seedKiroPane(t, "w-7001", busyWS)
	seedKiroPane(t, "w-7002", idleWS)
	writeKiroSession(t, dir, "busy", busyWS, []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"deploy it"}],"meta":{"timestamp":10}}}`,
	})
	writeKiroSession(t, dir, "idle", idleWS, []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"ping"}],"meta":{"timestamp":10}}}`,
		`{"kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"pong"}],"meta":{"timestamp":11}}}`,
	})

	busy := nativeSessionLiteMetrics("w-7001")
	if busy == nil || busy["status"] != "working" || busy["complete"] != false || busy["latest_question"] != "deploy it" {
		t.Fatalf("unanswered prompt must read working: %+v", busy)
	}
	idle := nativeSessionLiteMetrics("w-7002")
	if idle == nil || idle["status"] != "idle" || idle["complete"] != true {
		t.Fatalf("answered turn must read idle: %+v", idle)
	}
	statuses := pollAgentStatuses("w-7001", nil)
	if m, _ := statuses["w-7001:main.0"].(M); m == nil || m["status"] != "working" {
		t.Fatalf("poll_data must carry the kiro status: %+v", statuses)
	}
	// Not a native-session agent → the reply.json path stays in charge.
	if nativeSessionLiteMetrics("w-7999") != nil {
		t.Fatal("unknown agent must not get a native status")
	}
}

func TestNativeLiteMetricsSuspendedOrStaleIsIdle(t *testing.T) {
	v := nativeSessionView{SessionID: "s", ModTime: time.Now(), Items: []map[string]interface{}{
		{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "go"}}},
	}}
	if m := v.liteMetrics(false); m["status"] != "working" {
		t.Fatalf("live unanswered prompt: %+v", m)
	}
	if m := v.liteMetrics(true); m["status"] != "idle" || m["complete"] != true {
		t.Fatalf("a minimized agent runs no CLI — it cannot be working: %+v", m)
	}
	v.ModTime = time.Now().Add(-nativeInFlightMaxAge - time.Minute)
	if m := v.liteMetrics(false); m["status"] != "idle" {
		t.Fatalf("a stale log must not lock the card: %+v", m)
	}
}

// Ctrl+C while a tool runs: no request is in flight, so nothing ever finalized
// reply.json and the card stayed "working" — every later message was queued.
func TestSealInterruptedToolRun(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-7010"
	past := time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339)
	seed := func(status, updated string) {
		t.Helper()
		if err := aiGatewayWriteReplySnapshot(agent, aiGatewayReplySnapshot{
			TurnID: "t1", ConversationID: "c1", Status: status, StartedAt: past, UpdatedAt: updated,
			LastStopReason: "tool_use",
			Items:          []map[string]interface{}{{"id": 1, "type": "tool_use", "name": "Bash"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("working", past)
	if !aiGatewaySealInterruptedReply(agent, time.Now()) {
		t.Fatal("a working reply untouched since the interrupt must be sealed")
	}
	got, err := aiGatewayReadReplySnapshotFile(agent)
	if err != nil || got.Status != "completed" || got.LastStopReason != "" {
		t.Fatalf("sealed reply: status=%q stop=%q err=%v", got.Status, got.LastStopReason, err)
	}
	if last := got.Items[len(got.Items)-1]; last["cicy_outcome"] != "cancelled" {
		t.Fatalf("the stop must be recorded as a user cancel: %+v", last)
	}
	if m := agentInspectorLiteMetrics(agent); m == nil || m["complete"] != true {
		t.Fatalf("card status must be terminal after the seal: %+v", m)
	}
	// Already terminal → untouched.
	if aiGatewaySealInterruptedReply(agent, time.Now()) {
		t.Fatal("a finished reply must not be sealed again")
	}
	// Written after the interrupt → a live turn, leave it alone.
	seed("working", time.Now().Add(5*time.Second).UTC().Format(time.RFC3339))
	if aiGatewaySealInterruptedReply(agent, time.Now()) {
		t.Fatal("a reply updated after the interrupt is live and must be left alone")
	}
}

// kiro stores shell output as a json item; reading only text items left every
// tool card in the chat view empty.
func TestKiroToolResultTextReadsJSONOutput(t *testing.T) {
	kiroHome := t.TempDir()
	ws := t.TempDir()
	dir := filepath.Join(kiroHome, "sessions", "cli")
	writeKiroSession(t, dir, "sid", ws, []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"run"}],"meta":{"timestamp":10}}}`,
		`{"kind":"AssistantMessage","data":{"content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"ls"}}}],"meta":{"timestamp":11}}}`,
		`{"kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t1","status":"success","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"a.txt\nb.txt\n","stderr":""}}]}}],"meta":{"timestamp":12}}}`,
		`{"kind":"AssistantMessage","data":{"content":[{"kind":"toolUse","data":{"toolUseId":"t2","name":"shell","input":{"command":"false"}}}],"meta":{"timestamp":13}}}`,
		`{"kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t2","status":"error","content":[{"kind":"json","data":{"exit_status":"exit status: 1","stdout":"","stderr":"boom"}}]}}],"meta":{"timestamp":14}}}`,
	})
	items, _, err := kiroSessionProvider{}.parseItems(filepath.Join(dir, "sid.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	result := func(i int) string {
		block := aiGatewayMap(aiGatewaySlice(items[i]["content"])[0])
		return aiGatewayString(block["content"])
	}
	if got := result(2); got != "a.txt\nb.txt" {
		t.Fatalf("stdout must be the tool output, got %q", got)
	}
	if got := result(4); got != "boom\n[exit status: 1]" {
		t.Fatalf("stderr and a failing exit status must show, got %q", got)
	}
	if clipped := kiroClipToolResult(string(make([]rune, kiroToolResultMaxRunes+5))); len([]rune(clipped)) >= kiroToolResultMaxRunes+50 {
		t.Fatal("oversized output must be clipped")
	}
}
