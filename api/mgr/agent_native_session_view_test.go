// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKiroParseItemsChatShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"kind":"Prompt","data":{"content":[{"kind":"text","data":"test"}],"meta":{"timestamp":10}}}
{"kind":"AssistantMessage","data":{"content":[{"kind":"thinking","data":{"text":"","modelId":"auto"}},{"kind":"text","data":"hi"},{"kind":"toolUse","data":{"name":"read","toolUseId":"t1","input":"{\"path\":\"/x\"}"}}],"meta":{"timestamp":11}}}
{"kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t1","status":"success","content":[{"kind":"text","data":"ok"}]}}]}}
{"kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"done"}],"meta":{"timestamp":12}}}
{"kind":"Prompt","data":{"content":[{"kind":"te`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	items, model, err := kiroSessionProvider{}.parseItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if model != "auto" {
		t.Errorf("model = %q", model)
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	if len(items) != len(wantRoles) {
		t.Fatalf("want %d items, got %d: %+v", len(wantRoles), len(items), items)
	}
	for i, r := range wantRoles {
		if got := aiGatewayString(items[i]["role"]); got != r {
			t.Errorf("item %d role = %q want %q", i, got, r)
		}
	}
	// empty redacted thinking is dropped; text + tool_use kept
	blocks := aiGatewaySlice(items[1]["content"])
	if len(blocks) != 2 || aiGatewayString(aiGatewayMap(blocks[1])["type"]) != "tool_use" {
		t.Errorf("assistant blocks = %+v", blocks)
	}
	if aiGatewayString(aiGatewayMap(aiGatewaySlice(items[2]["content"])[0])["type"]) != "tool_result" {
		t.Errorf("tool result not mapped: %+v", items[2])
	}

	v := nativeSessionView{SessionID: "sid", Model: model, Items: items, ModTime: time.Now()}
	if p := v.prompts(); len(p) != 1 || p[0].ID != 1 || p[0].Content != "test" {
		t.Errorf("prompts = %+v", p)
	}
	page := v.window(5, 0)
	if len(page.Items) != 4 || page.HasMore || page.ConversationID != "sid" {
		t.Errorf("window = %+v", page)
	}
	if aiGatewayInt(page.Items[3]["history_id"]) != 4 {
		t.Errorf("history_id = %v", page.Items[3]["history_id"])
	}
	if v.inFlight() {
		t.Error("last item is assistant → must not be in flight")
	}
	rp := v.replyPayload("w-1")
	if rp["complete"] != true || aiGatewayInt(rp["history_id"]) != 5 {
		t.Errorf("reply payload = %+v", rp)
	}

	// trailing user question → in flight
	v.Items = append(v.Items, map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "next"}}})
	if !v.inFlight() || v.replyPayload("w-1")["complete"] != false {
		t.Error("trailing question should read as in flight")
	}
	// stale log never locks the composer
	v.ModTime = time.Now().Add(-time.Hour)
	if v.inFlight() {
		t.Error("stale log must not read as in flight")
	}
}

func TestKiroWindowPaging(t *testing.T) {
	items := make([]map[string]interface{}, 0, 12)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		items = append(items, map[string]interface{}{"role": role, "content": []interface{}{map[string]interface{}{"type": "text", "text": "x"}}})
	}
	v := nativeSessionView{SessionID: "s", Items: items}
	p := v.window(5, 0)
	if len(p.Items) != 5 || !p.HasMore || p.NextBefore != 8 {
		t.Fatalf("first page = items %d hasMore %v next %d", len(p.Items), p.HasMore, p.NextBefore)
	}
	p2 := v.window(5, p.NextBefore)
	if aiGatewayInt(p2.Items[len(p2.Items)-1]["history_id"]) != 7 {
		t.Fatalf("second page tail id = %v", p2.Items[len(p2.Items)-1]["history_id"])
	}
}
