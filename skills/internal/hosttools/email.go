package hosttools

// Email skill — thin wrapper around the Resend transactional email API.
//
// Subcommands:
//   config   — open ~/cicy-ai/db/email.json in code-server (auto-creates a
//              placeholder with chmod 600 so the api_key never has to be
//              pasted into chat).
//   status   — report config state. api_key is always masked.
//   send     — POST to https://api.resend.com/emails. Body comes from
//              --body / --html / --body-file / stdin (whichever is set).
//
// Security model: the wrapper is the only component that reads the JSON
// config. Agents must NOT cat / Read / grep ~/cicy-ai/db/email.json.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	emailConfigPath      = "cicy-ai/db/email.json"
	emailAPIEndpoint     = "https://api.resend.com/emails"
	emailAPIPlaceholder  = "<paste-your-resend-api-key-here>"
	emailFromPlaceholder = "<paste-your-verified-from-address-here>"
)

type emailConfig struct {
	APIKey      string `json:"api_key"`
	FromAddress string `json:"from_address"`
	DefaultTo   string `json:"default_to,omitempty"`
}

func emailConfigFile() string {
	return filepath.Join(userHomeDir(), emailConfigPath)
}

func loadEmailConfig() (emailConfig, error) {
	var cfg emailConfig
	data, err := os.ReadFile(emailConfigFile())
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", emailConfigFile(), err)
	}
	return cfg, nil
}

func emailPlaceholderJSON() string {
	return `{
  "api_key": "` + emailAPIPlaceholder + `",
  "from_address": "` + emailFromPlaceholder + `",
  "default_to": ""
}
`
}

func isEmailPlaceholder(cfg emailConfig) bool {
	ak := strings.TrimSpace(cfg.APIKey)
	from := strings.TrimSpace(cfg.FromAddress)
	if ak == "" || from == "" {
		return true
	}
	if strings.HasPrefix(ak, "<") || strings.HasPrefix(from, "<") {
		return true
	}
	return false
}

func (e *Env) writeEmailPlaceholder() (bool, error) {
	path := emailConfigFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(emailPlaceholderJSON()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ── dispatch ───────────────────────────────────────────────────────────────

func (e *Env) runEmail(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printEmailUsage(e.Stdout)
		return nil
	}
	switch args[0] {
	case "config":
		return e.runEmailConfigOpen()
	case "status":
		return e.runEmailStatus()
	case "send":
		return e.runEmailSend(args[1:])
	default:
		printEmailUsage(e.Stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func printEmailUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: email <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  config    Open ~/cicy-ai/db/email.json in code-server (auto-creates placeholder)")
	fmt.Fprintln(w, "  status    Show config state (api_key masked) and from_address")
	fmt.Fprintln(w, "  send      Send an email via Resend (see flags below)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "send flags:")
	fmt.Fprintln(w, "  --to <addr>          required; one recipient")
	fmt.Fprintln(w, "  --subject <text>     required")
	fmt.Fprintln(w, "  --body <text>        plain-text body (or pipe via stdin)")
	fmt.Fprintln(w, "  --body-file <path>   read plain-text body from file")
	fmt.Fprintln(w, "  --html <html>        HTML body (mutually exclusive with --body)")
	fmt.Fprintln(w, "  --from <addr>        override the configured from_address")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Agents must NOT cat / Read ~/cicy-ai/db/email.json — api_key is a secret.")
}

// ── config (open in code-server, auto-creates placeholder) ─────────────────

func (e *Env) runEmailConfigOpen() error {
	path := emailConfigFile()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if _, werr := e.writeEmailPlaceholder(); werr != nil {
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
	fmt.Fprintln(e.Stdout, "REQUIREMENTS — you (the user) must do these yourself; the agent cannot do them for you:")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "  Path 1: QUICK SANDBOX (no domain needed, can only mail yourself)")
	fmt.Fprintln(e.Stdout, "  ──────────────────────────────────────────────────────────────")
	fmt.Fprintln(e.Stdout, "  1. Sign up at https://resend.com/signup  (free tier, no credit card)")
	fmt.Fprintln(e.Stdout, "  2. Create an API key at https://resend.com/api-keys → copy `re_xxxxxxxx…`")
	fmt.Fprintln(e.Stdout, "  3. In the open email.json:")
	fmt.Fprintln(e.Stdout, "       api_key      = the re_xxx value")
	fmt.Fprintln(e.Stdout, "       from_address = onboarding@resend.dev")
	fmt.Fprintln(e.Stdout, "  4. Limitation: Resend's sandbox sender can ONLY deliver to the email you")
	fmt.Fprintln(e.Stdout, "     signed up with. Trying to send anywhere else returns 403.")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "  Path 2: PRODUCTION (your own domain, can mail anyone)")
	fmt.Fprintln(e.Stdout, "  ──────────────────────────────────────────────────────────")
	fmt.Fprintln(e.Stdout, "  1. Have a domain you own (any registrar — Namecheap, Cloudflare, GoDaddy, …)")
	fmt.Fprintln(e.Stdout, "  2. Sign up at https://resend.com/signup")
	fmt.Fprintln(e.Stdout, "  3. Add the domain at https://resend.com/domains → \"Add Domain\"")
	fmt.Fprintln(e.Stdout, "     Resend will display 3 TXT records you must add at your domain's DNS:")
	fmt.Fprintln(e.Stdout, "       • SPF      (root, type=TXT)  — typically `v=spf1 include:_spf.resend.com ~all`")
	fmt.Fprintln(e.Stdout, "                  If you already have SPF, merge: keep existing includes AND add")
	fmt.Fprintln(e.Stdout, "                  `include:_spf.resend.com` (one SPF record per domain only).")
	fmt.Fprintln(e.Stdout, "       • DKIM     (host=resend._domainkey, type=TXT) — long public-key value")
	fmt.Fprintln(e.Stdout, "       • DMARC    (host=_dmarc, type=TXT) — typically `v=DMARC1; p=none;`")
	fmt.Fprintln(e.Stdout, "  4. Wait until Resend shows the domain as \"verified\" (green) — usually 5-15 min")
	fmt.Fprintln(e.Stdout, "     after DNS propagates. SPF check: https://www.dmarcanalyzer.com/spf/checker/")
	fmt.Fprintln(e.Stdout, "  5. Create an API key at https://resend.com/api-keys → copy `re_xxxxxxxx…`")
	fmt.Fprintln(e.Stdout, "  6. In the open email.json:")
	fmt.Fprintln(e.Stdout, "       api_key      = the re_xxx value")
	fmt.Fprintln(e.Stdout, "       from_address = any@your-verified-domain.com  (must match the domain you verified)")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "After you save email.json, run:")
	fmt.Fprintln(e.Stdout, "  email status                                     # should print `config: ready`")
	fmt.Fprintln(e.Stdout, "  email send --to <addr> --subject \"hello\" --body \"world\"")
	fmt.Fprintln(e.Stdout, "")
	fmt.Fprintln(e.Stdout, "Reminder: never paste the api_key into chat — only edit the file.")
	return nil
}

// ── status ─────────────────────────────────────────────────────────────────

func (e *Env) runEmailStatus() error {
	cfgPath := emailConfigFile()
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintln(e.Stdout, "config: missing (run `email config`)")
		return nil
	}
	cfg, err := loadEmailConfig()
	if err != nil {
		fmt.Fprintf(e.Stdout, "config: parse error: %v\n", err)
		return nil
	}
	if isEmailPlaceholder(cfg) {
		fmt.Fprintln(e.Stdout, "config: placeholder — user has not filled in the api_key / from_address yet (run `email config`)")
		return nil
	}
	fmt.Fprintln(e.Stdout, "config: ready")
	fmt.Fprintf(e.Stdout, "  api_key:      %s\n", maskAK(cfg.APIKey))
	fmt.Fprintf(e.Stdout, "  from_address: %s\n", cfg.FromAddress)
	if strings.TrimSpace(cfg.DefaultTo) != "" {
		fmt.Fprintf(e.Stdout, "  default_to:   %s\n", cfg.DefaultTo)
	}
	fmt.Fprintln(e.Stdout, "backend: resend.com (POST https://api.resend.com/emails)")
	return nil
}

// ── send ───────────────────────────────────────────────────────────────────

func (e *Env) runEmailSend(args []string) error {
	cfg, err := loadEmailConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config missing — run `email config` first")
		}
		return err
	}
	if isEmailPlaceholder(cfg) {
		return fmt.Errorf("config still contains placeholder values — run `email config` to fill in the api_key / from_address, then re-run send")
	}

	var to, subject, body, html, bodyFile, from string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			i++
			if i < len(args) {
				to = args[i]
			}
		case "--subject":
			i++
			if i < len(args) {
				subject = args[i]
			}
		case "--body":
			i++
			if i < len(args) {
				body = args[i]
			}
		case "--html":
			i++
			if i < len(args) {
				html = args[i]
			}
		case "--body-file":
			i++
			if i < len(args) {
				bodyFile = args[i]
			}
		case "--from":
			i++
			if i < len(args) {
				from = args[i]
			}
		case "-h", "--help":
			printEmailUsage(e.Stdout)
			return nil
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if strings.TrimSpace(from) == "" {
		from = cfg.FromAddress
	}
	if strings.TrimSpace(to) == "" {
		to = cfg.DefaultTo
	}
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("--to <addr> required (or set default_to in config)")
	}
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("--subject <text> required")
	}
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return fmt.Errorf("read --body-file: %w", err)
		}
		body = string(data)
	} else if body == "" && html == "" {
		if !emailStdinIsTerminal() {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			body = string(data)
		}
	}
	if body == "" && html == "" {
		return fmt.Errorf("body required — pass --body, --html, --body-file, or pipe content into stdin")
	}

	payload := map[string]interface{}{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
	}
	if html != "" {
		payload["html"] = html
	} else {
		payload["text"] = body
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", emailAPIEndpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend api: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend api status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &result)
	fmt.Fprintf(e.Stdout, "sent: id=%s\n", result.ID)
	fmt.Fprintf(e.Stdout, "  from:    %s\n", from)
	fmt.Fprintf(e.Stdout, "  to:      %s\n", to)
	fmt.Fprintf(e.Stdout, "  subject: %s\n", subject)
	return nil
}

func emailStdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
