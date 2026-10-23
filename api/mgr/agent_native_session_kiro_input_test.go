// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// kiro-cli 2.31.x writes toolUse.input as a JSON object (older builds wrote an
// encoded string). Both must survive parsing — an object input used to fail
// unmarshal and drop the tool call, leaving nameless "tool_result" cards.
func TestKiroToolUseObjectInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"kind":"Prompt","data":{"content":[{"kind":"text","data":"go"}],"meta":{"timestamp":1}}}
{"kind":"AssistantMessage","data":{"content":[{"kind":"toolUse","data":{"name":"read","toolUseId":"t1","input":{"path":"/x","n":2}}},{"kind":"toolUse","data":{"name":"shell","toolUseId":"t2","input":"{\"command\":\"ls\"}"}}],"meta":{"timestamp":2}}}
{"kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t1","status":"success","content":[{"kind":"text","data":"ok"}]}}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	items, _, err := kiroSessionProvider{}.parseItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("items = %+v", items)
	}
	blocks := aiGatewaySlice(items[1]["content"])
	if len(blocks) != 2 {
		t.Fatalf("want 2 tool_use blocks, got %+v", blocks)
	}
	b0, b1 := aiGatewayMap(blocks[0]), aiGatewayMap(blocks[1])
	if aiGatewayString(b0["name"]) != "read" || aiGatewayString(b0["id"]) != "t1" {
		t.Errorf("object-input tool_use = %+v", b0)
	}
	if in := aiGatewayMap(b0["input"]); aiGatewayString(in["path"]) != "/x" {
		t.Errorf("object input not decoded: %+v", b0["input"])
	}
	if in := aiGatewayMap(b1["input"]); aiGatewayString(b1["name"]) != "shell" || aiGatewayString(in["command"]) != "ls" {
		t.Errorf("string-input tool_use = %+v", b1)
	}

	recs, err := kiroParseJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	for _, r := range recs {
		for _, tc := range r.ToolCalls {
			calls++
			if tc.Name == "read" && tc.Input != `{"path":"/x","n":2}` {
				t.Errorf("record input = %q", tc.Input)
			}
		}
	}
	if calls != 2 {
		t.Errorf("want 2 tool calls in records, got %d: %+v", calls, recs)
	}
}
