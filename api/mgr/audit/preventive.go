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
	// Action is the action the preventive layer applied:
	//   ActionBlock  — the gateway / webhook should refuse to forward.
	//   ActionNone   — preventive let it through.
	// (redact removed 2026-06-16: 审计绝不改写用户数据,只 log 或 block。)
	Action Action

	// Findings is the set of preventive-relevant hits (inline=true rules).
	Findings []Finding

	// Reason captures why a non-trivial decision was reached:
	//   "preventive_disabled"   — policy.preventive.enabled is false
	//   "allowlisted_<reason>"  — bypassed via policy.allow_list
	//   "no_inline_match"       — inline rules ran but none required action
	//   "block"                 — at least one block-default rule matched
	//   "scanner_panic"         — recovered panic, follows fail_mode
	Reason string

	// EventID is the audit event id written for this preventive decision.
	// Empty for Action=none.
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
	if pol == nil {
		return PreventiveDecision{Action: ActionNone, Reason: "preventive_disabled"}
	}
	// The per-rule action is the control now (the legacy global preventive.enabled
	// toggle / 实时拦截 switch was removed). Preventive runs whenever the active
	// rule set contains at least one block/redact rule; otherwise short-circuit
	// so non-blocking deployments pay nothing on the hot path.
	if !p.hasInterceptRule() {
		return PreventiveDecision{Action: ActionNone, Reason: "no_intercept_rule"}
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

	// Scan ONLY the inline rules — this path is synchronous and blocks the
	// request before it reaches the model, so it must not pay for the
	// detective-only rules (high_entropy etc.). Then partition by block/redact;
	// block beats redact when both fire on the same turn.
	allFindings := p.scanner.ScanInline(env.Payload, env.Direction, pol)
	blockFindings := partitionInlineFindings(p, allFindings)

	if len(blockFindings) > 0 {
		eventID := p.submitPreventiveBlock(context.Background(), env, blockFindings, pol.Preventive.FailMode)
		return PreventiveDecision{
			Action:   ActionBlock,
			Findings: blockFindings,
			Reason:   "block",
			EventID:  eventID,
		}
	}
	return PreventiveDecision{Action: ActionNone, Reason: "no_inline_match", Findings: allFindings}
}

// hasInterceptRule reports whether the active rule set contains at least one
// rule whose action is block — i.e. whether preventive interception is
// configured at all. Cheap O(rules) scan over the in-memory set.
func (p *Pipeline) hasInterceptRule() bool {
	bs, ok := p.scanner.(*BuiltinScanner)
	if !ok {
		return false
	}
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	for _, r := range bs.set.Rules {
		if r.DefaultAction == ActionBlock {
			return true
		}
	}
	return false
}

// partitionInlineFindings returns the findings whose source rule's action is
// block. Findings from non-inline / non-block rules are dropped (they belong to
// detective scanning, not preventive). redact was removed 2026-06-16 — the
// preventive layer never rewrites a payload, only blocks or passes.
func partitionInlineFindings(p *Pipeline, findings []Finding) (block []Finding) {
	if len(findings) == 0 {
		return nil
	}
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

	for _, f := range findings {
		rule, ok := index[f.RuleID]
		if !ok {
			continue
		}
		if rule.DefaultAction == ActionBlock {
			block = append(block, f)
		}
	}
	return block
}

// submitPreventiveBlock writes a single audit event representing the block
// decision and returns its event ID. Wraps the same buildEvent + store path
// the normal pipeline uses, so verify CLI walks the chain transparently.
func (p *Pipeline) submitPreventiveBlock(ctx context.Context, env Envelope, findings []Finding, failMode string) string {
	return p.submitPreventive(ctx, env, findings, ActionBlock, failMode, "")
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
	// Forensic q/reply snapshot: records conversation_id + history_id + the
	// request question + the response (here: the blocked/redacted note), so the
	// audit log can answer "当前请求/response 是什么". q/reply come from the
	// REDACTED payload — secrets are never re-exposed.
	if ref := p.saveQRSnapshot(e, env, findings); ref != "" {
		e.Meta.SnapshotRef = ref
	}
	persisted, err := p.store.Append(e)
	if err != nil {
		log.Printf("[audit] preventive append failed agent=%s id=%s: %v", env.AgentID, e.ID, err)
		return e.ID
	}
	// Preventive events ARE the headline forensic record for blocked/
	// redacted turns — dispatch incident emails on these too.
	go p.dispatchIncident(persisted)
	return persisted.ID
}
