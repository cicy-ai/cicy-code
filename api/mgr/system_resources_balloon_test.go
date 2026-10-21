// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFakeMemoryEnv(t *testing.T, onlineBlocks int, blockSize uint64, vmstat string) {
	t.Helper()
	root := t.TempDir()
	blocks := filepath.Join(root, "memory")
	if err := os.MkdirAll(blocks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocks, "block_size_bytes"), []byte(fmt.Sprintf("%x\n", blockSize)), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < onlineBlocks+1; i++ {
		dir := filepath.Join(blocks, fmt.Sprintf("memory%d", i))
		_ = os.MkdirAll(dir, 0o755)
		state := "online"
		if i == onlineBlocks { // one offline block must not be counted
			state = "offline"
		}
		_ = os.WriteFile(filepath.Join(dir, "state"), []byte(state+"\n"), 0o644)
	}
	vmPath := filepath.Join(root, "vmstat")
	if err := os.WriteFile(vmPath, []byte(vmstat), 0o644); err != nil {
		t.Fatal(err)
	}
	oldBlocks, oldVM := memoryBlocksRoot, procVMStatPath
	memoryBlocksRoot, procVMStatPath = blocks, vmPath
	t.Cleanup(func() { memoryBlocksRoot, procVMStatPath = oldBlocks, oldVM })
}

func TestReadMemoryBalloonReportsConfiguredAndHeld(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("balloon detection is linux-only")
	}
	const gib = uint64(1) << 30
	page := uint64(os.Getpagesize())
	// 32 GiB online (256 × 128 MiB blocks); balloon holds 19 GiB.
	held := 19 * gib / page
	writeFakeMemoryEnv(t, 256, 128<<20, fmt.Sprintf("nr_free_pages 1\nballoon_inflate %d\nballoon_deflate %d\n", held+1000, 1000))
	configured, balloon := readMemoryBalloon(13 * gib)
	if configured != 32*gib {
		t.Fatalf("configured = %d, want %d", configured, 32*gib)
	}
	if balloon != held*page {
		t.Fatalf("balloon = %d, want %d", balloon, held*page)
	}
}

func TestReadMemoryBalloonZeroWithoutBalloon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("balloon detection is linux-only")
	}
	writeFakeMemoryEnv(t, 256, 128<<20, "balloon_inflate 500\nballoon_deflate 500\n")
	if c, b := readMemoryBalloon(32 << 30); c != 0 || b != 0 {
		t.Fatalf("expected (0,0) when the balloon is fully deflated, got (%d,%d)", c, b)
	}
	writeFakeMemoryEnv(t, 256, 128<<20, "nr_free_pages 1\n")
	if c, b := readMemoryBalloon(32 << 30); c != 0 || b != 0 {
		t.Fatalf("expected (0,0) without balloon counters, got (%d,%d)", c, b)
	}
}
