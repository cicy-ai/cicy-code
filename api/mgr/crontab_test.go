// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallConfiguredCrontab(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("crontab is unavailable on Windows")
	}
	dir := t.TempDir()
	oldPath, oldRun := cicyCrontabPath, runCrontabInstall
	t.Cleanup(func() { cicyCrontabPath, runCrontabInstall = oldPath, oldRun })
	cicyCrontabPath = filepath.Join(dir, "crontab.txt")

	calls := 0
	runCrontabInstall = func(path string) error {
		calls++
		if path != cicyCrontabPath {
			t.Fatalf("unexpected path: %s", path)
		}
		return nil
	}

	if err := os.WriteFile(cicyCrontabPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	installConfiguredCrontab()
	if calls != 0 {
		t.Fatalf("empty crontab should not be installed; calls=%d", calls)
	}

	if err := os.WriteFile(cicyCrontabPath, []byte("0 * * * * echo ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	installConfiguredCrontab()
	if calls != 1 {
		t.Fatalf("configured crontab should be installed once; calls=%d", calls)
	}
}
