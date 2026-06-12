//go:build !windows

package main

import "os/exec"

// newTmuxCommand on Unix/macOS is exactly the old call — zero behavior change.
func newTmuxCommand(args []string) tmuxRunner { return exec.Command("tmux", args...) }

// nativePtyActive is always false off Windows — Unix/macOS keep tmux + boot.sh.
func nativePtyActive() bool { return false }
