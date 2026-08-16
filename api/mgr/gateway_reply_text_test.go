// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestRenderReplyItemForIMSkipsToolUse(t *testing.T) {
	for _, name := range []string{"wait", "exec", "write_stdin", "read", "apply_patch"} {
		got := renderReplyItemForIM(map[string]interface{}{
			"type":  "tool_use",
			"name":  name,
			"input": map[string]interface{}{"command": "secret internal detail"},
		})
		if got != "" {
			t.Fatalf("tool %q rendered to IM: %q", name, got)
		}
	}
}

func TestRenderReplyItemForIMKeepsUserText(t *testing.T) {
	got := renderReplyItemForIM(map[string]interface{}{"type": "text", "text": "  done  "})
	if got != "done" {
		t.Fatalf("text rendered as %q, want done", got)
	}
}

func TestRenderReplyItemForIMHidesTechnicalTransportFailure(t *testing.T) {
	for _, detail := range []string{
		"⚠️ 生成失败（HTTP 502）\n\nwrite tcp 127.0.0.1:62059->127.0.0.1:9001: write: broken pipe",
		"⚠️ 生成失败（HTTP 502）\n\nuse of closed network connection",
		"⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC",
		"⚠️ 生成失败（HTTP 502）\n\nEOF",
	} {
		got := renderReplyItemForIM(map[string]interface{}{"type": "text", "text": detail})
		if got != "" {
			t.Fatalf("technical failure should be skipped, rendered as %q", got)
		}
	}
}

func TestGatewayFiltersEveryTechnicalTransportFailureBeforePublishing(t *testing.T) {
	items := []map[string]interface{}{
		{"type": "text", "text": "正常回答"},
		{"type": "text", "text": "⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC"},
		{"type": "text", "text": "⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC"},
	}
	got := aiGatewayFilterTechnicalTransportFailures(items)
	if len(got) != 1 || got[0]["text"] != "正常回答" {
		t.Fatalf("technical transport failures reached publish payload: %#v", got)
	}
}

func TestGatewayDropsTechnicalTransportFailureBeforeStreamingFlush(t *testing.T) {
	s := &aiGatewayAuditSession{
		auxiliary: true,
		pendingItem: &aiGatewayPendingItem{
			Kind: "text",
			Text: "⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC",
		},
	}

	if item := s.pendingItemAsMapLocked(1); item != nil {
		t.Fatalf("technical failure reached live reply snapshot: %#v", item)
	}
	s.flushPendingItemLocked()
	if len(s.reply.Items) != 0 {
		t.Fatalf("technical failure reached flushed reply items: %#v", s.reply.Items)
	}
}

func TestGatewaySanitizesTechnicalTransportFailureAtReplyWriteBoundary(t *testing.T) {
	reply := aiGatewaySanitizeReplySnapshot(aiGatewayReplySnapshot{
		Answer: "local error: tls: bad record MAC",
		Items: []map[string]interface{}{
			{"type": "text", "text": "正常回答"},
			{"type": "text", "text": "⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC"},
		},
	})
	if reply.Answer != "" {
		t.Fatalf("technical failure answer reached reply writer: %q", reply.Answer)
	}
	if len(reply.Items) != 1 || reply.Items[0]["text"] != "正常回答" {
		t.Fatalf("reply writer kept technical failure: %#v", reply.Items)
	}
}

func TestGatewayFiltersNoopPollingToolsAtSource(t *testing.T) {
	cases := []struct {
		name string
		item map[string]interface{}
		hide bool
	}{
		{"wait", map[string]interface{}{"type": "tool_use", "name": "wait", "input": map[string]interface{}{"cell_id": "51"}}, true},
		{"empty write stdin", map[string]interface{}{"type": "tool_use", "name": "write_stdin", "input": map[string]interface{}{"session_id": 7, "chars": ""}}, true},
		{"wrapped empty write stdin", map[string]interface{}{"type": "tool_use", "name": "exec", "input": `const r = await tools.write_stdin({session_id:79666,chars:"",yield_time_ms:1000}); text(r.output);`}, true},
		{"wrapped wait", map[string]interface{}{"type": "tool_use", "name": "exec", "input": `const r = await tools.wait({cell_id:"51",yield_time_ms:10000}); text(r.output);`}, true},
		{"write stdin with input", map[string]interface{}{"type": "tool_use", "name": "write_stdin", "input": map[string]interface{}{"session_id": 7, "chars": "yes\n"}}, false},
		{"wrapped write stdin with input", map[string]interface{}{"type": "tool_use", "name": "exec", "input": `const r = await tools.write_stdin({session_id:7,chars:"yes\\n"}); text(r.output);`}, false},
		{"real command", map[string]interface{}{"type": "tool_use", "name": "exec", "input": `const r = await tools.exec_command({cmd:"npm run build"}); text(r.output);`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aiGatewayIsNoopPollingTool(tc.item); got != tc.hide {
				t.Fatalf("hide = %v, want %v", got, tc.hide)
			}
		})
	}

	items := []map[string]interface{}{
		cases[2].item,
		cases[4].item,
	}
	got := aiGatewayFilterTechnicalTransportFailures(items)
	if len(got) != 1 || got[0]["name"] != "write_stdin" {
		t.Fatalf("noop polling tool reached conversation items: %#v", got)
	}
}

func TestRenderReplyItemForIMHidesTechnicalToolError(t *testing.T) {
	got := renderReplyItemForIM(map[string]interface{}{
		"type": "tool_error", "name": "fetch", "error": "dial tcp 127.0.0.1:9001: connection reset by peer",
	})
	if got != "" {
		t.Fatalf("technical tool failure should be skipped, rendered as %q", got)
	}
}
