package audit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/resend/resend-go/v3"
)

// stubResendSender captures the most recent Send call and returns a
// pre-configured response/error. No network IO.
type stubResendSender struct {
	gotParams *resend.SendEmailRequest
	resp      *resend.SendEmailResponse
	err       error
}

func (s *stubResendSender) Send(params *resend.SendEmailRequest) (*resend.SendEmailResponse, error) {
	s.gotParams = params
	return s.resp, s.err
}

func TestResendMailer_HappyPath(t *testing.T) {
	stub := &stubResendSender{resp: &resend.SendEmailResponse{Id: "msg_abc123"}}
	m := &ResendMailer{sender: stub, from: "audit@corp"}
	err := m.Send(EmailMessage{
		To:       []string{"alice@corp", "bob@corp"},
		Subject:  "[CICY-AUDIT][HIGH] x",
		Body:     "body",
		EventID:  "evt_test",
		AgentID:  "w-x",
		Severity: SeverityHigh,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if stub.gotParams == nil {
		t.Fatal("Send never called on sender")
	}
	if stub.gotParams.From != "audit@corp" {
		t.Errorf("From = %q want audit@corp", stub.gotParams.From)
	}
	if got := len(stub.gotParams.To); got != 2 {
		t.Errorf("To len = %d want 2", got)
	}
	if h := stub.gotParams.Headers["X-Cicy-Audit-Event"]; h != "evt_test" {
		t.Errorf("X-Cicy-Audit-Event header = %q want evt_test", h)
	}
	if h := stub.gotParams.Headers["X-Cicy-Audit-Severity"]; h != "high" {
		t.Errorf("severity header = %q want high", h)
	}
}

func TestResendMailer_PropagatesError(t *testing.T) {
	stub := &stubResendSender{err: errors.New("boom")}
	m := &ResendMailer{sender: stub, from: "audit@corp"}
	err := m.Send(EmailMessage{To: []string{"x@y"}, Subject: "s", EventID: "e"})
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected propagated error, got %v", err)
	}
}

func TestResendMailer_RejectsEmptyFrom(t *testing.T) {
	m := &ResendMailer{sender: &stubResendSender{}, from: ""}
	err := m.Send(EmailMessage{To: []string{"x@y"}, Subject: "s"})
	if err == nil {
		t.Error("expected error on empty From")
	}
}

func TestResendMailer_RejectsEmptyTo(t *testing.T) {
	m := &ResendMailer{sender: &stubResendSender{}, from: "audit@corp"}
	err := m.Send(EmailMessage{Subject: "s"})
	if err == nil {
		t.Error("expected error on empty To")
	}
}

func TestLoadResendCredentials_EnvFirst(t *testing.T) {
	t.Setenv("CICY_RESEND_API_KEY", "re_envkey")
	t.Setenv("CICY_RESEND_FROM", "audit@corp")
	t.Setenv("CICY_RESEND_REPLY_TO", "noreply@corp")

	creds, src := loadResendCredentials()
	if creds == nil {
		t.Fatal("expected creds from env, got nil")
	}
	if src != "env" {
		t.Errorf("source = %q want env", src)
	}
	if creds.APIKey != "re_envkey" || creds.From != "audit@corp" || creds.ReplyTo != "noreply@corp" {
		t.Errorf("env creds wrong: %+v", creds)
	}
}

func TestLoadResendCredentials_FileFallback(t *testing.T) {
	// Clear env so file is the only source.
	t.Setenv("CICY_RESEND_API_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "cicy-ai", "db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "email.json"),
		[]byte(`{"api_key":"re_filekey","from":"file@corp"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, src := loadResendCredentials()
	if creds == nil {
		t.Fatal("expected creds from file, got nil")
	}
	if src != "file" {
		t.Errorf("source = %q want file", src)
	}
	if creds.APIKey != "re_filekey" || creds.From != "file@corp" {
		t.Errorf("file creds wrong: %+v", creds)
	}
}

func TestLoadResendCredentials_NoneConfigured(t *testing.T) {
	t.Setenv("CICY_RESEND_API_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	creds, src := loadResendCredentials()
	if creds != nil || src != "" {
		t.Errorf("expected (nil,\"\"), got (%+v, %q)", creds, src)
	}
}
