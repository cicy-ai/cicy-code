// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The webtty (ttyd) static bundle is baked into the binary as GENERATED Go
// source: `make asset` runs go-bindata over api/bindata/ and writes
// server/asset.go, which holds every file as a gzipped byte slice. Asset()
// only ever reads that in-memory map — there is no disk path and no debug
// mode. So a one-character edit in api/js/src/*.ts costs a full
// `make asset` + Go rebuild before you can see it.
//
// CICY_TTYD_DIST is the escape hatch, mirroring CICY_PREVIEW_DIST for the app
// SPA: point it at the bindata root and the asset lookups below read from disk
// first, falling back to the embedded copy for anything not found there.
//
//	CICY_TTYD_DIST=<repo>/api/bindata cicy-code
//
// Unset (the shipped default) → pure embedded, byte-for-byte as before.
func ttydDistDir() string {
	return strings.TrimSpace(os.Getenv("CICY_TTYD_DIST"))
}

// ttydDiskPath resolves an asset name ("static/js/gotty-bundle.js") under the
// override dir, or "" if the name escapes it.
//
// `name` reaches here straight off an HTTP request via assetfs, so a bare
// filepath.Join(dir, name) would happily serve ../../../etc/passwd. Clean the
// name, reject any traversal, and verify the result really is under dir.
func ttydDiskPath(dir, name string) string {
	if dir == "" || name == "" {
		return ""
	}
	clean := filepath.Clean("/" + filepath.FromSlash(name))       // anchor, collapse ".."
	full := filepath.Join(dir, strings.TrimPrefix(clean, string(filepath.Separator)))

	root, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return ""
	}
	// abs must be root itself or sit strictly beneath it.
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return ""
	}
	return abs
}

// AssetOrDisk is Asset() with the CICY_TTYD_DIST override applied.
func AssetOrDisk(name string) ([]byte, error) {
	if p := ttydDiskPath(ttydDistDir(), name); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			return b, nil
		}
		// Not on disk (or unreadable) → fall through to the embedded copy, so a
		// partial override dir still serves a working terminal.
	}
	return Asset(name)
}

// AssetDirOrDisk is AssetDir() with the CICY_TTYD_DIST override applied. The
// http.FileServer behind assetfs uses this to list directories.
func AssetDirOrDisk(name string) ([]string, error) {
	if p := ttydDiskPath(ttydDistDir(), name); p != "" {
		if entries, err := os.ReadDir(p); err == nil {
			out := make([]string, 0, len(entries))
			for _, e := range entries {
				out = append(out, e.Name())
			}
			return out, nil
		}
	}
	return AssetDir(name)
}

// TtydDistNote returns a one-line startup note when the override is active, so
// "why is the terminal serving stale JS" is answerable from the log. Empty when
// the override is off.
func TtydDistNote() string {
	dir := ttydDistDir()
	if dir == "" {
		return ""
	}
	return fmt.Sprintf("[ttyd] CICY_TTYD_DIST=%s — serving the terminal bundle from disk (embedded copy is the fallback)", dir)
}
