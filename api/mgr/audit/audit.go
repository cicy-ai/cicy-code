package audit

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Process-global pipeline. Init is called once at server startup;
// subsequent Submit calls are fire-and-forget no-ops if Init failed.
var (
	globalOnce     sync.Once
	globalPipeline *Pipeline
	globalErr      error

	// responseMailerKind records whether the active mailer can actually reach
	// humans ("resend") or only spools to disk ("file"). Read by the readiness
	// check so w-6001 can warn when owner notifications won't reach anyone.
	responseMailerKind = "file"
)

// Init wires up the audit pipeline using the standard cicy-code paths:
//
//	~/cicy-ai/audit/      — policy.json, machine_id, chain state, global index
//	~/cicy-ai/workers/    — per-agent audit.ndjson live alongside history
//
// Both directories are auto-created with mode 0700. Idempotent.
// Loads policy.json if present, then starts an fsnotify watcher so edits
// take effect within ~200ms without a restart.
func Init() error {
	globalOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			globalErr = err
			return
		}
		auditRoot := filepath.Join(home, "cicy-ai", "audit")
		workersRoot := filepath.Join(home, "cicy-ai", "workers")
		policyPath := filepath.Join(auditRoot, "policy.json")

		policy, err := LoadPolicy(policyPath)
		if err != nil {
			log.Printf("[audit] policy.json parse failed, falling back to default: %v", err)
			policy = DefaultPolicy()
		}
		scanner := NewBuiltinScanner()
		p, err := NewPipeline(auditRoot, workersRoot, scanner, policy)
		if err != nil {
			globalErr = err
			return
		}
		globalPipeline = p
		if werr := p.WatchPolicyFile(policyPath); werr != nil {
			log.Printf("[audit] policy watcher disabled (no hot reload): %v", werr)
		}
		if werr := p.WatchEmailCredentials(); werr != nil {
			log.Printf("[audit] email-credential watcher disabled: %v", werr)
		}

		// Phase 6 cut 2a: prefer ResendMailer when credentials and a From
		// address are both available. Falls back to FileMailer otherwise.
		mailerName := "FileMailer"
		mailerDetail := filepath.Join(auditRoot, "email-out")
		creds, credsSrc := loadResendCredentials()
		from := resolveEmailFrom(policy, creds)
		gcreds := loadGmailCredentials()
		scfg := loadSmtpCredentials()
		switch {
		case creds != nil && from != "":
			// Resend wins when fully configured (verified domain → best deliverability).
			p.SetMailer(NewResendMailer(creds.APIKey, from, creds.ReplyTo))
			mailerName = "ResendMailer"
			mailerDetail = "from=" + from + " src=" + credsSrc
			responseMailerKind = "resend"
		case gcreds != nil:
			// Gmail OAuth: no verified domain needed — sends as the authenticated account.
			p.SetMailer(NewGmailMailer(gcreds))
			mailerName = "GmailMailer"
			mailerDetail = "oauth (db/google.json)"
			responseMailerKind = "gmail"
		case scfg != nil:
			// Generic SMTP relay (company server / SES SMTP / Aliyun DirectMail / …).
			p.SetMailer(NewSmtpMailer(scfg))
			mailerName = "SmtpMailer"
			mailerDetail = "smtp " + scfg.Host + " (db/smtp.json)"
			responseMailerKind = "smtp"
		case creds != nil && from == "":
			log.Printf("[audit] resend credentials present but no From address (set policy.incident_response.email_from or CICY_RESEND_FROM) — using FileMailer")
		}

		log.Printf("[audit] initialized root=%s rules_version=%s active_rules=%d custom=%d policy_hash=%s mailer=%s (%s)",
			auditRoot, RulesVersion, p.activeRuleCount(), len(policy.CustomRules), policy.Hash, mailerName, mailerDetail)
	})
	return globalErr
}

// Submit hands an envelope to the global pipeline. Becomes a no-op if Init
// has not succeeded — audit failure must never break the caller path.
func Submit(ctx context.Context, env Envelope) {
	if globalPipeline == nil {
		return
	}
	globalPipeline.Submit(ctx, env)
}

// Wait blocks for all in-flight async submits. Use during shutdown / tests.
func Wait() {
	if globalPipeline != nil {
		globalPipeline.Wait()
	}
}

// CurrentEffectivePolicy is the package-level entry HTTP handlers use to
// expose "what does the audit treat events from this agent as". Returns nil
// when the pipeline is not initialized.
func CurrentEffectivePolicy(agentID string) *Policy {
	if globalPipeline == nil {
		return nil
	}
	return globalPipeline.EffectivePolicyFor(agentID)
}

// SubmitGatewayOutbound is a convenience wrapper for the cicy AI gateway
// outbound (request) write callback in ai_gateway_audit.go.
func SubmitGatewayOutbound(agentID, agentType, userID, sessionID, turnID, conversationID, provider, model string, payload []byte) {
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
		AgentType:      agentType,
		UserID:         userID,
		SessionID:      sessionID,
		SourceChannel:  SourceGateway,
		TurnID:         turnID,
		ConversationID: conversationID,
		Provider:       provider,
		Model:          model,
		Direction:      DirectionOutbound,
		Payload:        payload,
		PayloadRef:     fmt.Sprintf("current.json#%s", turnID),
	})
}

// SubmitGatewayInbound is the inbound (reply) counterpart.
func SubmitGatewayInbound(agentID, agentType, userID, sessionID, turnID, conversationID, provider, model string, payload []byte) {
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
		AgentType:      agentType,
		UserID:         userID,
		SessionID:      sessionID,
		SourceChannel:  SourceGateway,
		TurnID:         turnID,
		ConversationID: conversationID,
		Provider:       provider,
		Model:          model,
		Direction:      DirectionInbound,
		Payload:        payload,
		PayloadRef:     fmt.Sprintf("reply.json#%s", turnID),
	})
}

// SubmitMitmEvent is the entry point for mitmproxy-captured events arriving
// via the /api/audit/ingest webhook. Callers can fill any envelope fields they
// know; SourceChannel is forced to "mitm" regardless of what they passed.
//
// Unlike the gateway helpers, this always runs through the async path: mitm
// callers should not block on audit.
func SubmitMitmEvent(env Envelope) {
	env.SourceChannel = SourceMitm
	env.Inline = false
	Submit(context.Background(), env)
}
