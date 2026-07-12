// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTtydDist lays out a minimal on-disk bindata root and points
// CICY_TTYD_DIST at it.
func writeTtydDist(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CICY_TTYD_DIST", dir)
	return dir
}

func TestAssetOrDiskPrefersDisk(t *testing.T) {
	writeTtydDist(t, map[string]string{
		"static/js/gotty-bundle.js": "console.log('from disk')",
	})

	got, err := AssetOrDisk("static/js/gotty-bundle.js")
	if err != nil {
		t.Fatalf("AssetOrDisk: %v", err)
	}
	if string(got) != "console.log('from disk')" {
		t.Fatalf("got %q, want the on-disk bundle — CICY_TTYD_DIST was ignored", got)
	}
}

// A partial override dir must still serve a working terminal: anything absent
// on disk falls back to the embedded copy.
func TestAssetOrDiskFallsBackToEmbedded(t *testing.T) {
	writeTtydDist(t, map[string]string{
		"static/js/gotty-bundle.js": "console.log('from disk')",
	})

	got, err := AssetOrDisk("static/index.html")
	if err != nil {
		t.Fatalf("AssetOrDisk(index.html): %v — should have fallen back to the embedded copy", err)
	}
	if len(got) == 0 {
		t.Fatal("embedded index.html came back empty")
	}
	if strings.Contains(string(got), "from disk") {
		t.Fatal("index.html unexpectedly served from disk")
	}
}

// With the override unset, behaviour must be byte-for-byte the shipped default.
func TestAssetOrDiskUnsetIsEmbedded(t *testing.T) {
	t.Setenv("CICY_TTYD_DIST", "")
	os.Unsetenv("CICY_TTYD_DIST")

	got, err := AssetOrDisk("static/index.html")
	if err != nil {
		t.Fatalf("AssetOrDisk: %v", err)
	}
	want, err := Asset("static/index.html")
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("with CICY_TTYD_DIST unset, AssetOrDisk must equal Asset")
	}
}

// `name` arrives straight off an HTTP request via assetfs, so a naive
// filepath.Join(dir, name) would happily serve /etc/passwd. Nothing may escape
// the override root.
func TestAssetOrDiskRejectsPathTraversal(t *testing.T) {
	dir := writeTtydDist(t, map[string]string{"static/index.html": "ok"})

	// A secret sitting next to (but outside) the override root.
	outside := filepath.Join(filepath.Dir(dir), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"../outside-secret.txt",
		"static/../../outside-secret.txt",
		"static/../../../../../../etc/passwd",
		"/etc/passwd",
	} {
		if p := ttydDiskPath(dir, name); p != "" {
			// Escaping is only a failure if it actually left the root.
			abs, _ := filepath.Abs(dir)
			if !strings.HasPrefix(p, abs+string(filepath.Separator)) && p != abs {
				t.Errorf("ttydDiskPath(%q) = %q — escaped the override root %q", name, p, abs)
			}
		}
		// And the bytes must never be the secret.
		if b, err := AssetOrDisk(name); err == nil && strings.Contains(string(b), "SECRET") {
			t.Errorf("AssetOrDisk(%q) served the file OUTSIDE the override root", name)
		}
	}
}

// The whole point of the override is edit → refresh. If the ?v= cache-buster
// stayed pinned to the embedded bundle's hash, the browser would re-serve its
// cached copy and the edit would appear to do nothing — so the hash must track
// the CURRENT bytes on disk.
func TestIndexCacheBusterTracksDiskBundle(t *testing.T) {
	dir := writeTtydDist(t, map[string]string{
		"static/index.html":         `<script src="js/gotty-bundle.js?v={{.asset_v}}"></script>`,
		"static/js/gotty-bundle.js": "v1",
	})

	_, first := currentIndex()

	bundle := filepath.Join(dir, "static", "js", "gotty-bundle.js")
	if err := os.WriteFile(bundle, []byte("v2-edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, second := currentIndex()
	if first == second {
		t.Fatalf("cache-buster stayed %q after the bundle changed — the browser would keep serving a stale copy", first)
	}
}

// Without the override, currentIndex must still be the cached (sync.Once) path:
// same template pointer, same hash, no per-request re-read.
func TestIndexCachedWhenOverrideOff(t *testing.T) {
	t.Setenv("CICY_TTYD_DIST", "")
	os.Unsetenv("CICY_TTYD_DIST")

	t1, v1 := currentIndex()
	t2, v2 := currentIndex()
	if t1 != t2 {
		t.Error("index template was rebuilt with the override off — the sync.Once cache is not being used")
	}
	if v1 != v2 {
		t.Errorf("cache-buster changed between calls (%q → %q) with the override off", v1, v2)
	}
}
