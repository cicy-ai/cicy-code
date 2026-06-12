//go:build !windows

package main

import "os/exec"

// newTmuxCommand on Unix/macOS is exactly the old call — zero behavior change.
func newTmuxCommand(args []string) tmuxRunner { return exec.Command("tmux", args...) }
