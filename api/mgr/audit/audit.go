package audit

import (
	"context"
	"encoding/json"
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
	// humans ("smtp"/"gmail") or only spools to disk ("file"). Read by the
	// readiness check so the 审核策略专员 can warn when owner alerts reach no one.
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

		// First run: no policy.json → seed it with the builtins materialized as
		// editable rules (RulesManaged). From here on policy.json is the single
		// source of truth — every rule (incl. former built-ins) is editable and
		// deletable via this config.
		if _, statErr := os.Stat(policyPath); os.IsNotExist(statErr) {
			seed := DefaultPolicyWithBuiltins()
			if b, mErr := json.MarshalIndent(seed, "", "  "); mErr == nil {
				if mkErr := os.MkdirAll(filepath.Dir(policyPath), 0o700); mkErr == nil {
					if wErr := os.WriteFile(policyPath, b, 0o600); wErr == nil {
						log.Printf("[audit] seeded policy.json with %d rules (config is the single source)", len(seed.CustomRules))
					}
				}
			}
		}

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
		// Production: only persist alert/finding events, not clean pass-through
		// traffic (which is 99% of volume and has nothing to audit).
		p.dropCleanEvents = true
		globalPipeline = p
		if werr := p.WatchPolicyFile(policyPath); werr != nil {
			log.Printf("[audit] policy watcher disabled (no hot reload): %v", werr)
		}
		if werr := p.WatchEmailCredentials(); werr != nil {
			log.Printf("[audit] email-credential watcher disabled: %v", werr)
		}

		// Mailer selection — SMTP ONLY ("只用 smtp"). SMTP config comes from the
		// settings UI's db/email.json (single source); Gmail OAuth path retired.
		// FileMailer spools to disk when SMTP isn't configured.
		mailerName := "FileMailer"
		mailerDetail := filepath.Join(auditRoot, "email-out")
		if scfg := loadSmtpCredentials(); scfg != nil {
			p.SetMailer(NewSmtpMailer(scfg))
			mailerName = "SmtpMailer"
			mailerDetail = "smtp " + scfg.Host + " (db/email.json)"
			responseMailerKind = "smtp"
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

// SubmitGatewayTurn submits the per-turn audit unit assembled at turn
// completion (ai_gateway_audit.go completeFromResponse): the operator's
// outbound question plus the assistant reply's tool calls, serialized by the
// caller into one scannable payload. This is the audit-v2 trigger — it
// supersedes current.json-based triggering, scanning exactly {q + tool_calls}
// rather than the full rolling request body. The caller MUST already have
// filtered auxiliary/internal calls (X-Cicy-Aux). No-op on empty payload or
// uninitialized pipeline.
func SubmitGatewayTurn(agentID, provider, model, turnID, conversationID string, payload []byte) {
	if globalPipeline == nil || len(payload) == 0 {
		return
	}
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
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

// SubmitBehavior records one turn's TOOL CALLS as a DirectionBehavior event,
// scanned by the behaviour scanner (dangerous commands / file access) rather
// than the secret regexes. calls is the normalised tool-call list (provider +
// name + arguments). SourceChannel reflects which path produced it (gateway vs
// mitm). No-op on empty input / uninitialized pipeline.
func SubmitBehavior(agentID, sourceChannel, provider, model, turnID, conversationID string, calls []ToolCall) {
	if globalPipeline == nil || len(calls) == 0 {
		return
	}
	payload, err := json.Marshal(calls)
	if err != nil || len(payload) == 0 {
		return
	}
	if sourceChannel == "" {
		sourceChannel = SourceGateway
	}
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
		SourceChannel:  sourceChannel,
		TurnID:         turnID,
		ConversationID: conversationID,
		Provider:       provider,
		Model:          model,
		Direction:      DirectionBehavior,
		Payload:        payload,
		PayloadRef:     fmt.Sprintf("behavior#%s", turnID),
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
