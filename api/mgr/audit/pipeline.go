package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
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

	noise            *noiseTracker
	mailer           Mailer
	incidentCooldown *incidentCooldownTracker

	// dropCleanEvents skips persisting events with no findings (clean
	// pass-through traffic) — production sets this so the index only holds
	// alerts. Default false so tests keep the "record every event" contract
	// their chain/verify assertions rely on.
	dropCleanEvents bool

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
		store:            store,
		scanner:          scanner,
		policy:           policy,
		machineID:        mid,
		noise:            newNoiseTracker(),
		mailer:           &FileMailer{OutputDir: filepath.Join(auditRoot, "email-out")},
		incidentCooldown: newIncidentCooldownTracker(),
	}, nil
}

// SetMailer overrides the default FileMailer. Tests + Phase 6 cut 2
// (SmtpMailer) use this; production code paths can leave the FileMailer
// in place.
func (p *Pipeline) SetMailer(m Mailer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mailer = m
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

// effectivePolicyFor returns the merged global+agent policy for the given
// agent id, or the global policy unchanged when there is no override file.
// Read from disk every call: per-agent file is tiny (< 1KB typical) and the
// OS page cache makes this effectively free for hot agents.
func (p *Pipeline) effectivePolicyFor(agentID string, global *Policy) *Policy {
	if agentID == "" {
		return global
	}
	ov, err := LoadAgentOverride(p.store.workersRoot, agentID)
	if err != nil {
		log.Printf("[audit] agent override load failed agent=%s, falling back to global: %v",
			agentID, err)
		return global
	}
	if ov == nil {
		return global
	}
	return MergeIntoEffective(global, ov)
}

// LoadAgentOverride is the package-level entry the HTTP handler uses to
// read a per-agent file. Equivalent to (*Pipeline).effectivePolicyFor but
// without the merge — handlers expose raw override JSON in / out.
func (p *Pipeline) LoadAgentOverride(agentID string) (*AgentOverride, error) {
	return LoadAgentOverride(p.store.workersRoot, agentID)
}

func (p *Pipeline) SaveAgentOverride(agentID string, ov *AgentOverride) error {
	return SaveAgentOverride(p.store.workersRoot, agentID, ov)
}

// EffectivePolicyFor is the exported pipeline view of "what does the audit
// system actually use for events from this agent". Used by /api/audit/policy
// /effective/{agentID}.
func (p *Pipeline) EffectivePolicyFor(agentID string) *Policy {
	return p.effectivePolicyFor(agentID, p.CurrentPolicy())
}

// WorkersRoot returns the absolute path the pipeline uses for per-agent
// files. Exposed for handlers and tests.
func (p *Pipeline) WorkersRoot() string {
	return p.store.workersRoot
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
	log.Printf("[audit] policy reloaded hash=%s active_rules=%d custom=%d",
		pol.Hash, p.activeRuleCount(), len(pol.CustomRules))
	// Credentials/config may have changed; re-resolve the active mailer
	// (SMTP primary, Gmail fallback) or revert to FileMailer.
	p.reloadMailer()
}

// WatchEmailCredentials starts an fsnotify watcher on ~/cicy-ai/db/ so the
// pipeline hot-swaps the mailer whenever smtp.json/google.json appears or
// rotates. This is what catches the docker-cp window: audit.Init runs at
// container startup BEFORE dev.py's `docker cp` lands; the cp triggers
// a watcher event, we reload creds and swap the mailer in-place.
// No-op when filesystem watching cannot be initialized.
func (p *Pipeline) WatchEmailCredentials() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "cicy-ai", "db")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return mkErr
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return err
	}
	go func() {
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				switch filepath.Base(ev.Name) {
				case "email.json":
				default:
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				// Brief settle so docker cp / atomic writes complete.
				time.AfterFunc(200*time.Millisecond, p.reloadMailer)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("[audit] email-credential watcher error: %v", err)
			}
		}
	}()
	return nil
}

// reloadMailer re-evaluates the mailer config and swaps in the active mailer.
// Resend was cut: SMTP (db/smtp.json) is the primary external channel, Gmail
// OAuth the fallback. Idempotent: if the resolved config is identical to the
// current state, the swap is a no-op log-line-free operation.
func (p *Pipeline) reloadMailer() {
	// SMTP ONLY ("只用 smtp"), sourced from the UI's db/email.json. Downgrade to
	// FileMailer when SMTP is no longer configured.
	if scfg := loadSmtpCredentials(); scfg != nil {
		p.SetMailer(NewSmtpMailer(scfg))
		responseMailerKind = "smtp"
		log.Printf("[audit] mailer -> SmtpMailer (%s, db/email.json)", scfg.Host)
		return
	}
	p.SetMailer(&FileMailer{OutputDir: filepath.Join(p.store.auditRoot, "email-out")})
	responseMailerKind = "file"
	log.Printf("[audit] mailer -> FileMailer (no SMTP in db/email.json)")
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
	// L3 per-agent override merge. effectivePolicyFor reads the agent's
	// audit-overrides.json (if any) and returns a fresh *Policy. nil-safe
	// when no override file exists.
	effective := p.effectivePolicyFor(env.AgentID, pol)
	e := p.buildEvent(env, effective)

	// AllowList check uses the EFFECTIVE policy (global ∪ agent paths /
	// content hashes). Suppressed → drop findings, stamp the reason.
	allow := effective.CheckAllowList(env.AgentID, e.Subject.PayloadRef, e.Subject.PayloadSHA256)

	var findings []Finding
	scanStart := time.Now()
	if allow.Suppressed {
		findings = []Finding{}
		e.Meta.AllowlistedBy = allow.Reason
		e.Meta.AllowlistMatch = allow.Match
	} else {
		findings = p.scanner.Scan(env.Payload, env.Direction, effective)
		// Apply effective rules_override after scan: disable specific rules
		// or re-grade their severity per the merged override list.
		findings = ApplyRulesOverrideToFindings(findings, effective.RulesOverride)
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

	// Noise governance: when the would-be action is notify, ask the
	// in-memory tracker whether channels should actually be invoked. The
	// EVENT still records action=notify (intent is preserved for audit
	// replay); only meta.notify_suppressed_by carries the suppression flag
	// so future channel-delivery code (Phase 3) can skip accordingly.
	if e.Decision.Action == ActionNotify && len(findings) > 0 {
		hash := EventFindingHash(e)
		reason := p.noise.EvaluateNotify(
			env.AgentID,
			topFindingRule(findings),
			hash,
			env.submitMonoNs,
			pol.Notify,
			topSeverity(findings),
		)
		if reason != "" {
			e.Meta.NotifySuppressedBy = reason
		}
	}

	// Forensic q/reply snapshot for ANY finding event (log/notify, in/out): records
	// conversation_id + history_id + the request question + the response, taken
	// from the REDACTED payload so the secret is never re-exposed. Lets the audit
	// log answer "当前请求/response 是什么". (block/redact save theirs in
	// submitPreventive; this covers the detective path.)
	if len(findings) > 0 && !allow.Suppressed {
		if ref := p.saveQRSnapshot(e, env, findings); ref != "" {
			e.Meta.SnapshotRef = ref
		}
	}

	// Response delivery: for a notify event, attempt the SMTP/email alert NOW
	// (this worker is already off the agent hot path) and record the REAL
	// outcome on the event BEFORE persisting, so 发送状态 is auditable. log/none
	// events still always record — they just carry no alert. Email-ONLY: we
	// never forward to an agent (that costs an LLM call per hit).
	if e.Decision.Action == ActionNotify && len(findings) > 0 {
		switch {
		case allow.Suppressed:
			e.Meta.AlertStatus = "未发送:白名单"
		case e.Meta.NotifySuppressedBy != "":
			e.Meta.AlertStatus = "未发送:" + e.Meta.NotifySuppressedBy
		default:
			e.Meta.AlertStatus = p.dispatchIncident(e)
		}
	}

	// Only persist events worth auditing: a finding fired, the match was
	// allowlist-suppressed (worth a record), or the pipeline errored. Clean
	// pass-through traffic (no findings, action=none) is the overwhelming
	// majority and has nothing to audit — recording it just bloats the index
	// and slows every query. Drop it.
	if p.dropCleanEvents && len(e.Findings) == 0 && e.Meta.AllowlistedBy == "" && e.Meta.PipelineError == "" {
		return
	}

	if _, err := p.store.Append(e); err != nil {
		log.Printf("[audit] store.Append failed agent=%s id=%s: %v", env.AgentID, e.ID, err)
		return
	}

	if len(findings) > 0 {
		log.Printf("[audit] event=%s agent=%s findings=%d action=%s alert=%q scan_ms=%d total_ms=%d",
			e.ID, env.AgentID, len(findings), e.Decision.Action, e.Meta.AlertStatus,
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

	id := env.eventID
	if id == "" {
		id = "evt_" + uuid.NewString()
	}
	return Event{
		ID:            id,
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
			HistoryID:      env.HistoryID,
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
