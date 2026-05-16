package hosttools

// Aliyun CLI skill — thin wrapper around the official `aliyun` CLI.
//
// Subcommands:
//   install   — download the official aliyun CLI binary into ~/.local/bin
//   config    — open ~/.aliyun/config.json (the aliyun CLI's own native config
//               file) in code-server for the user to fill in id/secret. Creates
//               a native-format scaffold with chmod 600 if the file is missing,
//               so secrets never have to be pasted into chat or shell history.
//   status    — report binary path/version + which profile is active + AK
//               (always masked). Reads ~/.aliyun/config.json directly.
//
// Note: there is intentionally NO middleware JSON at ~/cicy-ai/db/aliyun.json
// anymore. The aliyun CLI owns its config file and reads from it on every
// invocation, so any intermediate JSON we maintain would just be one more
// file to keep in sync. The user edits ~/.aliyun/config.json directly via
// code-server (or `aliyun configure set` from the shell) — done.

import (
	"encoding/json"
	"archive/tar"
	"compress/gzip"
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
	aliyunNativeConfigPath = ".aliyun/config.json"
)

func aliyunBinaryPath() string {
	if p, err := exec.LookPath("aliyun"); err == nil {
		return p
	}
	return filepath.Join(userHomeDir(), ".local", "bin", "aliyun")
}

func aliyunConfigPath() string {
	return filepath.Join(userHomeDir(), aliyunNativeConfigPath)
}

// aliyunProfile is the subset of fields we read from ~/.aliyun/config.json for
// reporting. The CLI's own struct is larger; we only care about identifying
// the active profile and showing a masked AK.
type aliyunProfile struct {
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	AccessKeyID   string `json:"access_key_id"`
	RegionID      string `json:"region_id"`
}

type aliyunNativeConfig struct {
	Current  string          `json:"current"`
	Profiles []aliyunProfile `json:"profiles"`
}

func loadAliyunNativeConfig() (aliyunNativeConfig, error) {
	var cfg aliyunNativeConfig
	data, err := os.ReadFile(aliyunConfigPath())
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", aliyunConfigPath(), err)
	}
	return cfg, nil
}

func aliyunNativePlaceholder() string {
	return `{
  "current": "default",
  "profiles": [
    {
      "name": "default",
      "mode": "AK",
      "access_key_id": "<paste-your-access-key-id-here>",
      "access_key_secret": "<paste-your-access-key-secret-here>",
      "region_id": "us-west-1",
      "output_format": "json",
      "language": "en"
    }
  ],
  "meta_path": ""
}
`
}

func isAliyunPlaceholderProfile(p aliyunProfile) bool {
	ak := strings.TrimSpace(p.AccessKeyID)
	if ak == "" || strings.HasPrefix(ak, "<") {
		return true
	}
	return false
}

func (e *Env) writeAliyunPlaceholder() (bool, error) {
	path := aliyunConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(aliyunNativePlaceholder()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ── dispatch ───────────────────────────────────────────────────────────────

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
	fmt.Fprintln(w, "  config    Open ~/.aliyun/config.json in code-server (auto-creates a placeholder if missing)")
	fmt.Fprintln(w, "  status    Show binary version + active profile (AccessKey id masked)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Typical flow: install → config → (user fills in creds in code-server) → status")
	fmt.Fprintln(w, "After the user saves config.json, call the `aliyun` CLI directly:")
	fmt.Fprintln(w, "  aliyun ecs DescribeInstances --region us-west-1")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Agents must NOT cat / Read ~/.aliyun/config.json — the CLI reads it for itself.")
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

// ── config (open in code-server, auto-creates native-format placeholder) ───

func (e *Env) runAliyunConfigOpen() error {
	path := aliyunConfigPath()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if _, werr := e.writeAliyunPlaceholder(); werr != nil {
				return fmt.Errorf("config missing and failed to create placeholder: %w", werr)
			}
			fmt.Fprintln(e.Stdout, "config was missing — created native-format placeholder (chmod 600)")
		} else {
			return err
		}
	}
	fmt.Fprintln(e.Stdout, "opening ~/.aliyun/config.json in code-server...")
	cmd := exec.Command("agent-code-server", "open", path)
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(e.Stdout, "")
		fmt.Fprintf(e.Stdout, "agent-code-server returned %v (likely just a missing ack). The file is open in code-server if you see it in the editor; otherwise edit directly:\n  $EDITOR %s\n", err, path)
	}
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Fill in `access_key_id` and `access_key_secret` (and adjust `region_id` if needed), save the file.")
	fmt.Fprintln(e.Stdout, "Then call the `aliyun` CLI directly:")
	fmt.Fprintln(e.Stdout, "  aliyun ecs DescribeInstances --region us-west-1")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Reminder: never paste the credentials into chat — only edit them in the file.")
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

	cfgPath := aliyunConfigPath()
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintln(e.Stdout, "config: ~/.aliyun/config.json missing (run `aliyun-cli config`)")
	} else {
		cfg, err := loadAliyunNativeConfig()
		if err != nil {
			fmt.Fprintf(e.Stdout, "config: parse error: %v\n", err)
		} else {
			fmt.Fprintf(e.Stdout, "config: %s\n", cfgPath)
			fmt.Fprintf(e.Stdout, "  current_profile: %s\n", cfg.Current)
			for _, p := range cfg.Profiles {
				marker := "  "
				if p.Name == cfg.Current {
					marker = "* "
				}
				state := "ready"
				if isAliyunPlaceholderProfile(p) {
					state = "placeholder — user has not filled in the AccessKey yet"
				}
				masked := maskAK(p.AccessKeyID)
				if state != "ready" {
					fmt.Fprintf(e.Stdout, "%sprofile %s [%s] (%s)\n", marker, p.Name, p.Mode, state)
				} else {
					fmt.Fprintf(e.Stdout, "%sprofile %s [%s] access_key_id=%s region=%s\n", marker, p.Name, p.Mode, masked, p.RegionID)
				}
			}
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
