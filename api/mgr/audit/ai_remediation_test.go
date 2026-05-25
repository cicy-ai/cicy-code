package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubHTTPClient lets a test capture the request and respond with a
// canned body / status without touching the network.
type stubHTTPClient struct {
	gotReq *http.Request
	gotBody []byte
	status int
	body   string
	err    error
}

func (s *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	s.gotReq = req
	if req.Body != nil {
		s.gotBody, _ = io.ReadAll(req.Body)
	}
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func withStubAIClient(t *testing.T, stub *stubHTTPClient) {
	t.Helper()
	prev := defaultAIHTTPClient
	defaultAIHTTPClient = stub
	t.Cleanup(func() { defaultAIHTTPClient = prev })
}

func sampleEvent() Event {
	return Event{
		ID:        "evt_x",
		Timestamp: "2026-05-15T11:00:00Z",
		Identity:  Identity{AgentID: "w-x", AgentType: "claude"},
		Subject:   Subject{Provider: "anthropic", Model: "claude-opus", Direction: "outbound"},
		Findings: []Finding{{
			RuleID:     "secret.aws_akid",
			Severity:   SeverityHigh,
			Category:   "secret",
			MatchCount: 1,
			Spans:      []Span{{Preview: "AKIA****MPLE"}},
		}},
		Decision: Decision{Action: ActionRedact, Applied: true},
	}
}

func TestAIRemediation_DisabledNoHTTPCall(t *testing.T) {
	stub := &stubHTTPClient{status: 200, body: "{}"}
	withStubAIClient(t, stub)

	_, err := callAIRemediation(context.Background(), AIRemediationConfig{Enabled: false}, sampleEvent())
	if err == nil {
		t.Error("disabled should return error")
	}
	if stub.gotReq != nil {
		t.Error("disabled must not issue an HTTP call")
	}
}

func TestAIRemediation_EmptyEndpointError(t *testing.T) {
	_, err := callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Model: "x"}, sampleEvent())
	if err == nil || !strings.Contains(err.Error(), "endpoint empty") {
		t.Errorf("expected endpoint empty error, got %v", err)
	}
}

func TestAIRemediation_EmptyModelError(t *testing.T) {
	_, err := callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Endpoint: "https://x"}, sampleEvent())
	if err == nil || !strings.Contains(err.Error(), "model empty") {
		t.Errorf("expected model empty error, got %v", err)
	}
}

func TestAIRemediation_HappyPathParsesJSON(t *testing.T) {
	aiOutput := `{
		"summary": "AKID leaked from w-x; redacted before send.",
		"severity_explain": "AWS access key in plain text is a critical-tier credential.",
		"immediate_actions": ["Rotate AKID immediately", "Audit recent CloudTrail"],
		"longer_term": ["Add inline=true block for secret.aws_akid"]
	}`
	chatEnvelope := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"content": aiOutput}},
		},
	}
	body, _ := json.Marshal(chatEnvelope)
	stub := &stubHTTPClient{status: 200, body: string(body)}
	withStubAIClient(t, stub)

	out, err := callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Endpoint: "https://llm.corp", Model: "internal-1", APIKey: "k"},
		sampleEvent())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Summary == "" || len(out.ImmediateActions) != 2 || len(out.LongerTerm) != 1 {
		t.Errorf("output not parsed correctly: %+v", out)
	}
	// Confirm the HTTP request shape: endpoint url, Bearer auth header.
	if stub.gotReq.URL.String() != "https://llm.corp/chat/completions" {
		t.Errorf("URL = %q", stub.gotReq.URL.String())
	}
	if auth := stub.gotReq.Header.Get("Authorization"); auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestAIRemediation_StripsCodeFences(t *testing.T) {
	aiOutput := "```json\n{\"summary\":\"x\",\"severity_explain\":\"\",\"immediate_actions\":[\"a\"],\"longer_term\":[]}\n```"
	chatEnvelope := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"content": aiOutput}},
		},
	}
	body, _ := json.Marshal(chatEnvelope)
	stub := &stubHTTPClient{status: 200, body: string(body)}
	withStubAIClient(t, stub)

	out, err := callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Endpoint: "https://llm.corp", Model: "internal-1"},
		sampleEvent())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Summary != "x" {
		t.Errorf("fence-wrapped JSON not parsed: %+v", out)
	}
}

func TestAIRemediation_Non2xxIsError(t *testing.T) {
	stub := &stubHTTPClient{status: 500, body: "internal error"}
	withStubAIClient(t, stub)

	_, err := callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Endpoint: "https://llm.corp", Model: "internal-1"},
		sampleEvent())
	if err == nil || !strings.Contains(err.Error(), "http 500") {
		t.Errorf("expected http 500 error, got %v", err)
	}
}

// TestAIRemediation_NeverSendsPayload verifies the USER message in the
// chat-completions request contains masked previews + metadata but NOT
// the raw payload. The SYSTEM prompt may mention "payload" as an
// instruction; that is intentional and not checked here.
func TestAIRemediation_NeverSendsPayload(t *testing.T) {
	stub := &stubHTTPClient{status: 200, body: `{"choices":[{"message":{"content":"{}"}}]}`}
	withStubAIClient(t, stub)

	_, _ = callAIRemediation(context.Background(),
		AIRemediationConfig{Enabled: true, Endpoint: "https://llm.corp", Model: "internal-1"},
		sampleEvent())

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(stub.gotBody, &req); err != nil {
		t.Fatalf("parse outbound chat request: %v", err)
	}
	var userContent string
	for _, m := range req.Messages {
		if m.Role == "user" {
			userContent = m.Content
		}
	}
	if userContent == "" {
		t.Fatal("no user message in outbound request")
	}
	// User content MUST include the masked preview the model needs to
	// reason about.
	if !strings.Contains(userContent, "AKIA****MPLE") {
		t.Errorf("user message missing masked preview, got: %s", userContent)
	}
	// User content MUST NOT include the unmasked AKID or anything that
	// looks like raw payload bytes.
	for _, leak := range []string{"AKIAIOSFODNN7EXAMPLE", "BEGIN RSA PRIVATE", "BEGIN OPENSSH"} {
		if strings.Contains(userContent, leak) {
			t.Errorf("user message leaked %q in: %s", leak, userContent)
		}
	}
}

func TestBuildAIPrompt_StableMinimalShape(t *testing.T) {
	body, err := buildAIPrompt(sampleEvent())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("buildAIPrompt produced invalid JSON: %v\n%s", err, body)
	}
	// Required top-level fields the system prompt expects.
	for _, k := range []string{"severity", "agent_id", "agent_type", "provider", "model", "action_taken", "findings"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in prompt body: %s", k, body)
		}
	}
}

// Sanity for the stub itself (used by other tests).
func TestStubHTTPClient_Sanity(t *testing.T) {
	stub := &stubHTTPClient{status: 200, body: "{}"}
	resp, _ := stub.Do(httpReqOK())
	if resp.StatusCode != 200 {
		t.Error("stub status wiring broken")
	}
}

func httpReqOK() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "https://x/y", nil)
	return req
}

// Compile-time guard: assert the stub satisfies the interface.
var _ aiHTTPClient = (*stubHTTPClient)(nil)

// Silence "imported and not used" for fmt during refactors.
var _ = fmt.Sprint
