// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const knowledgeGitTokenEnv = "CICY_KNOWLEDGE_GH_TOKEN"

type knowledgePrivateConfig struct {
	GitHubToken string `json:"github_token"`
}

func knowledgePrivateConfigPath() string {
	return filepath.Join(cicyRootDir, "db", "knowledge-git.json")
}

func readKnowledgePrivateConfig() (knowledgePrivateConfig, error) {
	var cfg knowledgePrivateConfig
	b, err := os.ReadFile(knowledgePrivateConfigPath())
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse knowledge-git.json: %w", err)
	}
	return cfg, nil
}

func writeKnowledgePrivateConfig(cfg knowledgePrivateConfig) error {
	path := knowledgePrivateConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".knowledge-git.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
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

func loadKnowledgeGitTokenEnv() {
	cfg, err := readKnowledgePrivateConfig()
	if err == nil && strings.TrimSpace(cfg.GitHubToken) != "" {
		_ = os.Setenv(knowledgeGitTokenEnv, strings.TrimSpace(cfg.GitHubToken))
	}
}

func knowledgeRemoteOrigin() string {
	out, err := exec.Command("git", "-C", knowledgeRootDir(), "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func normalizeKnowledgeRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("origin must be an HTTPS repository URL without embedded credentials")
	}
	return u.String(), nil
}

func setKnowledgeRemoteOrigin(origin string) error {
	knowledgeGitMu.Lock()
	defer knowledgeGitMu.Unlock()
	args := []string{"-C", knowledgeRootDir(), "remote", "add", "origin", origin}
	if knowledgeRemoteOrigin() != "" {
		args = []string{"-C", knowledgeRootDir(), "remote", "set-url", "origin", origin}
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("set knowledge origin: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GET never returns the token itself. POST accepts a non-empty token to replace
// it; an empty token preserves the existing secret unless clear_token is true.
func handleKnowledgeConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Pane       string `json:"pane"`
			Origin     string `json:"origin"`
			Token      string `json:"token"`
			ClearToken bool   `json:"clear_token"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		origin, err := normalizeKnowledgeRemote(req.Origin)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := setKnowledgeSpecialistPane(req.Pane); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg, err := readKnowledgePrivateConfig()
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.ClearToken {
			cfg.GitHubToken = ""
			_ = os.Unsetenv(knowledgeGitTokenEnv)
		} else if token := strings.TrimSpace(req.Token); token != "" {
			cfg.GitHubToken = token
			_ = os.Setenv(knowledgeGitTokenEnv, token)
		}
		if err := writeKnowledgePrivateConfig(cfg); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if origin != "" && origin != knowledgeRemoteOrigin() {
			if err := setKnowledgeRemoteOrigin(origin); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	} else if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "GET or POST")
		return
	}
	cfg, err := readKnowledgePrivateConfig()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := strings.TrimSpace(cfg.GitHubToken)
	J(w, M{
		"pane":       knowledgeSpecialistPaneID(),
		"default":    knowledgeSpecialistDefaultPane,
		"origin":     knowledgeRemoteOrigin(),
		"token_set":  token != "",
		"token_tail": githubTokenTail(token),
	})
}
