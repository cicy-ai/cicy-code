package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubLLM returns whatever JSON content the test sets in `content`,
// wrapped in an OpenAI chat-completion envelope.
func stubLLM(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Smoke-check request body is well-formed.
		raw, _ := io.ReadAll(r.Body)
		var msg struct {
			Messages []map[string]string `json:"messages"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("stub llm got non-JSON: %s", raw)
		}
		if len(msg.Messages) < 2 || msg.Messages[0]["role"] != "system" {
			t.Fatalf("stub llm: bad messages: %v", msg.Messages)
		}
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestParseSuggesterResponse_StrictJSON(t *testing.T) {
	raw := `{"suggestions":[{"kind":"allow_list","severity":"safe","title":"t","rationale":"r","patch":{"allow_list":{"paths":["/internal"]}}}]}`
	got, err := parseSuggesterResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "allow_list" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Patch.AllowList == nil || len(got[0].Patch.AllowList.Paths) != 1 {
		t.Fatalf("patch lost: %+v", got[0].Patch)
	}
}

func TestParseSuggesterResponse_WithMarkdownFences(t *testing.T) {
	raw := "Sure! Here:\n```json\n{\"suggestions\":[]}\n```\nLet me know."
	got, err := parseSuggesterResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestMergePolicyPatch_AppendsAndOverrides(t *testing.T) {
	current := map[string]interface{}{
		"rules_override": []interface{}{
			map[string]interface{}{"id": "secret.aws_akid", "severity": "high"},
		},
	}
	patch := PolicyPatch{
		RulesOverride: []RuleOverride{
			{ID: "secret.aws_akid", Severity: "medium"},          // override existing
			{ID: "secret.bearer_token", DefaultAction: "log"},     // append new
		},
	}
	if err := mergePolicyPatch(current, patch); err != nil {
		t.Fatal(err)
	}
	list := current["rules_override"].([]interface{})
	if len(list) != 2 {
		t.Fatalf("expected 2 overrides, got %d", len(list))
	}
	first := list[0].(map[string]interface{})
	if first["severity"] != "medium" {
		t.Errorf("override severity not applied: %v", first)
	}
	second := list[1].(map[string]interface{})
	if second["id"] != "secret.bearer_token" {
		t.Errorf("append failed: %v", second)
	}
}

func TestMergePolicyPatch_AllowListSetSemantics(t *testing.T) {
	current := map[string]interface{}{
		"allow_list": map[string]interface{}{
			"paths": []interface{}{"/foo"},
		},
	}
	patch := PolicyPatch{
		AllowList: &AllowList{
			Paths:  []string{"/foo", "/bar"}, // /foo already present — dedup
			Agents: []string{"w-99999"},
		},
	}
	if err := mergePolicyPatch(current, patch); err != nil {
		t.Fatal(err)
	}
	al := current["allow_list"].(map[string]interface{})
	paths := al["paths"].([]interface{})
	if len(paths) != 2 {
		t.Fatalf("paths dedup broken: %v", paths)
	}
	agents := al["agents"].([]interface{})
	if len(agents) != 1 || agents[0] != "w-99999" {
		t.Fatalf("agents not merged: %v", agents)
	}
}

func TestGeneratePolicySuggestions_EndToEnd(t *testing.T) {
	// Construct a fresh pipeline manually so this test doesn't depend on
	// (or pollute) the package-level singleton.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	auditRoot := filepath.Join(tmpHome, "cicy-ai", "audit")
	workersRoot := filepath.Join(tmpHome, "cicy-ai", "workers")
	if err := os.MkdirAll(auditRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workersRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	p, err := NewPipeline(auditRoot, workersRoot, NoopScanner{}, policy)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	prevPipeline := globalPipeline
	globalPipeline = p
	defer func() {
		globalPipeline = prevPipeline
	}()

	llmResp := `{"suggestions":[{"kind":"allow_list","severity":"safe","title":"Trust internal /dev path","rationale":"Frequent FPs from internal traffic.","supporting_event_ids":["evt-1"],"patch":{"allow_list":{"paths":["/internal/dev"]}}}]}`
	srv := stubLLM(t, llmResp)
	defer srv.Close()

	cfg := PolicySuggesterConfig{
		Enabled:        true,
		Endpoint:       srv.URL,
		Model:          "test-model",
		MaxTokens:      500,
		TimeoutSeconds: 5,
		LookbackHours:  24,
	}
	if err := GeneratePolicySuggestions(context.Background(), cfg); err != nil {
		t.Fatalf("GeneratePolicySuggestions: %v", err)
	}

	path := filepath.Join(tmpHome, "cicy-ai", "audit", "policy.suggestions.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded SuggestionsFile
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(loaded.Suggestions))
	}
	s := loaded.Suggestions[0]
	if s.Kind != "allow_list" || s.Status != "open" {
		t.Errorf("unexpected suggestion: %+v", s)
	}
	if !strings.HasPrefix(s.ID, "sg-") {
		t.Errorf("ID stamp missing: %q", s.ID)
	}

	// Apply: policy.json now contains the allow_list path.
	if _, err := ApplySuggestion(s.ID); err != nil {
		t.Fatalf("ApplySuggestion: %v", err)
	}
	polBody, err := os.ReadFile(filepath.Join(tmpHome, "cicy-ai", "audit", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(polBody, []byte("/internal/dev")) {
		t.Errorf("policy.json missing applied path: %s", polBody)
	}

	again, _ := LookupSuggestion(s.ID)
	if again == nil || again.Status != "applied" {
		t.Errorf("status after apply: %+v", again)
	}
}
