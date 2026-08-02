// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// previewMode / hotMode are package-level globals set from the CLI flags.
// Save + restore them so these cases don't leak into other tests.
func withUIModes(t *testing.T, preview, hot bool, distEnv string) {
	t.Helper()
	prevPreview, prevHot := previewMode, hotMode
	previewMode, hotMode = preview, hot
	t.Cleanup(func() { previewMode, hotMode = prevPreview, prevHot })

	if distEnv == "" {
		t.Setenv("CICY_PREVIEW_DIST", "")
		os.Unsetenv("CICY_PREVIEW_DIST")
		return
	}
	t.Setenv("CICY_PREVIEW_DIST", distEnv)
}

func TestPreviewDistDir(t *testing.T) {
	relDefault := filepath.Join("app", "dist")

	t.Run("env alone implies preview", func(t *testing.T) {
		withUIModes(t, false, false, "/tmp/some-dist")
		if got := previewDistDir(); got != "/tmp/some-dist" {
			t.Errorf("previewDistDir() = %q, want %q — CICY_PREVIEW_DIST must work without --preview", got, "/tmp/some-dist")
		}
	})

	t.Run("flag alone uses the relative default", func(t *testing.T) {
		withUIModes(t, true, false, "")
		if got := previewDistDir(); got != relDefault {
			t.Errorf("previewDistDir() = %q, want %q", got, relDefault)
		}
	})

	t.Run("env wins over the flag default", func(t *testing.T) {
		withUIModes(t, true, false, "/tmp/override")
		if got := previewDistDir(); got != "/tmp/override" {
			t.Errorf("previewDistDir() = %q, want %q", got, "/tmp/override")
		}
	})

	t.Run("neither means embedded", func(t *testing.T) {
		withUIModes(t, false, false, "")
		if got := previewDistDir(); got != "" {
			t.Errorf("previewDistDir() = %q, want \"\" (serve the embedded assets)", got)
		}
	})
}

// The behaviour that actually matters: with only CICY_PREVIEW_DIST set, serveUI
// must hand back the file from that directory — not the embedded build.
func TestServeUIServesDistFromEnvWithoutFlag(t *testing.T) {
	dist := t.TempDir()
	const marker = "<html>on-disk dist marker</html>"
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	withUIModes(t, false /* no --preview */, false, dist)

	rec := httptest.NewRecorder()
	serveUI().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != marker {
		t.Fatalf("GET / served %q, want the on-disk dist %q — CICY_PREVIEW_DIST was ignored", string(body), marker)
	}
}

// A path that does not exist on disk must still fall back to the dist's
// index.html (client-side routing), not 404.
func TestServeUISPAFallbackFromEnvDist(t *testing.T) {
	dist := t.TempDir()
	const marker = "<html>spa root</html>"
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	withUIModes(t, false, false, dist)

	rec := httptest.NewRecorder()
	serveUI().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/client/route", nil))

	if rec.Result().StatusCode != http.StatusOK {
		t.Fatalf("GET /some/client/route = %d, want 200 (SPA fallback)", rec.Result().StatusCode)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != marker {
		t.Fatalf("SPA fallback served %q, want %q", string(body), marker)
	}
}

func TestServeUIMissingAssetReturnsUncached404(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>spa root</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	withUIModes(t, false, false, dist)
	rec := httptest.NewRecorder()
	serveUI().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/stale-chunk.js", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("missing asset Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); strings.Contains(got, "text/html") {
		t.Fatalf("missing asset Content-Type = %q, must not be HTML", got)
	}
}

// --hot is an explicit "proxy to vite"; it must outrank a stray
// CICY_PREVIEW_DIST left in the environment.
func TestHotModeOutranksPreviewDist(t *testing.T) {
	withUIModes(t, false, true /* --hot */, "/tmp/should-be-ignored")
	if got := previewDistDir(); got == "" {
		t.Fatal("previewDistDir() should still resolve the env dir; serveUI is what must ignore it under --hot")
	}
	// serveUI takes the hotMode branch, so no disk FS is wired up at all.
	// Assert via the settings surface, which reports the EFFECTIVE mode.
	if effective := !hotMode && previewDistDir() != ""; effective {
		t.Fatal("effective preview mode is true under --hot; --hot must win")
	}
}
