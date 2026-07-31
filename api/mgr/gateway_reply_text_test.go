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
