package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RulesVersion is stamped on every event so future replay knows which
// rule set was in effect. Bump on each ruleset release.
const RulesVersion = "2026.05.15-skeleton"

// Pipeline runs the six-stage audit processing for one process:
//
//  1. Identity bind
//  2. Timestamp (wall + monotonic)
//  3. Scan
//  4. Decide
//  5. Append + Hash (per-agent + global index)
//  6. Notify (skeleton: log only)
//
// Submit is fire-and-forget by default; callers do not block on audit.
type Pipeline struct {
	store     *Store
	scanner   Scanner
	policy    *Policy
	machineID string

	wg sync.WaitGroup
}

func NewPipeline(auditRoot, workersRoot string, scanner Scanner, policy *Policy) (*Pipeline, error) {
	store, err := NewStore(auditRoot, workersRoot)
	if err != nil {
		return nil, err
	}
	mid, err := MachineID(auditRoot)
	if err != nil {
		return nil, err
	}
	return &Pipeline{
		store:     store,
		scanner:   scanner,
		policy:    policy,
		machineID: mid,
	}, nil
}

// Submit ingests an envelope. Inline submits block until the event is
// persisted; otherwise it runs on a goroutine.
//
// Wall and monotonic timestamps are captured here, not in process(), so that
// the recorded event time reflects when the caller produced the data
// (forensic accuracy) rather than when the async pipeline got scheduled.
func (p *Pipeline) Submit(ctx context.Context, env Envelope) {
	_ = ctx
	env.submitWallNs = time.Now().UTC().UnixNano()
	env.submitMonoNs = time.Now().UnixNano()
	if env.Inline {
		p.process(env)
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.process(env)
	}()
}

// Wait blocks until all in-flight async submits finish.
func (p *Pipeline) Wait() { p.wg.Wait() }

func (p *Pipeline) process(env Envelope) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[audit] pipeline panic agent=%s: %v", env.AgentID, r)
		}
	}()

	startedAt := time.Now()
	e := p.buildEvent(env)

	scanStart := time.Now()
	findings := p.scanner.Scan(env.Payload, env.Direction, p.policy)
	e.Findings = findings
	e.Meta.ScannerDurationMs = elapsedMs(scanStart)
	if env.Inline {
		e.Decision.EvaluatedInline = true
	} else {
		e.Decision.EvaluatedAsync = true
	}

	e.Decision.Action = decideAction(findings)
	e.Decision.Applied = true
	e.Decision.FailMode = FailOpen

	if _, err := p.store.Append(e); err != nil {
		log.Printf("[audit] store.Append failed agent=%s id=%s: %v", env.AgentID, e.ID, err)
		return
	}

	if len(findings) > 0 {
		log.Printf("[audit] event=%s agent=%s findings=%d action=%s scan_ms=%d total_ms=%d",
			e.ID, env.AgentID, len(findings), e.Decision.Action,
			e.Meta.ScannerDurationMs, elapsedMs(startedAt))
	}
}

func (p *Pipeline) buildEvent(env Envelope) Event {
	wallNs := env.submitWallNs
	if wallNs == 0 {
		wallNs = time.Now().UTC().UnixNano()
	}
	monoNs := env.submitMonoNs
	if monoNs == 0 {
		monoNs = time.Now().UnixNano()
	}
	sum := sha256.Sum256(env.Payload)

	return Event{
		ID:            "evt_" + uuid.NewString(),
		SchemaVersion: SchemaVersion,
		RulesVersion:  RulesVersion,
		Timestamp:     time.Unix(0, wallNs).UTC().Format(time.RFC3339Nano),
		TsMonotonic:   monoNs,

		Identity: Identity{
			MachineID:     p.machineID,
			AgentID:       env.AgentID,
			AgentType:     env.AgentType,
			UserID:        env.UserID,
			SessionID:     env.SessionID,
			SourceChannel: env.SourceChannel,
		},
		Subject: Subject{
			TurnID:         env.TurnID,
			ConversationID: env.ConversationID,
			Provider:       env.Provider,
			Model:          env.Model,
			Direction:      env.Direction,
			PayloadSize:    len(env.Payload),
			PayloadRef:     env.PayloadRef,
			PayloadSHA256:  "sha256:" + hex.EncodeToString(sum[:]),
		},
		Findings: []Finding{},
		Decision: Decision{
			Action:   ActionNone,
			FailMode: FailOpen,
		},
		Meta: Meta{
			PolicyHash: p.policy.Hash,
		},
	}
}

// decideAction is the Phase 1 (detective-only) decision: actions degrade to
// log/notify regardless of rule.DefaultAction. Preventive actions (redact,
// block) are introduced in Phase 3 (§9 of the design doc).
//
//	no findings              -> none
//	highest severity = low   -> log
//	highest severity = med+  -> notify (also implies log)
func decideAction(findings []Finding) Action {
	if len(findings) == 0 {
		return ActionNone
	}
	top := topSeverity(findings)
	switch top {
	case SeverityCritical, SeverityHigh, SeverityMedium:
		return ActionNotify
	}
	return ActionLog
}

// topSeverity returns the most severe level present across the findings.
func topSeverity(findings []Finding) Severity {
	order := map[Severity]int{
		SeverityCritical: 4,
		SeverityHigh:     3,
		SeverityMedium:   2,
		SeverityLow:      1,
	}
	var best Severity = SeverityLow
	bestRank := 0
	for _, f := range findings {
		if r, ok := order[f.Severity]; ok && r > bestRank {
			bestRank = r
			best = f.Severity
		}
	}
	return best
}
