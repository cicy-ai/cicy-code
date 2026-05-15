package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"time"

	"github.com/google/uuid"
)

// PreventiveDecision is the result of an inline-mode scan.
type PreventiveDecision struct {
	// Action is the action the preventive layer applied:
	//   ActionBlock  — the gateway / webhook should refuse to forward.
	//   ActionRedact — caller should swap the request body to
	//                  ModifiedPayload before forwarding.
	//   ActionNone   — preventive let it through.
	Action Action

	// Findings is the set of preventive-relevant hits (inline=true rules).
	Findings []Finding

	// Reason captures why a non-trivial decision was reached:
	//   "preventive_disabled"   — policy.preventive.enabled is false
	//   "allowlisted_<reason>"  — bypassed via policy.allow_list
	//   "no_inline_match"       — inline rules ran but none required action
	//   "block"                 — at least one block-default rule matched
	//   "redact"                — block didn't match but redact-default did
	//   "scanner_panic"         — recovered panic, follows fail_mode
	Reason string

	// EventID is the audit event id written for this preventive decision.
	// Empty for Action=none.
	EventID string

	// ModifiedPayload is the request body after substituting matched spans
	// with "[REDACTED:<rule_id>]". Only set when Action=ActionRedact;
	// callers MUST forward this (not the original) to the LLM provider.
	ModifiedPayload []byte

	// PreRedactRef points at the encrypted original payload archived under
	// ~/cicy-ai/workers/<agent>/.cicy/history/pre-redact/. Used by auditor
	// tooling to retrieve the unredacted prompt for forensic review.
	PreRedactRef string
}

// PreventiveCheck runs only the inline=true subset of the active rule set
// over the payload. If any matched rule's DefaultAction is ActionBlock, a
// preventive event is recorded (action=block, applied=true) and the caller
// receives PreventiveDecision{Action: block}. Callers MUST honor block by
// short-circuiting the upstream call (HTTP 451 etc.).
//
// Pass through when:
//   - global pipeline not initialized,
//   - policy.Preventive.Enabled == false,
//   - no inline rule matches, OR
//   - matched rules all default to non-block actions (log / notify /
//     redact — redact is a Phase-3 cut-2 feature).
//
// On scanner panic: returns Action=none if fail_mode=open (default),
// Action=block if fail_mode=closed (compliance-strict mode).
func PreventiveCheck(env Envelope) PreventiveDecision {
	if globalPipeline == nil {
		return PreventiveDecision{Action: ActionNone, Reason: "preventive_disabled"}
	}
	return globalPipeline.PreventiveCheck(env)
}

// PreventiveCheck is the instance method. Exposed for tests + parity.
func (p *Pipeline) PreventiveCheck(env Envelope) (dec PreventiveDecision) {
	pol := p.CurrentPolicy()
	if pol == nil || !pol.Preventive.Enabled {
		return PreventiveDecision{Action: ActionNone, Reason: "preventive_disabled"}
	}

	// allow_list bypass: explicitly trusted agents / paths / content hashes
	// MUST never be blocked. The downstream post-write detective hook will
	// still record an event with allowlisted_by stamped — same semantics as
	// for non-preventive flow.
	sum := sha256.Sum256(env.Payload)
	payloadSHA := "sha256:" + hex.EncodeToString(sum[:])
	if dec := pol.CheckAllowList(env.AgentID, env.PayloadRef, payloadSHA); dec.Suppressed {
		return PreventiveDecision{Action: ActionNone, Reason: "allowlisted_" + dec.Reason}
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[audit] preventive scanner panic agent=%s: %v", env.AgentID, r)
			if pol.Preventive.FailMode == "closed" {
				dec = PreventiveDecision{Action: ActionBlock, Reason: "scanner_panic"}
			} else {
				dec = PreventiveDecision{Action: ActionNone, Reason: "scanner_panic"}
			}
		}
	}()

	// Run the full scanner, then partition findings by their source rule's
	// inline action (block / redact / other). Block beats redact when both
	// would fire on the same turn.
	allFindings := p.scanner.Scan(env.Payload, env.Direction, pol)
	blockFindings, redactFindings := partitionInlineFindings(p, allFindings)

	if len(blockFindings) > 0 {
		eventID := p.submitPreventiveBlock(context.Background(), env, blockFindings, pol.Preventive.FailMode)
		return PreventiveDecision{
			Action:   ActionBlock,
			Findings: blockFindings,
			Reason:   "block",
			EventID:  eventID,
		}
	}
	if len(redactFindings) > 0 {
		// Pre-allocate event_id so the encrypted-archive file lands at its
		// final canonical name on the first write.
		preID := "evt_" + uuid.NewString()
		env.eventID = preID
		redacted := RedactPayload(env.Payload, redactFindings)
		ref, err := SavePreRedact(
			p.store.auditRoot, p.store.workersRoot,
			env.AgentID, preID, env.Payload,
		)
		if err != nil {
			log.Printf("[audit] pre-redact save failed agent=%s: %v", env.AgentID, err)
		}
		eventID := p.submitPreventiveRedact(context.Background(), env, redactFindings, pol.Preventive.FailMode, ref)
		return PreventiveDecision{
			Action:          ActionRedact,
			Findings:        redactFindings,
			Reason:          "redact",
			EventID:         eventID,
			ModifiedPayload: redacted,
			PreRedactRef:    ref,
		}
	}
	return PreventiveDecision{Action: ActionNone, Reason: "no_inline_match", Findings: allFindings}
}

// partitionInlineFindings groups findings into (block, redact) buckets based
// on their source rule's Inline + DefaultAction. Findings from non-inline
// rules are dropped (they belong to detective scanning, not preventive).
func partitionInlineFindings(p *Pipeline, findings []Finding) (block, redact []Finding) {
	if len(findings) == 0 {
		return nil, nil
	}
	bs, ok := p.scanner.(*BuiltinScanner)
	if !ok {
		return nil, nil
	}
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	index := make(map[string]BuiltinRule, len(bs.set.Rules))
	for _, r := range bs.set.Rules {
		index[r.ID] = r
	}

	for _, f := range findings {
		rule, ok := index[f.RuleID]
		if !ok || !rule.Inline {
			continue
		}
		switch rule.DefaultAction {
		case ActionBlock:
			block = append(block, f)
		case ActionRedact:
			redact = append(redact, f)
		}
	}
	return block, redact
}

// submitPreventiveBlock writes a single audit event representing the block
// decision and returns its event ID. Wraps the same buildEvent + store path
// the normal pipeline uses, so verify CLI walks the chain transparently.
func (p *Pipeline) submitPreventiveBlock(ctx context.Context, env Envelope, findings []Finding, failMode string) string {
	return p.submitPreventive(ctx, env, findings, ActionBlock, failMode, "")
}

// submitPreventiveRedact writes the redact-decision event. preRedactRef is
// stamped in meta when known at event-build time.
func (p *Pipeline) submitPreventiveRedact(ctx context.Context, env Envelope, findings []Finding, failMode, preRedactRef string) string {
	return p.submitPreventive(ctx, env, findings, ActionRedact, failMode, preRedactRef)
}

func (p *Pipeline) submitPreventive(_ context.Context, env Envelope, findings []Finding, action Action, failMode, preRedactRef string) string {
	if env.submitWallNs == 0 {
		env.submitWallNs = time.Now().UTC().UnixNano()
	}
	if env.submitMonoNs == 0 {
		env.submitMonoNs = time.Now().UnixNano()
	}
	pol := p.CurrentPolicy()
	e := p.buildEvent(env, pol)
	e.Findings = findings
	e.Decision.EvaluatedInline = true
	e.Decision.Action = action
	e.Decision.Applied = true
	if failMode == "closed" {
		e.Decision.FailMode = FailClosed
	} else {
		e.Decision.FailMode = FailOpen
	}
	if preRedactRef != "" {
		e.Meta.PreRedactRef = preRedactRef
	}
	if _, err := p.store.Append(e); err != nil {
		log.Printf("[audit] preventive append failed agent=%s id=%s: %v", env.AgentID, e.ID, err)
	}
	return e.ID
}
