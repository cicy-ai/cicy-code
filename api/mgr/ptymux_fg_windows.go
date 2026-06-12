//go:build windows

package main

import (
	"strings"
	"unsafe"

	pty "github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

// ptmForeground reports the command in the foreground of the pane. ConPTY has
// no controlling-terminal foreground process group (no TIOCGPGRP), so we walk
// the DESCENDANT process tree of the shell and report the deepest non-infra
// descendant — the busy/idle signal cicy's watcher needs ("is the agent CLI
// running, or are we back at an idle shell?"). conhost/cmd/etc. are plumbing,
// not the agent.
func ptmForeground(_ pty.Pty, cmd *pty.Cmd) string {
	if cmd == nil || cmd.Process == nil {
		return ""
	}
	root := uint32(cmd.Process.Pid)

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)

	children := map[uint32][]uint32{}
	exeOf := map[uint32]string{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return ""
	}
	for {
		exeOf[e.ProcessID] = strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}

	bestName, bestDepth := "", -1
	var walk func(pid uint32, depth int)
	walk = func(pid uint32, depth int) {
		name := strings.TrimSuffix(exeOf[pid], ".exe")
		if pid != root && !ptmFgInfra[name] && depth > bestDepth {
			bestName, bestDepth = name, depth
		}
		for _, kid := range children[pid] {
			walk(kid, depth+1)
		}
	}
	walk(root, 0)

	if bestName == "" {
		return strings.TrimSuffix(exeOf[root], ".exe")
	}
	return bestName
}

var ptmFgInfra = map[string]bool{
	"conhost":     true,
	"openconsole": true,
	"cmd":         true,
	"where":       true,
	"choco":       true,
	"chocolatey":  true,
}
