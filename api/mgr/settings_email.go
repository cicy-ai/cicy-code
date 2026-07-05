package main

// Settings → General backend for the email (SMTP) config + API-token rotation.
// The UI configures the SAME ~/cicy-ai/db/email.json the `email` skill uses, and
// token rotation shells out to the (must-install) `globalApiToken` skill, which
// itself gates on the `email` skill being configured and delivers the new token
// by SMTP. One implementation (the skills) drives both the CLI and this UI.

import (
	"crypto/rand"
	"encoding/base64"
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
	// Strip leftover scaffold placeholders ("<paste-…>") from every section so
	// they never count as "set" — otherwise the UI (which loads what's on disk
	// and may not retype host/from) writes the placeholder straight back, and
	// the from←user default below never fires. Deleting them lets the default
	// kick in and keeps status honest ("missing" rather than a fake value).
	for _, sect := range []string{"smtp", "imap", "pop3"} {
		if b, _ := cur[sect].(map[string]any); b != nil {
			for k, v := range b {
				if s, ok := v.(string); ok && !emailFilled(strings.TrimSpace(s)) && k != "pass" {
					delete(b, k)
				}
			}
		}
	}
	// Derive sensible defaults so the UI only needs the account + password:
	//   smtp.from  ← smtp.user  (the `email` skill requires a non-empty from)
	//   default_to ← smtp.user  (send the rotated token to yourself by default)
	if sb, _ := cur["smtp"].(map[string]any); sb != nil {
		user := strings.TrimSpace(emailStr2(sb, "user"))
		if user != "" {
			if !emailFilled(strings.TrimSpace(emailStr2(sb, "from"))) {
				sb["from"] = user
			}
			if dt, _ := cur["default_to"].(string); !emailFilled(strings.TrimSpace(dt)) {
				cur["default_to"] = user
			}
		}
		cur["smtp"] = sb
	}
	return cur
}

func emailStr2(b map[string]any, k string) string {
	s, _ := b[k].(string)
	return s
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

// POST /api/settings/token/refresh → rotate the api_token and email the new one.
// The backend ORCHESTRATES the order itself (rather than delegating to a skill's
// version-specific behavior) so it can guarantee the safety property: the new
// token is emailed FIRST and only persisted on a successful send — a failed
// delivery rotates nothing, so the current token keeps working and the user is
// never locked out. Delivery uses the (must-install) `email` skill; recipient is
// the request's `to` or the email config's default_to.
func handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	var req M
	_ = readBody(r, &req)

	emailCfgMu.Lock()
	cfg := readEmailJSONLocked()
	emailCfgMu.Unlock()

	smtpReady := emailFilled(emailStr(cfg, "smtp", "host")) && emailFilled(emailStr(cfg, "smtp", "user")) &&
		emailFilled(emailStr(cfg, "smtp", "pass")) && emailFilled(emailStr(cfg, "smtp", "from"))
	if sb, _ := cfg["smtp"].(map[string]any); sb == nil || sb["port"] == nil {
		smtpReady = false
	}
	if !smtpReady {
		writeJSONStatus(w, 409, M{"ok": false, "code": "EMAIL_NOT_CONFIGURED", "detail": "SMTP is not configured — set it up first so the rotated token can be delivered"})
		return
	}

	to, _ := req["to"].(string)
	to = strings.TrimSpace(to)
	if to == "" {
		dt, _ := cfg["default_to"].(string)
		to = strings.TrimSpace(dt)
	}
	if to == "" {
		writeJSONStatus(w, 422, M{"ok": false, "code": "NO_RECIPIENT", "detail": "no recipient — set a recipient (default_to) in the SMTP config"})
		return
	}

	bin := resolveSkillBin("email")
	if bin == "" {
		writeJSONStatus(w, 409, M{"ok": false, "code": "EMAIL_NOT_INSTALLED", "detail": "the email skill is not installed"})
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSONStatus(w, 500, M{"ok": false, "code": "RAND", "detail": err.Error()})
		return
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)

	// Email FIRST.
	body := "Your CiCy api_token on this host was just rotated.\n\nNew token: " + tok +
		"\n\nThe previous token is now invalid. Keep this private — anyone with this token can control this host’s API."
	cmd := exec.Command(bin, "send", "--to", to, "--subject", "CiCy API token rotated", "--body", body)
	cmd.Env = skillEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		writeJSONStatus(w, 502, M{"ok": false, "code": "EMAIL_SEND_FAILED", "detail": strings.TrimSpace(string(out))})
		return
	}

	// Persist ONLY after a successful send.
	providersFileMu.Lock()
	gc := readGlobalJSONConfig()
	gc["api_token"] = tok
	werr := writeGlobalJSONConfig(gc)
	providersFileMu.Unlock()
	if werr != nil {
		writeJSONStatus(w, 500, M{"ok": false, "code": "WRITE_FAILED", "detail": werr.Error()})
		return
	}

	J(w, M{"ok": true, "token": tok, "emailed_to": to})
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
