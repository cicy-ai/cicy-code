// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// These cover the parts of the engine where being wrong is EXPENSIVE:
// the gate that decides whether work loops back, the shape contract that decides
// whether a station broke the line, and the JSON extraction that decides whether
// a perfectly good station gets failed over a ```json fence.
//
// The human gate's stop-the-world behaviour is covered in TestLineHumanGate*.

func TestEvalLineGate(t *testing.T) {
	gated := func(onFail string, max int) *LineStation {
		return &LineStation{
			ID: "qc",
			Gate: &LineGate{
				Type: "rule", Field: "result", Pass: "pass",
				OnFail: onFail, MaxRework: max,
			},
		}
	}

	t.Run("pass", func(t *testing.T) {
		_, rework, err := evalLineGate(gated("rework -> draft", 2), M{"result": "pass"})
		if err != nil || rework {
			t.Fatalf("rework=%v err=%v; a passing verdict must not loop back", rework, err)
		}
	})

	t.Run("fail loops back to the declared station", func(t *testing.T) {
		target, rework, err := evalLineGate(gated("rework -> draft", 2), M{"result": "rework"})
		if err != nil || !rework || target != "draft" {
			t.Fatalf("target=%q rework=%v err=%v; want draft,true,nil", target, rework, err)
		}
	})

	// A gate whose field the station never returned is NOT a pass. Treating a
	// missing verdict as "fine" is how a quality gate quietly stops gating.
	t.Run("missing verdict field is an error, never a pass", func(t *testing.T) {
		_, rework, err := evalLineGate(gated("rework -> draft", 2), M{"something_else": true})
		if err == nil {
			t.Fatal("a missing gate field passed the gate")
		}
		if rework {
			t.Fatal("missing gate field must not silently become a rework either")
		}
	})

	// A failing gate with nowhere to loop back to must HALT, not proceed.
	t.Run("failing gate with no on_fail halts", func(t *testing.T) {
		st := &LineStation{ID: "qc", Gate: &LineGate{Type: "rule", Field: "result", Pass: "pass"}}
		_, rework, err := evalLineGate(st, M{"result": "rework"})
		if err == nil {
			t.Fatal("a failing gate with no on_fail was allowed through")
		}
		if rework {
			t.Fatal("should halt, not rework")
		}
	})

	t.Run("no gate is a pass-through", func(t *testing.T) {
		_, rework, err := evalLineGate(&LineStation{ID: "draft"}, M{"anything": 1})
		if err != nil || rework {
			t.Fatalf("rework=%v err=%v", rework, err)
		}
	})

	// The verdict may arrive as a bool or a number, not just a string — a model
	// asked for `ok` will hand back `true`, not `"true"`.
	t.Run("non-string verdicts compare by value", func(t *testing.T) {
		st := &LineStation{ID: "qc", Gate: &LineGate{
			Type: "rule", Field: "ok", Pass: "true",
			OnFail: "rework -> draft", MaxRework: 2,
		}}
		if _, rework, err := evalLineGate(st, M{"ok": true}); err != nil || rework {
			t.Fatalf("bool true should satisfy pass:\"true\" (rework=%v err=%v)", rework, err)
		}
		target, rework, err := evalLineGate(st, M{"ok": false})
		if err != nil || !rework || target != "draft" {
			t.Fatalf("bool false must fail the gate and loop back (target=%q rework=%v err=%v)", target, rework, err)
		}
	})
}

// Models fence their JSON far more often than not. A station that fails because
// of a ```json wrapper is a station that fails for no reason — and every failure
// costs a rework loop, i.e. real money.
func TestParseLineStationJSON(t *testing.T) {
	want := "hello"
	cases := map[string]string{
		"bare":            `{"a":"hello"}`,
		"fenced":          "```json\n{\"a\":\"hello\"}\n```",
		"fenced no lang":  "```\n{\"a\":\"hello\"}\n```",
		"prose around it": "Sure! Here you go:\n\n```json\n{\"a\":\"hello\"}\n```\n\nHope that helps.",
		"leading prose":   "Here it is: {\"a\":\"hello\"}",
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := parseLineStationJSON(answer)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if out["a"] != want {
				t.Fatalf("a = %v, want %q", out["a"], want)
			}
		})
	}

	t.Run("pure prose is a station failure", func(t *testing.T) {
		if _, err := parseLineStationJSON("I could not do that."); err == nil {
			t.Fatal("prose was accepted as station output")
		}
	})
}

// A station that returns the WRONG SHAPE has broken its contract. Catching it at
// the station is the difference between one failed station and a corrupted run.
func TestMissingLineOutFields(t *testing.T) {
	out := M{"posts": []interface{}{"a"}, "extra": 1}
	if got := missingLineOutFields(out, []string{"posts"}); len(got) != 0 {
		t.Fatalf("missing = %v, want none", got)
	}
	got := missingLineOutFields(out, []string{"posts", "og_image", "schedule"})
	if len(got) != 2 || got[0] != "og_image" || got[1] != "schedule" {
		t.Fatalf("missing = %v, want [og_image schedule]", got)
	}
}

// Unit cost must be SUMMED FROM WHAT WAS MEASURED, never declared. (An earlier
// hand-written "est_unit_cost_usd: 0.70" in this very spec turned out to be off
// by an order of magnitude once the gateway's real per-request accounting was
// read — and the gateway itself was over-reporting 2×. Cost gets read, always.)
func TestComputeLineMetricsMeasuresCost(t *testing.T) {
	run := &LineRun{
		StartedAt: "2026-07-12T00:00:00Z",
		EndedAt:   "2026-07-12T00:02:00Z",
		Stations: []LineStationRun{
			{ID: "draft", Status: "rework", CostCredit: 0.10, Requests: 2},
			{ID: "draft", Status: "done", CostCredit: 0.20, Requests: 3},
			{ID: "qc", Status: "done", CostCredit: 0.05, Requests: 1},
			// An approval gate consumes no model and must not skew the numbers.
			{ID: "approve", Status: "approved"},
		},
	}
	computeLineMetrics(run)
	m := run.Metrics

	if m.UnitCostUSD != 0.35 {
		t.Errorf("unit cost = %v, want 0.35 (the sum of what the stations actually spent)", m.UnitCostUSD)
	}
	if m.Requests != 6 {
		t.Errorf("requests = %d, want 6", m.Requests)
	}
	if m.Attempts != 3 || m.Reworks != 1 {
		t.Errorf("attempts/reworks = %d/%d, want 3/1 (the approval gate must not count)", m.Attempts, m.Reworks)
	}
	// 1 rework out of 3 attempts.
	if want := round6(1 - 1.0/3.0); m.Yield != want {
		t.Errorf("yield = %v, want %v", m.Yield, want)
	}
	// draft spent 0.30 across its two attempts; qc spent 0.05.
	if m.Bottleneck != "draft" {
		t.Errorf("bottleneck = %q, want draft (the costliest station, summed across reworks)", m.Bottleneck)
	}
	if m.CycleTimeS != 120 {
		t.Errorf("cycle time = %v, want 120", m.CycleTimeS)
	}
}

// Two concurrent runs of the same line must never share a workspace, a
// conversation or a cost ledger — and a rework must not inherit the failed
// attempt's conversation.
func TestLineStationAgentIDsAreIsolated(t *testing.T) {
	a := lineStationAgentID("run-1", "draft")
	b := lineStationAgentID("run-2", "draft")
	if a == b {
		t.Fatalf("two runs collided on the same station agent id: %s", a)
	}
	if !strings.Contains(a, "run-1") || !strings.Contains(a, "draft") {
		t.Fatalf("agent id %q should name both the run and the station", a)
	}
}

// A station borrows an agent; it does not become one. The station's ROLE has to
// travel in the prompt — if it were written into the agent's AGENTS.md instead,
// a line would silently rewrite the identity of a worker it does not own.
func TestBuildLineStationPrompt(t *testing.T) {
	st := &LineStation{ID: "qc", In: "draft", Out: "verdict", OutFields: []string{"result", "issues"}}
	wip := map[string]interface{}{"draft": map[string]interface{}{"posts": []interface{}{"p1"}}}

	got, err := buildLineStationPrompt(st, wip, "CANON BODY", "ROLE: you are the editor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ROLE: you are the editor", // the station's character rides in the prompt
		"CANON BODY",               // the shared source of truth
		`"posts"`,                  // the declared input, and only it
		"result, issues",           // the output contract
		"SINGLE JSON object",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, got)
		}
	}

	// A station wired to an input nobody produces is a broken line, and it must
	// say so instead of silently prompting the model with nothing.
	_, err = buildLineStationPrompt(&LineStation{ID: "x", In: "nope"}, wip, "", "role")
	if err == nil || !strings.Contains(err.Error(), "no upstream station produced") {
		t.Fatalf("err = %v; want a clear 'no upstream station produced it'", err)
	}
}

// The worker has to be checked BEFORE any money is spent. Handing a line a
// claude/codex pane used to fail ten gateway retries deep with an opaque 503;
// it must fail immediately, in words that say what to do.
func TestResolveLineAgentRejectsEarly(t *testing.T) {
	_, _, err := resolveLineAgent("")
	if err == nil || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("err = %v; an empty agent must say how to pass one", err)
	}
}

// A cicy turn's text lands in reply.Items, NOT in reply.Answer — which stays
// empty. Reading only Answer made every station report "produced no answer"
// while the gateway log said answer_len=423: the model had answered, the money
// was spent, and the text was sitting one field over.
func TestLineReplyTextReadsItemsNotJustAnswer(t *testing.T) {
	t.Run("falls back to the text blocks", func(t *testing.T) {
		reply := aiGatewayReplySnapshot{
			Answer: "",
			Items: []map[string]interface{}{
				{"type": "text", "text": `{"angle":"a"}`},
			},
		}
		if got := lineReplyText(reply); got != `{"angle":"a"}` {
			t.Fatalf("got %q — the station's answer was in Items and got dropped", got)
		}
	})

	t.Run("thinking and tool blocks are not the answer", func(t *testing.T) {
		reply := aiGatewayReplySnapshot{
			Items: []map[string]interface{}{
				{"type": "thinking", "text": "let me consider…"},
				{"type": "tool_use", "text": "shell"},
				{"type": "text", "text": `{"result":"pass"}`},
			},
		}
		if got := lineReplyText(reply); got != `{"result":"pass"}` {
			t.Fatalf("got %q — thinking/tool blocks leaked into the station output", got)
		}
	})

	t.Run("Answer wins when a provider does populate it", func(t *testing.T) {
		reply := aiGatewayReplySnapshot{
			Answer: "from answer",
			Items:  []map[string]interface{}{{"type": "text", "text": "from items"}},
		}
		if got := lineReplyText(reply); got != "from answer" {
			t.Fatalf("got %q, want the Answer field", got)
		}
	})

	t.Run("genuinely empty is still empty", func(t *testing.T) {
		if got := lineReplyText(aiGatewayReplySnapshot{}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}
