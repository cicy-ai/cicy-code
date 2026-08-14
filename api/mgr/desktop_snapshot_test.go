// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestExtractSnapshotB64(t *testing.T) {
	const want = "/9j/desktop-jpeg-base64"
	tests := []struct {
		name string
		in   interface{}
	}{
		{name: "raw", in: want},
		{name: "legacy object", in: map[string]interface{}{"base64": want}},
		{name: "mcp content", in: map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": want}},
		}},
		{name: "json encoded mcp content", in: `{"content":[{"type":"text","text":"` + want + `"}]}`},
		{name: "nested result", in: map[string]interface{}{
			"result": map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": want}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractSnapshotB64(tt.in); got != want {
				t.Fatalf("extractSnapshotB64() = %q, want %q", got, want)
			}
		})
	}
}
