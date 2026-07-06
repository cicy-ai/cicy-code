//go:build !windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "syscall"

// readDiskSnapshot reports (total, used, usedPct) for the filesystem holding
// path. Unix implementation via statfs(2); the Windows counterpart lives in
// system_resources_windows.go (GetDiskFreeSpaceExW).
func readDiskSnapshot(path string) (uint64, uint64, float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	if available > total {
		available = total
	}
	used := total - available
	if total == 0 {
		return 0, 0, 0, nil
	}
	return total, used, float64(used) * 100 / float64(total), nil
}
