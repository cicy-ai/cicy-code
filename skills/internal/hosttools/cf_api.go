package hosttools

// Cloudflare API skill — secure config + curl wrapper.
//
// Shares ~/cicy-ai/db/cf.json with cf-tunnel (same env-keyed format).
// cf reads api_token + account_id; cf-tunnel also uses tunnel_id/domain/zone_id.
//
// Commands:
//   cf config              — open ~/cicy-ai/db/cf.json in code-server
//   cf status              — show config state (api_token masked)
//   cf curl <METHOD> <PATH> [body]  — call Cloudflare API, token injected by tool
//
// Security: agents must NOT cat / Read / grep ~/cicy-ai/db/cf.json.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// isCFAPIReady returns true when api_token and account_id are both filled in.
func isCFAPIReady(cfg cfTunnelEnvConfig) bool {
	for _, v := range []string{cfg.APIToken, cfg.AccountID} {
		if strings.TrimSpace(v) == "" || strings.HasPrefix(strings.TrimSpace(v), "<") {
			return false
		}
	}
	return true
}

// ── dispatch ───────────────────────────────────────────────────────────────

func (e *Env) runCFAPI(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCFAPIUsage(e.Stdout)
		return nil
	}
	switch args[0] {
	case "config":
		return e.runCFAPIConfigOpen()
	case "status":
		return e.runCFAPIStatus()
	case "curl":
		return e.runCFAPICurl(args[1:])
	case "exec":
		return e.runCFAPIExec(args[1:])
	default:
		printCFAPIUsage(e.Stderr)
		return fmt.Errorf("unknown cf command: %s", args[0])
	}
}

func printCFAPIUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cf <command> [args...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  config                     Open ~/cicy-ai/db/cf.json in code-server")
	fmt.Fprintln(w, "  status                     Show config state (api_token masked)")
	fmt.Fprintln(w, "  curl <METHOD> <PATH> [body]  Call Cloudflare API — token injected by the tool")
	fmt.Fprintln(w, "  exec <command> [args...]   Run a command with CLOUDFLARE_API_TOKEN+ACCOUNT_ID env injected")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, `  cf curl GET /zones`)
	fmt.Fprintln(w, `  cf curl GET /zones/ZONE_ID/dns_records`)
	fmt.Fprintln(w, `  cf curl POST /zones/ZONE_ID/dns_records '{"type":"A","name":"sub","content":"1.2.3.4","ttl":1}'`)
	fmt.Fprintln(w, `  cf curl DELETE /zones/ZONE_ID/dns_records/RECORD_ID`)
	fmt.Fprintln(w, `  cf exec npx wrangler deploy`)
	fmt.Fprintln(w, `  cf exec npx wrangler kv namespace create FOO`)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Config shared with cf-tunnel — same ~/cicy-ai/db/cf.json file.")
	fmt.Fprintln(w, "Agents must NOT cat / Read ~/cicy-ai/db/cf.json — api_token is a secret.")
}

// ── config ─────────────────────────────────────────────────────────────────

func (e *Env) runCFAPIConfigOpen() error {
	// cf and cf-tunnel share the same config file; reuse cf-tunnel's placeholder logic.
	path := cfTunnelConfigFile()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if _, werr := e.writeCFTunnelPlaceholder(); werr != nil {
				return fmt.Errorf("create placeholder: %w", werr)
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
		fmt.Fprintf(e.Stdout, "\nagent-code-server returned %v. Edit directly if needed:\n  $EDITOR %s\n", err, path)
	}
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Shared config for cf and cf-tunnel. Required fields:")
	fmt.Fprintln(e.Stdout, "  api_token   — Cloudflare API token (create at https://dash.cloudflare.com/profile/api-tokens)")
	fmt.Fprintln(e.Stdout, "  account_id  — Cloudflare account ID (visible in the dashboard URL)")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Additional fields used only by cf-tunnel:")
	fmt.Fprintln(e.Stdout, "  tunnel_id   — Cloudflare Tunnel ID")
	fmt.Fprintln(e.Stdout, "  domain      — base domain for tunnel routes")
	fmt.Fprintln(e.Stdout, "  zone_id     — Cloudflare Zone ID for that domain")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "After saving, run `cf status` or `cf-tunnel status` to verify.")
	fmt.Fprintln(e.Stdout, "Reminder: never paste the api_token into chat — only edit the file.")
	return nil
}

// ── status ─────────────────────────────────────────────────────────────────

func (e *Env) runCFAPIStatus() error {
	path := cfTunnelConfigFile()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintln(e.Stdout, "config: missing — run `cf config` to create it")
		return nil
	}
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		fmt.Fprintf(e.Stdout, "config: parse error: %v\n", err)
		return nil
	}
	cfEnv := envOr("CF_ENV", "prod")
	envCfg, ok := cfg[cfEnv]
	if !ok {
		fmt.Fprintf(e.Stdout, "config: no %q block in cf.json — run `cf config`\n", cfEnv)
		return nil
	}
	if !isCFAPIReady(envCfg) {
		fmt.Fprintf(e.Stdout, "config: placeholder (%s) — fill in cf.json (run `cf config`)\n", cfEnv)
		return nil
	}
	fmt.Fprintf(e.Stdout, "config: ready (%s)\n", cfEnv)
	fmt.Fprintf(e.Stdout, "  api_token:  %s\n", maskAK(envCfg.APIToken))
	fmt.Fprintf(e.Stdout, "  account_id: %s\n", envCfg.AccountID)
	return nil
}

// ── curl wrapper ───────────────────────────────────────────────────────────

func (e *Env) runCFAPICurl(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("Usage: cf curl <METHOD> <PATH> [json-body]\n  e.g. cf curl GET /zones\n  e.g. cf curl POST /zones/ID/dns_records '{\"type\":\"A\",...}'")
	}
	cfEnv := envOr("CF_ENV", "prod")
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `cf config` first")
		}
		return fmt.Errorf("load config: %w", err)
	}
	envCfg, ok := cfg[cfEnv]
	if !ok || !isCFAPIReady(envCfg) {
		return fmt.Errorf("cf.json missing or incomplete for env %q — run `cf config`", cfEnv)
	}

	method := strings.ToUpper(args[0])
	path := args[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := "https://api.cloudflare.com/client/v4" + path

	curlArgs := []string{
		"-sS",
		"-X", method,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer " + envCfg.APIToken,
	}
	if len(args) > 2 {
		curlArgs = append(curlArgs, "-d", strings.Join(args[2:], " "))
	}
	curlArgs = append(curlArgs, url)

	return runPassthrough(e.Stdout, e.Stderr, "curl", curlArgs...)
}

// ── exec wrapper ───────────────────────────────────────────────────────────

// runCFAPIExec runs an arbitrary command with CLOUDFLARE_API_TOKEN and
// CLOUDFLARE_ACCOUNT_ID injected into its env. Useful for wrangler, terraform,
// or any other CF tool that reads those env vars. The token never appears in
// the agent's stdout — it goes file → child process env only.
func (e *Env) runCFAPIExec(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: cf exec <command> [args...]\n  e.g. cf exec npx wrangler deploy\n  e.g. cf exec npx wrangler kv namespace create FOO")
	}
	cfEnv := envOr("CF_ENV", "prod")
	cfg, err := loadCFTunnelConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `cf config` first")
		}
		return fmt.Errorf("load config: %w", err)
	}
	envCfg, ok := cfg[cfEnv]
	if !ok || !isCFAPIReady(envCfg) {
		return fmt.Errorf("cf.json missing or incomplete for env %q — run `cf config`", cfEnv)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(),
		"CLOUDFLARE_API_TOKEN="+envCfg.APIToken,
		"CLOUDFLARE_ACCOUNT_ID="+envCfg.AccountID,
	)
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
