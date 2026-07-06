// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ackKeyName is the basename of the local HMAC key file. Separate from the
// pre-redact key so leaking the ack key cannot forge encrypted-archive
// decryptions (and vice versa).
const ackKeyName = ".ack.key"

// AckTokenDefaultTTL is the validity window for a freshly-signed ack URL.
// Per design §17.3 the link is "30 day signed token".
const AckTokenDefaultTTL = 30 * 24 * time.Hour

var (
	ackKeyOnce sync.Once
	ackKey     []byte
	ackKeyErr  error
)

// loadOrCreateAckKey returns the host-local HMAC key, generating 32
// random bytes on first call. Process-cached.
func loadOrCreateAckKey(auditRoot string) ([]byte, error) {
	ackKeyOnce.Do(func() {
		path := filepath.Join(auditRoot, ackKeyName)
		if data, err := os.ReadFile(path); err == nil && len(data) == 32 {
			ackKey = data
			return
		}
		if err := os.MkdirAll(auditRoot, 0o700); err != nil {
			ackKeyErr = err
			return
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			ackKeyErr = err
			return
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			ackKeyErr = err
			return
		}
		ackKey = key
	})
	return ackKey, ackKeyErr
}

// ackPayload is the inner JSON payload of an ack token. Field names are
// short to keep the token URL-readable.
type ackPayload struct {
	EID string `json:"eid"`           // event id of the original incident
	Exp int64  `json:"exp"`           // unix seconds, expiration
	IAT int64  `json:"iat,omitempty"` // issued-at (debug aid)
}

// SignAckToken returns a base64url(payload) "." hex(hmac) token valid for
// ttl. Tokens are URL-safe and ~120 chars long for a typical ULID event id.
func SignAckToken(auditRoot, eventID string, ttl time.Duration) (string, error) {
	if eventID == "" {
		return "", fmt.Errorf("audit: empty event_id")
	}
	if ttl <= 0 {
		ttl = AckTokenDefaultTTL
	}
	key, err := loadOrCreateAckKey(auditRoot)
	if err != nil {
		return "", err
	}
	now := time.Now()
	payload := ackPayload{
		EID: eventID,
		Exp: now.Add(ttl).Unix(),
		IAT: now.Unix(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(b64))
	sig := hex.EncodeToString(mac.Sum(nil))
	return b64 + "." + sig, nil
}

// VerifyAckToken parses, validates and returns the event id when the token
// is well-formed AND the HMAC matches AND it has not expired. Constant-
// time compare prevents timing oracles.
func VerifyAckToken(auditRoot, token string) (string, error) {
	key, err := loadOrCreateAckKey(auditRoot)
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("audit: malformed ack token")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0]))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(want)) {
		return "", fmt.Errorf("audit: ack signature mismatch")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("audit: ack payload decode: %w", err)
	}
	var p ackPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return "", fmt.Errorf("audit: ack payload parse: %w", err)
	}
	if p.EID == "" {
		return "", fmt.Errorf("audit: ack payload missing eid")
	}
	if p.Exp > 0 && time.Now().Unix() > p.Exp {
		return "", fmt.Errorf("audit: ack token expired")
	}
	return p.EID, nil
}

// RecordAck appends a meta_alert_ack event to the audit chain for the
// agent that owned the original event. Caller is the pipeline; agentID
// is resolved from the original event when known, else empty (which is
// fine — the meta event chains globally regardless).
//
// userAgent / requestIP are captured opportunistically from the HTTP
// handler so forensics can see WHO clicked the link.
func RecordAck(eventID, agentID, userAgent, requestIP string) (string, error) {
	if globalPipeline == nil {
		return "", fmt.Errorf("audit: pipeline not initialized")
	}
	if eventID == "" {
		return "", fmt.Errorf("audit: empty event_id")
	}
	return globalPipeline.recordAck(eventID, agentID, userAgent, requestIP)
}

func (p *Pipeline) recordAck(eventID, agentID, userAgent, requestIP string) (string, error) {
	now := time.Now()
	env := Envelope{
		AgentID:       agentID,
		SourceChannel: "audit_system",
		Direction:     DirectionOutbound, // semantically: the audit system emits an ack
		Payload:       []byte(userAgent + "|" + requestIP),
		PayloadRef:    "ack:" + eventID,
		Inline:        true,
		submitWallNs:  now.UTC().UnixNano(),
		submitMonoNs:  now.UnixNano(),
	}
	pol := p.CurrentPolicy()
	e := p.buildEvent(env, pol)
	e.Findings = []Finding{}
	e.Decision.Action = ActionLog
	e.Decision.Applied = true
	e.Decision.EvaluatedInline = true
	e.Decision.FailMode = FailOpen
	e.Meta.Category = "meta_alert_ack"
	e.Meta.AckEventID = eventID
	if agentID == "" {
		// Fall back to a synthetic agent so per-agent NDJSON pathing works.
		e.Identity.AgentID = "meta-audit"
	}
	persisted, err := p.store.Append(e)
	if err != nil {
		return "", err
	}
	return persisted.ID, nil
}
