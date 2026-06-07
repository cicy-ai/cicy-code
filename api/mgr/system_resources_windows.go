//go:build windows

package main

import "golang.org/x/sys/windows"

// readDiskSnapshot reports (total, used, usedPct) for the volume holding path.
// Windows implementation via GetDiskFreeSpaceExW; the unix counterpart lives
// in system_resources_unix.go (statfs).
func readDiskSnapshot(path string) (uint64, uint64, float64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, err
	}
	var freeForCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeForCaller, &total, &totalFree); err != nil {
		return 0, 0, 0, err
	}
	available := freeForCaller
	if available > total {
		available = total
	}
	used := total - available
	if total == 0 {
		return 0, 0, 0, nil
	}
	return total, used, float64(used) * 100 / float64(total), nil
}
