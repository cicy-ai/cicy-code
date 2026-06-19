package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultMihomoMixedPort = "9001"
const defaultMihomoControllerPort = "19001"

// mihomoController returns the mihomo external-controller base URL. Tests run
// on the same host as the api (no docker hop), so the loopback default is
// correct in both prod (api+mihomo on host) and dev container (api+mihomo
// co-located inside the container). Override with CICY_MIHOMO_CONTROLLER if
// you ever need to point it elsewhere.
func mihomoController() string {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_CONTROLLER")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:" + defaultMihomoControllerPort
}

// mihomoControllerAlive reports whether mihomo's external controller actually
// answers — the AUTHORITATIVE liveness check, independent of the wrapper's PID
// file (which goes stale across a restart). Polls until `within` elapses so it
// can be used right after a start to wait for mihomo to come up.
func mihomoControllerAlive(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mihomoController()+"/version", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// mihomoMixedAddr returns "host:port" for the mihomo mixed proxy port, used
// when we need to actually run traffic through mihomo (e.g. exit-IP test).
func mihomoMixedAddr() string {
	host := strings.TrimSpace(os.Getenv("CICY_MIHOMO_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := strings.TrimSpace(os.Getenv("CICY_MIHOMO_PORT"))
	if port == "" {
		port = defaultMihomoMixedPort
	}
	return host + ":" + port
}

// readMihomoGlobalPasswordFromYAML pulls the top-level `globalPassword:` value
// from ~/cicy-ai/db/mihomo.yaml. Empty when missing — callers should fall back
// gracefully (e.g. skip the exit-IP test rather than fail the whole request).
//
// CICY_MIHOMO_GLOBAL_PASSWORD env wins when set — used in dev/docker mode where
// the api container can't read the host's yaml. dev.py reads the host file
// once at startup and passes the value through.
func readMihomoGlobalPasswordFromYAML() string {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_GLOBAL_PASSWORD")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mihomo.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "globalPassword:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "globalPassword:"))
		return strings.Trim(v, "\"' ")
	}
	return ""
}

// isMihomoGroupType reports whether a proxy `type` field returned by the
// controller represents a selectable group (rather than a leaf node). Mirrors
// mihomo's adapter classification; built-in pseudo-proxies (DIRECT/REJECT/PASS
// /COMPATIBLE/GLOBAL) are filtered separately by name.
func isMihomoGroupType(t string) bool {
	switch t {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		return true
	}
	return false
}

// isMihomoBuiltinName reports whether a proxy name is a mihomo built-in we
// don't want to surface in the UI picker (the GLOBAL group, DIRECT/REJECT
// adapters, etc.).
func isMihomoBuiltinName(name string) bool {
	switch name {
	case "GLOBAL", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE":
		return true
	}
	return false
}

// handleProxyList — GET /api/proxy/list
// Returns {success, groups: [...], nodes: [...]}. Each entry has name, type,
// and last_delay_ms (0 if never tested). Groups also expose `now` and `members`.
func handleProxyList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mihomoController()+"/proxies", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpErr(w, 502, "mihomo controller unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpErr(w, 502, fmt.Sprintf("mihomo controller status %d", resp.StatusCode))
		return
	}
	var body struct {
		Proxies map[string]struct {
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			All     []string `json:"all"`
			History []struct {
				Time  string `json:"time"`
				Delay int    `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		httpErr(w, 502, "parse: "+err.Error())
		return
	}
	groups := []M{}
	nodes := []M{}
	for name, p := range body.Proxies {
		if isMihomoBuiltinName(name) {
			continue
		}
		var lastDelay int
		if len(p.History) > 0 {
			lastDelay = p.History[len(p.History)-1].Delay
		}
		item := M{
			"name":          name,
			"type":          p.Type,
			"last_delay_ms": lastDelay,
		}
		if isMihomoGroupType(p.Type) {
			item["now"] = p.Now
			// Filter ONLY GLOBAL from group members (it's mihomo's auto-rollup
			// of every proxy+group, would appear as a self-reference and is
			// never in user config). DIRECT / REJECT / REJECT-DROP are valid
			// user choices when the operator deliberately puts them in
			// `proxy-groups[].proxies` — DIRECT = "this group bypasses proxy",
			// REJECT = "drop this group's traffic" — so they must reach the UI
			// to be switchable. Stripping them was the cause of "I added DIRECT
			// to default_proxy_group but the panel still shows only one option".
			cleanMembers := make([]string, 0, len(p.All))
			for _, m := range p.All {
				if m == "GLOBAL" {
					continue
				}
				cleanMembers = append(cleanMembers, m)
			}
			item["members"] = cleanMembers
			groups = append(groups, item)
		} else {
			nodes = append(nodes, item)
		}
	}
	J(w, M{"success": true, "groups": groups, "nodes": nodes})
}

// handleProxyTest — POST /api/proxy/test
//
// Body: {"name": "<proxy_or_group>", "urls": ["https://...", ...]}
//
// For each URL, asks mihomo's controller for a latency probe via
// /proxies/<name>/delay (which is non-mutating — it dials a one-off TCP+HTTP
// HEAD and returns the rtt without changing the active selection).
//
// Also runs an exit-IP probe: it temporarily PUTs the named proxy into
// `default_proxy_group`, races the ipExitProbes pool (api.myip.com +
// api.ip.sb/geoip — whichever responds first) through the mihomo mixed
// port, then restores the previous selection. Concurrent tests can race
// on the selection — accept that for a debugging tool; callers are
// expected to not fan out N tests in parallel.
func handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string   `json:"name"`
		URLs []string `json:"urls"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpErr(w, 400, "name required")
		return
	}
	if len(req.URLs) == 0 {
		req.URLs = []string{
			"https://api.anthropic.com",
			"https://chatgpt.com",
			"https://api.myip.com",
		}
	}

	results := []M{}
	for _, target := range req.URLs {
		results = append(results, mihomoDelayProbe(r.Context(), name, target))
	}
	ip := mihomoExitIPProbe(name)
	J(w, M{
		"success": true,
		"name":    name,
		"results": results,
		"ip":      ip,
	})
}

// mihomoDelayProbe asks the controller to time a TCP+HEAD against `target`
// via `name`, with a 5-second budget. Returns a result row suitable for the
// UI: {url, ok, delay_ms?, error?}.
func mihomoDelayProbe(parent context.Context, name, target string) M {
	delayURL := fmt.Sprintf("%s/proxies/%s/delay?timeout=5000&url=%s",
		mihomoController(),
		url.PathEscape(name),
		url.QueryEscape(target),
	)
	ctx, cancel := context.WithTimeout(parent, 7*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, delayURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return M{"url": target, "ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	var d struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return M{"url": target, "ok": false, "error": msg}
	}
	return M{"url": target, "ok": true, "delay_ms": d.Delay}
}

// mihomoExitIPProbe runs an actual HTTP request through the mihomo mixed
// port, routed via `name`, racing every entry in ipExitProbes and taking
// whichever returns first. It does this by briefly mutating the
// `default_proxy_group` selection (PUT /proxies/default_proxy_group), then
// running the probe race with the mihomo proxy URL, then restoring the
// previous selection. Returns {ok, ip, country, cc, source[, asn, city]}.
//
// curl is used inside the race (vs http.Client) so we get robust auth +
// CONNECT semantics without re-implementing CONNECT-via-proxy here.
func mihomoExitIPProbe(name string) M {
	password := readMihomoGlobalPasswordFromYAML()
	if password == "" {
		return M{"ok": false, "error": "no globalPassword in mihomo.yaml"}
	}

	// `default_proxy_group` is the group that worker traffic flows through
	// (see IN-USER-PREFIX,w-,default_proxy_group). Switching it to itself is a
	// no-op the controller rejects with HTTP 400, so skip the switch when the
	// caller asked about the group itself — the probe naturally exercises
	// whatever node is currently selected. For any other target we temporarily
	// repoint the group to it, probe, then restore.
	skipSwitch := name == "default_proxy_group"
	var prevSelection string
	if !skipSwitch {
		prevSelection = readMihomoGroupSelection("default_proxy_group")
		if err := setMihomoGroupSelection("default_proxy_group", name); err != nil {
			return M{"ok": false, "error": "switch: " + err.Error()}
		}
	}
	defer func() {
		if !skipSwitch && prevSelection != "" && prevSelection != name {
			_ = setMihomoGroupSelection("default_proxy_group", prevSelection)
		}
	}()

	proxyURL := fmt.Sprintf("http://w-proxytest:%s@%s", password, mihomoMixedAddr())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	base := []string{"-sS", "-m", "8", "-x", proxyURL}
	parsed, ok, _ := probeExitIPRace(ctx, base)
	if !ok {
		return M{"ok": false, "error": "all exit-IP probes failed"}
	}
	out := M{
		"ok":      true,
		"ip":      parsed["ip"],
		"country": parsed["country"],
		"cc":      parsed["cc"],
		"source":  parsed["source"],
	}
	if asn, has := parsed["asn"]; has {
		out["asn"] = asn
	}
	if city, has := parsed["city"]; has {
		out["city"] = city
	}
	return out
}

// readMihomoGroupSelection returns the currently-selected member of a group,
// or "" on any error. Best-effort.
func readMihomoGroupSelection(group string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		mihomoController()+"/proxies/"+url.PathEscape(group), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		Now string `json:"now"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return body.Now
}

func setMihomoGroupSelection(group, member string) error {
	payload := fmt.Sprintf(`{"name":%q}`, member)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		mihomoController()+"/proxies/"+url.PathEscape(group),
		strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// ── exit-IP probe sources (race the first responder) ────────────────────
//
// api.myip.com used to be the single source; it'd occasionally 5xx / time
// out and the whole 🌍 panel would degrade. We now race a small list and
// take whoever returns a parseable IP first — cancel the stragglers.
//
// The catch: api.myip.com / api.ip.sb are *foreign* and unreachable from a
// mainland-China network when probing **direct** (no proxy) — so the 🌍 panel's
// "direct" side couldn't get IP/area without a VPN. ipip.net (myip.ipip.net)
// fixes both sides: it's a domestic site reachable from China without 翻墙 AND
// it correctly reports a foreign egress IP when probing through an overseas
// proxy, so one source covers 国内+国外. Because it's a first-responder race,
// ipip.net wins on a direct CN probe while the foreign sources win (or tie)
// abroad — both report whatever IP actually hits them, so it stays consistent.
// (The Cloudflare-fronted alternatives ifconfig.co / ipapi.co / freeipapi.com
// return WAF challenges to curl's UA, so they're deliberately not in the race.)
//
// To add another source: append to ipExitProbes with a parser that maps
// whatever JSON it returns into the common {ip, country, cc, source[, asn,
// city]} shape — the callers (curlExitIP / mihomoExitIPProbe) rename keys
// to fit their existing output contract.

type ipExitProbe struct {
	name  string
	url   string
	parse func(body []byte) (M, bool)
}

var ipExitProbes = []ipExitProbe{
	{
		name: "myip.com",
		url:  "https://api.myip.com",
		parse: func(body []byte) (M, bool) {
			var p struct {
				IP, Country, CC string
			}
			if json.Unmarshal(body, &p) != nil || p.IP == "" {
				return nil, false
			}
			return M{"ip": p.IP, "country": p.Country, "cc": p.CC, "source": "myip.com"}, true
		},
	},
	{
		name: "ip.sb",
		url:  "https://api.ip.sb/geoip",
		parse: func(body []byte) (M, bool) {
			var p struct {
				IP              string `json:"ip"`
				Country         string `json:"country"`
				CountryCode     string `json:"country_code"`
				City            string `json:"city"`
				ASN             int    `json:"asn"`
				ASNOrganization string `json:"asn_organization"`
			}
			if json.Unmarshal(body, &p) != nil || p.IP == "" {
				return nil, false
			}
			m := M{"ip": p.IP, "country": p.Country, "cc": p.CountryCode, "source": "ip.sb"}
			if p.City != "" {
				m["city"] = p.City
			}
			if p.ASNOrganization != "" {
				m["asn"] = fmt.Sprintf("AS%d %s", p.ASN, p.ASNOrganization)
			}
			return m, true
		},
	},
	{
		// ipip.net — reachable from mainland China without 翻墙.
		// {"ret":"ok","data":{"ip":"1.2.3.4","location":["中国","江苏","南京","","电信"]}}
		name: "ipip.net",
		url:  "https://myip.ipip.net/json",
		parse: func(body []byte) (M, bool) {
			var p struct {
				Ret  string `json:"ret"`
				Data struct {
					IP       string   `json:"ip"`
					Location []string `json:"location"`
				} `json:"data"`
			}
			if json.Unmarshal(body, &p) != nil || p.Data.IP == "" {
				return nil, false
			}
			country, city := "", ""
			if loc := p.Data.Location; len(loc) > 0 {
				country = loc[0]
				if len(loc) > 2 {
					city = loc[2]
				}
			}
			cc := ""
			if strings.Contains(country, "中国") || strings.EqualFold(country, "China") {
				cc = "CN"
			}
			m := M{"ip": p.Data.IP, "country": country, "cc": cc, "source": "ipip.net"}
			if city != "" {
				m["city"] = city
			}
			return m, true
		},
	},
}

// probeExitIPRace fans out the same curlBaseArgs (proxy/--noproxy/timeouts)
// to every entry in ipExitProbes in parallel and returns the first one whose
// body parses to a non-empty IP. Stragglers get cancelled via a shared ctx
// (curl picks up the kill via signal). Returns (m, true, elapsedMs) on first
// success, or (nil, false, elapsedMs) when every probe failed within budget.
//
// The returned M is the parser's raw common shape — callers (curlExitIP,
// mihomoExitIPProbe) rename keys + add their own outer fields (via, ok,
// elapsed_ms) before returning to the HTTP layer.
func probeExitIPRace(parent context.Context, curlBaseArgs []string) (M, bool, int64) {
	start := time.Now()
	raceCtx, cancel := context.WithCancel(parent)
	defer cancel()
	type result struct {
		m  M
		ok bool
	}
	results := make(chan result, len(ipExitProbes))
	for _, p := range ipExitProbes {
		p := p
		go func() {
			args := append([]string{}, curlBaseArgs...)
			args = append(args, p.url)
			out, err := exec.CommandContext(raceCtx, "curl", args...).Output()
			if err != nil {
				results <- result{ok: false}
				return
			}
			m, ok := p.parse(out)
			results <- result{m: m, ok: ok}
		}()
	}
	for i := 0; i < len(ipExitProbes); i++ {
		r := <-results
		if r.ok {
			return r.m, true, time.Since(start).Milliseconds()
		}
	}
	return nil, false, time.Since(start).Milliseconds()
}

// curlExitIP fetches the egress IP + area through whichever of ipExitProbes
// responds first, optionally via proxyURL ("" = direct, --noproxy so env
// proxies don't leak in). 5s budget for the whole race. Reports elapsed_ms
// so the 🌍 panel can show timing; both sides of the comparison use the same
// probe pool so IPs are apples-to-apples.
func curlExitIP(ctx context.Context, via, proxyURL string) M {
	// 8s budget: a CN-Mobile→ssh-tunnel→Linux-mihomo→trans-Pacific-socks5 chain
	// (Mac dev → HK relay → US exit) tops out at ~3.5-5s end-to-end and was
	// failing intermittently at the old 5s cap. handleProxyExitInfo's outer
	// ctx is also 8s, so this uses the full available budget. The direct
	// side still returns in ~0.3s; elapsed_ms reports the true latency.
	base := []string{"-sS", "-m", "8", "--connect-timeout", "5"}
	if proxyURL != "" {
		base = append(base, "-x", proxyURL)
	} else {
		base = append(base, "--noproxy", "*")
	}
	parsed, ok, elapsed := probeExitIPRace(ctx, base)
	if !ok {
		return M{"via": via, "ok": false, "error": "all exit-IP probes failed", "elapsed_ms": elapsed}
	}
	out := M{
		"via":        via,
		"ok":         true,
		"ip":         parsed["ip"],
		"area":       parsed["country"], // existing 🌍-panel shape uses 'area' for country
		"cc":         parsed["cc"],
		"source":     parsed["source"],
		"elapsed_ms": elapsed,
	}
	if asn, has := parsed["asn"]; has {
		out["asn"] = asn
	}
	if city, has := parsed["city"]; has {
		out["city"] = city
	}
	return out
}

// handleProxyExitInfo — GET /api/proxy/exit-info
//
// Powers the 🌍 panel. Fires TWO race-groups to the ipExitProbes pool
// (api.myip.com + api.ip.sb/geoip — whichever responds first) in parallel,
// 5s each: one through the local mihomo (the global proxy at 127.0.0.1:9001,
// no auth → current default_proxy_group selection) and one direct (no proxy).
// Returns ip + area + cc + source [+ asn + city] + elapsed_ms for each. If
// the two IPs match it collapses to ONE group (proxy is effectively direct);
// if they differ it returns BOTH groups (proxy is changing the exit IP — a
// real node is active).
func handleProxyExitInfo(w http.ResponseWriter, r *http.Request) {
	// 10s outer ctx vs the 8s per-probe curl budget — 2s headroom so a probe
	// that runs the full 8s isn't racing the ctx firing SIGKILL before curl
	// can write the body back.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var proxy, direct M
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); proxy = curlExitIP(ctx, "proxy", "http://"+mihomoMixedAddr()) }()
	go func() { defer wg.Done(); direct = curlExitIP(ctx, "direct", "") }()
	wg.Wait()

	pIP, _ := proxy["ip"].(string)
	dIP, _ := direct["ip"].(string)
	match := pIP != "" && pIP == dIP
	groups := []M{}
	if match {
		both := proxy
		both["via"] = "both"
		groups = append(groups, both)
	} else {
		groups = append(groups, proxy, direct)
	}
	J(w, M{
		"success": true,
		"match":   match,
		"groups":  groups,
		"current": readMihomoGroupSelection("default_proxy_group"),
	})
}

// handleProxySelect — POST /api/proxy/select  Body: {"group":"...","member":"..."}
//
// The global-proxy switch: changes which node a mihomo proxy-group selects, so
// all traffic flowing through that group (i.e. the global proxy) now exits via
// the chosen node — or DIRECT. Applied via the controller (live, no restart).
// group defaults to default_proxy_group (what the global proxy + worker traffic
// routes through).
func handleProxySelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Group  string `json:"group"`
		Member string `json:"member"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	group := strings.TrimSpace(req.Group)
	if group == "" {
		group = "default_proxy_group"
	}
	member := strings.TrimSpace(req.Member)
	if member == "" {
		httpErr(w, 400, "member required")
		return
	}
	if err := setMihomoGroupSelection(group, member); err != nil {
		httpErr(w, 502, "select failed: "+err.Error())
		return
	}
	J(w, M{"success": true, "group": group, "member": member, "now": readMihomoGroupSelection(group)})
}

// cicyMihomoBin resolves the wrapper binary path. CICY_MIHOMO_BIN overrides;
// otherwise the PATH lookup runs (the wrapper is typically symlinked into
// ~/.local/bin/cicy-mihomo by the cicy-skills installer).
func cicyMihomoBin() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_BIN")); v != "" {
		return v, nil
	}
	return exec.LookPath("cicy-mihomo")
}

// runCicyMihomo invokes the wrapper with the given subcommand and a short
// budget. Returns (stdout+stderr, error). The wrapper always exits 0 for
// `status` (running or stopped); other subcommands surface failures through
// the exit code, which we propagate as err.
func runCicyMihomo(ctx context.Context, args ...string) (string, error) {
	bin, err := cicyMihomoBin()
	if err != nil {
		return "", fmt.Errorf("cicy-mihomo not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// parseMihomoStatusJSON parses `cicy-mihomo status --json` output
// ({"ok":true,"data":{pid,running,binary,config,log,controller,
// controller_version,...}}) into the M shape handleProxyStatus returns.
// Returns nil when the output isn't the expected JSON envelope (older wrapper
// versions) so the caller can fall back to the legacy text parser.
func parseMihomoStatusJSON(out string) M {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			PID               json.Number `json:"pid"`
			Running           bool        `json:"running"`
			Binary            string      `json:"binary"`
			Config            string      `json:"config"`
			Log               string      `json:"log"`
			Controller        string      `json:"controller"`
			ControllerVersion string      `json:"controller_version"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(out), &envelope) != nil || !envelope.OK {
		return nil
	}
	d := envelope.Data
	pid := d.PID.String()
	if pid == "0" {
		pid = ""
	}
	return M{
		"running":    d.Running,
		"pid":        pid,
		"binary":     d.Binary,
		"config":     d.Config,
		"log":        d.Log,
		"started_at": "",
		"controller": d.Controller,
		"version":    d.ControllerVersion,
	}
}

// parseMihomoStatusOutput extracts pid / binary / config / log / started_at /
// controller / version from the canonical `cicy-mihomo status` block. Missing
// keys map to empty strings. `running` is true when the block starts with
// "status: running".
func parseMihomoStatusOutput(out string) M {
	info := M{
		"running":    false,
		"pid":        "",
		"binary":     "",
		"config":     "",
		"log":        "",
		"started_at": "",
		"controller": "",
		"version":    "",
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		switch key {
		case "status":
			info["running"] = val == "running"
		case "pid", "binary", "config", "log", "started_at", "controller":
			info[key] = val
		case "version":
			// The wrapper prints the controller's /version JSON verbatim:
			// `{"meta":true,"version":"v1.10.2"}`. Don't surface that JSON to
			// the UI — parse it and keep only the human-readable version
			// string. Falls back to the raw line if it isn't JSON-shaped.
			parsed := struct {
				Version string `json:"version"`
			}{}
			if json.Unmarshal([]byte(val), &parsed) == nil && parsed.Version != "" {
				info["version"] = parsed.Version
			} else {
				info["version"] = val
			}
		}
	}
	return info
}

// handleProxyStatus — GET /api/proxy/status
// Returns parsed `cicy-mihomo status` output: {running, pid, binary, config,
// log, started_at, controller, version}.
func handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	out, err := runCicyMihomo(ctx, "status", "--json")
	if err != nil {
		// status always exits 0 normally; non-zero means the wrapper itself
		// failed (e.g. binary missing). Surface the message + raw output so
		// the UI can render something useful instead of a generic 500.
		httpErr(w, 500, fmt.Sprintf("%s: %s", err.Error(), out))
		return
	}
	// The wrapper's text block dropped its `status:` line at some point, which
	// made the legacy parser report a running mihomo as stopped. Prefer the
	// --json envelope; fall back to text parsing for older wrappers.
	info := parseMihomoStatusJSON(out)
	if info == nil {
		info = parseMihomoStatusOutput(out)
	}
	info["raw"] = out
	info["success"] = true
	// The wrapper's `running` is derived from its own PID file, so it misses a
	// mihomo started by anyone else — e.g. the backend's startCicyMihomoIfNeeded
	// (→ /usr/local/bin/mihomo). The controller answering is the authoritative
	// liveness signal, so OR it in; otherwise the drawer shows a "启动" button
	// while mihomo is actually serving traffic.
	if running, _ := info["running"].(bool); !running && mihomoControllerAlive(0) {
		info["running"] = true
	}
	J(w, info)
}

// mihomoYAMLPath returns the canonical mihomo.yaml location.
func mihomoYAMLPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "db", "mihomo.yaml"), nil
}

// readMihomoAllowLAN reads the current `allow-lan:` value (defaults to true if
// the key is missing or the file doesn't exist — matches gen-config's
// template). A read error returns (true, nil) so callers don't have to
// distinguish "file not yet generated" from "real I/O failure."
func readMihomoAllowLAN() (bool, error) {
	path, err := mihomoYAMLPath()
	if err != nil {
		return true, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "allow-lan:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "allow-lan:"))
		v = strings.Trim(v, "\"' ")
		return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes"), nil
	}
	return true, nil
}

// writeMihomoAllowLAN rewrites the `allow-lan:` line in-place. Appends the key
// when missing. Preserves all other formatting (comments / whitespace). The
// caller is responsible for triggering `cicy-mihomo reload`.
//
// If mihomo.yaml doesn't exist yet (mihomo hasn't been started for this user
// before), we run `cicy-mihomo gen-config` to seed the default template first
// so the toggle still works without forcing the user to manually generate.
func writeMihomoAllowLAN(value bool) error {
	path, err := mihomoYAMLPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if _, runErr := runCicyMihomo(ctx, "gen-config"); runErr != nil {
			return fmt.Errorf("mihomo.yaml missing and gen-config failed: %w", runErr)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	repl := "false"
	if value {
		repl = "true"
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		// only match top-level keys (no leading whitespace)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "allow-lan:") {
			lines[i] = "allow-lan: " + repl
			replaced = true
			break
		}
	}
	if !replaced {
		// Insert just after the first non-empty / non-comment line so it sits
		// near other top-level scalars (mixed-port etc.). Falling back to the
		// top is fine if we can't find a good spot.
		insertAt := 0
		for i, line := range lines {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			insertAt = i + 1
			break
		}
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, "allow-lan: "+repl)
		out = append(out, lines[insertAt:]...)
		lines = out
	}
	return writeMihomoYAMLValidated(path, []byte(strings.Join(lines, "\n")))
}

// resolveMihomoBinary returns the mihomo binary path used by the validator,
// honoring MIHOMO_BIN, the canonical install location, and finally $PATH.
// Returns an empty string when none are present.
func resolveMihomoBinary() string {
	for _, cand := range []string{
		strings.TrimSpace(os.Getenv("MIHOMO_BIN")),
		filepath.Join(userHomeForMihomo(), ".local", "bin", "mihomo"),
	} {
		if cand == "" {
			continue
		}
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("mihomo"); err == nil {
		return p
	}
	return ""
}

func userHomeForMihomo() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// writeMihomoYAMLValidated stages data to /tmp, validates it with
// `mihomo -t -f <tmp>`, and only writes to `path` on success. `mihomo -t`
// always exits 0, so we parse stdout for the "test is successful" sentinel —
// exit code is useless. The target file is untouched on validation failure.
//
// /tmp may be on a separate filesystem (tmpfs) from the config dir, so we
// can't `rename` from /tmp directly. After validation passes we re-stage
// into a sibling temp file under the target dir and rename that — the
// rename stays atomic within the same filesystem.
func writeMihomoYAMLValidated(path string, data []byte) error {
	bin := resolveMihomoBinary()
	if bin == "" {
		return os.WriteFile(path, data, 0o644)
	}
	// 1) stage in /tmp for validation
	probe, err := os.CreateTemp("", "mihomo.yaml.validate-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	defer os.Remove(probePath)
	if _, err := probe.Write(data); err != nil {
		probe.Close()
		return err
	}
	if err := probe.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "-t", "-f", probePath).CombinedOutput()
	if !strings.Contains(string(out), "test is successful") {
		return fmt.Errorf("mihomo -t rejected the new config (yaml not written):\n%s", summarizeMihomoTestOutput(out))
	}
	// 2) atomic replace via sibling temp in the target dir
	dir := filepath.Dir(path)
	sibling, err := os.CreateTemp(dir, ".mihomo.yaml.commit-*")
	if err != nil {
		return err
	}
	siblingPath := sibling.Name()
	if _, err := sibling.Write(data); err != nil {
		sibling.Close()
		_ = os.Remove(siblingPath)
		return err
	}
	if err := sibling.Close(); err != nil {
		_ = os.Remove(siblingPath)
		return err
	}
	if err := os.Chmod(siblingPath, 0o644); err != nil {
		_ = os.Remove(siblingPath)
		return err
	}
	return os.Rename(siblingPath, path)
}

func summarizeMihomoTestOutput(out []byte) string {
	var keep []string
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.Contains(t, "level=error") ||
			strings.Contains(t, "test failed") ||
			strings.Contains(t, "test is successful") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		all := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(all) > 5 {
			all = all[len(all)-5:]
		}
		return strings.Join(all, "\n")
	}
	return strings.Join(keep, "\n")
}

// listLANIPv4 returns non-loopback IPv4 addresses bound on this host.
func listLANIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.String()
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
	}
	return out
}

// fetchPublicIPv4 dials ifconfig.me directly (no proxy env) so the result
// reflects this host's egress, not whatever upstream mihomo is currently
// pointing at.
func fetchPublicIPv4(ctx context.Context) (string, error) {
	tr := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: tr, Timeout: 6 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ifconfig.me/ip", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(body)), nil
}

// handleProxyBindMode — GET / PATCH /api/proxy/bind-mode
// GET returns `{allow_lan, listen_addr}`. PATCH {"allow_lan":bool} rewrites
// mihomo.yaml and triggers a reload so the change is live.
func handleProxyBindMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		v, err := readMihomoAllowLAN()
		if err != nil {
			httpErr(w, 500, "read mihomo.yaml: "+err.Error())
			return
		}
		listen := "127.0.0.1"
		if v {
			listen = "0.0.0.0"
		}
		J(w, M{"success": true, "allow_lan": v, "listen_addr": listen})
	case http.MethodPatch, http.MethodPost:
		var req struct {
			AllowLAN *bool `json:"allow_lan"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "bad body: "+err.Error())
			return
		}
		if req.AllowLAN == nil {
			httpErr(w, 400, "allow_lan required")
			return
		}
		if err := writeMihomoAllowLAN(*req.AllowLAN); err != nil {
			httpErr(w, 500, "write mihomo.yaml: "+err.Error())
			return
		}
		// reload so the new allow-lan takes effect without dropping connections
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		reloadOut, reloadErr := runCicyMihomo(ctx, "reload")
		resp := M{"success": true, "allow_lan": *req.AllowLAN, "reload_output": reloadOut}
		if reloadErr != nil {
			// the yaml is already written — bubble the reload failure up
			// but don't 500: the user can manually restart from the UI.
			resp["reload_error"] = reloadErr.Error()
		}
		J(w, resp)
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleProxyExport — GET /api/proxy/export?ip=local|public|<custom>&user=<name>
// Returns the same shell-export lines as `cicy_proxy_show 1`, parameterized by
// IP source and worker username. The default user is "cicy" (any username is
// fine because mihomo's globalPassword accepts wildcards).
func handleProxyExport(w http.ResponseWriter, r *http.Request) {
	mode := strings.TrimSpace(r.URL.Query().Get("ip"))
	if mode == "" {
		mode = "local"
	}
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if user == "" {
		// IN-USER-PREFIX,w-,default_proxy_group is the catch-all for the
		// fleet. Default to a `w-` prefixed name so an out-of-the-box export
		// actually routes; non-`w-` users would auth fine but get REJECTed
		// at the rules stage.
		user = "w-test"
	}
	password := readMihomoGlobalPasswordFromYAML()
	if password == "" {
		password = "<YOUR_PASSWORD_HERE>"
	}
	port := defaultMihomoMixedPort
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_PORT")); v != "" {
		port = v
	}
	lanIPs := listLANIPv4()
	host := "127.0.0.1"
	switch mode {
	case "local", "loopback", "127.0.0.1":
		host = "127.0.0.1"
		mode = "local"
	case "lan":
		if len(lanIPs) > 0 {
			host = lanIPs[0]
		}
	case "public":
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		ip, err := fetchPublicIPv4(ctx)
		if err == nil && ip != "" {
			host = ip
		}
	default:
		// custom IP / hostname literal
		host = mode
		mode = "custom"
	}
	proxyURL := fmt.Sprintf("http://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
	lines := []string{
		fmt.Sprintf("export HTTP_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export HTTPS_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export ALL_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export http_proxy=\"%s\"", proxyURL),
		fmt.Sprintf("export https_proxy=\"%s\"", proxyURL),
		fmt.Sprintf("export all_proxy=\"%s\"", proxyURL),
		"export NO_PROXY=\"localhost,127.0.0.1,::1\"",
		"export no_proxy=\"localhost,127.0.0.1,::1\"",
	}
	allowLAN, _ := readMihomoAllowLAN()
	J(w, M{
		"success":   true,
		"ip_mode":   mode,
		"host":      host,
		"port":      port,
		"user":      user,
		"password":  password,
		"proxy_url": proxyURL,
		"lines":     lines,
		"script":    strings.Join(lines, "\n"),
		"lan_ips":   lanIPs,
		"allow_lan": allowLAN,
	})
}

// handleProxyLifecycle — POST /api/proxy/lifecycle
// Body: {"action": "start"|"stop"|"restart"|"reload"}
// Shells out to the cicy-mihomo wrapper; relays stdout/stderr back so the UI
// can show the user what mihomo said. Unknown actions get 400.
func handleProxyLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		httpErr(w, 400, "action must be one of start|stop|restart|reload")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := runCicyMihomo(ctx, action)
	result := M{
		"success": err == nil,
		"action":  action,
		"output":  out,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}
