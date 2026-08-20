// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1" // #nosec G505 -- npm's legacy shasum is only a fallback when integrity is absent.
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxCicyUpdateArchiveSize = 256 << 20 // 256 MiB, comfortably above current packages.

var safeCicyUpdateVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)

type macLocalBinUpdateOptions struct {
	Version  string
	GOARCH   string
	BinDir   string
	Registry string
	Client   *http.Client
}

type npmPlatformPackageMetadata struct {
	Dist struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
		Shasum    string `json:"shasum"`
	} `json:"dist"`
}

func macPlatformPackage(goarch string) (packageName, archLabel string, err error) {
	switch goarch {
	case "amd64":
		return "cicy-code-darwin-x64", "x64", nil
	case "arm64":
		return "cicy-code-darwin-arm64", "arm64", nil
	default:
		return "", "", fmt.Errorf("unsupported macOS architecture %q", goarch)
	}
}

// installMacLocalBinUpdate stages a published cicy-code binary in the same
// side-by-side layout used by the Desktop local build flow. It intentionally
// does not stop or restart the running process; the new symlink is picked up
// when the user next restarts CiCy Desktop/cicy-code.
func installMacLocalBinUpdate(ctx context.Context, opts macLocalBinUpdateOptions) error {
	version := strings.TrimSpace(opts.Version)
	if !safeCicyUpdateVersion.MatchString(version) {
		return fmt.Errorf("invalid update version %q", version)
	}
	packageName, archLabel, err := macPlatformPackage(opts.GOARCH)
	if err != nil {
		return err
	}
	binDir := strings.TrimSpace(opts.BinDir)
	if binDir == "" {
		return errors.New("local-bin directory is empty")
	}
	registry := strings.TrimRight(strings.TrimSpace(opts.Registry), "/")
	if registry == "" {
		return errors.New("npm registry is empty")
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}

	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	metadataURL := registry + "/" + packageName + "/" + version
	metadata, err := fetchNpmPlatformPackageMetadata(requestCtx, client, metadataURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create local-bin directory: %w", err)
	}

	archivePath, err := downloadVerifiedNpmArchive(requestCtx, client, binDir, metadata)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	binaryTmp, err := extractCicyCodeBinary(archivePath, binDir)
	if err != nil {
		return err
	}
	defer os.Remove(binaryTmp)

	versionedPath := filepath.Join(binDir, "cicy-code-"+version+"-darwin-"+archLabel)
	if err := os.Rename(binaryTmp, versionedPath); err != nil {
		return fmt.Errorf("install versioned binary: %w", err)
	}
	if err := os.Chmod(versionedPath, 0o755); err != nil {
		return fmt.Errorf("mark versioned binary executable: %w", err)
	}
	if err := updateLocalBinManifest(filepath.Join(binDir, ".cicy-localbin.json"), version); err != nil {
		return err
	}
	if err := replaceSymlinkAtomically(filepath.Join(binDir, "cicy-code"), versionedPath); err != nil {
		return err
	}
	return nil
}

func fetchNpmPlatformPackageMetadata(ctx context.Context, client *http.Client, metadataURL string) (npmPlatformPackageMetadata, error) {
	var metadata npmPlatformPackageMetadata
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return metadata, fmt.Errorf("create npm metadata request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return metadata, fmt.Errorf("fetch npm package metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return metadata, fmt.Errorf("fetch npm package metadata: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata); err != nil {
		return metadata, fmt.Errorf("decode npm package metadata: %w", err)
	}
	if strings.TrimSpace(metadata.Dist.Tarball) == "" {
		return metadata, errors.New("npm package metadata has no tarball URL")
	}
	if strings.TrimSpace(metadata.Dist.Integrity) == "" && strings.TrimSpace(metadata.Dist.Shasum) == "" {
		return metadata, errors.New("npm package metadata has no integrity checksum")
	}
	return metadata, nil
}

func downloadVerifiedNpmArchive(ctx context.Context, client *http.Client, binDir string, metadata npmPlatformPackageMetadata) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.Dist.Tarball, nil)
	if err != nil {
		return "", fmt.Errorf("create npm tarball request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download npm tarball: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download npm tarball: HTTP %d", resp.StatusCode)
	}

	archive, err := os.CreateTemp(binDir, ".cicy-code-update-*.tgz")
	if err != nil {
		return "", fmt.Errorf("create update archive: %w", err)
	}
	archivePath := archive.Name()
	keep := false
	defer func() {
		_ = archive.Close()
		if !keep {
			_ = os.Remove(archivePath)
		}
	}()

	checksum, expected, err := npmChecksum(metadata)
	if err != nil {
		return "", err
	}
	written, err := io.Copy(io.MultiWriter(archive, checksum), io.LimitReader(resp.Body, maxCicyUpdateArchiveSize+1))
	if err != nil {
		return "", fmt.Errorf("save npm tarball: %w", err)
	}
	if written > maxCicyUpdateArchiveSize {
		return "", fmt.Errorf("npm tarball exceeds %d bytes", maxCicyUpdateArchiveSize)
	}
	if !equalBytes(checksum.Sum(nil), expected) {
		return "", errors.New("npm tarball integrity verification failed")
	}
	if err := archive.Sync(); err != nil {
		return "", fmt.Errorf("sync npm tarball: %w", err)
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("close npm tarball: %w", err)
	}
	keep = true
	return archivePath, nil
}

func npmChecksum(metadata npmPlatformPackageMetadata) (hash.Hash, []byte, error) {
	if integrity := strings.TrimSpace(metadata.Dist.Integrity); integrity != "" {
		algorithm, encoded, ok := strings.Cut(integrity, "-")
		if !ok || algorithm != "sha512" {
			return nil, nil, fmt.Errorf("unsupported npm integrity %q", integrity)
		}
		expected, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(expected) != sha512.Size {
			return nil, nil, errors.New("invalid npm sha512 integrity")
		}
		return sha512.New(), expected, nil
	}
	expected, err := hex.DecodeString(strings.TrimSpace(metadata.Dist.Shasum))
	if err != nil || len(expected) != sha1.Size {
		return nil, nil, errors.New("invalid npm sha1 shasum")
	}
	return sha1.New(), expected, nil // #nosec G401 -- compatibility fallback for npm metadata.
}

func extractCicyCodeBinary(archivePath, binDir string) (string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open npm tarball: %w", err)
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open npm tarball gzip stream: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read npm tarball: %w", err)
		}
		if header.Name != "package/cicy-code" {
			continue
		}
		if !header.FileInfo().Mode().IsRegular() {
			return "", errors.New("npm package cicy-code entry is not a regular file")
		}
		if header.Size <= 0 || header.Size > maxCicyUpdateArchiveSize {
			return "", fmt.Errorf("invalid cicy-code binary size %d", header.Size)
		}
		binary, err := os.CreateTemp(binDir, ".cicy-code-binary-*")
		if err != nil {
			return "", fmt.Errorf("create temporary cicy-code binary: %w", err)
		}
		binaryPath := binary.Name()
		keep := false
		defer func() {
			_ = binary.Close()
			if !keep {
				_ = os.Remove(binaryPath)
			}
		}()
		written, err := io.Copy(binary, io.LimitReader(tr, header.Size+1))
		if err != nil {
			return "", fmt.Errorf("extract cicy-code binary: %w", err)
		}
		if written != header.Size {
			return "", fmt.Errorf("extracted cicy-code size %d, want %d", written, header.Size)
		}
		if err := binary.Chmod(0o755); err != nil {
			return "", fmt.Errorf("mark temporary binary executable: %w", err)
		}
		if err := binary.Sync(); err != nil {
			return "", fmt.Errorf("sync temporary binary: %w", err)
		}
		if err := binary.Close(); err != nil {
			return "", fmt.Errorf("close temporary binary: %w", err)
		}
		keep = true
		return binaryPath, nil
	}
	return "", errors.New("npm package does not contain package/cicy-code")
}

func updateLocalBinManifest(path, version string) error {
	manifest := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("decode local-bin manifest: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read local-bin manifest: %w", err)
	}
	manifest["cicy-code"] = version
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local-bin manifest: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cicy-localbin-*.json")
	if err != nil {
		return fmt.Errorf("create local-bin manifest: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("set local-bin manifest permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write local-bin manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync local-bin manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close local-bin manifest: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace local-bin manifest: %w", err)
	}
	keep = true
	return nil
}

func replaceSymlinkAtomically(stablePath, versionedPath string) error {
	tmpPath := stablePath + ".update-link"
	_ = os.Remove(tmpPath)
	if err := os.Symlink(versionedPath, tmpPath); err != nil {
		return fmt.Errorf("create stable cicy-code symlink: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := os.Rename(tmpPath, stablePath); err != nil {
		return fmt.Errorf("replace stable cicy-code symlink: %w", err)
	}
	keep = true
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
