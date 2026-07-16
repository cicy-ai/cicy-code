// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// HTTP surface for the Line engine.
//
// Loopback-only, like the AI gateway and /api/cicy/chat: a line run spends real
// money and can drive a real browser, so it is not something a LAN peer gets to
// start. The CLI (`cicy-code line …`) is a thin client over this — the ENGINE
// lives in the daemon, where the gateway, the todo store and the agents already
// are. That split is also what makes the engine reachable later from a schedule,
// a webhook or a remote worker without reimplementing any of it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// POST /api/line/run   {spec: <path to line.yaml>, seed: {...}, auto_approve?: bool}
// Streams SSE progress; the terminal `done` event carries the full run record.
func handleLineRun(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "line_run_loopback_only")
		return
	}
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req struct {
		Spec        string                 `json:"spec"`
		Seed        map[string]interface{} `json:"seed"`
		Agent       string                 `json:"agent"`
		AutoApprove bool                   `json:"auto_approve"`
		ApprovedBy  string                 `json:"approved_by"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid body")
		return
	}
	spec, err := LoadLineSpec(strings.TrimSpace(req.Spec))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	agentID, err := pickLineAgent(req.Agent)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}

	sse := newLineSSE(w)
	if sse == nil {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	run, runErr := RunLine(spec, req.Seed, LineRunOptions{
		AgentID:     agentID,
		AutoApprove: req.AutoApprove,
		ApprovedBy:  req.ApprovedBy,
		Progress:    sse.emit,
	})
	sse.final(run, runErr)
}

// POST /api/line/approve   {run: <id>, by: <who>, note?: <why>}
//
// This is the other half of the human gate. The run does not continue until
// this lands, and the approval is written into the run record — an approval
// that leaves no trace is not a gate.
func handleLineApprove(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "line_run_loopback_only")
		return
	}
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req struct {
		Run  string `json:"run"`
		By   string `json:"by"`
		Note string `json:"note"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid body")
		return
	}
	if strings.TrimSpace(req.By) == "" {
		// Anonymous approval defeats the point: the record must name who let the
		// outward action through.
		httpErr(w, 400, "approve requires `by` — who is approving this?")
		return
	}
	sse := newLineSSE(w)
	if sse == nil {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	run, err := ApproveLine(strings.TrimSpace(req.Run), req.By, req.Note, LineRunOptions{Progress: sse.emit})
	sse.final(run, err)
}

// GET /api/line/runs        → recent runs (newest first)
// GET /api/line/runs?run=ID → one run record
func handleLineRuns(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "line_run_loopback_only")
		return
	}
	if id := strings.TrimSpace(r.URL.Query().Get("run")); id != "" {
		run, err := loadLineRun(id)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		J(w, M{"run": run})
		return
	}
	entries, _ := os.ReadDir(lineRunsDir())
	var runs []*LineRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if run, err := loadLineRun(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt > runs[j].StartedAt })
	if len(runs) > 50 {
		runs = runs[:50]
	}
	J(w, M{"runs": runs})
}

// POST /api/line/validate  {spec: <path>}
func handleLineValidate(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "line_run_loopback_only")
		return
	}
	var req struct {
		Spec string `json:"spec"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid body")
		return
	}
	spec, err := LoadLineSpec(strings.TrimSpace(req.Spec))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{
		"ok":          true,
		"id":          spec.ID,
		"version":     spec.Version,
		"stations":    len(spec.Stations),
		"human_gates": spec.HumanGates(),
		"dir":         filepath.Base(spec.Dir()),
	})
}

// pickLineAgent resolves which cicy agent the line runs on.
//
// With exactly one cicy agent on the box, making the operator type its id is
// pointless ceremony — so it is chosen. With several, it REFUSES to guess: a
// line spends money and writes into an agent's conversation, and picking the
// wrong worker silently is not a mistake worth saving a flag over.
func pickLineAgent(explicit string) (string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		return s, nil
	}
	rows, err := store.Query(
		"SELECT pane_id FROM agent_config WHERE agent_type='cicy' ORDER BY pane_id")
	if err != nil {
		return "", fmt.Errorf("look up cicy agents: %w", err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var pane string
		if err := rows.Scan(&pane); err == nil {
			found = append(found, shortPaneID(normPaneID(pane)))
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no cicy agent on this box — a line runs on one (create a cicy agent, or pass --agent)")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("several cicy agents (%s) — say which one with --agent, I won't guess: a line spends money and writes into that agent's conversation",
			strings.Join(found, ", "))
	}
}

// ── SSE plumbing ──────────────────────────────────────────────────────────

type lineSSE struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func newLineSSE(w http.ResponseWriter) *lineSSE {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return &lineSSE{w: w, fl: fl}
}

func (s *lineSSE) emit(ev M) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = s.w.Write([]byte("data: " + string(body) + "\n\n"))
	s.fl.Flush()
}

// final ships the full run record as the last event, so a client never has to
// go back and re-fetch what it just watched happen.
func (s *lineSSE) final(run *LineRun, err error) {
	ev := M{"type": "result"}
	if run != nil {
		ev["run"] = run
	}
	if err != nil {
		ev["error"] = err.Error()
	}
	s.emit(ev)
}
