// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func codexAuthPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "auth.json")
}

// POST /api/settings/codex-auth decodes a pasted CODEX_AUTH_B64 value and
// restores the exact decoded JSON bytes to ~/.codex/auth.json. The endpoint
// never returns the submitted credential or the existing file contents.
func handleCodexAuthImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Base64 string `json:"base64"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Base64))
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid base64")
		return
	}
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &auth); err != nil || auth == nil {
		httpErr(w, http.StatusBadRequest, "decoded value must be a JSON object")
		return
	}

	path := codexAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		httpErr(w, http.StatusInternalServerError, "prepare .codex directory: "+err.Error())
		return
	}
	// Do not chmod or otherwise alter permissions. For an existing auth.json,
	// os.WriteFile preserves its current mode; a new file follows the process umask.
	if err := os.WriteFile(path, decoded, 0o666); err != nil {
		httpErr(w, http.StatusInternalServerError, "write auth.json: "+err.Error())
		return
	}
	J(w, M{"success": true})
}
