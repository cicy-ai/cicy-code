// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWaitForAgentInputReadyChecksCodexPaneBeforeSending(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix tmux command shim test")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux-calls.log")
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"printf 'OpenAI Codex (v0.1)\\ndirectory: ~/cicy-ai/workers/w-test\\n› \\n· 100%% left ~/cicy-ai/workers/w-test\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := waitForAgentInputReady("w-test:main.0", "codex", nil); err != nil {
		t.Fatalf("ready Codex pane was rejected: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("Codex readiness was not checked before send: %v", err)
	}
}
