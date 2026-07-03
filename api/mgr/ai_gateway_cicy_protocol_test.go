package main

import "testing"

// cicy speaks Anthropic Messages always; these tests pin the gateway bridge that
// lets it ride EITHER an anthropic- or an openai-protocol provider: the /anthropic
// endpoint translates the request down to Chat Completions (and wraps the SSE back)
// only when the effective provider is openai-protocol.

const cicyProtoProvidersJSON = `{
  "api_token": "keep-me",
  "providers": {
    "default": { "cicy": "oai", "claude": "ant" },
    "items": [
      { "name": "OpenAI", "key": "oai", "url": "https://oai.example/v1", "apiKey": "sk-oai", "protocol": "openai", "defaultModel": "gpt-5.5" },
      { "name": "Anthropic", "key": "ant", "url": "https://ant.example", "apiKey": "sk-ant", "protocol": "anthropic", "defaultModel": "claude-opus-4-7" }
    ]
  }
}`

func insertCicyAgent(t *testing.T, paneID, configJSON string) {
	t.Helper()
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		paneID, "PM", "/tmp/"+paneID, "", configJSON, "worker", "", "cicy", true, true,
	); err != nil {
		t.Fatalf("insert cicy agent: %v", err)
	}
}

func TestCicyBridgesOpenAIProviderOnAnthropicEndpoint(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	writeProvidersGlobalJSON(t, cicyProtoProvidersJSON)
	insertCicyAgent(t, "w-cicy:main.0", "{}")
	agentID := "w-cicy:main.0"

	if got := aiGatewayEffectiveProviderProtocol("anthropic", agentID); got != "openai" {
		t.Fatalf("effective protocol = %q, want openai (default cicy->oai)", got)
	}
	// openai provider behind /anthropic + /messages → bridge ON.
	if !shouldAdaptAnthropicToChatCompletions("anthropic", agentID, "oai.example", "/v1", "/v1/messages") {
		t.Fatalf("expected bridge ON for openai provider on /anthropic")
	}
	// Non-messages suffix never bridges.
	if shouldAdaptAnthropicToChatCompletions("anthropic", agentID, "oai.example", "/v1", "/v1/models") {
		t.Fatalf("expected bridge OFF for non-/messages suffix")
	}
}

func TestCicyAnthropicProviderPassesThrough(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	// flip the cicy default to the anthropic-protocol provider
	j := `{"api_token":"k","providers":{"default":{"cicy":"ant"},"items":[
      {"name":"OpenAI","key":"oai","url":"https://oai.example/v1","apiKey":"sk","protocol":"openai","defaultModel":"gpt-5.5"},
      {"name":"Anthropic","key":"ant","url":"https://ant.example","apiKey":"sk","protocol":"anthropic","defaultModel":"claude-opus-4-7"}]}}`
	writeProvidersGlobalJSON(t, j)
	insertCicyAgent(t, "w-cicy2:main.0", "{}")
	agentID := "w-cicy2:main.0"

	if got := aiGatewayEffectiveProviderProtocol("anthropic", agentID); got != "anthropic" {
		t.Fatalf("effective protocol = %q, want anthropic", got)
	}
	// anthropic provider → NO translation (native Messages upstream).
	if shouldAdaptAnthropicToChatCompletions("anthropic", agentID, "ant.example", "", "/v1/messages") {
		t.Fatalf("expected bridge OFF for anthropic provider")
	}
}

func TestCicyProviderPickersOfferBothProtocols(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	writeProvidersGlobalJSON(t, cicyProtoProvidersJSON)

	// cicy must carry no protocol filter, so the routing/model/override pickers
	// (which drop options whose protocol != expected) show openai providers too.
	if got := runtimeAIExpectedProtocolForAgentType("cicy"); got != "" {
		t.Fatalf("cicy expected protocol = %q, want \"\" (no filter)", got)
	}
	opts := runtimeAIProviderOptionsForAgentType("cicy")
	var sawOpenAI, sawAnthropic bool
	for _, o := range opts {
		switch o.Key {
		case "oai":
			sawOpenAI = true
		case "ant":
			sawAnthropic = true
		}
	}
	if !sawOpenAI || !sawAnthropic {
		t.Fatalf("cicy provider options should include both protocols, got %+v", opts)
	}
}

func TestCicyOpenAIOverrideNotMismatch(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	writeProvidersGlobalJSON(t, cicyProtoProvidersJSON)
	// runtime_ai override pins this cicy agent to the openai provider explicitly.
	insertCicyAgent(t, "w-cicy3:main.0", `{"runtime_ai":{"provider_name":"oai"}}`)
	agentID := "w-cicy3:main.0"

	// /anthropic endpoint + openai override → bridged, so NOT a mismatch error.
	cfg, _, err := resolveRuntimeAIConfigForAgent("anthropic", agentID)
	if err != nil {
		t.Fatalf("anthropic+openai override should be bridged, got err=%v", err)
	}
	if cfg.Provider != "oai" {
		t.Fatalf("resolved provider = %q, want oai", cfg.Provider)
	}

	// The reverse direction has no bridge: /openai endpoint + anthropic override → 409.
	insertCicyAgent(t, "w-cicy4:main.0", `{"runtime_ai":{"provider_name":"ant"}}`)
	if _, _, err := resolveRuntimeAIConfigForAgent("openai", "w-cicy4:main.0"); err != ErrRuntimeAIProviderMismatch {
		t.Fatalf("openai+anthropic override should mismatch, got err=%v", err)
	}
}
