// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The unit tests above cover AssetOrDisk itself. This one goes through the
// handler the terminal actually serves from — StaticHandler() → assetfs →
// http.FileServer — because that's the path that can silently defeat the
// override (a wrong Size, a cached handler, a mangled prefix).
//
// StaticHandler memoizes behind a sync.Once, so it is built ONCE per process.
// That's fine — the Once only captures the AssetOrDisk *func*, and the env
// lookup happens per call inside it.
func TestStaticHandlerServesDiskBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "static", "js", "gotty-bundle.js")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "console.log('served from disk')"
	if err := os.WriteFile(bundle, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICY_TTYD_DIST", dir)

	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/gotty-bundle.js", nil))

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /js/gotty-bundle.js = %d, want 200", res.StatusCode)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != body {
		t.Fatalf("served %q, want the on-disk bundle %q", got, body)
	}
	// assetfs derives Size() from the bytes we hand back; a stale
	// Content-Length would truncate the bundle in the browser.
	if int(res.ContentLength) != len(body) && res.ContentLength != -1 {
		t.Errorf("Content-Length = %d, want %d", res.ContentLength, len(body))
	}
}

// A second request after the file changes must see the NEW bytes — no handler
// or FileInfo caching may pin the first read.
func TestStaticHandlerPicksUpEdits(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "static", "js", "gotty-bundle.js")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICY_TTYD_DIST", dir)

	fetch := func() string {
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/gotty-bundle.js", nil))
		b, _ := io.ReadAll(rec.Result().Body)
		return string(b)
	}

	if got := fetch(); got != "v1" {
		t.Fatalf("first fetch = %q, want v1", got)
	}
	if err := os.WriteFile(bundle, []byte("v2-edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fetch(); got != "v2-edited" {
		t.Fatalf("after editing the file on disk, fetch = %q, want v2-edited — something is caching the first read", got)
	}
}

// The override must not turn the terminal into an arbitrary file server.
func TestStaticHandlerRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "static", "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt") // inside dir, but OUTSIDE static/
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICY_TTYD_DIST", dir)

	for _, p := range []string{
		"/js/../../secret.txt",
		"/js/%2e%2e/%2e%2e/secret.txt",
		"/../secret.txt",
	} {
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		body, _ := io.ReadAll(rec.Result().Body)
		if string(body) == "SECRET" {
			t.Errorf("GET %s served the file outside static/ — the terminal became a file server", p)
		}
	}
}
