package audit

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// smtpSender is the seam SmtpMailer needs from the transport; tests inject a
// stub so the unit suite never opens a socket.
type smtpSender interface {
	send(from string, to []string, msg []byte) error
}

// SmtpMailer delivers incident emails through a generic SMTP server (company
// relay, SES SMTP, Aliyun DirectMail, etc.). Credentials come from
// ~/cicy-ai/db/smtp.json. Works with any provider — no verified domain logic
// of its own; deliverability is the relay's concern.
type SmtpMailer struct {
	sender smtpSender
	from   string
}

// NewSmtpMailer builds a network-backed SmtpMailer. From falls back to the
// auth username when not set explicitly.
func NewSmtpMailer(creds *smtpCredentials) *SmtpMailer {
	from := strings.TrimSpace(creds.From)
	if from == "" {
		from = strings.TrimSpace(creds.Username)
	}
	return &SmtpMailer{
		sender: &smtpDialer{
			host:     creds.Host,
			port:     creds.Port,
			username: creds.Username,
			password: creds.Password,
			tls:      creds.TLS,
		},
		from: from,
	}
}

func (m *SmtpMailer) Send(msg EmailMessage) error {
	if m.from == "" {
		return fmt.Errorf("smtp: From required (set from/username in db/smtp.json)")
	}
	if len(msg.To) == 0 {
		return fmt.Errorf("smtp: empty recipient list")
	}
	if err := m.sender.send(m.from, msg.To, []byte(buildRFC822(m.from, msg))); err != nil {
		return err
	}
	log.Printf("[audit] smtp sent event=%s recipients=%d", msg.EventID, len(msg.To))
	return nil
}

// ── real SMTP transport ──

type smtpDialer struct {
	host     string
	port     int
	username string
	password string
	tls      string // "starttls" | "implicit" | "none"; "" → inferred from port
}

// mode resolves the TLS strategy: explicit field wins, else 465 ⇒ implicit,
// everything else ⇒ STARTTLS (smtp.SendMail upgrades when the server offers it).
func (d *smtpDialer) mode() string {
	if v := strings.ToLower(strings.TrimSpace(d.tls)); v != "" {
		return v
	}
	if d.port == 465 {
		return "implicit"
	}
	return "starttls"
}

func (d *smtpDialer) send(from string, to []string, msg []byte) error {
	addr := net.JoinHostPort(d.host, strconv.Itoa(d.port))
	var auth smtp.Auth
	if d.username != "" {
		auth = smtp.PlainAuth("", d.username, d.password, d.host)
	}
	if d.mode() == "implicit" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: d.host})
		if err != nil {
			return fmt.Errorf("smtp tls dial %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, d.host)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		return smtpWriteMessage(c, from, to, msg)
	}
	// STARTTLS (587) or plain (25): SendMail does EHLO → STARTTLS (if
	// advertised) → AUTH → DATA. PLAIN auth is refused on an unencrypted link,
	// so "none" must be authless.
	return smtp.SendMail(addr, auth, from, to, msg)
}

func smtpWriteMessage(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ── credentials ──

type smtpCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	TLS      string `json:"tls"` // optional: starttls | implicit | none
}

// loadSmtpCredentials reads ~/cicy-ai/db/smtp.json. Returns nil unless host,
// port and a usable sender (from or username) are present.
func loadSmtpCredentials() *smtpCredentials {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "smtp.json"))
	if err != nil {
		return nil
	}
	var c smtpCredentials
	if json.Unmarshal(data, &c) != nil {
		return nil
	}
	c.Host = strings.TrimSpace(c.Host)
	c.From = strings.TrimSpace(c.From)
	c.Username = strings.TrimSpace(c.Username)
	if c.Host == "" || c.Port == 0 {
		return nil
	}
	if c.From == "" && c.Username == "" {
		return nil
	}
	return &c
}
