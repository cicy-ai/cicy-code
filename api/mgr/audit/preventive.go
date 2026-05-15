package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"time"
)

// PreventiveDecision is the result of an inline-mode scan.
type PreventiveDecision struct {
	// Action is the action the preventive layer applied. In Phase 3 cut 1
	// this is one of:
	//   ActionBlock — the gateway / webhook should refuse to forward.
	//   ActionNone  — preventive let it through (either no findings, or
	//                 preventive is disabled, or no rule's DefaultAction
	//                 is block).
	Action Action

	// Findings is the set of preventive-relevant hits (inline=true rules).
	// Empty when Action=none and no inline rules matched.
	Findings []Finding

	// Reason captures why a non-trivial decision was reached:
	//   "preventive_disabled" — policy.preventive.enabled is false
	//   "no_inline_match"     — inline rules ran but none required action
	//   "block"               — at least one finding mapped to ActionBlock
	//   "scanner_panic"       — recovered panic, follows fail_mode
	Reason string

	// EventID is the audit event id written for this preventive decision.
	// Empty for Action=none (the post-write detective hook handles it).
	EventID string
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

	// Run the full scanner, then keep only findings whose underlying rule
	// is marked inline=true AND whose default_action is block. The scanner
	// already implements direction gating and policy overrides.
	allFindings := p.scanner.Scan(env.Payload, env.Direction, pol)
	inlineFindings := filterInlineBlockFindings(p, allFindings)
	if len(inlineFindings) == 0 {
		return PreventiveDecision{Action: ActionNone, Reason: "no_inline_match", Findings: allFindings}
	}

	// Block path: synthesize an audit event with action=block applied=true
	// and submit synchronously (Inline=true). The post-write detective hook
	// in the gateway code path will NOT fire because the gateway short-
	// circuits before writing the snapshot.
	blockEnv := env
	blockEnv.Inline = true
	eventID := p.submitPreventiveBlock(context.Background(), blockEnv, inlineFindings, pol.Preventive.FailMode)
	return PreventiveDecision{
		Action:   ActionBlock,
		Findings: inlineFindings,
		Reason:   "block",
		EventID:  eventID,
	}
}

// filterInlineBlockFindings keeps the findings whose source rule has both
// inline=true and DefaultAction=block. Custom rules respect this too.
func filterInlineBlockFindings(p *Pipeline, findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	// Pull the current rule set from the scanner if it's a BuiltinScanner.
	bs, ok := p.scanner.(*BuiltinScanner)
	if !ok {
		return nil
	}
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	index := make(map[string]BuiltinRule, len(bs.set.Rules))
	for _, r := range bs.set.Rules {
		index[r.ID] = r
	}

	out := findings[:0]
	for _, f := range findings {
		rule, ok := index[f.RuleID]
		if !ok {
			continue
		}
		if !rule.Inline || rule.DefaultAction != ActionBlock {
			continue
		}
		out = append(out, f)
	}
	// Clone the slice header so we don't surprise the caller by mutating
	// their findings backing array.
	cp := make([]Finding, len(out))
	copy(cp, out)
	return cp
}

// submitPreventiveBlock writes a single audit event representing the block
// decision and returns its event ID. Wraps the same buildEvent + store path
// the normal pipeline uses, so verify CLI walks the chain transparently.
func (p *Pipeline) submitPreventiveBlock(ctx context.Context, env Envelope, findings []Finding, failMode string) string {
	_ = ctx
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
	e.Decision.Action = ActionBlock
	e.Decision.Applied = true
	if failMode == "closed" {
		e.Decision.FailMode = FailClosed
	} else {
		e.Decision.FailMode = FailOpen
	}
	if _, err := p.store.Append(e); err != nil {
		log.Printf("[audit] preventive append failed agent=%s id=%s: %v", env.AgentID, e.ID, err)
	}
	return e.ID
}
