// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// tmuxRunner is the subset of *exec.Cmd that cicy's tmux call sites use. It
// lets us swap the tmux binary for a native backend on Windows without
// touching the call sites' shape (.Run()/.Output()/.CombinedOutput()).
type tmuxRunner interface {
	Run() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
}

// tmuxCommand builds a runnable tmux invocation. On Unix/macOS it is exactly
// exec.Command("tmux", args...) — unchanged behavior. On Windows it dispatches
// to the native ConPTY pty backend when CICY_PTY_BACKEND is enabled, else also
// falls back to the tmux binary. See tmux_cmd_unix.go / tmux_cmd_windows.go.
func tmuxCommand(args ...string) tmuxRunner { return newTmuxCommand(args) }
