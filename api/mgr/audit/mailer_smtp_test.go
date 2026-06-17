package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubSmtpSender struct {
	gotFrom string
	gotTo   []string
	gotMsg  []byte
	err     error
}

func (s *stubSmtpSender) send(from string, to []string, msg []byte) error {
	s.gotFrom, s.gotTo, s.gotMsg = from, to, msg
	return s.err
}

func TestSmtpMailer_HappyPath(t *testing.T) {
	stub := &stubSmtpSender{}
	m := &SmtpMailer{sender: stub, from: "alerts@corp.com"}
	err := m.Send(EmailMessage{
		To:       []string{"officer@corp.com"},
		Subject:  "[CICY-AUDIT][HIGH] secret.aws_akid — w-x",
		Body:     "正文 body",
		EventID:  "evt_s1",
		AgentID:  "w-x",
		Severity: SeverityHigh,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if stub.gotFrom != "alerts@corp.com" {
		t.Errorf("envelope from = %q", stub.gotFrom)
	}
	if len(stub.gotTo) != 1 || stub.gotTo[0] != "officer@corp.com" {
		t.Errorf("rcpt = %v", stub.gotTo)
	}
	rfc := string(stub.gotMsg)
	for _, want := range []string{
		"From: alerts@corp.com",
		"To: officer@corp.com",
		"X-Cicy-Audit-Event: evt_s1",
		"X-Cicy-Audit-Severity: high",
		"正文 body",
	} {
		if !strings.Contains(rfc, want) {
			t.Errorf("message missing %q in:\n%s", want, rfc)
		}
	}
	if !strings.Contains(rfc, "Subject: =?utf-8?") {
		t.Errorf("subject not RFC2047-encoded:\n%s", rfc)
	}
}

func TestSmtpMailer_EmptyTo(t *testing.T) {
	m := &SmtpMailer{sender: &stubSmtpSender{}, from: "a@b"}
	if err := m.Send(EmailMessage{Subject: "s", EventID: "e"}); err == nil {
		t.Error("expected error on empty To")
	}
}

func TestSmtpMailer_NoFrom(t *testing.T) {
	m := &SmtpMailer{sender: &stubSmtpSender{}, from: ""}
	if err := m.Send(EmailMessage{To: []string{"x@y"}, EventID: "e"}); err == nil {
		t.Error("expected error on empty From")
	}
}

func TestSmtpMailer_SendError(t *testing.T) {
	m := &SmtpMailer{sender: &stubSmtpSender{err: errGmailStub}, from: "a@b"}
	if err := m.Send(EmailMessage{To: []string{"x@y"}, EventID: "e"}); err == nil {
		t.Error("expected propagated send error")
	}
}

func TestLoadSmtpCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	db := filepath.Join(home, "cicy-ai", "db")
	if err := os.MkdirAll(db, 0o700); err != nil {
		t.Fatal(err)
	}
	if loadSmtpCredentials() != nil {
		t.Error("expected nil when email.json absent")
	}
	// Credentials now live under ~/cicy-ai/db/email.json in a nested "smtp" object.
	os.WriteFile(filepath.Join(db, "email.json"),
		[]byte(`{"smtp":{"host":"smtp.corp.com","port":587,"user":"u@corp.com","pass":"pw","from":"alerts@corp.com"}}`), 0o600)
	c := loadSmtpCredentials()
	if c == nil {
		t.Fatal("expected creds")
	}
	if c.Host != "smtp.corp.com" || c.Port != 587 || c.From != "alerts@corp.com" {
		t.Errorf("parsed wrong: %+v", c)
	}
	// host missing → nil (incomplete config)
	os.WriteFile(filepath.Join(db, "email.json"), []byte(`{"smtp":{"port":587,"from":"x@y","pass":"pw"}}`), 0o600)
	if loadSmtpCredentials() != nil {
		t.Error("expected nil when host missing")
	}
}

func TestSmtpDialer_ModeInference(t *testing.T) {
	if (&smtpDialer{port: 465}).mode() != "implicit" {
		t.Error("465 should infer implicit TLS")
	}
	if (&smtpDialer{port: 587}).mode() != "starttls" {
		t.Error("587 should infer starttls")
	}
	if (&smtpDialer{port: 25, tls: "none"}).mode() != "none" {
		t.Error("explicit tls=none should win")
	}
}
