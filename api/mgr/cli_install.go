// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// On-demand coding-CLI installation.
//
// Previously boot.sh auto-installed a missing CLI (claude/codex/…) inline in the
// agent's terminal the first time it was opened. That is now CANCELLED: boot.sh
// only launches a CLI that is already present (see ensureAgentCommandLine). When
// a CLI is missing the frontend shows an install overlay over that agent's frame,
// which drives the backend-managed installer here:
//
//   GET  /api/agents/install-status?agent_type=claude  → {installed, version, …}
//   POST /api/agents/install  {agent_type, registry?}  → SSE: phase / log / done / error
//
// Install state is cached in ~/cicy-ai/db/cli-installed.json (keyed by CLI name —
// an npm global install is shared by every agent of that type, so the state is
// per-CLI, not per-agent). The cache is the "file cache" the overlay reads first;
// a miss falls back to live detection.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ttyd-go/skillcmd"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// cliInstallSpec describes how to detect and install one coding CLI.
type cliInstallSpec struct {
	agentType string // canonical agent_type (normalizeAgentType output)
	cliName   string // the binary to look for / launch (e.g. "claude")
	label     string // human label for the overlay ("Claude Code")
	npmPkg    string // npm package (registry-switchable); "" ⇒ use customCmd
	customCmd string // non-npm install shell command (cursor/kiro); "" ⇒ npm
}

// cliInstallSpecs is the registry of installable coding CLIs. cicy is omitted —
// it is the built-in lite agent (no external CLI to install).
func cliInstallSpecFor(agentType string) (cliInstallSpec, bool) {
	switch normalizeAgentType(agentType) {
	case "claude":
		return cliInstallSpec{"claude", "claude", "Claude Code", "@anthropic-ai/claude-code@latest", ""}, true
	case "codex":
		return cliInstallSpec{"codex", "codex", "Codex", "@openai/codex@latest", ""}, true
	case "gemini":
		return cliInstallSpec{"gemini", "gemini", "Gemini CLI", "@google/gemini-cli@latest", ""}, true
	case "opencode":
		return cliInstallSpec{"opencode", "opencode", "OpenCode", "opencode-ai@latest", ""}, true
	case "openclaw":
		return cliInstallSpec{"openclaw", "openclaw", "OpenClaw", "openclaw@latest", ""}, true
	case "copilot":
		return cliInstallSpec{"copilot", "copilot", "GitHub Copilot", "@github/copilot@latest", ""}, true
	case "cursor":
		return cliInstallSpec{"cursor", "cursor", "Cursor Agent", "", cursorInstallCmd()}, true
	case "kiro-cli":
		return cliInstallSpec{"kiro-cli", "kiro-cli", "Kiro CLI", "", kiroCliInstallCmd()}, true
	case "cicy-claude":
		return cliInstallSpec{"cicy-claude", "cicy", "CiCy Claude", "cicy-claude@latest", ""}, true
	}
	return cliInstallSpec{}, false
}

// ── detection ────────────────────────────────────────────────────────────────

// cliBinDirs are the install locations boot.sh's __cicy_refresh_command_path
// scans, in the same order, so backend detection agrees with the shell.
func cliBinDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
	}
}

// detectCli reports whether the CLI binary exists (and where), checking the known
// install dirs first then PATH. version is best-effort (`<cli> --version`).
func detectCli(spec cliInstallSpec) (installed bool, path string, version string) {
	for _, dir := range cliBinDirs() {
		p := filepath.Join(dir, spec.cliName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			return true, p, cliVersion(p)
		}
	}
	if p, err := exec.LookPath(spec.cliName); err == nil {
		return true, p, cliVersion(p)
	}
	return false, "", ""
}

func cliVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// ── file cache ───────────────────────────────────────────────────────────────

type cliInstallRecord struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

var (
	cliCacheMu sync.Mutex
)

func cliInstallCachePath() string {
	return filepath.Join(cicyRootDir, "db", "cli-installed.json")
}

func loadCliInstallCache() map[string]cliInstallRecord {
	out := map[string]cliInstallRecord{}
	if raw, err := os.ReadFile(cliInstallCachePath()); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func writeCliInstallCache(m map[string]cliInstallRecord) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cliInstallCachePath()), 0755)
	_ = os.WriteFile(cliInstallCachePath(), data, 0644)
}

// setCliInstallRecord updates one CLI's cached state (keyed by cliName).
func setCliInstallRecord(cliName string, rec cliInstallRecord) {
	cliCacheMu.Lock()
	defer cliCacheMu.Unlock()
	m := loadCliInstallCache()
	m[cliName] = rec
	writeCliInstallCache(m)
}

// cliInstallStatus returns the install record for an agent_type. It trusts a
// cached "installed:true" (fast path the overlay hits on every open), but always
// re-detects when the cache says not-installed or is missing — so a CLI installed
// out-of-band still resolves, and the cache self-heals.
func cliInstallStatus(agentType string) (cliInstallSpec, cliInstallRecord, bool) {
	spec, ok := cliInstallSpecFor(agentType)
	if !ok {
		return spec, cliInstallRecord{}, false
	}
	cliCacheMu.Lock()
	cached, hit := loadCliInstallCache()[spec.cliName]
	cliCacheMu.Unlock()
	if hit && cached.Installed {
		return spec, cached, true
	}
	installed, path, version := detectCli(spec)
	rec := cliInstallRecord{Installed: installed, Version: version, Path: path, CheckedAt: nowRFC3339()}
	setCliInstallRecord(spec.cliName, rec)
	return spec, rec, true
}

// ── install runner (phased + streaming + probe-first/retry-switch mirror) ─────

const (
	npmRegistryOfficial = "https://registry.npmjs.org"
	npmRegistryMirror   = "https://registry.npmmirror.com"
)

// cliInstallEvent is one SSE event streamed to the overlay.
type cliInstallEvent map[string]interface{}

// cnProbeURL is Google's connectivity-check endpoint: it returns 204 (empty body)
// when the open internet is reachable, and times out / resets in CN (and on
// corporate firewalls with the same effect). This is a far better "am I in CN"
// signal than pinging npmjs — in CN npmjs often answers a quick GET yet downloads
// crawl, so the old npmjs-reachable heuristic kept picking the official registry
// and left CN users on a painfully slow install.
const cnProbeURL = "https://www.google.com/generate_204"

// openInternetReachable returns true only on an exact 204 — a GFW-injected 200
// or a captive-portal page does NOT count as reachable (mirrors the renderer's
// detectRegion in lib/speedup/detect.ts).
func openInternetReachable(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cnProbeURL, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 204
}

// resolveNpmRegistry implements "探测优先,重试换源": an empty/auto choice probes
// the network and, if the host is in CN (open internet unreachable), uses the CN
// mirror — otherwise the official registry; an explicit "official"/"mirror"
// forces that source (the overlay's retry sends the opposite of registry_used).
// Returns the URL and a short label.
func resolveNpmRegistry(choice string) (url, label string) {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "mirror", "cn", "npmmirror":
		return npmRegistryMirror, "mirror"
	case "official", "npm":
		return npmRegistryOfficial, "official"
	}
	// auto:探测网络 —— 通得了开放网络就用官方源,是 CN/被墙就用 CN 镜像。
	if openInternetReachable(3 * time.Second) {
		return npmRegistryOfficial, "official"
	}
	return npmRegistryMirror, "mirror"
}

// npmInstallCmdRegistry builds the global npm install with an EXPLICIT registry
// (backend-controlled, unlike npmGlobalInstallCmd which probes inline).
//
// --loglevel verbose + --no-progress: verbose prints a line per resolve/fetch/
// extract step (so the overlay shows a detailed live log, not just the terse
// "added N packages"); no-progress drops npm's TTY progress bar, which doesn't
// render over a pipe. Both verbose and the install detail go to stderr, which the
// runner merges into the streamed log.
//
// --foreground-scripts: stream lifecycle-script (postinstall) output too. Several
// CLIs (notably @anthropic-ai/claude-code) download a large platform-native binary
// in postinstall — by default npm hides that output, so the log box sits empty for
// the whole (slow) download and only bursts the tail at the end, which reads as
// "not live". Foregrounding the scripts makes that download progress stream in.
// nodeEnvPreamble makes `node`/`npm` resolvable inside the NON-INTERACTIVE LOGIN
// shell we run installs in (`bash -lc`). Root cause it fixes: node is commonly
// managed by nvm/fnm, whose PATH setup lives in ~/.bashrc — and a login shell
// does NOT source ~/.bashrc (only ~/.profile / ~/.bash_profile), while a bare
// `bash -c` doesn't source it either. So the install shell had no npm even though
// the user's interactive terminal (which DID load ~/.bashrc) does — that's why
// `npx cicy-code` worked but the spawned `npm install claude` reported
// "npm: command not found" (seen on Google Cloud Shell).
//
// Sourcing nvm.sh alone isn't enough: without a `default` alias it loads the nvm
// function but selects no node version, so we also fall back to the newest
// installed nvm node bin. fnm/volta and the user's own npm-global bin are added
// too. All best-effort; if node is already on PATH this is a harmless no-op.
func nodeEnvPreamble() string {
	// PRIMARY fix (environment-agnostic): re-inject THIS process's own PATH. The
	// cicy-code server is normally launched via `npx cicy-code`, so its inherited
	// PATH already contains node/npm's dir — wherever the machine keeps it (nvm,
	// Cloud Shell, Homebrew, distro). The install runs in `bash -lc`, whose login
	// profiles RESET PATH and drop that dir → "npm: command not found". Prepending
	// our own PATH restores it regardless of how node was installed. (Confirmed on
	// Google Cloud Shell, where node is NOT under ~/.nvm and NVM_DIR is unset, so
	// path-guessing can't work — but the daemon's inherited PATH has npm.)
	self := os.Getenv("PATH")
	pre := ""
	if strings.TrimSpace(self) != "" && !strings.Contains(self, `"`) {
		pre = `export PATH="` + self + `:$PATH"; `
	}
	// FALLBACK for daemon launches that had no npm on PATH (systemd etc.): source
	// the common version managers. Best-effort; no-op when node is already found.
	return pre +
		`export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"; ` +
		`[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh" >/dev/null 2>&1; ` +
		`[ -d "$NVM_DIR/versions/node" ] && _cicy_nodebin="$(ls -d "$NVM_DIR"/versions/node/*/bin 2>/dev/null | tail -1)" && [ -n "$_cicy_nodebin" ] && export PATH="$_cicy_nodebin:$PATH"; ` +
		`[ -d "$HOME/.volta/bin" ] && export PATH="$HOME/.volta/bin:$PATH"; ` +
		`export PATH="$HOME/.npm-global/bin:$PATH"; `
}

func npmInstallCmdRegistry(pkg, registry string) string {
	rmOld := `rm -rf "$HOME/.npm-global/lib/node_modules/` + npmPkgDir(pkg) + `"`
	// Fetch resilience: that large platform-native binary is an OPTIONAL
	// dependency, so when its (~70MB) tarball is slow / the mirror CDN stalls,
	// npm hits the default 5-min fetch timeout and SILENTLY SKIPS it — the
	// wrapper lands without its native binary ("native binary not installed").
	// A long timeout + retries lets it actually complete.
	fetchOpts := `--fetch-retries=5 --fetch-retry-mintimeout=10000 --fetch-retry-maxtimeout=120000 --fetch-timeout=900000`
	return nodeEnvPreamble() +
		`mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib" "$HOME/.npm-global/lib/node_modules" && ` +
		rmOld + ` && npm install -g --include=optional ` + fetchOpts + ` --foreground-scripts --loglevel verbose --no-progress --registry=` + registry + ` --prefix "$HOME/.npm-global" ` + pkg
}

// runCliInstall executes the install, emitting phase/log/done/error events. It is
// synchronous (the HTTP handler streams its events); the caller's request context
// cancels the child process.
func runCliInstall(emit func(cliInstallEvent), spec cliInstallSpec, registryChoice string, cancelCh <-chan struct{}) {
	phase := func(key, text string, pct int) {
		emit(cliInstallEvent{"type": "phase", "phase": key, "text": text, "percent": pct})
	}
	logln := func(s string) {
		for _, ln := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			emit(cliInstallEvent{"type": "log", "line": ln})
		}
	}

	// Phase 1: probe / resolve source.
	phase("detect", "检测网络与镜像源…", 5)
	registryURL, registryLabel := npmRegistryOfficial, "official"
	cmd := spec.customCmd
	if spec.npmPkg != "" {
		registryURL, registryLabel = resolveNpmRegistry(registryChoice)
		cmd = npmInstallCmdRegistry(spec.npmPkg, registryURL)
		// Structured so the overlay localizes it off `code` (+ params); raw `line`
		// logs (npm's own output) stay as-is.
		emit(cliInstallEvent{"type": "log", "code": "logUsingRegistry", "label": registryLabel, "url": registryURL})
	} else {
		emit(cliInstallEvent{"type": "log", "code": "logOfficialScript"})
	}

	// Phase 2: run the installer, streaming output.
	phase("install", "正在安装 "+spec.label+"…", 35)
	exitOK := streamShellInstall(emit, logln, cmd, cancelCh)
	if !exitOK {
		// code lets the overlay render a localized message; error is the zh fallback.
		emit(cliInstallEvent{"type": "error", "code": "errInstallCmd", "error": "安装命令失败", "registry_used": registryLabel})
		return
	}

	// Phase 3: verify the binary now resolves.
	phase("verify", "校验安装结果…", 85)
	installed, path, version := detectCli(spec)
	rec := cliInstallRecord{Installed: installed, Version: version, Path: path, CheckedAt: nowRFC3339()}
	setCliInstallRecord(spec.cliName, rec)
	if !installed {
		emit(cliInstallEvent{"type": "error", "code": "errVerifyNotFound", "cli": spec.cliName, "error": "安装后仍未检测到 " + spec.cliName + " 可执行文件", "registry_used": registryLabel})
		return
	}

	phase("done", "安装完成", 100)
	emit(cliInstallEvent{"type": "done", "installed": true, "version": version, "cli": spec.cliName, "registry_used": registryLabel})
}

// streamShellInstall runs `bash -lc <cmd>` (login shell ⇒ node/npm on PATH),
// forwarding combined stdout/stderr line-by-line to logln. Returns true on exit 0.
// Aborts the child if cancelCh closes (client disconnected).
func streamShellInstall(emit func(cliInstallEvent), logln func(string), cmd string, cancelCh <-chan struct{}) bool {
	c := exec.Command("bash", "-lc", cmd)
	c.Env = os.Environ()
	stdout, err := c.StdoutPipe()
	if err != nil {
		logln("启动失败: " + err.Error())
		return false
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		logln("启动失败: " + err.Error())
		return false
	}
	if err := c.Start(); err != nil {
		logln("启动失败: " + err.Error())
		return false
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-cancelCh:
			_ = c.Process.Kill()
		case <-done:
		}
	}()
	var wg sync.WaitGroup
	scan := func(r interface{ Read([]byte) (int, error) }) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			logln(sc.Text())
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	err = c.Wait()
	close(done)
	return err == nil
}

// ── HTTP handlers ────────────────────────────────────────────────────────────

// handleAgentInstallStatus: GET /api/agents/install-status?agent_type=claude
// (also accepts agent_id). Returns the cached/detected install state.
func handleAgentInstallStatus(w http.ResponseWriter, r *http.Request) {
	agentType := strings.TrimSpace(r.URL.Query().Get("agent_type"))
	if agentType == "" {
		if id := strings.TrimSpace(r.URL.Query().Get("agent_id")); id != "" {
			agentType = paneAgentType(shortPaneID(normPaneID(id)) + ":main.0")
		}
	}
	spec, rec, ok := cliInstallStatus(agentType)
	if !ok {
		// Not an installable coding CLI (e.g. cicy) → treat as always-ready.
		J(w, M{"success": true, "installable": false, "installed": true})
		return
	}
	J(w, M{
		"success":     true,
		"installable": true,
		"installed":   rec.Installed,
		"cli":         spec.cliName,
		"label":       spec.label,
		"version":     rec.Version,
	})
}

// handleAgentInstall: POST /api/agents/install {agent_type, registry?}. Streams
// SSE phase/log/done/error events as the install runs.
func handleAgentInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req struct {
		AgentType string `json:"agent_type"`
		AgentID   string `json:"agent_id"`
		Registry  string `json:"registry"`
	}
	_ = readBody(r, &req)
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" && strings.TrimSpace(req.AgentID) != "" {
		agentType = paneAgentType(shortPaneID(normPaneID(req.AgentID)) + ":main.0")
	}
	spec, ok := cliInstallSpecFor(agentType)
	if !ok {
		httpErr(w, 400, "agent_type is not an installable coding CLI")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Defeat reverse-proxy (nginx) response buffering — without this the proxy can
	// hold the whole SSE stream and release it in one batch at the end, so the live
	// install log only appears all-at-once when the install finishes.
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	var mu sync.Mutex
	var installedOK bool
	emit := func(ev cliInstallEvent) {
		mu.Lock()
		defer mu.Unlock()
		if t, _ := ev["type"].(string); t == "done" {
			installedOK = true
		}
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	// Flush the headers + a priming comment immediately so the client opens the
	// stream right away (rather than waiting for the first real event).
	fmt.Fprint(w, ": stream-open\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	runCliInstall(emit, spec, req.Registry, r.Context().Done())
	// B: the CLI just landed on PATH — re-surface installed skills into its
	// ~/.<agent>/skills/ NOW, so the user gets skills without waiting for the next
	// cicy-code restart (surfacing otherwise only runs at startup). Idempotent;
	// also repairs a skills dir the CLI's own installer may have reset.
	if installedOK {
		if surfaced, _ := skillcmd.EnsureAgentSurfacing(); len(surfaced) > 0 {
			emit(cliInstallEvent{"type": "log", "line": fmt.Sprintf("已补齐技能到新装的 CLI: %v", surfaced)})
		}
	}
	emit(cliInstallEvent{"type": "end"})
}
