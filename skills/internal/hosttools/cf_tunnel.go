package hosttools

// Cloudflare Tunnel skill — thin wrapper around the Cloudflare API.
//
// Subcommands:
//   config  — create/open ~/cicy-ai/db/cf.json in code-server
//   status  — show config state (api_token masked)
//   list    — list current tunnel routes
//   add     — add one or more routes and DNS records
//   del     — delete one or more routes and DNS records
//
// Security model: the wrapper is the only component that reads the JSON config.
// Agents must NOT cat / Read / grep ~/cicy-ai/db/cf.json.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	cfTunnelConfigPath    = "cicy-ai/db/cf.json"
	cfTokenPlaceholder    = "<paste-your-cloudflare-api-token-here>"
	cfAccountPlaceholder  = "<paste-your-cloudflare-account-id-here>"
	cfTunnelIDPlaceholder = "<paste-your-cloudflare-tunnel-id-here>"
	cfDomainPlaceholder   = "<paste-your-domain-here>"
	cfZoneIDPlaceholder   = "<paste-your-cloudflare-zone-id-here>"
)

type cfTunnelEnvConfig struct {
	APIToken    string `json:"api_token"`
	AccountID   string `json:"account_id"`
	TunnelID    string `json:"tunnel_id"`
	Domain      string `json:"domain"`
	ZoneID      string `json:"zone_id"`
	TunnelToken string `json:"tunnel_token,omitempty"` // cached cloudflared connector token
}

// cfTunnelConfig is the top-level JSON: {"prod": {...}, "dev": {...}}
type cfTunnelConfig map[string]cfTunnelEnvConfig

func cfTunnelConfigFile() string {
	return filepath.Join(userHomeDir(), cfTunnelConfigPath)
}

func saveCFTunnelConfig(cfg cfTunnelConfig) error {
	path := cfTunnelConfigFile()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func loadCFTunnelConfig() (cfTunnelConfig, error) {
	target := cfTunnelConfigFile()

	var cfg cfTunnelConfig
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", target, err)
	}
	return cfg, nil
}

func cfTunnelPlaceholderJSON() string {
	env := cfTunnelEnvConfig{
		APIToken:  cfTokenPlaceholder,
		AccountID: cfAccountPlaceholder,
		TunnelID:  cfTunnelIDPlaceholder,
		Domain:    cfDomainPlaceholder,
		ZoneID:    cfZoneIDPlaceholder,
	}
	data, _ := json.MarshalIndent(cfTunnelConfig{"prod": env}, "", "  ")
	return string(data) + "\n"
}

func isCFTunnelPlaceholder(cfg cfTunnelEnvConfig) bool {
	for _, v := range []string{cfg.APIToken, cfg.AccountID, cfg.TunnelID, cfg.Domain, cfg.ZoneID} {
		if strings.TrimSpace(v) == "" || strings.HasPrefix(strings.TrimSpace(v), "<") {
			return true
		}
	}
	return false
}

func (e *Env) writeCFTunnelPlaceholder() (bool, error) {
	path := cfTunnelConfigFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(cfTunnelPlaceholderJSON()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ── dispatch ───────────────────────────────────────────────────────────────

func (e *Env) runCFTunnel(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCFTunnelUsage(e.Stdout)
		return nil
	}
	switch args[0] {
	case "config":
		return e.runCFTunnelConfigOpen()
	case "status":
		return e.runCFTunnelStatus()
	case "daemon":
		return e.runCFTunnelDaemon(args[1:])
	case "list", "add", "del":
		return e.runCFTunnelOp(args)
	default:
		printCFTunnelUsage(e.Stderr)
		return fmt.Errorf("unknown cf-tunnel command: %s", args[0])
	}
}

func printCFTunnelUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cf-tunnel <command> [args...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Config:")
	fmt.Fprintln(w, "  config                     Open ~/cicy-ai/db/cf.json in code-server")
	fmt.Fprintln(w, "  status                     Show config + daemon state (api_token masked)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Daemon (cloudflared service):")
	fmt.Fprintln(w, "  daemon                     Show cloudflared install and service status")
	fmt.Fprintln(w, "  daemon install             Fetch tunnel token, install cloudflared, start service")
	fmt.Fprintln(w, "  daemon uninstall           Stop and remove the cloudflared service")
	fmt.Fprintln(w, "  daemon start               Start the cloudflared service")
	fmt.Fprintln(w, "  daemon stop                Stop the cloudflared service")
	fmt.Fprintln(w, "  daemon restart             Restart the cloudflared service")
	fmt.Fprintln(w, "  daemon logs [n]            Show last n lines of cloudflared logs (default 50)")
	fmt.Fprintln(w, "  daemon token               Fetch and cache the tunnel connector token")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Routes:")
	fmt.Fprintln(w, "  list                       List current tunnel routes and local port status")
	fmt.Fprintln(w, "  add <port> [...]           Add one or more routes and DNS records")
	fmt.Fprintln(w, "  del <port> [...]           Delete one or more routes and DNS records")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Environment:")
	fmt.Fprintln(w, "  CF_ENV=prod|dev            Choose the environment block (default: prod)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Agents must NOT cat / Read ~/cicy-ai/db/cf.json — api_token is a secret.")
}

// ── config (open in code-server, auto-creates placeholder) ─────────────────

func (e *Env) runCFTunnelConfigOpen() error {
	path := cfTunnelConfigFile()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if _, werr := e.writeCFTunnelPlaceholder(); werr != nil {
				return fmt.Errorf("config missing and failed to create placeholder: %w", werr)
			}
			fmt.Fprintln(e.Stdout, "config was missing — created placeholder (chmod 600)")
		} else {
			return err
		}
	}
	fmt.Fprintln(e.Stdout, "opening config in code-server...")
	cmd := exec.Command("agent-code-server", "open", path)
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(e.Stdout, "")
		fmt.Fprintf(e.Stdout, "agent-code-server returned %v (likely just a missing ack). Edit directly if needed:\n  $EDITOR %s\n", err, path)
	}
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Fill in the values for the 'prod' (and optionally 'dev') environment block:")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "  api_token   — Cloudflare API token with Tunnel+DNS edit permissions")
	fmt.Fprintln(e.Stdout, "                Create at https://dash.cloudflare.com/profile/api-tokens")
	fmt.Fprintln(e.Stdout, "                Template: \"Edit Cloudflare Tunnel\" (zone DNS edit + account tunnel edit)")
	fmt.Fprintln(e.Stdout, "  account_id  — Cloudflare account ID (visible in the URL of the dashboard home)")
	fmt.Fprintln(e.Stdout, "  tunnel_id   — ID of the existing Cloudflare Tunnel (not the name)")
	fmt.Fprintln(e.Stdout, "                Find it in Zero Trust → Networks → Tunnels → <your tunnel> → Overview")
	fmt.Fprintln(e.Stdout, "  domain      — base domain for routes (e.g. example.com); routes will be g-<port>.domain")
	fmt.Fprintln(e.Stdout, "  zone_id     — Cloudflare Zone ID for that domain (right sidebar of the domain page)")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "After saving, run:")
	fmt.Fprintln(e.Stdout, "  cf-tunnel status       # should print 'config: ready (prod)'")
	fmt.Fprintln(e.Stdout, "  cf-tunnel list         # lists current tunnel routes")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Reminder: never paste the api_token into chat — only edit the file.")
	return nil
}

// ── status ─────────────────────────────────────────────────────────────────

func (e *Env) runCFTunnelStatus() error {
	cfgPath := cfTunnelConfigFile()
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintln(e.Stdout, "config: missing (run `cf-tunnel config`)")
		return nil
	}
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		fmt.Fprintf(e.Stdout, "config: parse error: %v\n", err)
		return nil
	}
	for _, envName := range []string{"prod", "dev"} {
		envCfg, ok := cfg[envName]
		if !ok {
			continue
		}
		if isCFTunnelPlaceholder(envCfg) {
			fmt.Fprintf(e.Stdout, "config: placeholder (%s) — fill in cf.json (run `cf-tunnel config`)\n", envName)
			continue
		}
		fmt.Fprintf(e.Stdout, "config: ready (%s)\n", envName)
		fmt.Fprintf(e.Stdout, "  api_token:    %s\n", maskAK(envCfg.APIToken))
		fmt.Fprintf(e.Stdout, "  account_id:   %s\n", envCfg.AccountID)
		fmt.Fprintf(e.Stdout, "  tunnel_id:    %s\n", envCfg.TunnelID)
		fmt.Fprintf(e.Stdout, "  domain:       %s\n", envCfg.Domain)
		fmt.Fprintf(e.Stdout, "  zone_id:      %s\n", envCfg.ZoneID)
		if envCfg.TunnelToken != "" {
			fmt.Fprintf(e.Stdout, "  tunnel_token: %s (cached)\n", maskAK(envCfg.TunnelToken))
		} else {
			fmt.Fprintf(e.Stdout, "  tunnel_token: not cached (run `cf-tunnel daemon token` to fetch)\n")
		}
	}
	fmt.Fprintln(e.Stdout, "")
	cfDaemonStatus(e.Stdout)
	return nil
}

// ── list / add / del ───────────────────────────────────────────────────────

func (e *Env) runCFTunnelOp(args []string) error {
	cfEnv := envOr("CF_ENV", "prod")
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `cf-tunnel config` first")
		}
		return err
	}
	envCfg, ok := cfg[cfEnv]
	if !ok || isCFTunnelPlaceholder(envCfg) {
		return fmt.Errorf("cf.json missing or incomplete for env %q — run `cf-tunnel config`", cfEnv)
	}

	ct := &cfTunnel{
		token:       envCfg.APIToken,
		accountID:   envCfg.AccountID,
		tunnelID:    envCfg.TunnelID,
		tunnelCNAME: envCfg.TunnelID + ".cfargotunnel.com",
		domain:      envCfg.Domain,
		zoneID:      envCfg.ZoneID,
		http:        e.HTTP,
		stdout:      e.Stdout,
	}
	switch args[0] {
	case "list":
		return ct.list()
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("Usage: cf-tunnel add <port> [port2 ...]")
		}
		return ct.add(parsePorts(args[1:]))
	case "del":
		if len(args) < 2 {
			return fmt.Errorf("Usage: cf-tunnel del <port> [port2 ...]")
		}
		return ct.del(parsePorts(args[1:]))
	default:
		return fmt.Errorf("unknown cf-tunnel command: %s", args[0])
	}
}

// ── cfTunnel struct and API methods ────────────────────────────────────────

type cfTunnel struct {
	token       string
	accountID   string
	tunnelID    string
	tunnelCNAME string
	domain      string
	zoneID      string
	http        *http.Client
	stdout      io.Writer
}

func (c *cfTunnel) hostnameFor(port int) string { return fmt.Sprintf("g-%d.%s", port, c.domain) }

func (c *cfTunnel) api(method, path string, payload any) (map[string]any, error) {
	endpoint := "https://api.cloudflare.com/client/v4/" + path
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cfTunnel) getConfig() (map[string]any, error) {
	out, err := c.api(http.MethodGet, fmt.Sprintf("accounts/%s/cfd_tunnel/%s/configurations", c.accountID, c.tunnelID), nil)
	if err != nil {
		return nil, err
	}
	if success, _ := out["success"].(bool); !success {
		return nil, fmt.Errorf("get tunnel config failed: %v", out["errors"])
	}
	result := asMap(out["result"])
	return asMap(result["config"]), nil
}

func (c *cfTunnel) putConfig(config map[string]any) error {
	out, err := c.api(http.MethodPut, fmt.Sprintf("accounts/%s/cfd_tunnel/%s/configurations", c.accountID, c.tunnelID), map[string]any{"config": config})
	if err != nil {
		return err
	}
	if success, _ := out["success"].(bool); !success {
		return fmt.Errorf("put tunnel config failed: %v", out["errors"])
	}
	return nil
}

func (c *cfTunnel) dnsList() ([]map[string]any, error) {
	var all []map[string]any
	for page := 1; ; page++ {
		out, err := c.api(http.MethodGet, fmt.Sprintf("zones/%s/dns_records?type=CNAME&per_page=100&page=%d", c.zoneID, page), nil)
		if err != nil {
			return nil, err
		}
		if success, _ := out["success"].(bool); !success {
			return nil, fmt.Errorf("dns list failed: %v", out["errors"])
		}
		result, _ := out["result"].([]any)
		if len(result) == 0 {
			break
		}
		for _, item := range result {
			all = append(all, asMap(item))
		}
		if len(result) < 100 {
			break
		}
	}
	return all, nil
}

func (c *cfTunnel) dnsAdd(hostname string) bool {
	out, err := c.api(http.MethodPost, fmt.Sprintf("zones/%s/dns_records", c.zoneID), map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": c.tunnelCNAME,
		"proxied": true,
		"ttl":     1,
	})
	if err != nil {
		return false
	}
	if success, _ := out["success"].(bool); success {
		return true
	}
	errorsList, _ := out["errors"].([]any)
	for _, item := range errorsList {
		if strings.Contains(strings.ToLower(fmt.Sprint(item)), "already") {
			return true
		}
	}
	return false
}

func (c *cfTunnel) dnsDel(hostname string) bool {
	records, err := c.dnsList()
	if err != nil {
		return false
	}
	for _, rec := range records {
		if anyString(rec["name"]) == hostname {
			out, err := c.api(http.MethodDelete, fmt.Sprintf("zones/%s/dns_records/%s", c.zoneID, anyString(rec["id"])), nil)
			if err != nil {
				return false
			}
			success, _ := out["success"].(bool)
			return success
		}
	}
	return false
}

func (c *cfTunnel) list() error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	count := 0
	for _, item := range ingress {
		if _, ok := asMap(item)["hostname"]; ok {
			count++
		}
	}
	_, _ = fmt.Fprintf(c.stdout, "Tunnel routes (%d):\n\n", count)
	for _, item := range ingress {
		rule := asMap(item)
		hostname := anyString(rule["hostname"])
		if hostname == "" {
			hostname = "(catch-all)"
		}
		service := anyString(rule["service"])
		status := ""
		if strings.Contains(service, "localhost:") {
			parts := strings.Split(service, ":")
			port, _ := strconv.Atoi(parts[len(parts)-1])
			if portListening(port) {
				status = " [up]"
			} else {
				status = " [down]"
			}
		}
		_, _ = fmt.Fprintf(c.stdout, "  %s → %s%s\n", hostname, service, status)
	}
	return nil
}

func (c *cfTunnel) add(ports []int) error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	catchAll := map[string]any{"service": "http_status:404"}
	var rules []map[string]any
	for _, item := range ingress {
		rule := asMap(item)
		if anyString(rule["hostname"]) == "" {
			catchAll = rule
			continue
		}
		rules = append(rules, rule)
	}
	existing := map[string]bool{}
	for _, rule := range rules {
		existing[anyString(rule["hostname"])] = true
	}
	type addItem struct {
		port     int
		hostname string
	}
	var added []addItem
	for _, port := range ports {
		hostname := c.hostnameFor(port)
		if existing[hostname] {
			_, _ = fmt.Fprintf(c.stdout, "  skip: %s already exists\n", hostname)
			continue
		}
		if !portListening(port) {
			_, _ = fmt.Fprintf(c.stdout, "  warn: localhost:%d not listening, adding route anyway\n", port)
		}
		rules = append(rules, map[string]any{"hostname": hostname, "service": fmt.Sprintf("http://localhost:%d", port)})
		added = append(added, addItem{port: port, hostname: hostname})
	}
	if len(added) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "no new routes added")
		return nil
	}
	config["ingress"] = append(anySliceFromMap(rules), catchAll)
	if err := c.putConfig(config); err != nil {
		return err
	}
	for _, item := range added {
		ok := c.dnsAdd(item.hostname)
		dnsStatus := "dns:ok"
		if !ok {
			dnsStatus = "dns:fail"
		}
		_, _ = fmt.Fprintf(c.stdout, "  added: %s → localhost:%d  (%s)\n", item.hostname, item.port, dnsStatus)
	}
	_, _ = fmt.Fprintf(c.stdout, "\nadded %d route(s)\n", len(added))
	return nil
}

func (c *cfTunnel) del(ports []int) error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	catchAll := map[string]any{"service": "http_status:404"}
	var rules []map[string]any
	for _, item := range ingress {
		rule := asMap(item)
		if anyString(rule["hostname"]) == "" {
			catchAll = rule
			continue
		}
		rules = append(rules, rule)
	}
	toDelete := map[string]bool{}
	for _, port := range ports {
		toDelete[c.hostnameFor(port)] = true
	}
	var kept []map[string]any
	var removed []string
	for _, rule := range rules {
		if toDelete[anyString(rule["hostname"])] {
			removed = append(removed, anyString(rule["hostname"]))
		} else {
			kept = append(kept, rule)
		}
	}
	if len(removed) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "no matching routes found")
		return nil
	}
	config["ingress"] = append(anySliceFromMap(kept), catchAll)
	if err := c.putConfig(config); err != nil {
		return err
	}
	for _, hostname := range removed {
		ok := c.dnsDel(hostname)
		dnsStatus := "dns:ok"
		if !ok {
			dnsStatus = "dns:not found"
		}
		_, _ = fmt.Fprintf(c.stdout, "  removed: %s  (%s)\n", hostname, dnsStatus)
	}
	_, _ = fmt.Fprintf(c.stdout, "\nremoved %d route(s)\n", len(removed))
	return nil
}

// ── daemon ─────────────────────────────────────────────────────────────────

func (e *Env) runCFTunnelDaemon(args []string) error {
	if len(args) == 0 || args[0] == "status" {
		cfDaemonStatus(e.Stdout)
		return nil
	}
	switch args[0] {
	case "token":
		return e.runCFTunnelFetchToken()
	case "install":
		return e.runCFTunnelDaemonInstall()
	case "uninstall":
		return e.runCFTunnelDaemonUninstall()
	case "start":
		return svcCtl(e.Stdout, e.Stderr, "start", "cloudflared")
	case "stop":
		return svcCtl(e.Stdout, e.Stderr, "stop", "cloudflared")
	case "restart":
		return svcCtl(e.Stdout, e.Stderr, "restart", "cloudflared")
	case "logs":
		n := "50"
		if len(args) > 1 {
			n = args[1]
		}
		if hasSystemctl() {
			return runPassthrough(e.Stdout, e.Stderr, "journalctl", "-u", "cloudflared", "-n", n, "--no-pager")
		}
		// SysV: try dedicated log file first, then syslog
		for _, path := range []string{"/var/log/cloudflared.err", "/var/log/cloudflared.log"} {
			if _, err := os.Stat(path); err == nil {
				return runPassthrough(e.Stdout, e.Stderr, "tail", "-n", n, path)
			}
		}
		return runPassthrough(e.Stdout, e.Stderr, "grep", "-i", "cloudflared", "/var/log/syslog")
	default:
		return fmt.Errorf("unknown daemon subcommand: %s (try: status, install, uninstall, start, stop, restart, logs, token)", args[0])
	}
}

// hasSystemctl returns true when systemctl is available (systemd systems).
func hasSystemctl() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

// svcBin returns the full path to the `service` binary, checking both PATH and /usr/sbin.
func svcBin() string {
	if p, err := exec.LookPath("service"); err == nil {
		return p
	}
	if _, err := os.Stat("/usr/sbin/service"); err == nil {
		return "/usr/sbin/service"
	}
	return "service"
}

// svcIsActive returns the active state of a service as a canonical string
// ("active", "inactive", "failed", "unknown").
func svcIsActive(name string) string {
	if hasSystemctl() {
		out, _ := exec.Command("systemctl", "is-active", name).Output()
		return strings.TrimSpace(string(out))
	}
	// SysV: `sudo service <name> status` (service may be in /usr/sbin, not in user PATH)
	svc := svcBin()
	var cmd *exec.Cmd
	if os.Getuid() != 0 {
		cmd = exec.Command("sudo", svc, name, "status")
	} else {
		cmd = exec.Command(svc, name, "status")
	}
	out, _ := cmd.CombinedOutput()
	lower := strings.ToLower(string(out))
	switch {
	case strings.Contains(lower, "running") || strings.Contains(lower, "start/running"):
		return "active"
	case strings.Contains(lower, "stop") || strings.Contains(lower, "not running"):
		return "inactive"
	default:
		return "unknown"
	}
}

// svcCtl runs a service lifecycle action (start/stop/restart/enable).
// Falls back from systemctl to SysV `service`; silently skips enable/disable on SysV.
func svcCtl(stdout, stderr io.Writer, action, name string) error {
	if hasSystemctl() {
		return runSudo(stdout, stderr, "systemctl", action, name)
	}
	if action == "enable" || action == "disable" {
		return nil // no SysV equivalent
	}
	return runSudo(stdout, stderr, svcBin(), name, action)
}

// cfDaemonStatus prints cloudflared binary + service state.
func cfDaemonStatus(w io.Writer) {
	// binary
	binPath, err := exec.LookPath("cloudflared")
	if err != nil {
		fmt.Fprintln(w, "daemon: cloudflared not installed (run `cf-tunnel daemon install`)")
		return
	}
	ver := ""
	if out, err := exec.Command(binPath, "--version").Output(); err == nil {
		ver = strings.TrimSpace(string(out))
	}
	fmt.Fprintf(w, "daemon: cloudflared installed  %s  (%s)\n", ver, binPath)

	// service
	switch svcIsActive("cloudflared") {
	case "active":
		fmt.Fprintln(w, "service: running (active)")
	case "inactive":
		fmt.Fprintln(w, "service: stopped (inactive) — run `cf-tunnel daemon start`")
	case "failed":
		fmt.Fprintln(w, "service: FAILED — run `cf-tunnel daemon logs` to diagnose")
	default:
		// Check whether it is installed at all.
		if hasSystemctl() {
			out2, _ := exec.Command("systemctl", "status", "cloudflared").CombinedOutput()
			if strings.Contains(string(out2), "could not be found") || strings.Contains(string(out2), "No such file") {
				fmt.Fprintln(w, "service: not installed — run `cf-tunnel daemon install`")
				return
			}
		} else {
			// SysV: check for the init script
			if _, err := os.Stat("/etc/init.d/cloudflared"); err != nil {
				fmt.Fprintln(w, "service: not installed — run `cf-tunnel daemon install`")
				return
			}
		}
		fmt.Fprintln(w, "service: unknown state")
	}
}

// runCFTunnelFetchToken fetches the tunnel connector token from the CF API and
// caches it in cf.json under the current CF_ENV block.
func (e *Env) runCFTunnelFetchToken() error {
	cfEnv := envOr("CF_ENV", "prod")
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `cf-tunnel config` first")
		}
		return err
	}
	envCfg, ok := cfg[cfEnv]
	if !ok || isCFTunnelPlaceholder(envCfg) {
		return fmt.Errorf("cf.json missing or incomplete for env %q — run `cf-tunnel config`", cfEnv)
	}

	fmt.Fprintf(e.Stdout, "fetching tunnel token for tunnel %s ...\n", envCfg.TunnelID)
	ct := &cfTunnel{token: envCfg.APIToken, accountID: envCfg.AccountID, tunnelID: envCfg.TunnelID, http: e.HTTP}
	token, err := ct.fetchToken()
	if err != nil {
		return fmt.Errorf("fetch token: %w", err)
	}

	envCfg.TunnelToken = token
	cfg[cfEnv] = envCfg
	if err := saveCFTunnelConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(e.Stdout, "tunnel_token: %s (cached in cf.json)\n", maskAK(token))
	return nil
}

// runCFTunnelDaemonInstall installs cloudflared if missing, fetches the tunnel
// token, runs `cloudflared service install`, and starts the service.
func (e *Env) runCFTunnelDaemonInstall() error {
	cfEnv := envOr("CF_ENV", "prod")
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `cf-tunnel config` first")
		}
		return err
	}
	envCfg, ok := cfg[cfEnv]
	if !ok || isCFTunnelPlaceholder(envCfg) {
		return fmt.Errorf("cf.json missing or incomplete for env %q — run `cf-tunnel config`", cfEnv)
	}

	// 1. Install cloudflared binary if missing
	if _, err := exec.LookPath("cloudflared"); err != nil {
		fmt.Fprintln(e.Stdout, "cloudflared not found — installing ...")
		if err := installCloudflared(e.Stdout, e.Stderr); err != nil {
			return fmt.Errorf("install cloudflared: %w", err)
		}
		fmt.Fprintln(e.Stdout, "cloudflared installed")
	} else {
		out, _ := exec.Command("cloudflared", "--version").Output()
		fmt.Fprintf(e.Stdout, "cloudflared already installed: %s\n", strings.TrimSpace(string(out)))
	}

	// 2. Fetch / reuse tunnel token
	token := envCfg.TunnelToken
	if token == "" {
		fmt.Fprintf(e.Stdout, "fetching tunnel token for %s ...\n", envCfg.TunnelID)
		ct := &cfTunnel{token: envCfg.APIToken, accountID: envCfg.AccountID, tunnelID: envCfg.TunnelID, http: e.HTTP}
		token, err = ct.fetchToken()
		if err != nil {
			return fmt.Errorf("fetch token: %w", err)
		}
		envCfg.TunnelToken = token
		cfg[cfEnv] = envCfg
		if saveErr := saveCFTunnelConfig(cfg); saveErr != nil {
			fmt.Fprintf(e.Stderr, "warn: could not cache token: %v\n", saveErr)
		} else {
			fmt.Fprintln(e.Stdout, "tunnel_token cached in cf.json")
		}
	} else {
		fmt.Fprintf(e.Stdout, "using cached tunnel_token: %s\n", maskAK(token))
	}

	// 3. Install as service
	fmt.Fprintln(e.Stdout, "installing cloudflared as system service ...")
	if err := runSudo(e.Stdout, e.Stderr, "cloudflared", "service", "install", token); err != nil {
		return fmt.Errorf("service install: %w", err)
	}
	fmt.Fprintln(e.Stdout, "service installed")

	// 4. Enable + start
	_ = svcCtl(e.Stdout, e.Stderr, "enable", "cloudflared")
	if err := svcCtl(e.Stdout, e.Stderr, "start", "cloudflared"); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Fprintln(e.Stdout, "cloudflared service started")
	fmt.Fprintln(e.Stdout, "")
	cfDaemonStatus(e.Stdout)
	return nil
}

func (e *Env) runCFTunnelDaemonUninstall() error {
	fmt.Fprintln(e.Stdout, "stopping cloudflared service ...")
	_ = svcCtl(e.Stdout, e.Stderr, "stop", "cloudflared")
	fmt.Fprintln(e.Stdout, "removing cloudflared service ...")
	if err := runSudo(e.Stdout, e.Stderr, "cloudflared", "service", "uninstall"); err != nil {
		return fmt.Errorf("service uninstall: %w", err)
	}
	fmt.Fprintln(e.Stdout, "cloudflared service removed")
	return nil
}

// fetchToken retrieves the cloudflared connector token for the tunnel from the CF API.
func (c *cfTunnel) fetchToken() (string, error) {
	out, err := c.api(http.MethodGet, fmt.Sprintf("accounts/%s/cfd_tunnel/%s/token", c.accountID, c.tunnelID), nil)
	if err != nil {
		return "", err
	}
	if success, _ := out["success"].(bool); !success {
		return "", fmt.Errorf("API error: %v", out["errors"])
	}
	token, _ := out["result"].(string)
	if token == "" {
		return "", fmt.Errorf("empty token in API response")
	}
	return token, nil
}

// installCloudflared downloads the cloudflared binary for the current arch and
// places it at /usr/local/bin/cloudflared.
func installCloudflared(stdout, stderr io.Writer) error {
	arch := cloudflaredArch()
	url := fmt.Sprintf("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-%s", arch)
	dest := "/tmp/cloudflared-download"
	fmt.Fprintf(stdout, "downloading %s ...\n", url)
	if err := runPassthrough(stdout, stderr, "curl", "-fsSL", "-o", dest, url); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if err := runSudo(stdout, stderr, "mv", dest, "/usr/local/bin/cloudflared"); err != nil {
		return fmt.Errorf("move binary: %w", err)
	}
	return runSudo(stdout, stderr, "chmod", "+x", "/usr/local/bin/cloudflared")
}

func cloudflaredArch() string {
	out, _ := exec.Command("uname", "-m").Output()
	switch strings.TrimSpace(string(out)) {
	case "aarch64", "arm64":
		return "arm64"
	default:
		return "amd64"
	}
}

// runSudo runs a command, prepending sudo if not already root.
func runSudo(stdout, stderr io.Writer, name string, args ...string) error {
	if os.Getuid() != 0 {
		args = append([]string{name}, args...)
		name = "sudo"
	}
	return runPassthrough(stdout, stderr, name, args...)
}

// runPassthrough runs a command, streaming stdout/stderr to the given writers.
func runPassthrough(stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// ── helpers ────────────────────────────────────────────────────────────────

func parsePorts(args []string) []int {
	var ports []int
	for _, arg := range args {
		if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil {
			ports = append(ports, n)
		}
	}
	return ports
}

func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func anySliceFromMap(items []map[string]any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
