// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWaitForAgentInputReadyDoesNotDelayCodexSend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix tmux command shim test")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux-calls.log")
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := make(chan error, 1)
	go func() { result <- waitForAgentInputReady("w-test:main.0", "codex", nil) }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Codex send was delayed by readiness detection: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Codex send waited for terminal readiness")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("Codex send unexpectedly invoked tmux readiness detection: %v", err)
	}
}
