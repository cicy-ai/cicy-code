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

func claudeAuthPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", ".credentials.json")
}

// POST /api/settings/claude-auth decodes a pasted CLAUDE_AUTH_B64 value and
// restores the exact decoded JSON bytes to ~/.claude/.credentials.json. The endpoint
// never returns the submitted credential or the existing file contents.
func handleClaudeAuthImport(w http.ResponseWriter, r *http.Request) {
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

	path := claudeAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		httpErr(w, http.StatusInternalServerError, "prepare .claude directory: "+err.Error())
		return
	}
	// Do not chmod an existing file: os.WriteFile preserves its current mode.
	// A NEW file is created 0600 — unlike ~/.codex/auth.json, Claude's
	// .credentials.json is owner-only, and a umask-wide default would quietly
	// downgrade that.
	if err := os.WriteFile(path, decoded, 0o600); err != nil {
		httpErr(w, http.StatusInternalServerError, "write .credentials.json: "+err.Error())
		return
	}
	J(w, M{"success": true})
}
