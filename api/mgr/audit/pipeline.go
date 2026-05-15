package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
)

// RulesVersion is stamped on every event so future replay knows which
// rule set was in effect. Bump on each ruleset release.
const RulesVersion = "2026.05.15-skeleton"

// Pipeline runs the six-stage audit processing for one process:
//
//  1. Identity bind
//  2. Timestamp (wall + monotonic)
//  3. Scan + AllowList suppression
//  4. Decide
//  5. Append + Hash (per-agent + global index)
//  6. Notify (Phase 3 — currently just logs)
//
// Submit is fire-and-forget by default; callers do not block on audit.
type Pipeline struct {
	store     *Store
	scanner   Scanner
	machineID string

	policyPath string
	mu         sync.RWMutex
	policy     *Policy

	watcher    *fsnotify.Watcher
	debounceMu sync.Mutex
	debounce   *time.Timer

	wg sync.WaitGroup
}

// NewPipeline assembles the audit pipeline. The scanner is any Scanner; if it
// is a *BuiltinScanner, ApplyPolicy will also rebuild its ruleset on reload.
// A nil initial policy uses DefaultPolicy().
func NewPipeline(auditRoot, workersRoot string, scanner Scanner, policy *Policy) (*Pipeline, error) {
	store, err := NewStore(auditRoot, workersRoot)
	if err != nil {
		return nil, err
	}
	mid, err := MachineID(auditRoot)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		policy = DefaultPolicy()
	}
	if scanner == nil {
		scanner = NewBuiltinScanner()
	}
	if bs, ok := scanner.(*BuiltinScanner); ok {
		if err := bs.SetPolicy(policy); err != nil {
			return nil, err
		}
	}
	return &Pipeline{
		store:     store,
		scanner:   scanner,
		policy:    policy,
		machineID: mid,
	}, nil
}

// CurrentPolicy returns a snapshot pointer to the active policy. Callers MUST
// treat the result as read-only.
func (p *Pipeline) CurrentPolicy() *Policy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.policy
}

// ApplyPolicy validates the new policy, rebuilds the scanner rule set, then
// swaps both into place atomically. Returns an error WITHOUT swapping if any
// step fails — the previously-active policy keeps serving (forensic safety).
func (p *Pipeline) ApplyPolicy(pol *Policy) error {
	if pol == nil {
		pol = DefaultPolicy()
	}
	if bs, ok := p.scanner.(*BuiltinScanner); ok {
		if err := bs.SetPolicy(pol); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.policy = pol
	p.mu.Unlock()
	return nil
}

// activeRuleCount returns the scanner's current rule count when known,
// else -1 (NoopScanner / custom scanners do not expose this).
func (p *Pipeline) activeRuleCount() int {
	if bs, ok := p.scanner.(*BuiltinScanner); ok {
		return bs.RuleCount()
	}
	return -1
}

// WatchPolicyFile starts an fsnotify watcher on the parent directory of
// policyPath and reloads on every WRITE/CREATE/RENAME of the base name.
// Debounces to 200ms (atomic writes fire CREATE+WRITE in quick succession).
// Safe to call once per Pipeline; subsequent calls are no-ops.
func (p *Pipeline) WatchPolicyFile(policyPath string) error {
	if p.watcher != nil {
		return nil
	}
	p.policyPath = policyPath
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(filepath.Dir(policyPath)); err != nil {
		w.Close()
		return err
	}
	p.watcher = w
	go p.watchLoop()
	return nil
}

func (p *Pipeline) watchLoop() {
	base := filepath.Base(p.policyPath)
	for {
		select {
		case ev, ok := <-p.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			p.scheduleReload()
		case err, ok := <-p.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[audit] policy watcher error: %v", err)
		}
	}
}

func (p *Pipeline) scheduleReload() {
	p.debounceMu.Lock()
	defer p.debounceMu.Unlock()
	if p.debounce != nil {
		p.debounce.Stop()
	}
	p.debounce = time.AfterFunc(200*time.Millisecond, p.reload)
}

func (p *Pipeline) reload() {
	pol, err := LoadPolicy(p.policyPath)
	if err != nil {
		log.Printf("[audit] policy reload failed, keeping previous (hash=%s): %v",
			p.CurrentPolicy().Hash, err)
		return
	}
	if err := p.ApplyPolicy(pol); err != nil {
		log.Printf("[audit] policy apply failed, keeping previous (hash=%s): %v",
			p.CurrentPolicy().Hash, err)
		return
	}
	log.Printf("[audit] policy reloaded hash=%s active_rules=%d custom=%d enabled=%v",
		pol.Hash, p.activeRuleCount(), len(pol.CustomRules), pol.Enabled)
}

// Submit ingests an envelope. Wall and monotonic timestamps are captured
// here, not in process(), so the recorded event time reflects when the
// caller produced the data rather than when the async pipeline ran.
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
	pol := p.CurrentPolicy()
	e := p.buildEvent(env, pol)

	// AllowList check uses agent_id + payload_ref + payload_sha256. If the
	// event is suppressed we record an empty findings list AND stamp the
	// reason — the EVENT is still preserved (Detective completeness), the
	// finding output is silenced.
	allow := pol.CheckAllowList(env.AgentID, e.Subject.PayloadRef, e.Subject.PayloadSHA256)

	var findings []Finding
	scanStart := time.Now()
	if allow.Suppressed {
		findings = []Finding{}
		e.Meta.AllowlistedBy = allow.Reason
		e.Meta.AllowlistMatch = allow.Match
	} else {
		findings = p.scanner.Scan(env.Payload, env.Direction, pol)
	}
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

func (p *Pipeline) buildEvent(env Envelope, pol *Policy) Event {
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
			PolicyHash: pol.Hash,
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
