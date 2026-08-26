// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Shared on-disk store for the account matrix (~/cicy-ai/db/<name>.json).
// Every platform keeps its own struct; only the read/atomic-write/0600 plumbing
// is common, so npm/docker/aliyun don't each re-implement it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func accountStorePath(file string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "db", file), nil
}

func readAccountStore[T any](file string) (map[string]T, error) {
	path, err := accountStorePath(file)
	if err != nil {
		return nil, err
	}
	accounts := map[string]T{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return accounts, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	return accounts, nil
}

func writeAccountStore[T any](file string, accounts map[string]T) error {
	path, err := accountStorePath(file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic0600(path, append(data, '\n'))
}

// writeFileAtomic0600 replaces path via a same-directory temp file so a crash
// never leaves a half-written secret behind, and never widens the mode.
func writeFileAtomic0600(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// secretTail is what the list endpoints expose instead of a secret: enough to
// tell two credentials apart, not enough to use one.
func secretTail(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 4 {
		return secret
	}
	return secret[len(secret)-4:]
}
