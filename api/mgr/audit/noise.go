// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// noiseTracker is the in-memory state for noise-governance evaluation.
// Holds (1) a sliding window of recent notify timestamps per (agent, rule)
// and (2) a cool-down map keyed by finding-identity hash.
//
// Process-local: restart resets governance state. That is intentional for v2 —
// after a restart, the audit is in fresh-eyes mode and the operator should
// not be drowned in resumed alerts.
type noiseTracker struct {
	mu       sync.Mutex
	hits     map[string][]int64 // key = "<agent>|<rule>", values = ns timestamps
	cooldown map[string]int64   // key = finding hash, value = last-notify ns
}

func newNoiseTracker() *noiseTracker {
	return &noiseTracker{
		hits:     map[string][]int64{},
		cooldown: map[string]int64{},
	}
}

// EvaluateNotify decides whether a notification should be emitted for one
// (agent, rule, finding) triple at time nowNs. Returns the suppression
// reason ("" if not suppressed). When NOT suppressed, the call ALSO
// records the hit and updates the cooldown — callers must invoke this
// exactly once per audit event that intends to notify.
//
// Order of checks: suspended > cooldown > rate_limit.
func (n *noiseTracker) EvaluateNotify(
	agentID, ruleID, findingHash string,
	nowNs int64,
	cfg NotifyConfig,
	severity Severity,
) string {
	// Severity-aware suppression. Noise controls (cooldown / rate limit) exist
	// to stop a chatty rule from spamming — they must NEVER silence a real
	// high-impact leak. A critical finding pierces everything (incl. an
	// explicit Suspend); a high finding pierces the automatic controls but
	// still respects a deliberate operator Suspend.
	critical := severity == SeverityCritical
	high := critical || severity == SeverityHigh

	if cfg.Suspended && !critical {
		return "suspended"
	}
	if n == nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	// 1. Cooldown — silences EVERY repeat of the same finding for the whole
	// window. This is the dangerous silencer for a real leak, so high and
	// critical findings pierce it (you always hear about a serious match).
	if !high && findingHash != "" && cfg.Cooldown.Seconds > 0 {
		cdNs := int64(cfg.Cooldown.Seconds) * int64(time.Second)
		if last, ok := n.cooldown[findingHash]; ok && nowNs-last < cdNs {
			return "cooldown"
		}
	}

	// 2. Rate limit — per-(agent, rule) flood control. Kept for high (a noisy
	// serious rule can still flood); only a critical finding pierces it.
	if !critical && cfg.RateLimit.WindowSeconds > 0 && cfg.RateLimit.MaxPerAgentPerRule > 0 {
		windowNs := int64(cfg.RateLimit.WindowSeconds) * int64(time.Second)
		cutoff := nowNs - windowNs
		key := agentID + "|" + ruleID

		hits := n.hits[key]
		fresh := hits[:0]
		for _, t := range hits {
			if t > cutoff {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) >= cfg.RateLimit.MaxPerAgentPerRule {
			n.hits[key] = fresh
			return "rate_limit"
		}
		n.hits[key] = append(fresh, nowNs)
	}

	// 3. Not suppressed — record the cooldown anchor for future calls.
	if findingHash != "" && cfg.Cooldown.Seconds > 0 {
		n.cooldown[findingHash] = nowNs
	}
	return ""
}

// gc trims expired entries. Optional maintenance; cheap to skip given map
// sizes are bounded by (active agents × distinct rules) and per-finding
// uniqueness.
func (n *noiseTracker) gc(nowNs int64, cfg NotifyConfig) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if cfg.RateLimit.WindowSeconds > 0 {
		windowNs := int64(cfg.RateLimit.WindowSeconds) * int64(time.Second)
		cutoff := nowNs - windowNs
		for k, hits := range n.hits {
			fresh := hits[:0]
			for _, t := range hits {
				if t > cutoff {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) == 0 {
				delete(n.hits, k)
			} else {
				n.hits[k] = fresh
			}
		}
	}
	if cfg.Cooldown.Seconds > 0 {
		cdNs := int64(cfg.Cooldown.Seconds) * int64(time.Second)
		cutoff := nowNs - cdNs
		for k, t := range n.cooldown {
			if t < cutoff {
				delete(n.cooldown, k)
			}
		}
	}
}

// EventFindingHash returns the cooldown-identity hash for an event's
// highest-severity finding. The key is intentionally tight: same agent +
// same rule + same first-span preview means "the same value leaked again".
// Different agents leaking the same value are tracked separately so each
// agent's auditor sees the alert at least once.
//
// Returns "" when the event has no findings.
func EventFindingHash(e Event) string {
	if len(e.Findings) == 0 {
		return ""
	}
	top := topSeverity(e.Findings)
	for _, f := range e.Findings {
		if f.Severity != top {
			continue
		}
		h := sha256.New()
		h.Write([]byte(e.Identity.AgentID))
		h.Write([]byte("|"))
		h.Write([]byte(f.RuleID))
		if len(f.Spans) > 0 {
			h.Write([]byte("|"))
			h.Write([]byte(f.Spans[0].Preview))
		}
		return "sha256:" + hex.EncodeToString(h.Sum(nil))
	}
	return ""
}

// topFindingRule returns the rule_id of the highest-severity finding in the
// list (used as the rate-limit key alongside agent_id).
func topFindingRule(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	top := topSeverity(findings)
	for _, f := range findings {
		if f.Severity == top {
			return f.RuleID
		}
	}
	return findings[0].RuleID
}
