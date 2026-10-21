// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Minimizing a card must exit only the agent process, never the tmux session.
func TestStopAgentProcessInPaneKeepsSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sess := "cicy-stoptest-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	pane := sess + ":0.0"
	if err := tmuxCommand("new-session", "-d", "-s", sess, "bash", "--norc", "-i").Run(); err != nil {
		t.Skipf("cannot create tmux session: %v", err)
	}
	defer tmuxCommand("kill-session", "-t", sess).Run()
	time.Sleep(300 * time.Millisecond)
	// A child that spawns its own child, like an agent CLI with tool processes.
	runTmux("send-keys", "-t", pane, "python3 -c 'import subprocess,time; subprocess.Popen([\"sleep\",\"301\"]); time.sleep(300)'", "Enter")
	deadline := time.Now().Add(3 * time.Second)
	for !paneAgentProcessRunning(pane) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !paneAgentProcessRunning(pane) {
		t.Fatalf("foreground job never started (cmd=%q)", paneCurrentCommand(pane))
	}
	if !stopAgentProcessInPane(pane) {
		t.Fatal("stopAgentProcessInPane returned false for a running job")
	}
	if tmuxCommand("has-session", "-t", sess).Run() != nil {
		t.Fatal("tmux session was killed; only the agent process should exit")
	}
	deadline = time.Now().Add(2 * time.Second)
	for paneAgentProcessRunning(pane) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if paneAgentProcessRunning(pane) {
		t.Fatalf("pane still running %q after stop", paneCurrentCommand(pane))
	}
	if stopAgentProcessInPane(pane) {
		t.Fatal("second stop on an idle shell should be a no-op")
	}
}

// The minimized marker must round-trip on disk so it survives restarts.
func TestAgentSuspendedMarker(t *testing.T) {
	ws := t.TempDir()
	if agentSuspended(ws) {
		t.Fatal("fresh workspace reported suspended")
	}
	m := agentSuspendedMarker(ws)
	if err := ensureRuntimeDir(filepath.Dir(m), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !agentSuspended(ws) {
		t.Fatal("marker not detected")
	}
	os.Remove(m)
	if agentSuspended(ws) {
		t.Fatal("marker removal not detected")
	}
	if agentSuspended("") || agentSuspendedMarker("") != "" {
		t.Fatal("empty workspace must never be suspended")
	}
}
