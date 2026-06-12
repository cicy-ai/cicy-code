package main

// Settings → General backend for the email (SMTP) config + API-token rotation.
// The UI configures the SAME ~/cicy-ai/db/email.json the `email` skill uses, and
// token rotation shells out to the (must-install) `globalApiToken` skill, which
// itself gates on the `email` skill being configured and delivers the new token
// by SMTP. One implementation (the skills) drives both the CLI and this UI.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var emailCfgMu sync.Mutex

func emailDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "db", "email.json")
}

func readEmailJSONLocked() map[string]any {
	data, err := os.ReadFile(emailDBPath())
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if json.Unmarshal(data, &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func writeEmailJSONLocked(cfg map[string]any) error {
	p := emailDBPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, append(data, '\n'), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(p, 0o600)
	return nil
}

func emailStr(cfg map[string]any, sect, key string) string {
	b, _ := cfg[sect].(map[string]any)
	if b == nil {
		return ""
	}
	s, _ := b[key].(string)
	return strings.TrimSpace(s)
}

func emailFilled(s string) bool {
	return s != "" && !strings.HasPrefix(s, "<paste")
}

// emailConfigPublic strips secrets (passwords) and reports readiness — the only
// shape ever sent to the client.
func emailConfigPublic(cfg map[string]any) M {
	block := func(sect string) M {
		b, _ := cfg[sect].(map[string]any)
		out := M{}
		for _, k := range []string{"host", "port", "secure", "user", "from"} {
			if b != nil {
				if v, ok := b[k]; ok {
					out[k] = v
				}
			}
		}
		out["pass_set"] = emailFilled(emailStr(cfg, sect, "pass"))
		return out
	}
	smtpReady := emailFilled(emailStr(cfg, "smtp", "host")) &&
		emailFilled(emailStr(cfg, "smtp", "user")) &&
		emailFilled(emailStr(cfg, "smtp", "pass")) &&
		emailFilled(emailStr(cfg, "smtp", "from"))
	if sb, _ := cfg["smtp"].(map[string]any); sb == nil || sb["port"] == nil {
		smtpReady = false
	}
	imapReady := emailFilled(emailStr(cfg, "imap", "host")) && emailFilled(emailStr(cfg, "imap", "user")) && emailFilled(emailStr(cfg, "imap", "pass"))
	pop3Ready := emailFilled(emailStr(cfg, "pop3", "host")) && emailFilled(emailStr(cfg, "pop3", "user")) && emailFilled(emailStr(cfg, "pop3", "pass"))
	dt, _ := cfg["default_to"].(string)
	return M{
		"smtp":          block("smtp"),
		"imap":          block("imap"),
		"pop3":          block("pop3"),
		"default_to":    dt,
		"smtp_ready":    smtpReady,
		"send_ready":    smtpReady,
		"imap_ready":    imapReady,
		"pop3_ready":    pop3Ready,
		"receive_ready": imapReady || pop3Ready,
		"config_path":   emailDBPath(),
	}
}

// mergeEmailConfig overlays the submitted (non-secret-or-changed) fields onto the
// existing config. A blank/omitted password keeps the stored one, so the UI can
// edit host/user without re-entering the password.
func mergeEmailConfig(cur, req map[string]any) map[string]any {
	if cur == nil {
		cur = map[string]any{}
	}
	for _, sect := range []string{"smtp", "imap", "pop3"} {
		rb, ok := req[sect].(map[string]any)
		if !ok {
			continue
		}
		cb, _ := cur[sect].(map[string]any)
		if cb == nil {
			cb = map[string]any{}
		}
		for k, v := range rb {
			if k == "pass" {
				if s, _ := v.(string); strings.TrimSpace(s) == "" {
					continue // keep existing password
				}
			}
			cb[k] = v
		}
		cur[sect] = cb
	}
	if v, ok := req["default_to"]; ok {
		cur["default_to"] = v
	}
	return cur
}

// GET  /api/settings/email  → public (secret-stripped) config + readiness
// POST /api/settings/email  → merge-write ~/cicy-ai/db/email.json
func handleEmailConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		emailCfgMu.Lock()
		cfg := readEmailJSONLocked()
		emailCfgMu.Unlock()
		J(w, emailConfigPublic(cfg))
	case "POST":
		var req M
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "bad json")
			return
		}
		emailCfgMu.Lock()
		merged := mergeEmailConfig(readEmailJSONLocked(), req)
		err := writeEmailJSONLocked(merged)
		emailCfgMu.Unlock()
		if err != nil {
			httpErr(w, 500, "write email.json: "+err.Error())
			return
		}
		J(w, M{"success": true, "config": emailConfigPublic(merged)})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

// GET /api/settings/token → the current api_token (caller is already authed).
func handleTokenShow(w http.ResponseWriter, r *http.Request) {
	J(w, M{"token": loadAPIToken()})
}

// POST /api/settings/token/refresh → rotate + email via the globalApiToken skill.
// Returns the new token; maps the skill's gating errors to HTTP statuses the UI
// uses to drive the "configure SMTP first" flow.
func handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	bin := resolveSkillBin("globalApiToken")
	if bin == "" {
		writeJSONStatus(w, 500, M{"ok": false, "code": "SKILL_NOT_INSTALLED", "detail": "globalApiToken skill not installed"})
		return
	}
	var req M
	_ = readBody(r, &req)
	args := []string{"refresh", "--json"}
	if to, _ := req["to"].(string); strings.TrimSpace(to) != "" {
		args = append(args, "--to", strings.TrimSpace(to))
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = skillEnv()
	out, _ := cmd.Output() // skill prints its {ok,...} envelope to stdout for ok+err

	var res struct {
		OK   bool `json:"ok"`
		Data struct {
			APIToken  string `json:"api_token"`
			EmailedTo string `json:"emailed_to"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &res) != nil {
		writeJSONStatus(w, 500, M{"ok": false, "code": "BAD_OUTPUT", "detail": strings.TrimSpace(string(out))})
		return
	}
	if !res.OK {
		status := 500
		switch res.Error.Code {
		case "EMAIL_NOT_INSTALLED", "EMAIL_NOT_CONFIGURED":
			status = 409 // UI → prompt to configure SMTP first
		case "NO_RECIPIENT":
			status = 422
		case "EMAIL_SEND_FAILED":
			status = 502
		}
		writeJSONStatus(w, status, M{"ok": false, "code": res.Error.Code, "detail": res.Error.Message})
		return
	}
	J(w, M{"ok": true, "token": res.Data.APIToken, "emailed_to": res.Data.EmailedTo})
}

func writeJSONStatus(w http.ResponseWriter, status int, body M) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// resolveSkillBin finds an installed skill CLI: PATH first, then ~/.local/bin
// (where skills symlink), then the skill's repo bin. Returns "" if not found.
func resolveSkillBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, "cicy-ai", "skills", name, "bin", name),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// skillEnv ensures ~/.local/bin is on PATH for the child so a shelled skill
// (globalApiToken) can in turn find its own dependency (`email`).
func skillEnv() []string {
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			if !strings.Contains(e, localBin) {
				env[i] = "PATH=" + localBin + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			}
			return env
		}
	}
	return append(env, "PATH="+localBin)
}
