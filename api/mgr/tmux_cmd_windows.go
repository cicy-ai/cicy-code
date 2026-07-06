//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "os/exec"

// newTmuxCommand on Windows runs the MSYS2 tmux binary, same as Unix. (The
// former native ConPTY pty backend / ptymux has been removed — Windows always
// uses tmux now.)
func newTmuxCommand(args []string) tmuxRunner { return exec.Command("tmux", args...) }

// nativePtyActive is always false — there is no native pty backend anymore.
func nativePtyActive() bool { return false }
