package hosttools

// Google Workspace skill — Go port of providers/google-node.
//
// All 18 commands (gmail/sheets/drive/calendar) live in one file so it tracks
// the JS implementation 1:1 during the migration window. Auth pulls
// credentials in the same order as api/mgr/oauth_google.go so the
// "Authorize Google" marketplace button and any legacy global.json setup
// remain interchangeable.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	calendarapi "google.golang.org/api/calendar/v3"
	driveapi "google.golang.org/api/drive/v3"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"
)

const (
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	gmailCachePath      = ".cache/gmail-ids.json"
)

// ── credential loading ──────────────────────────────────────────────────────

type googleCreds struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

func loadGoogleCreds() (googleCreds, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return googleCreds{}, err
	}

	read := func(p string) map[string]any {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		return m
	}

	dbCfg := read(filepath.Join(home, "cicy-ai", "db", "google.json"))
	sharedClient := read(filepath.Join(home, "cicy-ai", "db", "google_oauth_client.json"))
	globalJSON := read(filepath.Join(home, "cicy-ai", "global.json"))

	pick := func(keys ...func() string) string {
		for _, fn := range keys {
			if v := strings.TrimSpace(fn()); v != "" {
				return v
			}
		}
		return ""
	}
	mapStr := func(m map[string]any, k string) string {
		if m == nil {
			return ""
		}
		v, _ := m[k].(string)
		return v
	}

	creds := googleCreds{
		ClientID: pick(
			func() string { return mapStr(dbCfg, "client_id") },
			func() string { return mapStr(sharedClient, "client_id") },
			func() string { return os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_ID") },
			func() string { return mapStr(globalJSON, "GMAIL_CLIENT_ID") },
			func() string { return mapStr(globalJSON, "GMAIL_WEB_CLIENT_ID") },
		),
		ClientSecret: pick(
			func() string { return mapStr(dbCfg, "client_secret") },
			func() string { return mapStr(sharedClient, "client_secret") },
			func() string { return os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET") },
			func() string { return mapStr(globalJSON, "GMAIL_CLIENT_SECRET") },
			func() string { return mapStr(globalJSON, "GMAIL_WEB_CLIENT_SECRET") },
		),
		RefreshToken: pick(
			func() string { return mapStr(dbCfg, "refresh_token") },
			func() string { return mapStr(globalJSON, "GMAIL_REFRESH_TOKEN") },
		),
	}
	if creds.ClientID == "" || creds.ClientSecret == "" || creds.RefreshToken == "" {
		return creds, fmt.Errorf("Google not authorized — open the marketplace, find Google, click Authorize (or ensure ~/cicy-ai/db/google.json has refresh_token)")
	}
	return creds, nil
}

func googleHTTPClient(ctx context.Context) (*http.Client, error) {
	creds, err := loadGoogleCreds()
	if err != nil {
		return nil, err
	}
	cfg := &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: googleTokenEndpoint},
	}
	tok := &oauth2.Token{RefreshToken: creds.RefreshToken}
	return cfg.Client(ctx, tok), nil
}

// ── helpers for cli arg parsing / id cache ─────────────────────────────────

func gmailCacheFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, gmailCachePath)
}

func saveGmailIDs(ids []string) error {
	path := gmailCacheFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(ids)
	return os.WriteFile(path, data, 0o644)
}

func loadGmailIDs() []string {
	data, err := os.ReadFile(gmailCacheFile())
	if err != nil {
		return nil
	}
	var ids []string
	_ = json.Unmarshal(data, &ids)
	return ids
}

// resolveGmailID: if arg is 1-100, treat as index into last `list` cache.
// Otherwise treat as raw message id.
func resolveGmailID(arg string) string {
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 && n <= 100 {
		ids := loadGmailIDs()
		if n-1 < len(ids) {
			return ids[n-1]
		}
	}
	return arg
}

// readStdin returns piped-in body text. Empty if stdin is a TTY.
func readStdinAll() string {
	info, err := os.Stdin.Stat()
	if err != nil {
		return ""
	}
	if (info.Mode() & os.ModeCharDevice) != 0 {
		return "" // TTY — no piped input
	}
	data, _ := io.ReadAll(os.Stdin)
	return string(data)
}

// ── dispatcher ──────────────────────────────────────────────────────────────

func (e *Env) runGoogle(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printGoogleUsage(e.Stdout)
		if len(args) >= 2 && args[0] == "help" {
			fmt.Fprintln(e.Stdout)
			printGoogleServiceHelp(e.Stdout, args[1])
		}
		return nil
	}
	service := args[0]
	rest := args[1:]
	var cmd string
	if len(rest) > 0 {
		cmd = rest[0]
		rest = rest[1:]
	}

	ctx := context.Background()
	switch service {
	case "gmail":
		return e.runGoogleGmail(ctx, cmd, rest)
	case "sheets":
		return e.runGoogleSheets(ctx, cmd, rest)
	case "drive":
		return e.runGoogleDrive(ctx, cmd, rest)
	case "calendar":
		return e.runGoogleCalendar(ctx, cmd, rest)
	case "login", "status":
		return e.runGoogleLogin(ctx, service)
	default:
		printGoogleUsage(e.Stdout)
		return nil
	}
}

func isHelpArg(v string) bool {
	switch v {
	case "help", "-h", "--help":
		return true
	}
	return false
}

func printGoogleUsage(w io.Writer) {
	fmt.Fprintln(w, "Available services:")
	fmt.Fprintln(w, "  login      - Authorize this server with Google (OAuth device flow)")
	fmt.Fprintln(w, "  status     - Show current authorization status")
	fmt.Fprintln(w, "  gmail      - Email management")
	fmt.Fprintln(w, "  sheets     - Spreadsheet operations")
	fmt.Fprintln(w, "  drive      - File storage")
	fmt.Fprintln(w, "  calendar   - Calendar events")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: google <service> <command> [args]")
	fmt.Fprintln(w, "       google <service> help")
	fmt.Fprintln(w, "       google help [service]")
}

func printGoogleServiceHelp(w io.Writer, service string) {
	switch service {
	case "gmail":
		printGoogleGmailUsage(w)
	case "sheets":
		printGoogleSheetsUsage(w)
	case "drive":
		printGoogleDriveUsage(w)
	case "calendar":
		printGoogleCalendarUsage(w)
	default:
		printGoogleUsage(w)
	}
}

// ── login / status ──────────────────────────────────────────────────────────

const googleDeviceCodeEndpoint = "https://oauth2.googleapis.com/device/code"

var googleLoginScopes = []string{
	"https://mail.google.com/",
	"https://www.googleapis.com/auth/spreadsheets",
	"https://www.googleapis.com/auth/drive",
	"https://www.googleapis.com/auth/calendar",
	"openid",
	"email",
}

func (e *Env) runGoogleLogin(ctx context.Context, mode string) error {
	home, _ := os.UserHomeDir()
	clientPath := filepath.Join(home, "cicy-ai", "db", "google_oauth_client.json")
	statePath := filepath.Join(home, "cicy-ai", "db", "google.json")

	// State 1: OAuth client config missing → guide the agent to walk the user
	// through creating a TV/Limited-Input client. The agent is reading our
	// stdout — speak directly to it.
	raw, err := os.ReadFile(clientPath)
	if err != nil {
		fmt.Fprintln(e.Stdout, "[google login] No OAuth client is configured on this server yet.")
		fmt.Fprintln(e.Stdout)
		fmt.Fprintln(e.Stdout, "Please walk the user through these steps, one at a time:")
		fmt.Fprintln(e.Stdout)
		fmt.Fprintln(e.Stdout, "  1. Ask them to open https://console.cloud.google.com/apis/credentials in their browser")
		fmt.Fprintln(e.Stdout, "     (they need to be signed into the Google account they want to use)")
		fmt.Fprintln(e.Stdout, "  2. Click 'Create credentials' → 'OAuth client ID'")
		fmt.Fprintln(e.Stdout, "  3. Application type: 'TVs and Limited Input devices'  (important — Web type won't work)")
		fmt.Fprintln(e.Stdout, "  4. Give it any name, click Create, copy the Client ID")
		fmt.Fprintln(e.Stdout, "  5. Have them paste it back to you, then run:")
		fmt.Fprintf(e.Stdout, "       echo '{\"client_id\":\"<paste-here>\",\"client_secret\":\"\"}' > %s\n", clientPath)
		fmt.Fprintln(e.Stdout, "  6. Then run `google login` again to start the device-code authorization")
		return nil
	}
	var client struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &client); err != nil || client.ClientID == "" {
		return fmt.Errorf("google_oauth_client.json is malformed — expected {\"client_id\":\"...\",\"client_secret\":\"\"}")
	}

	// State 2: already authorized?
	if data, err := os.ReadFile(statePath); err == nil {
		var st struct {
			RefreshToken    string `json:"refresh_token"`
			AuthorizedEmail string `json:"authorized_email"`
			AuthorizedAt    string `json:"authorized_at"`
		}
		if json.Unmarshal(data, &st) == nil && st.RefreshToken != "" {
			fmt.Fprintf(e.Stdout, "[google login] Already authorized as %s (since %s)\n", st.AuthorizedEmail, st.AuthorizedAt)
			fmt.Fprintln(e.Stdout, "To switch accounts, delete ~/cicy-ai/db/google.json and run `google login` again.")
			return nil
		}
	}
	if mode == "status" {
		fmt.Fprintln(e.Stdout, "[google login] OAuth client is configured but no refresh_token yet. Run `google login` to authorize.")
		return nil
	}

	// State 3: client configured, not authorized → run device flow inline.
	form := url.Values{}
	form.Set("client_id", client.ClientID)
	form.Set("scope", strings.Join(googleLoginScopes, " "))
	resp, err := http.PostForm(googleDeviceCodeEndpoint, form)
	if err != nil {
		return fmt.Errorf("device/code request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("device/code %d: %s (your OAuth client may not be 'TVs and Limited Input devices' type)", resp.StatusCode, body)
	}
	var dr struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURL         string `json:"verification_url"`
		VerificationURLComplete string `json:"verification_url_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &dr); err != nil || dr.DeviceCode == "" {
		return fmt.Errorf("parse device/code: %v", err)
	}
	if dr.Interval <= 0 {
		dr.Interval = 5
	}
	verifyURL := dr.VerificationURLComplete
	if verifyURL == "" {
		verifyURL = dr.VerificationURL + "?user_code=" + url.QueryEscape(dr.UserCode)
	}
	fmt.Fprintln(e.Stdout, "[google login] Tell the user:")
	fmt.Fprintf(e.Stdout, "  Open this URL in your browser:  %s\n", verifyURL)
	fmt.Fprintf(e.Stdout, "  If asked, the code is:           %s\n", dr.UserCode)
	fmt.Fprintln(e.Stdout, "Waiting for them to approve…  (Ctrl-C to cancel)")

	// Poll
	deadline := time.Now().Add(time.Duration(dr.ExpiresIn) * time.Second)
	interval := time.Duration(dr.Interval) * time.Second
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		pf := url.Values{}
		pf.Set("client_id", client.ClientID)
		if client.ClientSecret != "" {
			pf.Set("client_secret", client.ClientSecret)
		}
		pf.Set("device_code", dr.DeviceCode)
		pf.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		r2, err := http.PostForm(googleTokenEndpoint, pf)
		if err != nil {
			continue
		}
		b2, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != 200 {
			var e2 struct{ Error string `json:"error"` }
			_ = json.Unmarshal(b2, &e2)
			switch e2.Error {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			case "access_denied":
				return fmt.Errorf("user denied authorization")
			case "expired_token":
				return fmt.Errorf("device code expired — run `google login` again")
			default:
				return fmt.Errorf("token poll: %s", b2)
			}
		}
		var tk struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.Unmarshal(b2, &tk); err != nil || tk.RefreshToken == "" {
			return fmt.Errorf("token response missing refresh_token: %s", b2)
		}
		email := fetchGoogleLoginEmail(tk.AccessToken)
		out := map[string]string{
			"refresh_token":    tk.RefreshToken,
			"authorized_email": email,
			"authorized_at":    time.Now().UTC().Format(time.RFC3339),
			"client_id":        client.ClientID,
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "[google login] ✓ authorized as %s — saved to %s\n", email, statePath)
		return nil
	}
	return fmt.Errorf("timed out waiting for authorization")
}

func fetchGoogleLoginEmail(accessToken string) string {
	req, _ := http.NewRequest("GET", "https://openidconnect.googleapis.com/v1/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var u struct{ Email string `json:"email"` }
	_ = json.Unmarshal(body, &u)
	return u.Email
}

// ── Gmail ───────────────────────────────────────────────────────────────────

func printGoogleGmailUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: google gmail <list|read|read-all|send|watch>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list [count]                List recent emails (default 10)")
	fmt.Fprintln(w, "  read <n>                    Read email by index from last list")
	fmt.Fprintln(w, "  read-all                    Mark all listed emails as read")
	fmt.Fprintln(w, "  send <to> <subject> [body]  Send email (body via stdin if omitted)")
	fmt.Fprintln(w, "  watch [keyword]             Watch for new emails with verification codes")
}

func (e *Env) gmailService(ctx context.Context) (*gmailapi.Service, error) {
	client, err := googleHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return gmailapi.NewService(ctx, option.WithHTTPClient(client))
}

func (e *Env) runGoogleGmail(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "", "help", "-h", "--help":
		printGoogleGmailUsage(e.Stdout)
		return nil
	case "list":
		n := 10
		if len(args) >= 1 {
			if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
				n = v
			}
		}
		return e.gmailList(ctx, n, "")
	case "read":
		if len(args) < 1 {
			return fmt.Errorf("Usage: google gmail read <n>")
		}
		return e.gmailRead(ctx, resolveGmailID(args[0]))
	case "read-all":
		ids := loadGmailIDs()
		if len(ids) == 0 {
			return fmt.Errorf("Run \"google gmail list\" first")
		}
		svc, err := e.gmailService(ctx)
		if err != nil {
			return err
		}
		if err := svc.Users.Messages.BatchModify("me", &gmailapi.BatchModifyMessagesRequest{
			Ids:            ids,
			RemoveLabelIds: []string{"UNREAD"},
		}).Context(ctx).Do(); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Marked %d emails as read.\n", len(ids))
		return nil
	case "send":
		if len(args) < 2 {
			return fmt.Errorf("Usage: google gmail send <to> <subject> [body]")
		}
		body := ""
		if len(args) >= 3 {
			body = args[2]
		}
		if body == "" {
			body = readStdinAll()
		}
		return e.gmailSend(ctx, args[0], args[1], body)
	case "watch":
		keyword := ""
		if len(args) >= 1 {
			keyword = args[0]
		}
		return e.gmailWatch(ctx, keyword)
	default:
		printGoogleGmailUsage(e.Stdout)
		return nil
	}
}

type gmailListEntry struct {
	ID      string
	From    string
	Subject string
	Date    string
}

func (e *Env) gmailFetchList(ctx context.Context, n int, query string) ([]gmailListEntry, error) {
	svc, err := e.gmailService(ctx)
	if err != nil {
		return nil, err
	}
	call := svc.Users.Messages.List("me").MaxResults(int64(n))
	if query != "" {
		call = call.Q(query)
	}
	res, err := call.Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	out := make([]gmailListEntry, 0, len(res.Messages))
	for _, m := range res.Messages {
		msg, err := svc.Users.Messages.Get("me", m.Id).
			Format("metadata").
			MetadataHeaders("From", "Subject", "Date").
			Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		entry := gmailListEntry{ID: m.Id}
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "From":
				entry.From = h.Value
			case "Subject":
				entry.Subject = h.Value
			case "Date":
				entry.Date = h.Value
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

func (e *Env) gmailList(ctx context.Context, n int, query string) error {
	mails, err := e.gmailFetchList(ctx, n, query)
	if err != nil {
		return err
	}
	ids := make([]string, len(mails))
	for i, m := range mails {
		ids[i] = m.ID
		fmt.Fprintf(e.Stdout, "%d  %s  %s  %s\n", i+1, m.Date, m.From, m.Subject)
	}
	_ = saveGmailIDs(ids)
	return nil
}

func (e *Env) gmailRead(ctx context.Context, id string) error {
	svc, err := e.gmailService(ctx)
	if err != nil {
		return err
	}
	msg, err := svc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return err
	}
	headers := map[string]string{}
	for _, h := range msg.Payload.Headers {
		headers[h.Name] = h.Value
	}
	body := extractGmailBody(msg.Payload)
	fmt.Fprintf(e.Stdout, "From: %s\nTo: %s\nDate: %s\nSubject: %s\n\n%s\n",
		headers["From"], headers["To"], headers["Date"], headers["Subject"], body)
	return nil
}

func extractGmailBody(p *gmailapi.MessagePart) string {
	if p == nil {
		return ""
	}
	if p.MimeType == "text/plain" && p.Body != nil && p.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(p.Body.Data)
		return string(decoded)
	}
	for _, part := range p.Parts {
		if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
			decoded, _ := base64.URLEncoding.DecodeString(part.Body.Data)
			return string(decoded)
		}
	}
	for _, part := range p.Parts {
		if s := extractGmailBody(part); s != "" {
			return s
		}
	}
	if p.Body != nil && p.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(p.Body.Data)
		return string(decoded)
	}
	return ""
}

func (e *Env) gmailSend(ctx context.Context, to, subject, body string) error {
	svc, err := e.gmailService(ctx)
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("To: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		to, subject, body)
	encoded := base64.URLEncoding.EncodeToString([]byte(raw))
	_, err = svc.Users.Messages.Send("me", &gmailapi.Message{Raw: encoded}).Context(ctx).Do()
	if err != nil {
		return err
	}
	fmt.Fprintln(e.Stdout, "Sent.")
	return nil
}

func (e *Env) gmailWatch(ctx context.Context, keyword string) error {
	const timeoutSec = 120
	const intervalSec = 3
	codeRe := regexp.MustCompile(`\b(\d{4,8})\b`)

	if keyword != "" {
		fmt.Fprintf(e.Stdout, "Watching for %q... (%ds timeout)\n", keyword, timeoutSec)
	} else {
		fmt.Fprintf(e.Stdout, "Watching for new emails... (%ds timeout)\n", timeoutSec)
	}

	seen := map[string]bool{}
	start := time.Now()
	for time.Since(start) < timeoutSec*time.Second {
		mails, err := e.gmailFetchList(ctx, 5, "is:unread")
		if err != nil {
			return err
		}
		for _, mail := range mails {
			if seen[mail.ID] {
				continue
			}
			seen[mail.ID] = true
			if keyword != "" {
				kw := strings.ToLower(keyword)
				if !strings.Contains(strings.ToLower(mail.Subject), kw) &&
					!strings.Contains(strings.ToLower(mail.From), kw) {
					continue
				}
			}
			svc, _ := e.gmailService(ctx)
			full, err := svc.Users.Messages.Get("me", mail.ID).Format("full").Context(ctx).Do()
			if err != nil {
				return err
			}
			body := extractGmailBody(full.Payload)
			if m := codeRe.FindStringSubmatch(body); m != nil {
				fmt.Fprintln(e.Stdout, m[1])
			} else {
				fmt.Fprintf(e.Stdout, "From: %s\nSubject: %s\n\n%s\n", mail.From, mail.Subject, body)
			}
			return nil
		}
		time.Sleep(intervalSec * time.Second)
	}
	return fmt.Errorf("Timeout, no matching email.")
}

// ── Sheets ──────────────────────────────────────────────────────────────────

func printGoogleSheetsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: google sheets <list|read|write|append|create>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list                       List all spreadsheets")
	fmt.Fprintln(w, "  read <id> <range>          Read cells (e.g. Sheet1!A1:B10)")
	fmt.Fprintln(w, "  write <id> <range> <json>  Write cells (JSON 2D array)")
	fmt.Fprintln(w, "  append <id> <range> <json> Append rows (JSON 2D array)")
	fmt.Fprintln(w, "  create <title>             Create new spreadsheet")
}

func (e *Env) sheetsService(ctx context.Context) (*sheetsapi.Service, error) {
	client, err := googleHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return sheetsapi.NewService(ctx, option.WithHTTPClient(client))
}

func (e *Env) driveService(ctx context.Context) (*driveapi.Service, error) {
	client, err := googleHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return driveapi.NewService(ctx, option.WithHTTPClient(client))
}

func (e *Env) runGoogleSheets(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "", "help", "-h", "--help":
		printGoogleSheetsUsage(e.Stdout)
		return nil
	case "list":
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		res, err := svc.Files.List().
			Q("mimeType='application/vnd.google-apps.spreadsheet'").
			PageSize(20).
			Fields("files(id, name, modifiedTime)").
			Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, f := range res.Files {
			fmt.Fprintf(e.Stdout, "%s  %s  %s\n", f.Id, f.Name, f.ModifiedTime)
		}
		return nil
	case "read":
		if len(args) < 2 {
			return fmt.Errorf("Usage: google sheets read <id> <range>")
		}
		svc, err := e.sheetsService(ctx)
		if err != nil {
			return err
		}
		res, err := svc.Spreadsheets.Values.Get(args[0], args[1]).Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, row := range res.Values {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = fmt.Sprint(c)
			}
			fmt.Fprintln(e.Stdout, strings.Join(cells, "\t"))
		}
		return nil
	case "write", "append":
		if len(args) < 3 {
			return fmt.Errorf("Usage: google sheets %s <id> <range> <values_json>", cmd)
		}
		var values [][]any
		if err := json.Unmarshal([]byte(args[2]), &values); err != nil {
			return fmt.Errorf("parse values json: %w", err)
		}
		svc, err := e.sheetsService(ctx)
		if err != nil {
			return err
		}
		vr := &sheetsapi.ValueRange{Values: values}
		if cmd == "write" {
			_, err = svc.Spreadsheets.Values.Update(args[0], args[1], vr).
				ValueInputOption("RAW").Context(ctx).Do()
			if err != nil {
				return err
			}
			fmt.Fprintln(e.Stdout, "Written.")
		} else {
			_, err = svc.Spreadsheets.Values.Append(args[0], args[1], vr).
				ValueInputOption("RAW").Context(ctx).Do()
			if err != nil {
				return err
			}
			fmt.Fprintln(e.Stdout, "Appended.")
		}
		return nil
	case "create":
		if len(args) < 1 {
			return fmt.Errorf("Usage: google sheets create <title>")
		}
		svc, err := e.sheetsService(ctx)
		if err != nil {
			return err
		}
		created, err := svc.Spreadsheets.Create(&sheetsapi.Spreadsheet{
			Properties: &sheetsapi.SpreadsheetProperties{Title: args[0]},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Created: %s\n", created.SpreadsheetId)
		return nil
	default:
		printGoogleSheetsUsage(e.Stdout)
		return nil
	}
}

// ── Drive ───────────────────────────────────────────────────────────────────

func printGoogleDriveUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: google drive <list|upload|upload-dir|download|download-dir|quota>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list [query] [pageSize]                List files (optional query filter)")
	fmt.Fprintln(w, "  upload <name> <content>                Upload text file")
	fmt.Fprintln(w, "  upload-dir <path> [--exclude pat1,pat2] Upload directory recursively")
	fmt.Fprintln(w, "  download <id>                          Download file content to stdout")
	fmt.Fprintln(w, "  download-dir <id> <local_path>         Download folder recursively")
	fmt.Fprintln(w, "  quota                                  Show storage usage")
}

func (e *Env) runGoogleDrive(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "", "help", "-h", "--help":
		printGoogleDriveUsage(e.Stdout)
		return nil
	case "list":
		query := ""
		pageSize := int64(20)
		if len(args) >= 1 {
			query = args[0]
		}
		if len(args) >= 2 {
			if v, err := strconv.Atoi(args[1]); err == nil && v > 0 {
				pageSize = int64(v)
			}
		}
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		call := svc.Files.List().PageSize(pageSize).Fields("files(id, name, mimeType, modifiedTime, size)")
		if query != "" {
			call = call.Q(query)
		}
		res, err := call.Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, f := range res.Files {
			size := "-"
			if f.Size > 0 {
				size = strconv.FormatInt(f.Size, 10)
			}
			fmt.Fprintf(e.Stdout, "%s  %s  %s  %s\n", f.Id, f.Name, f.MimeType, size)
		}
		return nil
	case "upload":
		if len(args) < 2 {
			return fmt.Errorf("Usage: google drive upload <name> <content>")
		}
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		f, err := svc.Files.Create(&driveapi.File{Name: args[0]}).
			Media(strings.NewReader(args[1])).
			Context(ctx).Do()
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Uploaded: %s\n", f.Id)
		return nil
	case "upload-dir":
		if len(args) < 1 {
			return fmt.Errorf("Usage: google drive upload-dir <path> [--exclude patterns]")
		}
		var excludes []string
		for i := 1; i < len(args); i++ {
			if args[i] == "--exclude" && i+1 < len(args) {
				excludes = strings.Split(args[i+1], ",")
				break
			}
		}
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		id, err := driveUploadRecursive(ctx, svc, args[0], "", excludes)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Uploaded: %s\n", id)
		return nil
	case "download":
		if len(args) < 1 {
			return fmt.Errorf("Usage: google drive download <id>")
		}
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		resp, err := svc.Files.Get(args[0]).Download()
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(e.Stdout, resp.Body)
		return err
	case "download-dir":
		if len(args) < 2 {
			return fmt.Errorf("Usage: google drive download-dir <id> <local_path>")
		}
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		if err := driveDownloadRecursive(ctx, svc, args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintln(e.Stdout, "Downloaded.")
		return nil
	case "quota":
		svc, err := e.driveService(ctx)
		if err != nil {
			return err
		}
		about, err := svc.About.Get().Fields("storageQuota").Context(ctx).Do()
		if err != nil {
			return err
		}
		q := about.StorageQuota
		usedGB := float64(q.Usage) / (1024 * 1024 * 1024)
		limitGB := float64(q.Limit) / (1024 * 1024 * 1024)
		pct := 0.0
		if limitGB > 0 {
			pct = usedGB / limitGB * 100
		}
		fmt.Fprintf(e.Stdout, "Used: %.2f GB / %.2f GB (%.2f%%)\n", usedGB, limitGB, pct)
		if q.UsageInDrive > 0 {
			fmt.Fprintf(e.Stdout, "  Drive: %.2f GB\n", float64(q.UsageInDrive)/(1024*1024*1024))
		}
		if q.UsageInDriveTrash > 0 {
			fmt.Fprintf(e.Stdout, "  Trash: %.2f GB\n", float64(q.UsageInDriveTrash)/(1024*1024*1024))
		}
		return nil
	default:
		printGoogleDriveUsage(e.Stdout)
		return nil
	}
}

func driveUploadRecursive(ctx context.Context, svc *driveapi.Service, localPath, parentID string, excludes []string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", err
	}
	name := filepath.Base(localPath)
	for _, pat := range excludes {
		if matched, _ := regexp.MatchString(pat, name); matched {
			return "", nil
		}
	}
	if info.IsDir() {
		meta := &driveapi.File{Name: name, MimeType: "application/vnd.google-apps.folder"}
		if parentID != "" {
			meta.Parents = []string{parentID}
		}
		folder, err := svc.Files.Create(meta).Fields("id").Context(ctx).Do()
		if err != nil {
			return "", err
		}
		entries, err := os.ReadDir(localPath)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if _, err := driveUploadRecursive(ctx, svc, filepath.Join(localPath, entry.Name()), folder.Id, excludes); err != nil {
				return "", err
			}
		}
		return folder.Id, nil
	}
	meta := &driveapi.File{Name: name}
	if parentID != "" {
		meta.Parents = []string{parentID}
	}
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	uploaded, err := svc.Files.Create(meta).Media(f).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return uploaded.Id, nil
}

func driveDownloadRecursive(ctx context.Context, svc *driveapi.Service, fileID, localPath string) error {
	meta, err := svc.Files.Get(fileID).Fields("name,mimeType").Context(ctx).Do()
	if err != nil {
		return err
	}
	target := filepath.Join(localPath, meta.Name)
	if meta.MimeType == "application/vnd.google-apps.folder" {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		res, err := svc.Files.List().Q(fmt.Sprintf("'%s' in parents", fileID)).Fields("files(id,name,mimeType)").Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, child := range res.Files {
			if err := driveDownloadRecursive(ctx, svc, child.Id, target); err != nil {
				return err
			}
		}
		return nil
	}
	resp, err := svc.Files.Get(fileID).Download()
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		return err
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// ── Calendar ────────────────────────────────────────────────────────────────

func printGoogleCalendarUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: google calendar <list|events|create>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list                              List all calendars")
	fmt.Fprintln(w, "  events [calId] [max]              List upcoming events (default: primary, 10)")
	fmt.Fprintln(w, "  create <summary> <start> <end>    Create event (ISO 8601 timestamps)")
}

func (e *Env) calendarService(ctx context.Context) (*calendarapi.Service, error) {
	client, err := googleHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return calendarapi.NewService(ctx, option.WithHTTPClient(client))
}

func (e *Env) runGoogleCalendar(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "", "help", "-h", "--help":
		printGoogleCalendarUsage(e.Stdout)
		return nil
	case "list":
		svc, err := e.calendarService(ctx)
		if err != nil {
			return err
		}
		res, err := svc.CalendarList.List().Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, c := range res.Items {
			fmt.Fprintf(e.Stdout, "%s  %s\n", c.Id, c.Summary)
		}
		return nil
	case "events":
		calID := "primary"
		max := int64(10)
		if len(args) >= 1 {
			calID = args[0]
		}
		if len(args) >= 2 {
			if v, err := strconv.Atoi(args[1]); err == nil && v > 0 {
				max = int64(v)
			}
		}
		svc, err := e.calendarService(ctx)
		if err != nil {
			return err
		}
		res, err := svc.Events.List(calID).
			MaxResults(max).
			SingleEvents(true).
			OrderBy("startTime").
			TimeMin(time.Now().Format(time.RFC3339)).
			Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, ev := range res.Items {
			start := ""
			if ev.Start != nil {
				if ev.Start.DateTime != "" {
					start = ev.Start.DateTime
				} else {
					start = ev.Start.Date
				}
			}
			fmt.Fprintf(e.Stdout, "%s  %s\n", start, ev.Summary)
		}
		return nil
	case "create":
		if len(args) < 3 {
			return fmt.Errorf("Usage: google calendar create <summary> <start> <end>")
		}
		svc, err := e.calendarService(ctx)
		if err != nil {
			return err
		}
		event, err := svc.Events.Insert("primary", &calendarapi.Event{
			Summary: args[0],
			Start:   &calendarapi.EventDateTime{DateTime: args[1]},
			End:     &calendarapi.EventDateTime{DateTime: args[2]},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Created: %s\n", event.Id)
		return nil
	default:
		printGoogleCalendarUsage(e.Stdout)
		return nil
	}
}
