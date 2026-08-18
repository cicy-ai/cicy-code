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
	"time"
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

func effectiveKnowledgeGitToken(cfg knowledgePrivateConfig) string {
	if token := strings.TrimSpace(cfg.GitHubToken); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv(knowledgeGitTokenEnv))
}

func knowledgeRemoteOriginRaw() string {
	out, err := exec.Command("git", "-C", knowledgeRootDir(), "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func knowledgeRemoteOrigin() string {
	raw := knowledgeRemoteOriginRaw()
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

func normalizeKnowledgeRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("origin is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.User != nil {
		return "", fmt.Errorf("origin must be an HTTPS github.com repository URL without embedded credentials")
	}
	return u.String(), nil
}

func authenticatedKnowledgeRemote(origin, token string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}

func validateKnowledgeGitHubAccess(origin, token string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("origin must identify a GitHub owner/repository")
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("validate GitHub token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub rejected the token or repository access (HTTP %d)", resp.StatusCode)
	}
	return nil
}

var validateKnowledgeGitHubAccessFn = validateKnowledgeGitHubAccess

func setKnowledgeRemoteOrigin(origin, token string) error {
	remote := origin
	var err error
	if strings.TrimSpace(token) != "" {
		remote, err = authenticatedKnowledgeRemote(origin, token)
		if err != nil {
			return err
		}
	}
	knowledgeGitMu.Lock()
	defer knowledgeGitMu.Unlock()
	args := []string{"-C", knowledgeRootDir(), "remote", "add", "origin", remote}
	if knowledgeRemoteOriginRaw() != "" {
		args = []string{"-C", knowledgeRootDir(), "remote", "set-url", "origin", remote}
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("set knowledge origin: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, item := range [][2]string{
		{"branch.main.remote", "origin"},
		{"branch.main.merge", "refs/heads/main"},
		{"user.name", "cicy-knowledge-sync-gh"},
		{"user.email", "cicybot@qq.com"},
	} {
		if out, err := exec.Command("git", "-C", knowledgeRootDir(), "config", "--local", item[0], item[1]).CombinedOutput(); err != nil {
			return fmt.Errorf("set knowledge git config %s: %w: %s", item[0], err, strings.TrimSpace(string(out)))
		}
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
		}
		token := effectiveKnowledgeGitToken(cfg)
		if !req.ClearToken {
			if token == "" {
				httpErr(w, http.StatusBadRequest, "CICY_KNOWLEDGE_GH_TOKEN is required")
				return
			}
			if err := validateKnowledgeGitHubAccessFn(origin, token); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := setKnowledgeSpecialistPane(req.Pane); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := writeKnowledgePrivateConfig(cfg); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !req.ClearToken {
			_ = os.Setenv(knowledgeGitTokenEnv, token)
		}
		if err := setKnowledgeRemoteOrigin(origin, token); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
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
	token := effectiveKnowledgeGitToken(cfg)
	resp := M{
		"pane":       knowledgeSpecialistPaneID(),
		"default":    knowledgeSpecialistDefaultPane,
		"origin":     knowledgeRemoteOrigin(),
		"token_set":  token != "",
		"token_tail": githubTokenTail(token),
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("reveal_token") == "1" {
		resp["token"] = token
	}
	J(w, resp)
}
