// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

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
	} {
		got := renderReplyItemForIM(map[string]interface{}{"type": "text", "text": detail})
		if got != "⚠️ 服务连接暂时中断，正在重试。" {
			t.Fatalf("technical failure rendered as %q", got)
		}
		if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "broken pipe") {
			t.Fatalf("technical details leaked to IM: %q", got)
		}
	}
}

func TestRenderReplyItemForIMHidesTechnicalToolError(t *testing.T) {
	got := renderReplyItemForIM(map[string]interface{}{
		"type": "tool_error", "name": "fetch", "error": "dial tcp 127.0.0.1:9001: connection reset by peer",
	})
	if got != "❌ fetch 执行失败，请稍后重试。" {
		t.Fatalf("technical tool failure rendered as %q", got)
	}
}
