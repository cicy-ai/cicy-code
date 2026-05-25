package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExplainDecision_StubWhenLLMUnconfigured(t *testing.T) {
	withTempHome(t)
	// Seed a decision so ExplainDecision finds it.
	dec := AutonomyDecision{
		ID:               "dec-stub-1",
		Timestamp:        time.Now().UTC(),
		Trigger:          "manual",
		EventsConsidered: 12,
		Actions: []AutonomyDecisionAction{
			{Kind: "allow_list", Rationale: "FP rate 85%", Applied: true},
			{Kind: "preventive_toggle", Applied: false, SkippedReason: "forbidden: enable_preventive_block"},
		},
		PolicyHashBefore: "sha256:DEFAULT",
		PolicyHashAfter:  "sha256:abc12345def",
	}
	appendDecision(dec)

	// Force LLM unconfigured.
	prev := autonomyCfg
	autonomyCfg = nil
	defer func() { autonomyCfg = prev }()

	r, err := ExplainDecision(context.Background(), "dec-stub-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "manual") {
		t.Errorf("summary missing trigger: %q", r.Summary)
	}
	if !strings.Contains(r.WhatChanged, "allow_list") {
		t.Errorf("WhatChanged missing applied kind: %q", r.WhatChanged)
	}
	if !strings.Contains(r.WhyNow, "FP rate 85%") {
		t.Errorf("WhyNow lost the rationale: %q", r.WhyNow)
	}
	if r.Confidence != "low" {
		t.Errorf("confidence should be low for stub: %q", r.Confidence)
	}
}

func TestExplainDecision_NotFound(t *testing.T) {
	withTempHome(t)
	_, err := ExplainDecision(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestExplainDecision_LLMSuccess(t *testing.T) {
	withTempHome(t)
	dec := AutonomyDecision{
		ID:               "dec-llm-1",
		Timestamp:        time.Now().UTC(),
		Trigger:          "interval",
		EventsConsidered: 5,
		Actions: []AutonomyDecisionAction{
			{Kind: "rule_override", Applied: true, Rationale: "secret.aws_akid never matched in 30 days"},
		},
		PolicyHashAfter: "sha256:xyz",
	}
	appendDecision(dec)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{
					"role": "assistant",
					"content": `{"summary":"disabled noisy rule","what_changed":"set secret.aws_akid to disabled","why_now":"0 matches in 30 days","impact":"all agents","confidence":"high"}`,
				}},
			},
		})
	}))
	defer srv.Close()

	autonomyCfg = &AutonomyConfig{
		Enabled: true,
		LLM:     AutonomyLLM{Endpoint: srv.URL, Model: "test", APIKey: "x"},
	}
	defer func() { autonomyCfg = nil }()

	r, err := ExplainDecision(context.Background(), "dec-llm-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary != "disabled noisy rule" {
		t.Errorf("summary = %q", r.Summary)
	}
	if r.Confidence != "high" {
		t.Errorf("confidence = %q", r.Confidence)
	}
	if r.DecisionID != "dec-llm-1" {
		t.Errorf("DecisionID = %q", r.DecisionID)
	}
}

func TestExplainDecision_LLMFailureFallsBackToStub(t *testing.T) {
	withTempHome(t)
	dec := AutonomyDecision{
		ID:        "dec-llm-down",
		Timestamp: time.Now().UTC(),
		Trigger:   "manual",
		Actions:   []AutonomyDecisionAction{{Applied: true, Kind: "allow_list"}},
	}
	appendDecision(dec)

	autonomyCfg = &AutonomyConfig{
		Enabled: true,
		LLM:     AutonomyLLM{Endpoint: "http://127.0.0.1:1/unreachable", Model: "x", APIKey: "x"},
	}
	defer func() { autonomyCfg = nil }()

	r, err := ExplainDecision(context.Background(), "dec-llm-down")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.RawMarkdown, "LLM call failed") {
		t.Errorf("expected fallback flag, RawMarkdown=%q", r.RawMarkdown)
	}
	if r.WhatChanged == "" {
		t.Error("stub should still produce WhatChanged")
	}
}
