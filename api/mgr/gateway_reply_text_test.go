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

func TestRenderReplyItemForIMHidesTechnicalToolError(t *testing.T) {
	got := renderReplyItemForIM(map[string]interface{}{
		"type": "tool_error", "name": "fetch", "error": "dial tcp 127.0.0.1:9001: connection reset by peer",
	})
	if got != "" {
		t.Fatalf("technical tool failure should be skipped, rendered as %q", got)
	}
}
