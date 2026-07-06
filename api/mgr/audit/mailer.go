// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EmailMessage is the transport-agnostic envelope the incident dispatcher
// hands to a Mailer. cut 1 carries only the minimum fields; cut 2/3 will
// add From / Reply-To / X-Cicy-Event headers etc.
type EmailMessage struct {
	To       []string
	Subject  string
	Body     string // plain-text alternative (always set)
	HTMLBody string // optional product HTML; when set, sent as multipart/alternative
	EventID  string
	AgentID  string
	Severity Severity
}

// Mailer is the transport interface. FileMailer (cut 1) writes .eml files
// to disk for an out-of-process relay to pick up; SmtpMailer (cut 2) will
// send directly via TLS SMTP with DKIM.
type Mailer interface {
	Send(msg EmailMessage) error
}

// FileMailer renders an .eml-like file under OutputDir. The file is named
// <event_id>.eml. Recipients live in the To: header so the relay knows
// where to fan out. Sufficient for walking skeleton; production should
// switch to SmtpMailer or pipe the files through `email send`.
type FileMailer struct {
	OutputDir string
}

func (m *FileMailer) Send(msg EmailMessage) error {
	if err := os.MkdirAll(m.OutputDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(m.OutputDir, msg.EventID+".eml")
	content := renderEMLFile(msg)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// renderEMLFile produces a minimal but RFC822-shaped message body. The
// out-of-process relay can either parse this or forward verbatim.
func renderEMLFile(msg EmailMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&b, "X-Cicy-Audit-Event: %s\r\n", msg.EventID)
	fmt.Fprintf(&b, "X-Cicy-Audit-Agent: %s\r\n", msg.AgentID)
	fmt.Fprintf(&b, "X-Cicy-Audit-Severity: %s\r\n", msg.Severity)
	fmt.Fprintf(&b, "X-Cicy-Audit-Generated-At: %s\r\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "\r\n")
	b.WriteString(msg.Body)
	if !strings.HasSuffix(msg.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
