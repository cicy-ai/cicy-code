// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeCicyCodeTarball(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "package/cicy-code",
		Mode: 0o755,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLocalBinPlatformPackage(t *testing.T) {
	tests := []struct {
		goos      string
		arch      string
		wantPkg   string
		wantLabel string
		wantErr   bool
	}{
		{goos: "darwin", arch: "amd64", wantPkg: "cicy-code-darwin-x64", wantLabel: "darwin-x64"},
		{goos: "darwin", arch: "arm64", wantPkg: "cicy-code-darwin-arm64", wantLabel: "darwin-arm64"},
		{goos: "linux", arch: "amd64", wantPkg: "cicy-code-linux-x64", wantLabel: "linux-x64"},
		{goos: "linux", arch: "arm64", wantPkg: "cicy-code-linux-arm64", wantLabel: "linux-arm64"},
		{goos: "windows", arch: "amd64", wantErr: true},
		{goos: "linux", arch: "386", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.arch, func(t *testing.T) {
			pkg, label, err := localBinPlatformPackage(tt.goos, tt.arch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected unsupported architecture error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if pkg != tt.wantPkg || label != tt.wantLabel {
				t.Fatalf("got (%q, %q), want (%q, %q)", pkg, label, tt.wantPkg, tt.wantLabel)
			}
		})
	}
}

func TestInstallLocalBinUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix semantics")
	}
	const targetVersion = "9.8.7"
	wantBinary := []byte("fake-cicy-code-binary")
	tarball := fakeCicyCodeTarball(t, wantBinary)
	sum := sha512.Sum512(tarball)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cicy-code-darwin-x64/" + targetVersion:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dist": map[string]string{
					"tarball":   server.URL + "/package.tgz",
					"integrity": integrity,
				},
			})
		case "/package.tgz":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	binDir := t.TempDir()
	oldBinary := filepath.Join(binDir, "cicy-code-1.0.0-darwin-x64")
	if err := os.WriteFile(oldBinary, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stablePath := filepath.Join(binDir, "cicy-code")
	if err := os.Symlink(oldBinary, stablePath); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(binDir, ".cicy-localbin.json")
	if err := os.WriteFile(manifestPath, []byte("{\n  \"version\": \"2.3.226\",\n  \"other\": true,\n  \"cicy-code\": \"1.0.0\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := installLocalBinUpdate(context.Background(), localBinUpdateOptions{
		Version:  targetVersion,
		GOOS:     "darwin",
		GOARCH:   "amd64",
		BinDir:   binDir,
		Registry: server.URL,
		Client:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	versionedPath := filepath.Join(binDir, "cicy-code-"+targetVersion+"-darwin-x64")
	gotBinary, err := os.ReadFile(versionedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBinary, wantBinary) {
		t.Fatalf("installed binary = %q, want %q", gotBinary, wantBinary)
	}
	info, err := os.Stat(versionedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o, want 755", info.Mode().Perm())
	}
	gotTarget, err := os.Readlink(stablePath)
	if err != nil {
		t.Fatal(err)
	}
	if gotTarget != versionedPath {
		t.Fatalf("stable symlink = %q, want %q", gotTarget, versionedPath)
	}
	var manifest map[string]any
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["cicy-code"] != targetVersion || manifest["version"] != "2.3.226" || manifest["other"] != true {
		t.Fatalf("manifest not merged correctly: %#v", manifest)
	}
}

func TestInstallLocalBinUpdateRejectsBadIntegrityWithoutSwitching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix semantics")
	}
	tarball := fakeCicyCodeTarball(t, []byte("tampered"))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/9.8.7") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dist": map[string]string{
					"tarball":   server.URL + "/package.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, sha512.Size)),
				},
			})
			return
		}
		_, _ = w.Write(tarball)
	}))
	defer server.Close()

	binDir := t.TempDir()
	oldBinary := filepath.Join(binDir, "old")
	if err := os.WriteFile(oldBinary, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stablePath := filepath.Join(binDir, "cicy-code")
	if err := os.Symlink(oldBinary, stablePath); err != nil {
		t.Fatal(err)
	}

	err := installLocalBinUpdate(context.Background(), localBinUpdateOptions{
		Version:  "9.8.7",
		GOOS:     "darwin",
		GOARCH:   "amd64",
		BinDir:   binDir,
		Registry: server.URL,
		Client:   server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("error = %v, want integrity failure", err)
	}
	gotTarget, readErr := os.Readlink(stablePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if gotTarget != oldBinary {
		t.Fatalf("stable symlink changed after failed verification: %q", gotTarget)
	}
	if _, statErr := os.Stat(filepath.Join(binDir, "cicy-code-9.8.7-darwin-x64")); !os.IsNotExist(statErr) {
		t.Fatalf("unverified binary should not be installed; stat error = %v", statErr)
	}
}
