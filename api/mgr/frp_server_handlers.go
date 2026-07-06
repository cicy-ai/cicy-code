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
	"strconv"
	"strings"
	"time"
)

func frpServerBin() string {
	home, err := os.UserHomeDir()
	if err == nil {
		cand := filepath.Join(home, ".local", "bin", "frp-server")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("frp-server"); err == nil {
		return p
	}
	return ""
}

func runFrpServer(ctx context.Context, args ...string) (string, error) {
	bin := frpServerBin()
	if bin == "" {
		return "", errorf("frp-server not installed — run `cicy-skills install frp-server` first")
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}

// parseFrpServerStatus turns the key: value text from `frp-server status`
// into a flat map. Multiline sections (listeners:, proxy status:) are folded
// into a single "extra" string to avoid leaking raw config details.
func parseFrpServerStatus(raw string) map[string]any {
	m := map[string]any{}
	var extra []string
	inSection := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		// section headers (listeners:, proxy status:)
		if strings.HasSuffix(strings.TrimSpace(line), ":") && !strings.Contains(line, " ") {
			inSection = true
			continue
		}
		if inSection {
			extra = append(extra, strings.TrimSpace(line))
			continue
		}
		kv := strings.SplitN(line, ": ", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	if len(extra) > 0 {
		m["listeners"] = strings.Join(extra, "\n")
	}
	return m
}

// handleFrpServerStatus — GET /api/frp-server/status
func handleFrpServerStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "status")
	if err != nil && strings.TrimSpace(out) == "" {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed := parseFrpServerStatus(out)
	// normalise the state field
	state, _ := parsed["state"].(string)
	J(w, M{"success": true, "running": state == "running", "info": parsed})
}

// handleFrpServerLifecycle — POST /api/frp-server/lifecycle
// Body: {"action":"start"|"stop"|"restart"|"reload"}
func handleFrpServerLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		httpErr(w, http.StatusBadRequest, "action must be start|stop|restart|reload")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, action)
	result := M{"success": err == nil, "action": action, "output": strings.TrimSpace(out)}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}

// handleFrpServerConnections — GET /api/frp-server/connections
func handleFrpServerConnections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "connections")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, strings.TrimSpace(out)+": "+err.Error())
		return
	}
	J(w, M{"success": true, "output": strings.TrimSpace(out)})
}

// handleFrpServerClients — GET /api/frp-server/clients
// Parses tab-separated output: key user clientID hostname clientIP online
func handleFrpServerClients(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "clients")
	text := strings.TrimSpace(out)
	if err != nil {
		// "not running" or "dashboard not configured" are soft errors
		J(w, M{"success": false, "error": text + ": " + err.Error(), "clients": []any{}})
		return
	}
	var clients []map[string]any
	for _, line := range strings.Split(text, "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 6 {
			continue
		}
		clients = append(clients, map[string]any{
			"key":      cols[0],
			"user":     cols[1],
			"clientID": cols[2],
			"hostname": cols[3],
			"clientIP": cols[4],
			"online":   cols[5] == "true",
		})
	}
	if clients == nil {
		clients = []map[string]any{}
	}
	J(w, M{"success": true, "clients": clients})
}

// handleFrpServerLogs — GET /api/frp-server/logs?lines=50
func handleFrpServerLogs(w http.ResponseWriter, r *http.Request) {
	lines := r.URL.Query().Get("lines")
	if lines == "" {
		lines = "50"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "logs", "--lines", lines)
	if err != nil && strings.TrimSpace(out) == "" {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "logs": strings.TrimSpace(out)})
}

// handleFrpServerInstallInfo — GET /api/frp-server/install-info
//
// Returns everything a teammate needs to register a frp-client against THIS
// server: public IPv4 (fetched no-proxy so mihomo egress doesn't leak in),
// the bindPort from frps.toml, the auth.token, plus a ready-to-paste bash
// one-liner the user can copy onto the client box. Token is real (this
// endpoint is auth-protected via authM in main.go), so render it carefully
// in the UI (masked by default + reveal toggle).
func handleFrpServerInstallInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	publicIP, err := fetchPublicIPv4(ctx)
	if err != nil {
		publicIP = ""
	}

	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, "cicy-ai", "db", "frps.toml")
	port, token := parseFrpsConfig(cfgPath)

	remotePort := nextFreeFrpRemotePort(ctx)
	proxyName := "cli-" + strconv.Itoa(remotePort)
	bashCmd := buildFrpClientInstallCommand(publicIP, port, token, remotePort, proxyName)
	psCmd := buildFrpClientInstallCommandPowerShell(publicIP, port, token, remotePort, proxyName)
	J(w, M{
		"success":                    true,
		"public_ip":                  publicIP,
		"server_port":                port,
		"token":                      token,
		"remote_port":                remotePort,
		"proxy_name":                 proxyName,
		"config_path":                cfgPath,
		"install_command":            bashCmd, // back-compat: bash
		"install_command_bash":       bashCmd,
		"install_command_powershell": psCmd,
	})
}

// nextFreeFrpRemotePort returns the next unused remote TCP port for a NEW frp
// client, so installing multiple clients doesn't collide on the fixed default.
// It asks frps (via `frp-server clients --json`) which remote ports are already
// taken and returns the first free port at/above the CiCy base (9502). Falls
// back to the base when frps can't be queried.
func nextFreeFrpRemotePort(ctx context.Context) int {
	const base = 9502
	used := map[int]bool{}
	if out, err := runFrpServer(ctx, "clients", "--json"); err == nil {
		var parsed struct {
			Data struct {
				Clients []struct {
					RemotePort int `json:"remote_port"`
				} `json:"clients"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed) == nil {
			for _, c := range parsed.Data.Clients {
				if c.RemotePort > 0 {
					used[c.RemotePort] = true
				}
			}
		}
	}
	p := base
	for used[p] {
		p++
	}
	return p
}

// parseFrpsConfig pulls bindPort + auth.token from a frps.toml. Tiny
// regex-free parser — frps.toml is hand-written and shallow, importing a
// full TOML lib for two scalars is overkill.
func parseFrpsConfig(path string) (port int, token string) {
	port = 9500 // CiCy convention (upstream frp default is 7000)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if v, ok := tomlScalar(line, "bindPort"); ok {
			if p, err := strconv.Atoi(strings.Trim(v, `"`)); err == nil {
				port = p
			}
		}
		if v, ok := tomlScalar(line, "auth.token"); ok {
			token = strings.Trim(v, `"`)
		}
	}
	return
}

func tomlScalar(line, key string) (string, bool) {
	if !strings.HasPrefix(line, key+" ") && !strings.HasPrefix(line, key+"=") {
		return "", false
	}
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", false
	}
	return strings.TrimSpace(line[eq+1:]), true
}

func buildFrpClientInstallCommand(publicIP string, port int, token string, remotePort int, name string) string {
	if publicIP == "" {
		publicIP = "<your-server-public-ip>"
	}
	if token == "" {
		token = "<your-token>"
	}
	// Canonical network-install path: install.cicy-ai.com/frp downloads the
	// official fatedier/frp release, writes ~/cicy-ai/db/frpc.toml, and
	// registers as a launchd/systemd service. Works on macOS / Linux / WSL.
	// Re-running with no args reuses the existing config and hot-reloads.
	// --remote-port + --name are server-assigned (next free port / unique name)
	// so each new client lands on a distinct port instead of colliding on 9502.
	cmd := "curl -fsSL https://install.cicy-ai.com/frp | bash -s -- " +
		"--server " + publicIP +
		" --server-port " + strconv.Itoa(port) +
		" --token " + token
	if remotePort > 0 {
		cmd += " --remote-port " + strconv.Itoa(remotePort)
	}
	if name != "" {
		cmd += " --name " + name
	}
	return cmd + "\n"
}

// buildFrpClientInstallCommandPowerShell emits the native-Windows variant.
// install.cicy-ai.com is a Cloudflare Worker that does User-Agent content
// negotiation — PowerShell gets the .ps1 script automatically — so we just
// download to %TEMP% and exec with the right flags. Self-elevates to install
// as a Windows service.
func buildFrpClientInstallCommandPowerShell(publicIP string, port int, token string, remotePort int, name string) string {
	if publicIP == "" {
		publicIP = "<your-server-public-ip>"
	}
	if token == "" {
		token = "<your-token>"
	}
	cmd := "$u='https://install.cicy-ai.com/frp'; " +
		"$p=Join-Path $env:TEMP 'install-frpc-client.ps1'; " +
		"irm $u -OutFile $p; " +
		"powershell -ExecutionPolicy Bypass -File $p " +
		"-Server '" + publicIP + "' " +
		"-ServerPort " + strconv.Itoa(port) + " " +
		"-Token '" + token + "'"
	if remotePort > 0 {
		cmd += " -RemotePort " + strconv.Itoa(remotePort)
	}
	if name != "" {
		cmd += " -Name '" + name + "'"
	}
	return cmd + "\n"
}

func errorf(s string) error {
	return &stringError{s}
}

type stringError struct{ msg string }

func (e *stringError) Error() string { return e.msg }
