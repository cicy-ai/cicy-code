package hosttools

// Aliyun CLI skill — thin wrapper around the official `aliyun` CLI.
//
// Subcommands:
//   install   — download the official aliyun CLI binary into ~/.local/bin
//   config    — open ~/cicy-ai/db/aliyun.json in code-server for the user to
//               edit. Auto-creates a placeholder JSON (chmod 600) if missing,
//               so secrets never have to be pasted into chat or shell history.
//   apply     — read ~/cicy-ai/db/aliyun.json and `aliyun configure set ...`.
//               Refuses to apply placeholder values.
//   status    — report binary path/version + config file state + active
//               profile. Always masks AccessKey id.
//
// Note: this wrapper's `apply` is distinct from the native `aliyun configure`
// builtin. We avoid the name `configure` so the two never get confused.
//
// Security model: the wrapper is the *only* component that reads the JSON
// config. Agents must NOT cat/grep the file or print its raw contents. After
// `configure` succeeds the Aliyun CLI persists credentials at
// ~/.aliyun/config.json and the bootstrap JSON is no longer needed at runtime.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	aliyunConfigPath  = "cicy-ai/db/aliyun.json"
	aliyunProfileName = "default"
	aliyunAKPlaceholder = "<paste-your-access-key-id-here>"
	aliyunSKPlaceholder = "<paste-your-access-key-secret-here>"
)

type aliyunConfig struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	RegionID        string `json:"region_id"`
}

func aliyunConfigFile() string {
	return filepath.Join(userHomeDir(), aliyunConfigPath)
}

func aliyunBinaryPath() string {
	if p, err := exec.LookPath("aliyun"); err == nil {
		return p
	}
	return filepath.Join(userHomeDir(), ".local", "bin", "aliyun")
}

func loadAliyunConfig() (aliyunConfig, error) {
	var cfg aliyunConfig
	data, err := os.ReadFile(aliyunConfigFile())
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", aliyunConfigFile(), err)
	}
	return cfg, nil
}

func (e *Env) runAliyunCLI(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printAliyunUsage(e.Stdout)
		return nil
	}
	switch args[0] {
	case "install":
		return e.runAliyunInstall()
	case "config":
		return e.runAliyunConfigOpen()
	case "apply":
		return e.runAliyunApply()
	case "status":
		return e.runAliyunStatus()
	default:
		printAliyunUsage(e.Stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func printAliyunUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: aliyun-cli <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  install   Download the official `aliyun` CLI binary into ~/.local/bin")
	fmt.Fprintln(w, "  config    Open ~/cicy-ai/db/aliyun.json in code-server (auto-creates placeholder if missing)")
	fmt.Fprintln(w, "  apply     Apply ~/cicy-ai/db/aliyun.json to the `aliyun` CLI default profile")
	fmt.Fprintln(w, "  status    Show binary version, config file state, and active profile")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Typical flow: install → config → (user edits in code-server) → apply → status")
	fmt.Fprintln(w, "After `apply`, call the `aliyun` CLI directly (e.g. `aliyun ecs DescribeInstances`).")
	fmt.Fprintln(w, "Agents must NOT cat / Read the config file — the CLI handles credentials itself.")
}

// ── install ────────────────────────────────────────────────────────────────

func aliyunDownloadURL() (string, error) {
	const base = "https://aliyuncli.alicdn.com"
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return base + "/aliyun-cli-linux-latest-amd64.tgz", nil
		case "arm64":
			return base + "/aliyun-cli-linux-latest-arm64.tgz", nil
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			return base + "/aliyun-cli-macosx-latest-amd64.tgz", nil
		case "arm64":
			return base + "/aliyun-cli-macosx-latest-arm64.tgz", nil
		}
	}
	return "", fmt.Errorf("no prebuilt aliyun CLI for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func (e *Env) runAliyunInstall() error {
	if p, err := exec.LookPath("aliyun"); err == nil {
		fmt.Fprintf(e.Stdout, "aliyun already installed: %s\n", p)
		ver, _ := exec.Command("aliyun", "version").Output()
		if v := strings.TrimSpace(string(ver)); v != "" {
			fmt.Fprintf(e.Stdout, "version: %s\n", v)
		}
		return nil
	}

	url, err := aliyunDownloadURL()
	if err != nil {
		return err
	}
	binDir := filepath.Join(userHomeDir(), ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(binDir, "aliyun")

	fmt.Fprintf(e.Stdout, "downloading %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != "aliyun" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
		found = true
		break
	}
	if !found {
		return fmt.Errorf("aliyun binary not found inside archive")
	}
	fmt.Fprintf(e.Stdout, "installed: %s\n", dest)
	ver, _ := exec.Command(dest, "version").Output()
	if v := strings.TrimSpace(string(ver)); v != "" {
		fmt.Fprintf(e.Stdout, "version: %s\n", v)
	}
	if _, err := exec.LookPath("aliyun"); err != nil {
		fmt.Fprintf(e.Stdout, "note: %s is not on PATH — add ~/.local/bin to PATH or use full path.\n", dest)
	}
	return nil
}

// ── init-config ────────────────────────────────────────────────────────────

func aliyunPlaceholderJSON() string {
	return `{
  "access_key_id": "` + aliyunAKPlaceholder + `",
  "access_key_secret": "` + aliyunSKPlaceholder + `",
  "region_id": "us-west-1"
}
`
}

func isAliyunPlaceholder(cfg aliyunConfig) bool {
	ak := strings.TrimSpace(cfg.AccessKeyID)
	sk := strings.TrimSpace(cfg.AccessKeySecret)
	if ak == "" || sk == "" {
		return true
	}
	if strings.HasPrefix(ak, "<") || strings.HasPrefix(sk, "<") {
		return true
	}
	return false
}

func (e *Env) writeAliyunPlaceholder() (bool, error) {
	path := aliyunConfigFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(aliyunPlaceholderJSON()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Env) printAliyunFillInstructions() {
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Fill in the AccessKey id and secret WITHOUT pasting them into chat:")
	fmt.Fprintln(e.Stdout, "  - open in code-server:  aliyun-cli config")
	fmt.Fprintln(e.Stdout, "  - then apply:           aliyun-cli apply")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Agents must NOT read the config back. After `apply` succeeds the aliyun CLI")
	fmt.Fprintln(e.Stdout, "persists credentials at ~/.aliyun/config.json — the bootstrap JSON is no longer")
	fmt.Fprintln(e.Stdout, "needed for normal `aliyun ecs / vpc / ram` calls.")
}

// ── config (open in code-server, auto-creates placeholder) ─────────────────

func (e *Env) runAliyunConfigOpen() error {
	path := aliyunConfigFile()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if _, werr := e.writeAliyunPlaceholder(); werr != nil {
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
	// agent-code-server sends a `code.open_file` event and then waits for the
	// page-side `code.opened` ack. The send is reliable; the ack is best-effort
	// and frequently times out even when the file did open. Don't propagate
	// that exit code — the placeholder JSON is on disk and the open request
	// was dispatched, so the user can proceed from code-server either way.
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(e.Stdout, "")
		fmt.Fprintf(e.Stdout, "agent-code-server returned %v (likely just a missing ack). The file is open in code-server if you see it in the editor; otherwise edit directly:\n  $EDITOR %s\n", err, path)
	}
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "After the user saves the AccessKey id and secret, run: aliyun-cli apply")
	fmt.Fprintln(e.Stdout, "Reminder: do NOT ask for the credentials in chat — only edit them in the file.")
	return nil
}

// ── apply (push JSON → aliyun CLI default profile) ─────────────────────────

func (e *Env) runAliyunApply() error {
	cfg, err := loadAliyunConfig()
	if err != nil {
		if os.IsNotExist(err) {
			created, werr := e.writeAliyunPlaceholder()
			if werr != nil {
				return fmt.Errorf("config not found and failed to create placeholder: %w", werr)
			}
			if created {
				fmt.Fprintln(e.Stdout, "config was missing — created placeholder (chmod 600)")
				e.printAliyunFillInstructions()
				return fmt.Errorf("config is a placeholder — fill in the AccessKey id/secret (via `aliyun-cli config`) and re-run `aliyun-cli apply`")
			}
		}
		return err
	}
	if isAliyunPlaceholder(cfg) {
		e.printAliyunFillInstructions()
		return fmt.Errorf("config still contains placeholder values — run `aliyun-cli config` to edit, then re-run `aliyun-cli apply`")
	}
	region := strings.TrimSpace(cfg.RegionID)
	if region == "" {
		region = "cn-hangzhou"
	}
	bin := aliyunBinaryPath()
	if _, err := os.Stat(bin); err != nil {
		if p, lerr := exec.LookPath("aliyun"); lerr == nil {
			bin = p
		} else {
			return fmt.Errorf("aliyun CLI not installed — run `aliyun-cli install` first")
		}
	}
	cmd := exec.Command(bin, "configure", "set",
		"--profile", aliyunProfileName,
		"--mode", "AK",
		"--region", region,
		"--access-key-id", cfg.AccessKeyID,
		"--access-key-secret", cfg.AccessKeySecret,
	)
	out, err := cmd.CombinedOutput()
	if s := strings.TrimSpace(string(out)); s != "" {
		fmt.Fprintln(e.Stdout, s)
	}
	if err != nil {
		return fmt.Errorf("aliyun configure set: %w", err)
	}
	fmt.Fprintf(e.Stdout, "applied profile %q (region=%s)\n", aliyunProfileName, region)
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "The aliyun CLI now owns the credentials at ~/.aliyun/config.json.")
	fmt.Fprintln(e.Stdout, "The bootstrap JSON is no longer needed for normal `aliyun` calls.")
	return nil
}

// ── status ─────────────────────────────────────────────────────────────────

func (e *Env) runAliyunStatus() error {
	binPath, lookErr := exec.LookPath("aliyun")
	if lookErr != nil {
		alt := aliyunBinaryPath()
		if _, statErr := os.Stat(alt); statErr == nil {
			binPath = alt
		}
	}
	if binPath == "" {
		fmt.Fprintln(e.Stdout, "binary: not installed (run `aliyun-cli install`)")
	} else {
		fmt.Fprintf(e.Stdout, "binary: %s\n", binPath)
		if ver, err := exec.Command(binPath, "version").Output(); err == nil {
			if v := strings.TrimSpace(string(ver)); v != "" {
				fmt.Fprintf(e.Stdout, "version: %s\n", v)
			}
		}
	}

	cfgPath := aliyunConfigFile()
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintln(e.Stdout, "config: missing (run `aliyun-cli config`)")
	} else {
		cfg, err := loadAliyunConfig()
		if err != nil {
			fmt.Fprintf(e.Stdout, "config: parse error: %v\n", err)
		} else if isAliyunPlaceholder(cfg) {
			fmt.Fprintln(e.Stdout, "config: placeholder — user has not filled in the AccessKey yet (run `aliyun-cli config`)")
		} else {
			masked := maskAK(cfg.AccessKeyID)
			region := cfg.RegionID
			if region == "" {
				region = "(default)"
			}
			fmt.Fprintf(e.Stdout, "config: ready\n  access_key_id: %s\n  region_id: %s\n", masked, region)
		}
	}

	if binPath != "" {
		out, err := exec.Command(binPath, "configure", "list").CombinedOutput()
		if err == nil {
			fmt.Fprintln(e.Stdout, "")
			fmt.Fprintln(e.Stdout, "aliyun configure list:")
			fmt.Fprintln(e.Stdout, strings.TrimRight(string(out), "\n"))
		}
	}
	return nil
}

func maskAK(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 8 {
		if s == "" {
			return "(empty)"
		}
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}
