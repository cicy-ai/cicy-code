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

func TestCodexInputReadyRejectsVisibleBusyComposer(t *testing.T) {
	out := `• Working (23s • esc to interrupt)

• Messages to be submitted after next tool call (press esc to interrupt and send immediately)
  ↳ queued prompt

› another prompt

  tab to queue message                                      26% context left ~/cicy-ai/workers/w-test`
	if isCodexInputReady(out) {
		t.Fatal("busy Codex composer must stay in the CiCy pane queue")
	}
}

func TestDeferredCodexSendUsesVisibleBusyComposerQueue(t *testing.T) {
	out := `• Working (23s • esc to interrupt)

› another prompt

  tab to queue message                                      26% context left ~/cicy-ai/workers/w-test`
	if !canQueuePromptWhileBusy("codex", out, deferredAgentReadyTimeout) {
		t.Fatal("deferred Codex send should use the visible busy composer queue")
	}
}

func TestSynchronousCodexSendStillWaitsForIdle(t *testing.T) {
	out := `• Working (23s • esc to interrupt)

› another prompt

  tab to queue message                                      26% context left ~/cicy-ai/workers/w-test`
	if canQueuePromptWhileBusy("codex", out, 0) {
		t.Fatal("synchronous Codex send must retain idle-wait semantics")
	}
}

func TestDeferredCodexSendWaitsWhileComposerIsMissing(t *testing.T) {
	out := `OpenAI Codex starting…`
	if canQueuePromptWhileBusy("codex", out, deferredAgentReadyTimeout) {
		t.Fatal("deferred Codex send must wait until the composer exists")
	}
}

func TestCodexInputReadyIgnoresOldBusyScrollback(t *testing.T) {
	out := `• Working (2m • esc to interrupt)
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output
• old tool output

› Ready for a new prompt

  gpt-5 default · ~/cicy-ai/workers/w-test · 26% left`
	if !isCodexInputReady(out) {
		t.Fatal("an old busy marker outside the active screen tail must not block an idle composer")
	}
}

func TestPaneSendReadyTimeoutDefaultsToSynchronousLimit(t *testing.T) {
	if got := effectiveAgentReadyTimeout("codex", 0); got != agentReadyTimeout {
		t.Fatalf("effectiveAgentReadyTimeout() = %v, want %v", got, agentReadyTimeout)
	}
}

func TestPaneSendReadyTimeoutUsesDeferredLimit(t *testing.T) {
	req := newDeferredPaneSendRequest("queued prompt")
	if got := effectiveAgentReadyTimeout("codex", req.readyTimeout); got != deferredAgentReadyTimeout {
		t.Fatalf("effectiveAgentReadyTimeout() = %v, want %v", got, deferredAgentReadyTimeout)
	}
}

func TestMergePaneSendReadyTimeoutUsesLongestRequest(t *testing.T) {
	batch := []paneSendRequest{
		{readyTimeout: agentReadyTimeout},
		{readyTimeout: deferredAgentReadyTimeout},
	}
	if got := mergePaneSendReadyTimeout(batch); got != deferredAgentReadyTimeout {
		t.Fatalf("mergePaneSendReadyTimeout() = %v, want %v", got, deferredAgentReadyTimeout)
	}
}
