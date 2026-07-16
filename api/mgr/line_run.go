// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// The Line engine — it EXECUTES a Line Spec instead of executing a script.
//
// A station is ONE TURN on an existing cicy agent:
//
//	agent     an already-registered cicy agent (the line's worker), named by --agent
//	isolation the conversation is /clear'd before every station, so a station sees
//	          its DECLARED INPUT and nothing else — the I/O contract in the spec is
//	          the only channel between stations
//	role      the station's role file goes into the PROMPT (it is the station's
//	          character, not the agent's — the agent's own AGENTS.md is untouched)
//	turn      deliverCicyMessage(...) → the in-process cicy loop → the local gateway
//	readback  reply.json → the answer AND the REAL cost_credit
//
// Running on a real agent (rather than a fabricated one) is what makes the line
// WORK and what makes it VISIBLE: provider and model resolve off agent_config
// like they do for every other turn, and the operator watches the stations
// stream into the chat they are already looking at.
//
// Cost is still measured PER STATION: reply.CostCredit resets on every user turn,
// so one station = one turn = one ledger. It is never estimated. (A hand-written
// "est_unit_cost" is exactly the kind of plausible number that turned out to be
// an order of magnitude off once the gateway's real accounting was read — and
// the gateway itself was over-reporting 2×.)
//
// The human gate is enforced HERE, by the engine — the run stops dead and
// persists. It is not a prompt asking an agent to please behave.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LineRun is one execution of a line. Persisted, so a run that stops at a human
// gate survives a restart.
type LineRun struct {
	ID          string `json:"id"`
	LineID      string `json:"line_id"`
	LineVersion string `json:"line_version"`
	SpecPath    string `json:"spec_path"`
	// AgentID is the cicy agent this line runs ON — its worker. Recorded so a
	// resumed run goes back to the same one, and so the operator can go read the
	// actual turns.
	AgentID string `json:"agent_id"`

	// Status: running | awaiting_approval | done | failed
	Status string `json:"status"`
	// AwaitingStation is the human gate the run is parked on (when awaiting_approval).
	AwaitingStation string `json:"awaiting_station,omitempty"`
	Error           string `json:"error,omitempty"`

	Seed map[string]interface{} `json:"seed"`

	// Plan is the spec's stations in order. Stations[] only holds what has RUN,
	// so without this a viewer could not draw the stations still ahead — and a
	// board that only shows the past is not a board.
	Plan []LinePlanStation `json:"plan"`

	Stations []LineStationRun `json:"stations"`

	// Cursor is the index of the next station to run — the resume point.
	Cursor int `json:"cursor"`

	Metrics LineMetrics `json:"metrics"`

	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`

	// Approvals records who let an outward action through, and when. An approval
	// gate that leaves no trace is not a gate.
	Approvals []LineApproval `json:"approvals,omitempty"`
}

// LinePlanStation is one station as DECLARED — what the line promises to do,
// independent of what a given run has got round to yet.
type LinePlanStation struct {
	ID    string `json:"id"`
	Human bool   `json:"human"`
}

type LineApproval struct {
	Station string `json:"station"`
	By      string `json:"by"`
	At      string `json:"at"`
	Note    string `json:"note,omitempty"`
}

// LineStationRun is one station attempt.
type LineStationRun struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id,omitempty"`
	// Status: done | rework | failed | awaiting_approval | approved
	Status  string `json:"status"`
	Attempt int    `json:"attempt"`

	Output map[string]interface{} `json:"output,omitempty"`
	Error  string                 `json:"error,omitempty"`

	// CostCredit is READ from the gateway's per-request accounting for this
	// station's own agent. Never estimated.
	CostCredit float64 `json:"cost_credit"`
	Requests   int     `json:"requests"`

	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	DurationS float64 `json:"duration_s"`
}

// LineMetrics is the factory board's read model — computed, never declared.
type LineMetrics struct {
	UnitCostUSD float64 `json:"unit_cost_usd"`
	CycleTimeS  float64 `json:"cycle_time_s"`
	// Yield is 1 - rework/total attempts: how much of the work was first-pass.
	Yield      float64 `json:"yield"`
	Attempts   int     `json:"attempts"`
	Reworks    int     `json:"reworks"`
	Requests   int     `json:"requests"`
	Bottleneck string  `json:"bottleneck,omitempty"` // costliest station
}

var (
	lineRunMu sync.Mutex
	// lineJSONRe pulls the JSON object out of a station's answer. Models fence
	// their JSON far more often than they don't, and a station that fails because
	// of a ```json wrapper is a station that fails for no reason.
	lineJSONFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
)

func lineRunsDir() string { return filepath.Join(cicyDBDir, "lines") }

func lineRunPath(runID string) string {
	return filepath.Join(lineRunsDir(), runID+".json")
}

// lineStationAgentID is the ephemeral agent that IS the station. Keeping the run
// id in the name means two concurrent runs of the same line never share a
// conversation, a workspace, or a cost ledger.
func lineStationAgentID(runID, stationID string) string {
	return fmt.Sprintf("line-%s-%s", runID, stationID)
}

func saveLineRun(run *LineRun) error {
	if err := os.MkdirAll(lineRunsDir(), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp := lineRunPath(run.ID) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, lineRunPath(run.ID))
}

func loadLineRun(runID string) (*LineRun, error) {
	body, err := os.ReadFile(lineRunPath(runID))
	if err != nil {
		return nil, fmt.Errorf("no such run %q", runID)
	}
	var run LineRun
	if err := json.Unmarshal(body, &run); err != nil {
		return nil, fmt.Errorf("corrupt run record %q: %w", runID, err)
	}
	return &run, nil
}

// LineRunOptions tunes one execution.
type LineRunOptions struct {
	// AgentID is the cicy agent the line runs on — its worker.
	AgentID string
	// AutoApprove skips human gates. It is EXPLICIT and RECORDED — a CI run may
	// need it, but it must never be the default and must never be invisible in
	// the run record.
	AutoApprove bool
	ApprovedBy  string
	// Progress, if set, is called as stations start and finish.
	Progress func(ev M)
}

// RunLine executes a spec from the beginning.
func RunLine(spec *LineSpec, seed map[string]interface{}, opts LineRunOptions) (*LineRun, error) {
	// Check the worker BEFORE spending anything. A wrong agent type used to fail
	// ten gateway retries deep with an opaque 503; it fails here now, in words.
	agentID, _, err := resolveLineAgent(opts.AgentID)
	if err != nil {
		return nil, err
	}
	plan := make([]LinePlanStation, 0, len(spec.Stations))
	for i := range spec.Stations {
		plan = append(plan, LinePlanStation{
			ID:    spec.Stations[i].ID,
			Human: spec.Stations[i].IsHumanGate(),
		})
	}
	run := &LineRun{
		ID:          newLineRunID(),
		LineID:      spec.ID,
		LineVersion: spec.Version,
		SpecPath:    filepath.Join(spec.Dir(), "line.yaml"),
		AgentID:     agentID,
		Status:      "running",
		Seed:        seed,
		Plan:        plan,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	return resumeLine(spec, run, opts)
}

// ApproveLine records an approval on a parked run and continues it.
func ApproveLine(runID, by, note string, opts LineRunOptions) (*LineRun, error) {
	lineRunMu.Lock()
	run, err := loadLineRun(runID)
	lineRunMu.Unlock()
	if err != nil {
		return nil, err
	}
	if run.Status != "awaiting_approval" {
		return nil, fmt.Errorf("run %s is %s, not awaiting approval", runID, run.Status)
	}
	spec, err := LoadLineSpec(run.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("reload spec for run %s: %w", runID, err)
	}
	station := run.AwaitingStation
	run.Approvals = append(run.Approvals, LineApproval{
		Station: station,
		By:      strings.TrimSpace(by),
		At:      time.Now().UTC().Format(time.RFC3339),
		Note:    strings.TrimSpace(note),
	})
	for i := range run.Stations {
		if run.Stations[i].ID == station && run.Stations[i].Status == "awaiting_approval" {
			run.Stations[i].Status = "approved"
			run.Stations[i].EndedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	run.AwaitingStation = ""
	run.Status = "running"
	run.Cursor++ // step past the gate
	return resumeLine(spec, run, opts)
}

// resumeLine drives the conveyor from run.Cursor to the end (or to a gate).
func resumeLine(spec *LineSpec, run *LineRun, opts LineRunOptions) (*LineRun, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(M) {}
	}
	// wip is the work-in-progress passed station→station: the seed, then each
	// station's declared output, keyed by the station's `out` name.
	wip := map[string]interface{}{"seed": run.Seed}
	for _, sr := range run.Stations {
		if sr.Status != "done" || sr.Output == nil {
			continue
		}
		if st, _, ok := spec.Station(sr.ID); ok && st.Out != "" {
			wip[st.Out] = sr.Output
		}
	}

	canon := ""
	if spec.Inputs.Canon != "" {
		if p, err := spec.Resolve(spec.Inputs.Canon); err == nil {
			if b, err := os.ReadFile(p); err == nil {
				canon = string(b)
			}
		}
	}

	reworks := map[string]int{}
	for _, sr := range run.Stations {
		if sr.Status == "rework" {
			reworks[sr.ID]++
		}
	}

	for run.Cursor < len(spec.Stations) {
		st := &spec.Stations[run.Cursor]

		// ── the human gate ────────────────────────────────────────────────────
		// The engine stops. Not a suggestion to the model — a halt.
		if st.IsHumanGate() {
			if !opts.AutoApprove {
				run.Status = "awaiting_approval"
				run.AwaitingStation = st.ID
				run.Stations = append(run.Stations, LineStationRun{
					ID:        st.ID,
					Status:    "awaiting_approval",
					StartedAt: time.Now().UTC().Format(time.RFC3339),
				})
				computeLineMetrics(run)
				lineRunMu.Lock()
				err := saveLineRun(run)
				lineRunMu.Unlock()
				progress(M{"type": "awaiting_approval", "station": st.ID, "run": run.ID})
				return run, err
			}
			// Auto-approved: still RECORDED, so the run record never lies about
			// whether a human looked at it.
			by := strings.TrimSpace(opts.ApprovedBy)
			if by == "" {
				by = "auto-approve"
			}
			run.Approvals = append(run.Approvals, LineApproval{
				Station: st.ID, By: by, At: time.Now().UTC().Format(time.RFC3339),
				Note: "AUTO-APPROVED — no human reviewed this",
			})
			run.Stations = append(run.Stations, LineStationRun{
				ID: st.ID, Status: "approved",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
				EndedAt:   time.Now().UTC().Format(time.RFC3339),
			})
			progress(M{"type": "auto_approved", "station": st.ID})
			run.Cursor++
			continue
		}

		// ── a working station ─────────────────────────────────────────────────
		progress(M{"type": "station_start", "station": st.ID, "attempt": reworks[st.ID] + 1})
		sr := runLineStation(spec, run, st, wip, canon, reworks[st.ID]+1)
		run.Stations = append(run.Stations, sr)

		if sr.Status == "failed" {
			run.Status = "failed"
			run.Error = fmt.Sprintf("station %s: %s", st.ID, sr.Error)
			run.EndedAt = time.Now().UTC().Format(time.RFC3339)
			computeLineMetrics(run)
			lineRunMu.Lock()
			_ = saveLineRun(run)
			lineRunMu.Unlock()
			progress(M{"type": "failed", "station": st.ID, "error": sr.Error})
			return run, fmt.Errorf("%s", run.Error)
		}

		// ── the rule gate ─────────────────────────────────────────────────────
		if target, rework, err := evalLineGate(st, sr.Output); err != nil {
			run.Status = "failed"
			run.Error = fmt.Sprintf("station %s gate: %v", st.ID, err)
			run.EndedAt = time.Now().UTC().Format(time.RFC3339)
			computeLineMetrics(run)
			lineRunMu.Lock()
			_ = saveLineRun(run)
			lineRunMu.Unlock()
			return run, fmt.Errorf("%s", run.Error)
		} else if rework {
			reworks[st.ID]++
			run.Stations[len(run.Stations)-1].Status = "rework"
			max := st.Gate.MaxRework
			if reworks[st.ID] > max {
				run.Status = "failed"
				run.Error = fmt.Sprintf("station %s: gate still failing after %d rework(s)", st.ID, max)
				run.EndedAt = time.Now().UTC().Format(time.RFC3339)
				computeLineMetrics(run)
				lineRunMu.Lock()
				_ = saveLineRun(run)
				lineRunMu.Unlock()
				progress(M{"type": "failed", "station": st.ID, "error": run.Error})
				return run, fmt.Errorf("%s", run.Error)
			}
			_, idx, _ := spec.Station(target)
			progress(M{"type": "rework", "station": st.ID, "back_to": target, "attempt": reworks[st.ID]})
			run.Cursor = idx
			computeLineMetrics(run)
			lineRunMu.Lock()
			_ = saveLineRun(run)
			lineRunMu.Unlock()
			continue
		}

		if st.Out != "" {
			wip[st.Out] = sr.Output
		}
		progress(M{"type": "station_done", "station": st.ID, "cost": sr.CostCredit})
		run.Cursor++
		computeLineMetrics(run)
		lineRunMu.Lock()
		_ = saveLineRun(run)
		lineRunMu.Unlock()
	}

	run.Status = "done"
	run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	computeLineMetrics(run)
	lineRunMu.Lock()
	err := saveLineRun(run)
	lineRunMu.Unlock()
	progress(M{"type": "done", "run": run.ID, "unit_cost": run.Metrics.UnitCostUSD})
	return run, err
}

// resolveLineAgent checks the line's worker BEFORE any money is spent. The cicy
// loop only runs on a cicy-type agent; a claude/codex pane is a CLI in tmux and
// cannot be driven this way. Failing here, loudly, beats failing ten gateway
// retries deep with an opaque 503.
func resolveLineAgent(agentID string) (string, string, error) {
	short := shortPaneID(normPaneID(strings.TrimSpace(agentID)))
	if short == "" {
		return "", "", fmt.Errorf("no agent given — a line runs ON an agent (pass --agent <pane-id>)")
	}
	at := normalizeAgentType(paneAgentType(short + ":main.0"))
	if at == "" {
		return "", "", fmt.Errorf("agent %s does not exist", short)
	}
	if at != "cicy" {
		return "", "", fmt.Errorf("agent %s is a %q agent; a line runs on a cicy agent (the others are CLIs in tmux)", short, at)
	}
	ws := paneWorkspace(short)
	if ws == "" {
		return "", "", fmt.Errorf("agent %s has no workspace", short)
	}
	return short, ws, nil
}

// runLineStation executes ONE station as ONE TURN on the line's agent.
func runLineStation(spec *LineSpec, run *LineRun, st *LineStation, wip map[string]interface{}, canon string, attempt int) LineStationRun {
	started := time.Now()
	sr := LineStationRun{
		ID:        st.ID,
		AgentID:   run.AgentID,
		Attempt:   attempt,
		StartedAt: started.UTC().Format(time.RFC3339),
	}
	fail := func(format string, a ...interface{}) LineStationRun {
		sr.Status = "failed"
		sr.Error = fmt.Sprintf(format, a...)
		sr.EndedAt = time.Now().UTC().Format(time.RFC3339)
		sr.DurationS = time.Since(started).Seconds()
		return sr
	}

	agentID, workspace, err := resolveLineAgent(run.AgentID)
	if err != nil {
		return fail("%v", err)
	}

	// The station's role. It goes in the PROMPT, not into the agent's AGENTS.md —
	// the role belongs to the station, not to the worker, and a line must never
	// rewrite the identity of the agent it borrows.
	role := st.RoleFile
	if role == "" {
		role = filepath.Join("stations", st.ID+".md")
	}
	roleBody := ""
	if p, err := spec.Resolve(role); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			roleBody = string(b)
		}
	}
	if strings.TrimSpace(roleBody) == "" {
		return fail("station role file not found or empty (%s)", role)
	}

	// ISOLATION. Wipe the conversation before the station speaks. Without this a
	// station would inherit the previous one's chat history and the declared I/O
	// contract would be a fiction — QC would "see" the draft it is meant to
	// review only through the writer's own words, and a rework would carry the
	// failed attempt's reasoning straight back into the retry.
	session := getCicySession(agentID, workspace)
	if !runCicySlashCommand(context.Background(), session, agentID, workspace, "/clear", func(M) {}) {
		return fail("could not clear the agent's conversation before the station")
	}

	prompt, err := buildLineStationPrompt(st, wip, canon, roleBody)
	if err != nil {
		return fail("%v", err)
	}

	// One station = one turn. deliverCicyMessage is synchronous and drives the
	// full in-process tool loop through the local gateway, so the station gets
	// the agent's tools AND lands in its audit trail.
	if !deliverCicyMessage(agentID, workspace, prompt) {
		return fail("agent %s is busy (a turn is already in flight) — a line needs its worker to itself", agentID)
	}

	// deliverCicyMessage returns when the model loop is done — but reply.json is
	// written by the AUDIT layer, off the gateway's SSE finalisation, which is a
	// different path and lands a beat later. Reading it straight away gets the
	// snapshot as it was BEFORE the answer arrived: every station reported "no
	// answer" while the gateway log cheerfully said answer_len=434 and the money
	// was already spent. Wait for it to settle.
	reply, answer, err := waitForLineReply(agentID, lineReplySettleTimeout)
	if err != nil {
		return fail("%v", err)
	}
	// Cost is READ, not estimated. CostCredit resets on every user turn, so this
	// is exactly this station's spend.
	sr.CostCredit = reply.CostCredit
	out, err := parseLineStationJSON(answer)
	if err != nil {
		return fail("%v", err)
	}
	// A station that returns the wrong SHAPE has broken its contract — that is a
	// station failure, not a downstream surprise.
	if missing := missingLineOutFields(out, st.OutFields); len(missing) > 0 {
		return fail("output is missing required field(s): %s", strings.Join(missing, ", "))
	}

	sr.Output = out
	sr.Status = "done"
	sr.EndedAt = time.Now().UTC().Format(time.RFC3339)
	sr.DurationS = time.Since(started).Seconds()
	return sr
}

// lineReplySettleTimeout bounds the wait for the audit layer to finish writing
// the turn's snapshot. Generous: a station is model work, and a slow write is
// not a reason to throw away an answer we already paid for.
const lineReplySettleTimeout = 60 * time.Second

// waitForLineReply blocks until the turn's snapshot has actually landed on disk.
//
// The model loop and the audit layer are different paths: deliverCicyMessage
// returns on the former, reply.json is written by the latter. Polling closes
// that gap. Failing here means the answer really never came — not that we
// looked too early.
func waitForLineReply(agentID string, timeout time.Duration) (aiGatewayReplySnapshot, string, error) {
	deadline := time.Now().Add(timeout)
	var last aiGatewayReplySnapshot
	for {
		reply, err := aiGatewayReadReplySnapshotFile(agentID)
		if err == nil {
			last = reply
			if text := lineReplyText(reply); text != "" {
				return reply, text, nil
			}
			// A settled turn with genuinely no text is a real failure, not a slow
			// write — stop waiting for something that is not coming.
			if strings.EqualFold(strings.TrimSpace(reply.Status), "failed") {
				return reply, "", fmt.Errorf("the turn failed")
			}
		}
		if time.Now().After(deadline) {
			return last, "", fmt.Errorf("station produced no answer (waited %s for the reply snapshot; status=%q)",
				timeout, strings.TrimSpace(last.Status))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// lineReplyText pulls the station's answer out of the reply snapshot.
//
// reply.Answer is NOT where a cicy turn's text lands — the in-process loop
// accumulates content blocks into reply.Items, and Answer stays empty. Reading
// only Answer made every station report "produced no answer" while the gateway
// log cheerfully said answer_len=423: the text was there the whole time, one
// field over. Prefer Answer when a provider does populate it, then fall back to
// concatenating the text blocks.
func lineReplyText(reply aiGatewayReplySnapshot) string {
	if s := strings.TrimSpace(reply.Answer); s != "" {
		return s
	}
	var b strings.Builder
	for _, item := range reply.Items {
		// Skip thinking / tool_use blocks: a station's OUTPUT is its text.
		switch strings.TrimSpace(aiGatewayString(item["type"])) {
		case "thinking", "tool_use", "tool_result":
			continue
		}
		if t := strings.TrimSpace(aiGatewayString(item["text"])); t != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	return strings.TrimSpace(b.String())
}

// buildLineStationPrompt hands the station its role, the canon, and its declared
// input — and nothing else. That "nothing else" is the contract.
func buildLineStationPrompt(st *LineStation, wip map[string]interface{}, canon, role string) (string, error) {
	var b strings.Builder
	if strings.TrimSpace(role) != "" {
		b.WriteString(role)
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(canon) != "" {
		b.WriteString("## Canon (the shared source of truth — every claim must trace back to this)\n\n")
		b.WriteString(canon)
		b.WriteString("\n\n")
	}
	in := strings.TrimSpace(st.In)
	if in != "" {
		val, ok := wip[in]
		if !ok {
			return "", fmt.Errorf("station %q wants input %q, which no upstream station produced", st.ID, in)
		}
		body, err := json.MarshalIndent(val, "", "  ")
		if err != nil {
			return "", fmt.Errorf("encode input %q: %w", in, err)
		}
		fmt.Fprintf(&b, "## Your input (`%s`)\n\n```json\n%s\n```\n\n", in, body)
	}
	b.WriteString("## Your output\n\n")
	b.WriteString("Reply with a SINGLE JSON object and nothing else — no prose before or after.\n")
	if len(st.OutFields) > 0 {
		fmt.Fprintf(&b, "It MUST contain these keys: %s\n", strings.Join(st.OutFields, ", "))
	}
	return b.String(), nil
}

// parseLineStationJSON pulls the object out of a station's answer.
func parseLineStationJSON(answer string) (map[string]interface{}, error) {
	try := func(s string) (map[string]interface{}, bool) {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &m); err == nil && m != nil {
			return m, true
		}
		return nil, false
	}
	if m, ok := try(answer); ok {
		return m, nil
	}
	if g := lineJSONFenceRe.FindStringSubmatch(answer); g != nil {
		if m, ok := try(g[1]); ok {
			return m, nil
		}
	}
	// Last resort: the outermost {...}.
	if i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}"); i >= 0 && j > i {
		if m, ok := try(answer[i : j+1]); ok {
			return m, nil
		}
	}
	return nil, fmt.Errorf("station did not return JSON (got %d chars of prose)", len(answer))
}

func missingLineOutFields(out map[string]interface{}, want []string) []string {
	var missing []string
	for _, f := range want {
		if _, ok := out[f]; !ok {
			missing = append(missing, f)
		}
	}
	return missing
}

// evalLineGate applies the station's rule gate.
// Returns (reworkTarget, needsRework, error).
func evalLineGate(st *LineStation, out map[string]interface{}) (string, bool, error) {
	g := st.Gate
	if g == nil || !strings.EqualFold(strings.TrimSpace(g.Type), "rule") {
		return "", false, nil
	}
	raw, ok := out[g.Field]
	if !ok {
		return "", false, fmt.Errorf("gate reads %q, which the station did not return", g.Field)
	}
	got := strings.TrimSpace(fmt.Sprintf("%v", raw))
	if strings.EqualFold(got, strings.TrimSpace(g.Pass)) {
		return "", false, nil
	}
	target, ok := g.ReworkTarget()
	if !ok {
		// A failing gate with nowhere to loop back to is a hard stop, not a pass.
		return "", false, fmt.Errorf("gate failed (%s=%q, want %q) and declares no on_fail", g.Field, got, g.Pass)
	}
	return target, true, nil
}

// lineStationRequestCount counts the gateway round-trips this station's agent
// made — read from its own usage log, the same source the 2× overcharge was
// caught with.
func lineStationRequestCount(agentID string) int {
	body, err := os.ReadFile(filepath.Join(aiGatewayHistoryDir(agentID), usageLogFileName))
	if err != nil {
		return 0
	}
	n := 0
	for _, ln := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// computeLineMetrics derives the board's numbers from what actually happened.
// Nothing here is declared in the spec; it is all measured.
func computeLineMetrics(run *LineRun) {
	m := LineMetrics{}
	byStation := map[string]float64{}
	for _, sr := range run.Stations {
		if sr.Status == "awaiting_approval" || sr.Status == "approved" {
			continue
		}
		m.Attempts++
		if sr.Status == "rework" {
			m.Reworks++
		}
		m.UnitCostUSD += sr.CostCredit
		m.Requests += sr.Requests
		byStation[sr.ID] += sr.CostCredit
	}
	m.UnitCostUSD = round6(m.UnitCostUSD)
	if m.Attempts > 0 {
		m.Yield = round6(1 - float64(m.Reworks)/float64(m.Attempts))
	}
	top := 0.0
	for id, c := range byStation {
		if c > top {
			top, m.Bottleneck = c, id
		}
	}
	if run.StartedAt != "" {
		start, err1 := time.Parse(time.RFC3339, run.StartedAt)
		end := time.Now().UTC()
		if run.EndedAt != "" {
			if e, err := time.Parse(time.RFC3339, run.EndedAt); err == nil {
				end = e
			}
		}
		if err1 == nil {
			m.CycleTimeS = round6(end.Sub(start).Seconds())
		}
	}
	run.Metrics = m
}

// newLineRunID is time-ordered (runs sort chronologically on disk) plus random
// bytes, so two runs started in the same second never collide on a workspace or
// a cost ledger.
func newLineRunID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102t150405"), hex.EncodeToString(b))
}
