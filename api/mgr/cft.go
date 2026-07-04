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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// cftTunnelURL holds the current public URL ("" until assigned / while down).
// Read by handleHealth so `cicy-agent team ping` / curl show where this
// instance is reachable.
var cftTunnelURL atomic.Value

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
	return filepath.Join(home, "cicy-ai", "runtime", "cloudflared")
}

// cloudflaredBinPath is where a downloaded cloudflared is cached (persisted
// under ~/cicy-ai/runtime, which survives Cloud Shell's rootfs reset).
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

// startCFT launches + supervises the quick tunnel for the given local port.
// Called as a goroutine right before the main listener starts; cloudflared
// retries the origin on its own, so the small startup race is harmless.
func startCFT(port string) {
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
	// Clear any orphaned tunnel to this port BEFORE starting a fresh one, so the
	// new URL is the only live tunnel and the published address is current.
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

func cftRunOnce(bin, port string) error {
	cmd := exec.Command(bin, "tunnel", "--url", "http://127.0.0.1:"+port, "--no-autoupdate")
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
			if m == "" || strings.Contains(m, "api.trycloudflare.com") {
				continue
			}
			if cftCurrentURL() == m {
				continue
			}
			cftTunnelURL.Store(m)
			cftWriteState(m, port)
			token := getFirstToken()
			log.Printf("[cft] ─────────────────────────────────────────────")
			log.Printf("[cft] ✅ Public: %s/?token=%s", m, token)
			log.Printf("[cft] register from another team's box:")
			log.Printf("[cft]   cicy-agent team add <name> %s %s", m, token)
			log.Printf("[cft] ─────────────────────────────────────────────")
		}
	}()
	err := cmd.Wait()
	pw.Close()
	return err
}

// cftWriteState persists the current URL for tooling (~/cicy-ai/db/cft.json).
func cftWriteState(url, port string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, "cicy-ai", "db", "cft.json")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	b, _ := json.MarshalIndent(M{
		"url":        url,
		"port":       port,
		"started_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	_ = os.WriteFile(path, append(b, '\n'), 0644)
}
