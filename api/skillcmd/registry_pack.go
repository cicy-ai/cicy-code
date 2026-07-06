// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package skillcmd

// registry_pack.go — pack a local skill directory into a <name>/-rooted zip,
// mirroring the exclusion rules of cicy-skills/tools/pack-skill.js. The
// resulting layout matches what extractZip() in installer.go expects (a single
// top-level <name>/ directory that gets stripped on install).

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// packExcludeDirs are directory names skipped entirely during packing.
var packExcludeDirs = map[string]bool{
	"node_modules": true,
	".cache":       true,
	".git":         true,
	"test":         true,
	"tests":        true,
}

// packExcludeFile reports whether a base filename should be skipped.
func packExcludeFile(base string) bool {
	if base == ".DS_Store" {
		return true
	}
	if strings.HasPrefix(base, ".env") {
		return true
	}
	if strings.HasSuffix(base, ".zip") || strings.HasSuffix(base, ".log") {
		return true
	}
	return false
}

// packSkill walks srcDir and returns a zip whose entries are all prefixed with
// "<name>/". Excludes node_modules/.git/test(s)/.DS_Store/*.zip/*.log/.env*.
func packSkill(srcDir, name string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if packExcludeDirs[base] {
				return filepath.SkipDir
			}
			return nil
		}
		if packExcludeFile(base) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// zip uses forward slashes; prefix with "<name>/".
		zipName := name + "/" + filepath.ToSlash(rel)
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = zipName
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	})
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sha256Hex returns the lowercase hex sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
