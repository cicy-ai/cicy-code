// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// --cft: bind this instance's API port onto a Cloudflare quick tunnel at
// startup — `npx cicy-code --cft` and the box is reachable from anywhere at a
// https://<random>.trycloudflare.com URL, no account or DNS needed.
//
// cloudflared is resolved from PATH, else downloaded once into
// ~/cicy-ai/runtime/cloudflared/ (persisted; on Cloud Shell that's under /home,
// which survives the ephemeral-rootfs reset). The tunnel process is supervised:
// when it dies (quick tunnels are unregistered as soon as the process exits)
// it is relaunched with backoff — note each relaunch mints a NEW URL, which is
// re-logged and re-written to ~/cicy-ai/db/cft.json and /api/health.

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// cftTunnelURL holds the current public URL ("" until assigned / while down).
// Read by handleHealth so `cicy-agent team ping` / curl show where this
// instance is reachable.
var cftTunnelURL atomic.Value
var cftStartOnce sync.Once

func cftEnabledPath() string { return filepath.Join(cicyDBDir, "cft-enabled") }

func cftEnabledFromConfig() bool {
	_, err := os.Stat(cftEnabledPath())
	return err == nil
}

func enableCFT(port string, persist bool) error {
	if persist {
		if err := os.MkdirAll(cicyDBDir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(cftEnabledPath(), []byte("1\n"), 0o600); err != nil {
			return err
		}
	}
	cftMode = true
	cftStartOnce.Do(func() { go startCFT(port) })
	return nil
}

func cftCurrentURL() string {
	if v, ok := cftTunnelURL.Load().(string); ok {
		return v
	}
	return ""
}

func cftRuntimeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "cloudflared")
	}
	return filepath.Join(home, ".local", "bin")
}

// cloudflaredBinPath is where a downloaded cloudflared is cached: ~/.local/bin/
// cloudflared. This is on PATH, so ensureCloudflared's LookPath hits it — and
// the mac cicy-desktop cp's its bundled cloudflared to exactly this path at
// startup, so cicy-code never needs the GitHub download. (Windows is unaffected:
// the docker image ships cloudflared.)
func cloudflaredBinPath() string {
	bin := filepath.Join(cftRuntimeDir(), "cloudflared")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	return bin
}

// ensureCloudflared returns a runnable cloudflared path: PATH first, then the
// cached download, else downloads the official release binary for this OS/arch.
func ensureCloudflared() (string, error) {
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	bin := cloudflaredBinPath()
	if fileExistsPlain(bin) {
		return bin, nil
	}

	arch := runtime.GOARCH // amd64 / arm64 match cloudflared's asset names
	var file string
	archive := false
	switch runtime.GOOS {
	case "linux":
		file = "cloudflared-linux-" + arch
	case "darwin":
		file = "cloudflared-darwin-" + arch + ".tgz"
		archive = true
	case "windows":
		file = "cloudflared-windows-" + arch + ".exe"
	default:
		return "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, arch)
	}
	url := "https://github.com/cloudflare/cloudflared/releases/latest/download/" + file
	log.Printf("[cft] downloading cloudflared: %s", url)

	if err := os.MkdirAll(cftRuntimeDir(), 0755); err != nil {
		return "", err
	}
	// A bounded client — http.DefaultClient has no timeout, so a stalled GitHub
	// fetch would hang the tunnel goroutine forever with no error. On timeout we
	// error out; startCFT's ensureCloudflared retry loop tries again with backoff.
	// The window covers the redirect to objects.githubusercontent.com plus the
	// ~30MB body on a slow link.
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download cloudflared: http %d", resp.StatusCode)
	}

	tmp := bin + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	if archive {
		err = cftExtractTgzMember(resp.Body, "cloudflared", out)
	} else {
		_, err = io.Copy(out, resp.Body)
	}
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, bin); err != nil {
		return "", err
	}
	log.Printf("[cft] cloudflared installed -> %s", bin)
	return bin, nil
}

// cftExtractTgzMember streams the named member of a .tgz into w (the darwin
// release ships cloudflared inside a tarball).
func cftExtractTgzMember(r io.Reader, name string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			_, err = io.Copy(w, tr)
			return err
		}
	}
}

// The assigned quick-tunnel host: https://<random-words>.trycloudflare.com.
// api.trycloudflare.com shows up in cloudflared's own request/error lines and
// must not be mistaken for the assignment (Go RE2 has no lookahead — filtered
// in code).
var cftURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// cftKillStale kills any cloudflared quick tunnel left over from a previous
// cicy-code run that was tunneling THIS port. When cicy-code is SIGKILLed the
// child cloudflared is orphaned but keeps its (now useless) tunnel alive; a
// fresh start must clear it so only one tunnel + one current URL exists. The
// match is exact — `--url http://127.0.0.1:<port>` — so a named tunnel or an
// unrelated cloudflared on another port is never touched.
func cftKillStale(port string) {
	if runtime.GOOS == "windows" {
		return // no fine-grained match on Windows; skip rather than nuke all cloudflared
	}
	pattern := "cloudflared tunnel --url http://127.0.0.1:" + port
	// pkill -f matches the pattern against the full command line; -f is a regex,
	// but our pattern's metacharacters (., /, :) are all matched literally enough.
	if err := exec.Command("pkill", "-f", pattern).Run(); err == nil {
		log.Printf("[cft] killed a stale tunnel on port %s from a previous run", port)
		time.Sleep(300 * time.Millisecond) // let the OS release the process
	}
}

// cftResolveTokenHost resolves the named-tunnel token + public hostname from,
// in order: the --cft-token/--cft-host flags, the CICY_CFT_TOKEN/CICY_CFT_HOST
// env vars, then ~/cicy-ai/db/cft.json ({"token":..,"host":..}). A token
// selects the NAMED (stable-hostname) tunnel; empty token → quick tunnel.
func cftResolveTokenHost() (token, host string) {
	token = strings.TrimSpace(cftToken)
	host = strings.TrimSpace(cftHost)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CICY_CFT_TOKEN"))
	}
	if host == "" {
		host = strings.TrimSpace(os.Getenv("CICY_CFT_HOST"))
	}
	if token != "" && host != "" {
		return token, host
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return token, host
	}
	b, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "cft.json"))
	if err != nil {
		return token, host
	}
	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil {
		return token, host
	}
	if token == "" {
		if s, ok := m["token"].(string); ok {
			token = strings.TrimSpace(s)
		}
	}
	if host == "" {
		if s, ok := m["host"].(string); ok {
			host = strings.TrimSpace(s)
		}
	}
	return token, host
}

// startCFT launches + supervises the tunnel for the given local port. Called as
// a goroutine right before the main listener starts; cloudflared retries the
// origin on its own, so the small startup race is harmless. With a named-tunnel
// token it runs the stable-hostname tunnel; otherwise a random quick tunnel.
func startCFT(port string) {
	token, host := cftResolveTokenHost()
	// Resolve cloudflared with retry — a transient download failure (timeout /
	// network blip) must not permanently disable the tunnel for this process.
	var bin string
	for prep := 2 * time.Second; ; {
		b, err := ensureCloudflared()
		if err == nil {
			bin = b
			break
		}
		log.Printf("[cft] cloudflared not ready: %v — retrying in %s", err, prep)
		time.Sleep(prep)
		if prep < time.Minute {
			prep *= 2
		}
	}

	if token != "" {
		// Named tunnel: stable hostname, no stale-kill (multiple connectors to
		// the same named tunnel are fine — Cloudflare load-balances them).
		cftRunNamed(bin, token, host, port) // never returns
		return
	}

	// Quick tunnel: clear any orphaned tunnel to this port BEFORE starting a
	// fresh one, so the new (random) URL is the only live tunnel.
	cftKillStale(port)
	backoff := 2 * time.Second
	for {
		start := time.Now()
		if err := cftRunOnce(bin, port); err != nil {
			log.Printf("[cft] tunnel exited: %v", err)
		}
		cftTunnelURL.Store("")
		// A tunnel that held for a while earns a fresh backoff; rapid-fail doubles.
		if time.Since(start) > 2*time.Minute {
			backoff = 2 * time.Second
		} else if backoff < time.Minute {
			backoff *= 2
		}
		log.Printf("[cft] restarting tunnel in %s (each restart gets a NEW URL)", backoff)
		time.Sleep(backoff)
	}
}

// cftRunNamed launches + supervises a NAMED tunnel (stable hostname). The token
// is passed via TUNNEL_TOKEN env, NOT argv, so it never appears in `ps`. host
// (the public FQDN configured in the Cloudflare dashboard) is only used to
// report/publish the URL — cloudflared doesn't print it and the token doesn't
// reveal it.
func cftRunNamed(bin, token, host, port string) {
	url := ""
	if host != "" {
		h := strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
		url = "https://" + strings.TrimRight(h, "/")
	}
	log.Printf("[cft] named tunnel mode — hostname is STABLE across restarts (%s)", func() string {
		if url != "" {
			return url
		}
		return "hostname set in Cloudflare dashboard; pass --cft-host to display it"
	}())
	backoff := 2 * time.Second
	for {
		start := time.Now()
		if err := cftNamedOnce(bin, token, url, port); err != nil {
			log.Printf("[cft] named tunnel exited: %v", err)
		}
		cftTunnelURL.Store("")
		if time.Since(start) > 2*time.Minute {
			backoff = 2 * time.Second
		} else if backoff < time.Minute {
			backoff *= 2
		}
		log.Printf("[cft] reconnecting named tunnel in %s (hostname unchanged)", backoff)
		time.Sleep(backoff)
	}
}

func cftNamedOnce(bin, token, url, port string) error {
	cmd := exec.Command(bin, "tunnel", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token) // keep the token out of ps
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		announced := false
		for sc.Scan() {
			line := sc.Text()
			low := strings.ToLower(line)
			if strings.Contains(line, "ERR") || strings.Contains(low, "error") || strings.Contains(low, "failed") {
				log.Printf("[cft][cloudflared] %s", line)
			}
			// cloudflared logs "Registered tunnel connection" once each edge
			// connection is up — that's our "connected" signal.
			if !announced && strings.Contains(low, "registered tunnel connection") {
				announced = true
				if url != "" {
					cftTunnelURL.Store(url)
					cftWriteState(url, port)
					log.Printf("[cft] ─────────────────────────────────────────────")
					log.Printf("[cft] ✅ Public (named, stable): %s", url)
					log.Printf("[cft] ─────────────────────────────────────────────")
				} else {
					log.Printf("[cft] ✅ named tunnel connected (hostname configured in the Cloudflare dashboard; pass --cft-host to display/publish it)")
				}
			}
		}
	}()
	err := cmd.Wait()
	pw.Close()
	return err
}

type cftQuickResult struct {
	Success bool `json:"success"`
	Result  struct {
		AccountTag string `json:"account_tag"`
		Hostname   string `json:"hostname"`
		ID         string `json:"id"`
		Secret     string `json:"secret"`
	} `json:"result"`
}

// cftQuickCredentials creates an anonymous Quick Tunnel while trying every
// address returned for api.trycloudflare.com. Cloudflare occasionally leaves
// one anycast address accepting TCP but stalling/resetting TLS; cloudflared's
// built-in request only tries the first address and can then fail forever.
func cftQuickCredentials() (hostname, token string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, "api.trycloudflare.com")
	if err != nil {
		return "", "", err
	}
	var lastErr error
	for _, addr := range addrs {
		if addr.IP.To4() == nil {
			continue
		}
		ip := addr.IP.String()
		transport := &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
			},
			TLSHandshakeTimeout: 6 * time.Second,
		}
		client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.trycloudflare.com/tunnel", nil)
		if reqErr != nil {
			transport.CloseIdleConnections()
			return "", "", reqErr
		}
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			transport.CloseIdleConnections()
			lastErr = reqErr
			continue
		}
		var payload cftQuickResult
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)
		resp.Body.Close()
		transport.CloseIdleConnections()
		if decodeErr != nil || resp.StatusCode != http.StatusOK || !payload.Success {
			lastErr = fmt.Errorf("quick tunnel API %s: status %d", ip, resp.StatusCode)
			continue
		}
		host := strings.ToLower(strings.TrimSpace(payload.Result.Hostname))
		if !regexp.MustCompile(`^[a-z0-9-]+\.trycloudflare\.com$`).MatchString(host) ||
			payload.Result.AccountTag == "" || payload.Result.ID == "" || payload.Result.Secret == "" {
			lastErr = fmt.Errorf("quick tunnel API %s returned invalid credentials", ip)
			continue
		}
		raw, marshalErr := json.Marshal(map[string]string{
			"a": payload.Result.AccountTag, "t": payload.Result.ID, "s": payload.Result.Secret,
		})
		if marshalErr != nil {
			return "", "", marshalErr
		}
		return "https://" + host, base64.StdEncoding.EncodeToString(raw), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("quick tunnel API has no IPv4 address")
	}
	return "", "", lastErr
}

func cftRunOnce(bin, port string) error {
	if url, token, err := cftQuickCredentials(); err == nil {
		return cftRunTokenOnce(bin, port, url, token)
	} else {
		log.Printf("[cft] direct Quick Tunnel bootstrap failed, falling back to cloudflared: %v", err)
	}
	return cftRunLegacyOnce(bin, port)
}

func cftRunTokenOnce(bin, port, url, token string) error {
	cmd := exec.Command(bin, "tunnel", "--url", "http://127.0.0.1:"+port,
		"--protocol", "http2", "--no-autoupdate", "run", "--token", token)
	return cftRunCommand(cmd, port, url)
}

func cftRunLegacyOnce(bin, port string) error {
	// HTTP/2 is substantially more reliable in Colab and other constrained
	// containers where QUIC cannot raise the UDP receive buffer. Without this,
	// cloudflared can print a Quick Tunnel URL before it has any registered edge
	// connection, leaving the Cloud dashboard with a link that only returns 530.
	cmd := exec.Command(bin, "tunnel", "--url", "http://127.0.0.1:"+port,
		"--protocol", "http2", "--no-autoupdate")
	return cftRunCommand(cmd, port, "")
}

func cftRunCommand(cmd *exec.Cmd, port, initialURL string) error {
	// cloudflared logs the assigned URL on stderr; merge both to be safe.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		pendingURL := initialURL
		publishedURL := ""
		for sc.Scan() {
			line := sc.Text()
			// Surface cloudflared's own failures — otherwise a dead tunnel is
			// just an opaque "exit status 1". NB: quick tunnels need DIRECT
			// egress to api.trycloudflare.com; cloudflared ignores proxy env,
			// so on SNI-filtered networks this fails ("connection reset" /
			// timeout) — run --cft on a box with clean egress instead.
			low := strings.ToLower(line)
			if strings.Contains(line, "ERR") || strings.Contains(low, "error") || strings.Contains(low, "failed") {
				log.Printf("[cft][cloudflared] %s", line)
			}
			m := cftURLRe.FindString(line)
			if m != "" && !strings.Contains(m, "api.trycloudflare.com") {
				pendingURL = m
			}
			// A URL assignment alone is not proof that the connector is usable.
			// Publish only after cloudflared confirms an edge registration.
			if !strings.Contains(low, "registered tunnel connection") || pendingURL == "" || pendingURL == publishedURL {
				continue
			}
			publishedURL = pendingURL
			cftTunnelURL.Store(pendingURL)
			cftWriteState(pendingURL, port)
			log.Printf("[cft] ─────────────────────────────────────────────")
			log.Printf("[cft] ✅ Public: %s", pendingURL)
			log.Printf("[cft] ─────────────────────────────────────────────")
		}
	}()
	err := cmd.Wait()
	pw.Close()
	return err
}

// cftWriteState persists the current URL for tooling (~/cicy-ai/db/cft.json).
// It MERGES into the existing file so a user-provided token/name config there
// is preserved (not clobbered by the runtime state write).
func cftWriteState(url, port string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, "cicy-ai", "db", "cft.json")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	m := map[string]interface{}{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m) // keep existing keys (token, name, …)
	}
	m["url"] = url
	m["port"] = port
	m["started_at"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(path, append(b, '\n'), 0644)
}
