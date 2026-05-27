package audit

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errGmailStub = errors.New("stub failure")

type stubGmailAPI struct {
	from     string
	fromErr  error
	gotRaw   string
	sendErr  error
	returnID string
}

func (s *stubGmailAPI) senderAddress() (string, error) { return s.from, s.fromErr }
func (s *stubGmailAPI) sendRaw(raw string) (string, error) {
	s.gotRaw = raw
	return s.returnID, s.sendErr
}

func TestGmailMailer_HappyPath(t *testing.T) {
	stub := &stubGmailAPI{from: "secops@cicy-ai.com", returnID: "msg_123"}
	m := &GmailMailer{api: stub}
	err := m.Send(EmailMessage{
		To:       []string{"officer@corp"},
		Subject:  "[CICY-AUDIT][HIGH] secret.private_key — w-x",
		Body:     "正文 body",
		EventID:  "evt_g1",
		AgentID:  "w-x",
		Severity: SeverityHigh,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if stub.gotRaw == "" {
		t.Fatal("sendRaw never called")
	}
	dec, err := base64.URLEncoding.DecodeString(stub.gotRaw)
	if err != nil {
		t.Fatalf("raw not valid base64url: %v", err)
	}
	rfc := string(dec)
	for _, want := range []string{
		"From: secops@cicy-ai.com",
		"To: officer@corp",
		"X-Cicy-Audit-Event: evt_g1",
		"X-Cicy-Audit-Severity: high",
		"正文 body",
	} {
		if !strings.Contains(rfc, want) {
			t.Errorf("RFC822 missing %q in:\n%s", want, rfc)
		}
	}
	// Subject carries non-ASCII (em dash) → must be RFC2047-encoded, not raw.
	if !strings.Contains(rfc, "Subject: =?utf-8?") {
		t.Errorf("subject not RFC2047-encoded:\n%s", rfc)
	}
}

func TestGmailMailer_EmptyTo(t *testing.T) {
	m := &GmailMailer{api: &stubGmailAPI{from: "x@y"}}
	if err := m.Send(EmailMessage{Subject: "s", EventID: "e"}); err == nil {
		t.Error("expected error on empty To")
	}
}

func TestGmailMailer_SenderError(t *testing.T) {
	m := &GmailMailer{api: &stubGmailAPI{fromErr: errGmailStub}}
	if err := m.Send(EmailMessage{To: []string{"x@y"}, EventID: "e"}); err == nil {
		t.Error("expected error when sender resolution fails")
	}
}

func TestGmailMailer_SendError(t *testing.T) {
	m := &GmailMailer{api: &stubGmailAPI{from: "x@y", sendErr: errGmailStub}}
	if err := m.Send(EmailMessage{To: []string{"a@b"}, EventID: "e"}); err == nil {
		t.Error("expected propagated send error")
	}
}

// credential resolution prefers the canonical db/ location over legacy global.json.
func TestLoadGmailCredentials_PrefersDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CICY_GOOGLE_OAUTH_CLIENT_ID", "")
	t.Setenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET", "")
	db := filepath.Join(home, "cicy-ai", "db")
	if err := os.MkdirAll(db, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(db, "google.json"),
		[]byte(`{"refresh_token":"rt_db","authorized_email":"sec@corp.com","client_id":"cid_db"}`), 0o600)
	os.WriteFile(filepath.Join(db, "google_oauth_client.json"),
		[]byte(`{"web":{"client_id":"cid_db","client_secret":"secret_db"}}`), 0o600)
	// legacy global.json also present — must be ignored in favour of db/.
	os.WriteFile(filepath.Join(home, "cicy-ai", "global.json"),
		[]byte(`{"GMAIL_WEB_CLIENT_ID":"cid_g","GMAIL_WEB_CLIENT_SECRET":"sec_g","GMAIL_REFRESH_TOKEN":"rt_g"}`), 0o600)

	c := loadGmailCredentials()
	if c == nil {
		t.Fatal("expected creds from db/")
	}
	if c.RefreshToken != "rt_db" || c.ClientID != "cid_db" || c.ClientSecret != "secret_db" {
		t.Errorf("db creds wrong: %+v", c)
	}
	if c.From != "sec@corp.com" {
		t.Errorf("From should be authorized_email, got %q", c.From)
	}
}

func TestLoadGmailCredentials_LegacyFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CICY_GOOGLE_OAUTH_CLIENT_ID", "")
	t.Setenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET", "")
	if err := os.MkdirAll(filepath.Join(home, "cicy-ai"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "cicy-ai", "global.json"),
		[]byte(`{"GMAIL_WEB_CLIENT_ID":"cid_g","GMAIL_WEB_CLIENT_SECRET":"sec_g","GMAIL_REFRESH_TOKEN":"rt_g"}`), 0o600)

	c := loadGmailCredentials()
	if c == nil || c.RefreshToken != "rt_g" || c.ClientID != "cid_g" || c.ClientSecret != "sec_g" {
		t.Fatalf("legacy fallback failed: %+v", c)
	}
	if c.From != "" {
		t.Errorf("legacy From should be empty, got %q", c.From)
	}
}

func TestLoadGmailCredentials_None(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if loadGmailCredentials() != nil {
		t.Error("expected nil when nothing configured")
	}
}
