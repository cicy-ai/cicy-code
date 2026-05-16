package hosttools

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type mihomoTool struct {
	stdout io.Writer
	stderr io.Writer
}

type mihomoState struct {
	PID       int    `json:"pid"`
	Binary    string `json:"binary"`
	Config    string `json:"config"`
	Log       string `json:"log"`
	StartedAt string `json:"started_at"`
}

type mihomoFlagOptions struct {
	Config string
	Binary string
	Lines  int
	Follow bool
}

func (e *Env) runCicyMihomo(args []string) error {
	return newMihomoTool(e.Stdout, e.Stderr).run(args)
}

func newMihomoTool(stdout, stderr io.Writer) *mihomoTool {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &mihomoTool{stdout: stdout, stderr: stderr}
}

func (t *mihomoTool) run(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = strings.TrimSpace(args[0])
		args = args[1:]
	}

	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(t.stdout, t.helpText())
		return nil
	case "template":
		_, _ = fmt.Fprint(t.stdout, t.defaultTemplate())
		return nil
	case "show-config":
		return t.showConfig()
	case "gen-config":
		return t.genConfig()
	case "status":
		return t.status()
	case "start":
		return t.start()
	case "stop":
		return t.stop()
	case "restart":
		if err := t.stop(); err != nil && !errors.Is(err, errMihomoNotRunning) {
			return err
		}
		return t.start()
	case "reload":
		return t.reload()
	case "logs", "log":
		opts, extra, err := parseMihomoFlags(args)
		if err != nil {
			return err
		}
		if len(extra) > 1 {
			return fmt.Errorf("usage: cicy-mihomo logs [N]")
		}
		if len(extra) == 1 {
			n, convErr := strconv.Atoi(strings.TrimSpace(extra[0]))
			if convErr != nil || n <= 0 {
				return fmt.Errorf("invalid log line count: %s", extra[0])
			}
			opts.Lines = n
		}
		return t.logs(opts)
	case "install":
		return t.doInstall()
	case "test":
		return t.testAll()
	case "addUser", "add-user":
		// cicy-mihomo addUser <user> <target> [password]
		//   user:     worker name (e.g. w-12345)
		//   target:   proxy-group or proxy node name to route this user to
		//   password: optional; auto-generated when omitted
		if len(args) < 2 {
			return fmt.Errorf("usage: cicy-mihomo addUser <user> <target> [password]")
		}
		user := strings.TrimSpace(args[0])
		target := strings.TrimSpace(args[1])
		password := ""
		if len(args) >= 3 {
			password = strings.TrimSpace(args[2])
		}
		return t.addUser(user, target, password)
	case "addProxy", "add-proxy":
		// cicy-mihomo addProxy name=<id> type=<adapter> server=<host> port=<n> [k=v ...]
		// Re-running with the same `name` replaces the existing entry (key
		// rotation). Numbers/bools are auto-detected; everything else is a
		// quoted string.
		if len(args) < 1 {
			return fmt.Errorf("usage: cicy-mihomo addProxy name=<id> type=<adapter> server=<host> port=<n> [k=v ...]")
		}
		return t.addProxy(args)
	case "addGroup", "add-group":
		// cicy-mihomo addGroup <name> [member1 member2 ...]
		// Adds (or replaces) a select group with the given members. Use
		// member names as they appear under `proxies:` (or other groups).
		if len(args) < 1 {
			return fmt.Errorf("usage: cicy-mihomo addGroup <name> [member1 member2 ...]")
		}
		name := strings.TrimSpace(args[0])
		members := args[1:]
		return t.addGroup(name, members)
	default:
		_, _ = fmt.Fprintln(t.stdout, t.helpText())
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

var errMihomoNotRunning = errors.New("cicy-mihomo is not running")

func (t *mihomoTool) helpText() string {
	return `cicy-mihomo - manage local mihomo proxy

Usage:
  cicy-mihomo help
  cicy-mihomo template
  cicy-mihomo gen-config
  cicy-mihomo show-config
  cicy-mihomo status
  cicy-mihomo start
  cicy-mihomo stop
  cicy-mihomo restart
  cicy-mihomo reload
  cicy-mihomo logs [N|-f]
  cicy-mihomo install
  cicy-mihomo test                  test all proxy node speed (anthropic/google/github/cf)
  cicy-mihomo addUser <user> <target> [password]
                                    add IN-USER,<user>,<target> rule + auth entry
                                    (password auto-generated when omitted)
  cicy-mihomo addProxy name=<id> type=<adapter> server=<host> port=<n> [k=v ...]
                                    append or replace a proxies[] entry
  cicy-mihomo addGroup <name> [member1 member2 ...]
                                    append or replace a select proxy-group

Defaults:
  binary:   ~/.local/bin/mihomo (or set MIHOMO_BIN)
  config:   ~/cicy-ai/db/mihomo.yaml
  port:     9001
  api:      127.0.0.1:19001

Conventions (gen-config writes this layout — see cicy-ai/cicy-mihomo v1.10.2):
  - globalPassword: any non-empty username + this password authenticates.
                    Add per-user entries under authentication: only when you
                    need a different password for that user.
  - IN-USER-PREFIX,w-,default_proxy_group:
                    every username starting with "w-" routes via
                    default_proxy_group. Add IN-USER,<user>,<target> ABOVE
                    this line to pin one worker to a different proxy.
  - default_proxy_group is a select group; swap the active node via the
                    controller (PUT /proxies/default_proxy_group).
`
}

const defaultMihomoVersion = "v1.10.2"
const defaultMihomoGitHubProxy = "https://gh-proxy.com/"

// doInstall downloads the platform-matching mihomo binary from
// cicy-ai/cicy-mihomo releases into ~/.local/bin/mihomo. Overrides:
//
//	CICY_MIHOMO_VERSION      pin a specific release tag (default v1.10.2)
//	GITHUB_PROXY             URL prefix for github.com (default https://gh-proxy.com/)
//	CICY_MIHOMO_RELEASE_URL  fully-qualified direct download URL — wins over the
//	                         version + proxy derivation entirely
func (t *mihomoTool) doInstall() error {
	version := strings.TrimSpace(os.Getenv("CICY_MIHOMO_VERSION"))
	if version == "" {
		version = defaultMihomoVersion
	}
	asset := fmt.Sprintf("mihomo-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	url := strings.TrimSpace(os.Getenv("CICY_MIHOMO_RELEASE_URL"))
	if url == "" {
		proxy := strings.TrimSpace(os.Getenv("GITHUB_PROXY"))
		if proxy == "" {
			proxy = defaultMihomoGitHubProxy
		}
		if proxy != "" && !strings.HasSuffix(proxy, "/") {
			proxy += "/"
		}
		url = proxy + fmt.Sprintf("https://github.com/cicy-ai/cicy-mihomo/releases/download/%s/%s", version, asset)
	}

	binDir := filepath.Join(userHomeDir(), ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(binDir, "mihomo")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	tmp := target + ".tmp"

	fmt.Fprintf(t.stdout, "downloading: %s\n", url)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	fmt.Fprintf(t.stdout, "installed: %s (%s)\n", target, version)
	return nil
}

func (t *mihomoTool) stateDir() string {
	return filepath.Join(userHomeDir(), ".local", "state", "cicy-skills", "mihomo")
}

func (t *mihomoTool) pidFile() string {
	return filepath.Join(t.stateDir(), "pid")
}

func (t *mihomoTool) stateFile() string {
	return filepath.Join(t.stateDir(), "state.json")
}

func (t *mihomoTool) logFile() string {
	return filepath.Join(t.stateDir(), "mihomo.log")
}

func (t *mihomoTool) configPath() string {
	return filepath.Join(userHomeDir(), "cicy-ai", "db", "mihomo.yaml")
}

func (t *mihomoTool) binaryCandidates() []string {
	home := userHomeDir()
	return []string{
		strings.TrimSpace(os.Getenv("MIHOMO_BIN")),
		filepath.Join(home, "projects", "cicy-mihomo", "bin", "mihomo-darwin-amd64"),
		filepath.Join(home, ".local", "bin", "mihomo"),
		filepath.Join(home, ".local", "bin", "mihomo-test"),
		"mihomo",
		"mihomo-test",
	}
}

func (t *mihomoTool) resolveBinary() (string, error) {
	for _, candidate := range t.binaryCandidates() {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, string(os.PathSeparator)) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("mihomo binary not found; set MIHOMO_BIN or build mihomo first")
}

// writeMihomoYAMLValidated stages data to /tmp, validates it with
// `mihomo -t -f <tmp>`, and only writes to `path` on success. On failure the
// target file is untouched and the test output is returned in the error.
//
// `mihomo -t` always exits 0, so we have to read its stdout for the
// "test is successful" sentinel and fail open on anything else (including
// the "test failed" line and any "level=error" lines).
//
// When the mihomo binary can't be located we fall back to a plain write —
// blocking the edit just because the validator is missing would be more
// hostile than helpful (the user almost certainly hasn't run install yet).
//
// Atomicity: /tmp may be on a separate filesystem (tmpfs) from the config
// dir, so we can't `rename` from /tmp directly. After validation passes we
// re-stage into a sibling temp file under the target dir and rename that —
// the rename stays atomic within the same filesystem.
func (t *mihomoTool) writeMihomoYAMLValidated(path string, data []byte) error {
	bin, binErr := t.resolveBinary()
	if binErr != nil {
		_, _ = fmt.Fprintf(t.stderr, "warn: skipping mihomo -t (binary not found); writing anyway\n")
		return os.WriteFile(path, data, 0o644)
	}
	// 1) stage in /tmp for validation
	probe, err := os.CreateTemp("/tmp", "mihomo.yaml.validate-*")
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
	out, _ := exec.Command(bin, "-t", "-f", probePath).CombinedOutput()
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

// summarizeMihomoTestOutput pulls just the error lines and the final
// status line out of mihomo -t's verbose output so callers don't have to
// surface the full transcript to the user.
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
		// nothing matched — fall back to the last 5 non-empty lines
		all := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(all) > 5 {
			all = all[len(all)-5:]
		}
		return strings.Join(all, "\n")
	}
	return strings.Join(keep, "\n")
}

func parseMihomoFlags(args []string) (mihomoFlagOptions, []string, error) {
	opts := mihomoFlagOptions{Lines: 80}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--config" && i+1 < len(args):
			opts.Config = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--config="):
			opts.Config = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		case arg == "--bin" && i+1 < len(args):
			opts.Binary = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--bin="):
			opts.Binary = strings.TrimSpace(strings.TrimPrefix(arg, "--bin="))
		case arg == "-f" || arg == "--follow":
			opts.Follow = true
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func (t *mihomoTool) defaultTemplate() string {
	// globalPassword is the single shared secret for all agents on this host.
	// We generate it fresh at gen-config time and never rotate it automatically.
	// cicy-mihomo's Verify lets any non-empty username through when the
	// password matches globalPassword, so per-user `authentication:` entries
	// aren't necessary.
	//
	// Routing: IN-USER-PREFIX,w-,default_proxy_group is a catch-all for any
	// worker username starting with `w-` (added in cicy-mihomo v1.10.2). Pin a
	// specific worker to a different proxy by adding a more-specific
	// IN-USER,<user>,<target> rule ABOVE the prefix line. The `default_proxy_group`
	// indirection is required so the controller PUT /proxies/default_proxy_group
	// (see selectProxy below) can swap the active node without rewriting rules.
	//
	// Out-of-the-box behavior: `default_proxy_node` is a `direct` adapter, so
	// a worker that turns on the proxy works immediately even before the user
	// has added any real upstream — traffic just passes through. The user
	// adds their real proxies under `proxies:` and into `default_proxy_group`
	// when they want actual upstream routing.
	password := randomAlphaNum(16)
	return fmt.Sprintf("mixed-port: 9001\nallow-lan: true\nbind: 0.0.0.0\nmode: rule\nlog-level: debug\n\nexternal-controller: 127.0.0.1:19001\n\nglobalPassword: %q\n\nproxies:\n  - name: \"default_proxy_node\"\n    type: direct\n\nproxy-groups:\n  - name: default_proxy_group\n    type: select\n    proxies:\n      - default_proxy_node\n\nrules:\n  - IN-USER-PREFIX,w-,default_proxy_group\n  - MATCH,REJECT\n", password)
}

func randomAlphaNum(n int) string {
	if n <= 0 {
		return ""
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	buf := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		for i := range buf {
			buf[i] = alphabet[i%len(alphabet)]
		}
		return string(buf)
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf)
}

func (t *mihomoTool) genConfig() error {
	path := t.configPath()
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(t.defaultTemplate()), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, path)
	return nil
}

// addUser is the implementation of `cicy-mihomo addUser <user> <target> [password]`.
//
// What it does to mihomo.yaml:
//
//  1. Ensures an `authentication:` block exists at top level and contains a
//     `- "<user>:<password>"` entry. An existing entry for the same user is
//     replaced (password rotation).
//
//  2. Ensures a `rules:` block exists and contains an `IN-USER,<user>,<target>`
//     line ABOVE the catch-all `IN-USER-PREFIX,w-,...` (so this user-specific
//     rule wins). An existing IN-USER line for the same user is replaced.
//
// After mutating the file we hot-reload the running mihomo via its controller
// API so the change is live. If mihomo isn't running, the reload is a no-op
// and the user is told they need to start it.
//
// The flow is intentionally line-based — mihomo's YAML uses a flat top-level
// shape and we'd rather preserve user-authored comments / ordering than round-trip
// through a structural parser that drops them.
func (t *mihomoTool) addUser(user, target, password string) error {
	if user == "" {
		return fmt.Errorf("user is required")
	}
	if target == "" {
		return fmt.Errorf("target is required (proxy-group or proxy node name)")
	}
	if strings.ContainsAny(user, ":\"' \t,") {
		return fmt.Errorf("user contains invalid characters")
	}
	if strings.ContainsAny(target, "\"' \t,") {
		return fmt.Errorf("target contains invalid characters")
	}
	if password == "" {
		password = randomAlphaNum(24)
	}
	path := t.configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("config does not exist: %s (run `cicy-mihomo gen-config` first)", path)
	} else if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	lines = upsertMihomoAuthLine(lines, user, password)
	lines = upsertMihomoUserRule(lines, user, target)
	if err := t.writeMihomoYAMLValidated(path, []byte(strings.Join(lines, "\n"))); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(t.stdout, "added: authentication[%s] + rule IN-USER,%s,%s\n", user, user, target)
	_, _ = fmt.Fprintf(t.stdout, "password: %s\n", password)
	if err := t.reload(); err != nil {
		if errors.Is(err, errMihomoNotRunning) {
			_, _ = fmt.Fprintln(t.stdout, "note: mihomo not running — start it with `cicy-mihomo start` to pick up the new entry")
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, "reloaded")
	return nil
}

// upsertMihomoAuthLine inserts or replaces a `- "<user>:<password>"` entry
// under the `authentication:` top-level block. If the block is missing it is
// created near the other top-level scalars (just below globalPassword if
// present, otherwise at the top).
func upsertMihomoAuthLine(lines []string, user, password string) []string {
	entry := fmt.Sprintf("  - \"%s:%s\"", user, password)
	authIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "authentication:") {
			authIdx = i
			break
		}
	}
	if authIdx == -1 {
		// create the block after globalPassword (or after the first top-level
		// scalar, falling back to the very top).
		insertAt := 0
		for i, line := range lines {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "globalPassword:") {
				insertAt = i + 1
				break
			}
		}
		if insertAt == 0 {
			for i, line := range lines {
				if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
					continue
				}
				t := strings.TrimSpace(line)
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				insertAt = i + 1
				break
			}
		}
		out := make([]string, 0, len(lines)+3)
		out = append(out, lines[:insertAt]...)
		out = append(out, "authentication:", entry)
		out = append(out, lines[insertAt:]...)
		return out
	}
	// find the end of the authentication block (first non-indented / non-empty
	// line after the header), looking for an existing entry for this user.
	prefix := fmt.Sprintf("- \"%s:", user)
	prefixBare := fmt.Sprintf("- %s:", user)
	end := len(lines)
	for i := authIdx + 1; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		// indented line OR a list entry — still in the block
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") || trimmed == "" {
			if strings.HasPrefix(trimmed, prefix) || strings.HasPrefix(trimmed, prefixBare) {
				lines[i] = entry
				return lines
			}
			continue
		}
		// non-indented, non-empty → block ended at i
		end = i
		break
	}
	// no existing entry — insert just before `end`
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:end]...)
	out = append(out, entry)
	out = append(out, lines[end:]...)
	return out
}

// upsertMihomoUserRule inserts or replaces an `IN-USER,<user>,<target>` rule
// inside the top-level `rules:` block. The rule is placed ABOVE the catch-all
// `IN-USER-PREFIX,w-,*` line (so this more-specific rule wins). A `rules:`
// block is created at the bottom of the file when missing.
func upsertMihomoUserRule(lines []string, user, target string) []string {
	rule := fmt.Sprintf("  - IN-USER,%s,%s", user, target)
	rulesIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "rules:") {
			rulesIdx = i
			break
		}
	}
	if rulesIdx == -1 {
		out := append([]string{}, lines...)
		out = append(out, "rules:", rule)
		return out
	}
	// scan the rules block: look for existing IN-USER,<user>, line OR the
	// catch-all IN-USER-PREFIX,w-,... line to insert above.
	userMatch := fmt.Sprintf("IN-USER,%s,", user)
	prefixMatch := "IN-USER-PREFIX,"
	insertBefore := -1
	endOfBlock := len(lines)
	for i := rulesIdx + 1; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if !(strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") || trimmed == "") {
			endOfBlock = i
			break
		}
		body := strings.TrimPrefix(trimmed, "- ")
		if strings.HasPrefix(body, userMatch) {
			lines[i] = rule
			return lines
		}
		if insertBefore == -1 && strings.HasPrefix(body, prefixMatch) {
			insertBefore = i
		}
	}
	if insertBefore == -1 {
		// no IN-USER-PREFIX catch-all — drop it just before MATCH,REJECT if
		// present, otherwise at the end of the block.
		for i := rulesIdx + 1; i < endOfBlock; i++ {
			trimmed := strings.TrimSpace(lines[i])
			body := strings.TrimPrefix(trimmed, "- ")
			if strings.HasPrefix(body, "MATCH,") {
				insertBefore = i
				break
			}
		}
	}
	if insertBefore == -1 {
		insertBefore = endOfBlock
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertBefore]...)
	out = append(out, rule)
	out = append(out, lines[insertBefore:]...)
	return out
}

// kvArg captures one `key=value` argument with the value preserving its
// original literal form so we can defer type detection until YAML emission.
type kvArg struct {
	key, value string
}

func parseKVArgs(args []string) ([]kvArg, error) {
	out := make([]kvArg, 0, len(args))
	for _, a := range args {
		idx := strings.IndexByte(a, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("expected key=value, got %q", a)
		}
		key := strings.TrimSpace(a[:idx])
		val := a[idx+1:]
		if key == "" {
			return nil, fmt.Errorf("empty key in %q", a)
		}
		out = append(out, kvArg{key: key, value: val})
	}
	return out, nil
}

// yamlScalar formats a string as a YAML scalar with the cheapest valid
// representation: bare for safe identifiers, double-quoted otherwise. Numbers
// and booleans are passed through unquoted so callers that want a bool/int
// value can hand "443" or "true" verbatim.
func yamlScalar(v string) string {
	if v == "" {
		return "\"\""
	}
	if v == "true" || v == "false" || v == "null" {
		return v
	}
	// integer?
	allDigits := true
	for i, c := range v {
		if i == 0 && c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			allDigits = false
			break
		}
	}
	if allDigits && len(v) > 0 && (v[0] != '-' || len(v) > 1) {
		return v
	}
	// bare identifier? (letters, digits, dash, underscore, dot)
	bare := len(v) > 0 && v[0] != '-' && v[0] != '!' && v[0] != '&' && v[0] != '*' && v[0] != '#'
	for _, c := range v {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			bare = false
			break
		}
	}
	if bare {
		return v
	}
	// fall back to double-quoted; escape backslash and double quote
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(v, "\\", "\\\\"), "\"", "\\\"") + "\""
}

// renderProxyEntry returns the YAML lines for a single proxies[] entry given
// an ordered list of kv pairs. Indentation matches gen-config's template (two
// spaces per level, with `- ` on the first line).
func renderProxyEntry(kv []kvArg) []string {
	out := make([]string, 0, len(kv))
	for i, p := range kv {
		prefix := "    "
		if i == 0 {
			prefix = "  - "
		}
		out = append(out, prefix+p.key+": "+yamlScalar(p.value))
	}
	return out
}

// findSectionBounds locates the line range of a top-level YAML list section
// (e.g. `proxies:` or `proxy-groups:`). Returns (headerIdx, endIdx) — exclusive
// end. (-1, -1) when the section is missing.
func findSectionBounds(lines []string, header string) (int, int) {
	headerIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), header) {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		return -1, -1
	}
	end := len(lines)
	for i := headerIdx + 1; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") || trimmed == "" {
			continue
		}
		end = i
		break
	}
	return headerIdx, end
}

// removeProxyEntryByName drops the proxies[] / proxy-groups[] entry whose
// `- name:` line equals the given name. Returns the modified slice (or the
// input unchanged when no entry matched).
func removeYAMLListEntryByName(lines []string, sectionHeader, name string) []string {
	headerIdx, end := findSectionBounds(lines, sectionHeader)
	if headerIdx == -1 {
		return lines
	}
	// A top-level list entry starts with EXACTLY "  - " (two-space indent).
	// Nested list items (`      - default_proxy_node` inside `proxies:` of a
	// group) also start with `- ` after trimming whitespace, so we have to
	// pin the indent to avoid treating them as new entries.
	isTopEntry := func(line string) bool {
		return strings.HasPrefix(line, "  - ") && !strings.HasPrefix(line, "   ")
	}
	startLine := -1
	endLine := -1
	for i := headerIdx + 1; i < end; i++ {
		if !isTopEntry(lines[i]) {
			continue
		}
		// candidate entry start at i
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(lines[i], " \t"), "- "))
		// the name may be on the same line ("- name: foo") or on a child line
		// — check both forms over the entry's full body lines.
		entryEnd := end
		for j := i + 1; j < end; j++ {
			if isTopEntry(lines[j]) {
				entryEnd = j
				break
			}
		}
		hit := false
		if strings.HasPrefix(body, "name:") {
			v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(body, "name:")), "\"' ")
			if v == name {
				hit = true
			}
		}
		if !hit {
			for j := i + 1; j < entryEnd; j++ {
				tj := strings.TrimSpace(lines[j])
				if strings.HasPrefix(tj, "name:") {
					v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(tj, "name:")), "\"' ")
					if v == name {
						hit = true
					}
					break
				}
			}
		}
		if hit {
			startLine = i
			endLine = entryEnd
			break
		}
		i = entryEnd - 1
	}
	if startLine == -1 {
		return lines
	}
	out := make([]string, 0, len(lines)-(endLine-startLine))
	out = append(out, lines[:startLine]...)
	out = append(out, lines[endLine:]...)
	return out
}

// appendToYAMLSection inserts the given entry lines at the end of the named
// top-level section. The section is created at the bottom of the file when
// missing.
func appendToYAMLSection(lines []string, sectionHeader string, entry []string) []string {
	headerIdx, end := findSectionBounds(lines, sectionHeader)
	if headerIdx == -1 {
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, sectionHeader)
		out = append(out, entry...)
		return out
	}
	out := make([]string, 0, len(lines)+len(entry))
	out = append(out, lines[:end]...)
	out = append(out, entry...)
	out = append(out, lines[end:]...)
	return out
}

// addProxy parses `key=value` args, sanity-checks the required `name`/`type`,
// and writes a new proxies[] entry. An existing entry with the same name is
// dropped first so the call acts as an upsert.
func (t *mihomoTool) addProxy(args []string) error {
	kvs, err := parseKVArgs(args)
	if err != nil {
		return err
	}
	name := ""
	for _, p := range kvs {
		if p.key == "name" {
			name = strings.TrimSpace(p.value)
		}
	}
	if name == "" {
		return fmt.Errorf("addProxy: name=<id> is required")
	}
	path := t.configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("config does not exist: %s (run `cicy-mihomo gen-config` first)", path)
	} else if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	lines = removeYAMLListEntryByName(lines, "proxies:", name)
	lines = appendToYAMLSection(lines, "proxies:", renderProxyEntry(kvs))
	if err := t.writeMihomoYAMLValidated(path, []byte(strings.Join(lines, "\n"))); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(t.stdout, "added: proxy %q\n", name)
	if err := t.reload(); err != nil {
		if errors.Is(err, errMihomoNotRunning) {
			_, _ = fmt.Fprintln(t.stdout, "note: mihomo not running — start it with `cicy-mihomo start` to pick up the new entry")
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, "reloaded")
	return nil
}

// addGroup writes a `type: select` proxy-groups[] entry with the given members.
// Re-running with the same `name` replaces the existing entry.
func (t *mihomoTool) addGroup(name string, members []string) error {
	if name == "" {
		return fmt.Errorf("addGroup: name is required")
	}
	path := t.configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("config does not exist: %s (run `cicy-mihomo gen-config` first)", path)
	} else if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	lines = removeYAMLListEntryByName(lines, "proxy-groups:", name)
	entry := []string{
		"  - name: " + yamlScalar(name),
		"    type: select",
	}
	if len(members) > 0 {
		entry = append(entry, "    proxies:")
		for _, m := range members {
			entry = append(entry, "      - "+yamlScalar(strings.TrimSpace(m)))
		}
	}
	lines = appendToYAMLSection(lines, "proxy-groups:", entry)
	if err := t.writeMihomoYAMLValidated(path, []byte(strings.Join(lines, "\n"))); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(t.stdout, "added: proxy-group %q (members: %d)\n", name, len(members))
	if err := t.reload(); err != nil {
		if errors.Is(err, errMihomoNotRunning) {
			_, _ = fmt.Fprintln(t.stdout, "note: mihomo not running — start it with `cicy-mihomo start` to pick up the new entry")
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, "reloaded")
	return nil
}

func (t *mihomoTool) showConfig() error {
	data, err := os.ReadFile(t.configPath())
	if err != nil {
		return err
	}
	_, _ = t.stdout.Write(data)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, _ = fmt.Fprintln(t.stdout)
	}
	return nil
}

func (t *mihomoTool) start() error {
	binary, err := t.resolveBinary()
	if err != nil {
		return err
	}
	configPath := t.configPath()
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found: %s", configPath)
	}
	if err := os.MkdirAll(t.stateDir(), 0o755); err != nil {
		return err
	}
	logPath := t.logFile()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "-d", filepath.Join(userHomeDir(), "cicy-ai", "db"), "-f", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	state := mihomoState{PID: cmd.Process.Pid, Binary: binary, Config: configPath, Log: logPath, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := t.writeState(state); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, "started")
	return nil
}

func (t *mihomoTool) stop() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errMihomoNotRunning
		}
		return err
	}
	process, err := os.FindProcess(state.PID)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(t.stateFile())
	_, _ = fmt.Fprintln(t.stdout, "stopped")
	return nil
}

func (t *mihomoTool) status() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(t.stdout, "status: stopped")
			return nil
		}
		return err
	}
	_, controller := parseMihomoPortsFromConfig(state.Config)
	if strings.TrimSpace(controller) == "" {
		controller = "127.0.0.1:19001"
	}
	controllerURL := "http://" + controller + "/version"
	version := ""
	if resp, err := http.Get(controllerURL); err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		version = strings.TrimSpace(string(body))
	}
	_, _ = fmt.Fprintf(t.stdout, "status: running\npid: %d\nbinary: %s\nconfig: %s\nlog: %s\nstarted_at: %s\ncontroller: %s\nversion: %s\n", state.PID, state.Binary, state.Config, state.Log, state.StartedAt, controllerURL, version)
	return nil
}

func (t *mihomoTool) reload() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errMihomoNotRunning
		}
		return err
	}
	configPath := state.Config
	if strings.TrimSpace(configPath) == "" {
		configPath = t.configPath()
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	_, controller := parseMihomoPortsFromConfig(configPath)
	if strings.TrimSpace(controller) == "" {
		controller = "127.0.0.1:19001"
	}
	endpoint := "http://" + controller + "/configs?force=true"
	payload := map[string]any{
		"path":    configPath,
		"payload": string(data),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("reload failed: %s", strings.TrimSpace(string(respBody)))
	}
	_, _ = fmt.Fprintln(t.stdout, "reloaded")
	return nil
}

func (t *mihomoTool) logs(opts mihomoFlagOptions) error {
	path := t.logFile()
	if opts.Follow {
		cmd := exec.Command("tail", "-f", path)
		cmd.Stdout = t.stdout
		cmd.Stderr = t.stderr
		return cmd.Run()
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > opts.Lines {
			lines = lines[1:]
		}
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(t.stdout, line)
	}
	return scanner.Err()
}

func (t *mihomoTool) readState() (mihomoState, error) {
	var state mihomoState
	data, err := os.ReadFile(t.stateFile())
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (t *mihomoTool) writeState(state mihomoState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(t.stateFile(), data, 0o644)
}

type proxyEntry struct {
	Name string
	Type string
	Host string
	Port int
	User string
	Pass string
}

var testURLs = []string{
	"https://api.anthropic.com",
	"https://chatgpt.com",
	"https://api.myip.com",
}

func (t *mihomoTool) testAll() error {
	state, err := t.readState()
	if err != nil {
		return fmt.Errorf("cannot read state: %w", err)
	}
	cfgPath := state.Config
	if cfgPath == "" {
		cfgPath = t.configPath()
	}
	proxies := parseProxiesFromConfig(cfgPath)
	// Auth password for the local mixed port. gen-config writes a random
	// globalPassword and any non-empty username matches it (IN-USER-PREFIX,w-…
	// then picks default_proxy_group). Reading it here avoids the previous
	// hard-coded w-10001:MsZTKFsSCWrQC25d that broke after gen-config changed
	// the secret.
	password := readGlobalPasswordFromYAML(cfgPath)
	_, _ = fmt.Fprintf(t.stdout, "testing %d proxy nodes:\n", len(proxies))
	// header
	_, _ = fmt.Fprintf(t.stdout, "%-20s", "")
	for _, url := range testURLs {
		short := strings.TrimPrefix(url, "https://")
		if len(short) > 16 {
			short = short[:16]
		}
		_, _ = fmt.Fprintf(t.stdout, " %10s", short)
	}
	_, _ = fmt.Fprintln(t.stdout)
	// rows
	for _, p := range proxies {
		_, _ = fmt.Fprintf(t.stdout, "%-20s", p.Name)
		for _, url := range testURLs {
			t.testViaLocal(p, url, password)
		}
		_, _ = fmt.Fprintln(t.stdout)
	}
	return nil
}

func (t *mihomoTool) testViaLocal(p proxyEntry, url, password string) {
	ctrl := "http://127.0.0.1:19001"
	body := fmt.Sprintf(`{"name":"%s"}`, p.Name)
	req, _ := http.NewRequest("PUT", ctrl+"/proxies/default_proxy_group", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, " %10s", "sel_err")
		return
	}
	resp.Body.Close()

	time.Sleep(200 * time.Millisecond)

	if password == "" {
		_, _ = fmt.Fprintf(t.stdout, " %10s", "no_pass")
		return
	}
	proxyURL := fmt.Sprintf("http://w-test:%s@127.0.0.1:9001", password)
	cmd := exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "8", "--max-time", "15", "-x", proxyURL, url)
	out, err := cmd.Output()
	timeStr := strings.TrimSpace(string(out))
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, " %10s", "timeout")
	} else if sec, parseErr := strconv.ParseFloat(timeStr, 64); parseErr == nil {
		_, _ = fmt.Fprintf(t.stdout, " %7.2fs ", sec)
	} else {
		_, _ = fmt.Fprintf(t.stdout, " %10s", timeStr)
	}
}

// readGlobalPasswordFromYAML pulls the top-level `globalPassword:` value from
// the mihomo config without parsing the whole file. Returns "" if the file or
// key is missing; callers should treat that as "can't auth via local proxy".
func readGlobalPasswordFromYAML(path string) string {
	data, err := os.ReadFile(path)
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

func parseProxiesFromConfig(path string) []proxyEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var proxies []proxyEntry
	inProxies := false
	var cur *proxyEntry
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "proxies:" {
			inProxies = true
			continue
		}
		if !inProxies {
			continue
		}
		if strings.HasPrefix(trimmed, "- name:") {
			if cur != nil {
				proxies = append(proxies, *cur)
			}
			cur = &proxyEntry{}
			name := strings.TrimPrefix(trimmed, "- name:")
			name = strings.TrimSpace(name)
			name = strings.Trim(name, "\"")
			cur.Name = name
			continue
		}
		if strings.HasPrefix(trimmed, "type:") {
			if cur == nil {
				continue
			}
			cur.Type = strings.TrimSpace(strings.TrimPrefix(trimmed, "type:"))
			continue
		}
		if strings.HasPrefix(trimmed, "server:") {
			if cur == nil {
				continue
			}
			cur.Host = strings.TrimSpace(strings.TrimPrefix(trimmed, "server:"))
			continue
		}
		if strings.HasPrefix(trimmed, "port:") {
			if cur == nil {
				continue
			}
			portStr := strings.TrimSpace(strings.TrimPrefix(trimmed, "port:"))
			cur.Port, _ = strconv.Atoi(portStr)
			continue
		}
		if strings.HasPrefix(trimmed, "username:") {
			if cur == nil {
				continue
			}
			cur.User = strings.TrimSpace(strings.TrimPrefix(trimmed, "username:"))
			cur.User = strings.Trim(cur.User, "\"")
			continue
		}
		if strings.HasPrefix(trimmed, "password:") {
			if cur == nil {
				continue
			}
			cur.Pass = strings.TrimSpace(strings.TrimPrefix(trimmed, "password:"))
			cur.Pass = strings.Trim(cur.Pass, "\"")
			continue
		}
		if strings.HasPrefix(trimmed, "authentication:") || strings.HasPrefix(trimmed, "rules:") || strings.HasPrefix(trimmed, "proxy-groups:") {
			if cur != nil {
				proxies = append(proxies, *cur)
				cur = nil
			}
			inProxies = false
		}
	}
	if cur != nil {
		proxies = append(proxies, *cur)
	}
	// fallback to global authentication for proxies without per-proxy auth
	authRe := regexp.MustCompile(`(?m)^\s*-\s+"([^:]+):([^"]+)"\s*$`)
	authMatches := authRe.FindAllStringSubmatch(string(data), -1)
	for i := range proxies {
		if proxies[i].User == "" && len(authMatches) > 0 {
			proxies[i].User = authMatches[0][1]
			proxies[i].Pass = authMatches[0][2]
		}
	}
	return proxies
}

func (t *mihomoTool) testProxy(p proxyEntry) {
	url := "https://api.anthropic.com"
	var cmd *exec.Cmd
	switch strings.ToLower(p.Type) {
	case "socks5":
		addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
		cmd = exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "10", "--max-time", "20", "--socks5-hostname", addr, url)
	case "http":
		addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
		proxyURL := fmt.Sprintf("http://%s", addr)
		if p.User != "" && p.Pass != "" {
			proxyURL = fmt.Sprintf("http://%s:%s@%s", p.User, p.Pass, addr)
		}
		cmd = exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "10", "--max-time", "20", "-x", proxyURL, url)
	}
	if cmd == nil {
		return
	}
	out, err := cmd.Output()
	timeStr := strings.TrimSpace(string(out))
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, "%-20s ❌ %v\n", p.Name, err)
	} else if sec, parseErr := strconv.ParseFloat(timeStr, 64); parseErr == nil {
		_, _ = fmt.Fprintf(t.stdout, "%-20s %.2fs\n", p.Name, sec)
	} else {
		_, _ = fmt.Fprintf(t.stdout, "%-20s %s\n", p.Name, timeStr)
	}
}

func parseMihomoPortsFromConfig(path string) (mixedPort int, controller string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	text := string(data)
	mixedRe := regexp.MustCompile(`(?m)^mixed-port:\s*(\d+)\s*$`)
	controllerRe := regexp.MustCompile(`(?m)^external-controller:\s*([^\s]+)\s*$`)
	if m := mixedRe.FindStringSubmatch(text); len(m) == 2 {
		mixedPort, _ = strconv.Atoi(m[1])
	}
	if m := controllerRe.FindStringSubmatch(text); len(m) == 2 {
		controller = strings.TrimSpace(m[1])
	}
	return mixedPort, controller
}
