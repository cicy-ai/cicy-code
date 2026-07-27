// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func kouboSkillBin() string {
	if bin := resolveSkillBin("cicy-koubo"); bin != "" {
		return bin
	}
	if !devMode {
		return ""
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, "projects", "cicy-skills", "skills", "cicy-koubo", "bin", "cicy-koubo")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

func isKouboAgent(paneID string) bool {
	paneID = normPaneID(paneID)
	var roleTemplate string
	if err := store.QueryRow(
		"SELECT COALESCE(role_template,'') FROM agent_config WHERE pane_id=? AND COALESCE(active,1)=1",
		paneID,
	).Scan(&roleTemplate); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(roleTemplate), "koubo")
}

func runKouboSkill(ctx context.Context, args ...string) ([]byte, error) {
	bin := kouboSkillBin()
	if bin == "" {
		return nil, errorf("cicy-koubo skill is not installed")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = skillEnv()
	return cmd.CombinedOutput()
}

// GET /api/koubo/status?pane_id=w-105
func handleKouboStatus(w http.ResponseWriter, r *http.Request) {
	paneID := strings.TrimSpace(r.URL.Query().Get("pane_id"))
	if !isKouboAgent(paneID) {
		httpErr(w, http.StatusForbidden, "pane is not a koubo agent")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runKouboSkill(ctx, "status", "--json")
	if err != nil {
		J(w, M{"success": false, "installed": kouboSkillBin() != "", "error": strings.TrimSpace(string(out))})
		return
	}
	var status map[string]any
	if json.Unmarshal(out, &status) != nil {
		J(w, M{"success": false, "error": "invalid cicy-koubo status response"})
		return
	}
	status["success"] = true
	J(w, status)
}

// POST /api/koubo/start-open {"pane_id":"w-105"}
func handleKouboStartOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaneID string `json:"pane_id"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
		return
	}
	if !isKouboAgent(req.PaneID) {
		httpErr(w, http.StatusForbidden, "pane is not a koubo agent")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	out, err := runKouboSkill(ctx, "open-or-start")
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		J(w, M{"success": false, "error": message})
		return
	}
	J(w, M{"success": true, "output": strings.TrimSpace(string(out))})
}
