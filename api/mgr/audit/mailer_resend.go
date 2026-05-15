package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/resend/resend-go/v3"
)

// resendSender is the minimum surface ResendMailer requires from the Resend
// SDK client. Real usage wires resend.Client.Emails (which is *EmailsSvcImpl);
// tests inject a stub so the unit suite never reaches the network.
type resendSender interface {
	Send(params *resend.SendEmailRequest) (*resend.SendEmailResponse, error)
}

// ResendMailer delivers incident emails through the Resend transactional
// API. API key + From are captured at construction time and never
// re-fetched. Failures are returned to the caller; the pipeline layer
// already wraps the call in a best-effort goroutine.
type ResendMailer struct {
	sender  resendSender
	from    string
	replyTo string
}

// NewResendMailer constructs a ResendMailer. apiKey MUST be a valid
// "re_..." string; the constructor does not validate beyond non-empty.
func NewResendMailer(apiKey, from, replyTo string) *ResendMailer {
	client := resend.NewClient(apiKey)
	return &ResendMailer{
		sender:  client.Emails,
		from:    from,
		replyTo: replyTo,
	}
}

func (m *ResendMailer) Send(msg EmailMessage) error {
	if m.from == "" {
		return fmt.Errorf("resend: From address required")
	}
	if len(msg.To) == 0 {
		return fmt.Errorf("resend: empty recipient list")
	}
	params := &resend.SendEmailRequest{
		From:    m.from,
		To:      msg.To,
		Subject: msg.Subject,
		Text:    msg.Body,
		Headers: map[string]string{
			"X-Cicy-Audit-Event":    msg.EventID,
			"X-Cicy-Audit-Agent":    msg.AgentID,
			"X-Cicy-Audit-Severity": string(msg.Severity),
		},
	}
	if m.replyTo != "" {
		params.ReplyTo = m.replyTo
	}
	resp, err := m.sender.Send(params)
	if err != nil {
		return err
	}
	id := ""
	if resp != nil {
		id = resp.Id
	}
	log.Printf("[audit] resend message_id=%s event=%s recipients=%d", id, msg.EventID, len(msg.To))
	return nil
}

// resendCredentials is the contract for the on-disk credential file at
// ~/cicy-ai/db/email.json (shared with the host `email` skill).
type resendCredentials struct {
	APIKey  string `json:"api_key"`
	From    string `json:"from"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// resolveEmailFrom returns the From address for ResendMailer, picking the
// first non-empty source in this priority order:
//
//  1. policy.incident_response.email_from
//  2. CICY_RESEND_FROM env (already captured in creds.From when set)
//  3. ~/cicy-ai/db/email.json "from" field (creds.From when src=file)
//
// Returns "" when none resolved; caller MUST fall back to FileMailer.
func resolveEmailFrom(policy *Policy, creds *resendCredentials) string {
	if policy != nil {
		if v := strings.TrimSpace(policy.IncidentResponse.EmailFrom); v != "" {
			return v
		}
	}
	if creds != nil {
		if v := strings.TrimSpace(creds.From); v != "" {
			return v
		}
	}
	return ""
}

// loadResendCredentials returns (creds, source) when configured, where
// source is the human-readable origin ("env" or "file"). Returns
// (nil, "") when nothing is configured — caller should fall back to
// FileMailer.
//
// Order:
//  1. CICY_RESEND_API_KEY / CICY_RESEND_FROM / CICY_RESEND_REPLY_TO env
//  2. ~/cicy-ai/db/email.json (same path the host `email` skill uses)
func loadResendCredentials() (*resendCredentials, string) {
	if key := strings.TrimSpace(os.Getenv("CICY_RESEND_API_KEY")); key != "" {
		return &resendCredentials{
			APIKey:  key,
			From:    strings.TrimSpace(os.Getenv("CICY_RESEND_FROM")),
			ReplyTo: strings.TrimSpace(os.Getenv("CICY_RESEND_REPLY_TO")),
		}, "env"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ""
	}
	path := filepath.Join(home, "cicy-ai", "db", "email.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	var c resendCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		log.Printf("[audit] parse %s failed: %v", path, err)
		return nil, ""
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, ""
	}
	return &c, "file"
}
