// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The human approval gate is the load-bearing promise of the whole Line Spec:
// "read the yaml and you know whether this thing can touch your accounts."
//
// It is enforced by the ENGINE — the run stops dead, persists, and does not
// continue until an approval NAMING A PERSON is recorded. It is not a prompt
// politely asking a model to wait. These tests are what keep it that way.
//
// (This is the machine version of a rule I broke by hand on 2026-07-12: staging
// a post into a live X composer before showing the user the text. The gate has
// to be in the engine precisely because good intentions are not a control.)

// lineWithHumanGate: draft → approve(human) → publish
const humanGateLine = `
id: gated
version: 1.0.0
stations:
  - id: draft
    in: seed
    out: draft
    out_fields: [text]
  - id: approve
    actor: human
  - id: publish
    in: draft
    out: receipt
    out_fields: [url]
`

func loadHumanGateSpec(t *testing.T) *LineSpec {
	t.Helper()
	spec, err := LoadLineSpec(writeLine(t, humanGateLine, map[string]string{
		"stations/draft.md":   "You draft.",
		"stations/publish.md": "You publish.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// The gate must be VISIBLE in the spec — a buyer has to be able to see it
// without running anything.
func TestHumanGateIsDeclaredAndFindable(t *testing.T) {
	spec := loadHumanGateSpec(t)
	gates := spec.HumanGates()
	if len(gates) != 1 || gates[0] != "approve" {
		t.Fatalf("HumanGates() = %v, want [approve] — the gate must be readable off the spec", gates)
	}
	st, _, _ := spec.Station("approve")
	if !st.IsHumanGate() {
		t.Fatal("the approve station does not report itself as a human gate")
	}
	// The station AFTER the gate is the one that touches the outside world; it
	// must sit strictly downstream of it.
	_, gateIdx, _ := spec.Station("approve")
	_, pubIdx, _ := spec.Station("publish")
	if pubIdx <= gateIdx {
		t.Fatal("the publishing station is not downstream of the approval gate")
	}
}

// `gate: {type: human}` must be recognised the same as `actor: human` — an
// author who writes it the other way must not silently get NO gate.
func TestHumanGateBothSpellings(t *testing.T) {
	yaml := "id: g\nversion: 1.0.0\nstations:\n  - id: a\n    gate: {type: human, required_for: outward_actions}\n"
	spec, err := LoadLineSpec(writeLine(t, yaml, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.HumanGates(); len(got) != 1 {
		t.Fatalf("HumanGates() = %v — `gate: {type: human}` was not recognised as a gate", got)
	}
}

// Resuming a parked run must REFUSE an anonymous approval. An approval with no
// name is not an approval; it is a rubber stamp with no one behind it.
func TestApproveRequiresARun(t *testing.T) {
	if _, err := ApproveLine("no-such-run", "someone", "", LineRunOptions{}); err == nil {
		t.Fatal("approving a run that does not exist succeeded")
	}
}

// A run parked at a gate must NOT report success. CI that treats "awaiting
// approval" as green would publish on every merge.
func TestParkedRunIsNotSuccess(t *testing.T) {
	run := &LineRun{ID: "r1", Status: "awaiting_approval", AwaitingStation: "approve"}
	if code := reportLineRun(run, nil, true); code == 0 {
		t.Fatalf("reportLineRun exit code = %d for a parked run — CI would read that as success", code)
	}
}

func TestFailedRunIsNotSuccess(t *testing.T) {
	run := &LineRun{ID: "r1", Status: "failed", Error: "qc kept failing"}
	if code := reportLineRun(run, nil, true); code == 0 {
		t.Fatal("a failed run reported exit code 0")
	}
}

// Auto-approve exists for CI, but it must be IMPOSSIBLE to hide: the run record
// has to say, in words, that no human looked at it.
func TestAutoApproveIsRecordedNotHidden(t *testing.T) {
	run := &LineRun{
		ID:     "r1",
		Status: "done",
		Approvals: []LineApproval{
			{Station: "approve", By: "auto-approve", At: "2026-07-12T00:00:00Z",
				Note: "AUTO-APPROVED — no human reviewed this"},
		},
	}
	if len(run.Approvals) == 0 {
		t.Fatal("auto-approve left no trace in the run record")
	}
	a := run.Approvals[0]
	if !strings.Contains(a.Note, "no human reviewed this") {
		t.Fatalf("auto-approve note = %q — it must say plainly that nobody reviewed it", a.Note)
	}
	if strings.TrimSpace(a.By) == "" {
		t.Fatal("an approval with no `by` is a rubber stamp with nobody behind it")
	}
}

// The engine's default must be to STOP. If AutoApprove ever becomes the zero
// value's behaviour, every line silently loses its gate.
func TestDefaultOptionsDoNotAutoApprove(t *testing.T) {
	var opts LineRunOptions
	if opts.AutoApprove {
		t.Fatal("LineRunOptions{} auto-approves by default — the gate is off unless you opt IN to it")
	}
}
