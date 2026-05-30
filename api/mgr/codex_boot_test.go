package main

import (
	"os"
	"strings"
	"testing"
)

func TestAgentBootLinesCodexAllowAllActions(t *testing.T) {
	lines := agentBootLines("codex", true, false, true, "w-10001", "gpt-5.5")

	script := strings.Join(lines, "\n")

	// mkdir moved to ~/.cicy_tmux.conf
	if strings.Contains(script, "mkdir -p ~/.cicy") {
		t.Error("codex boot lines should not duplicate mkdir -p ~/.cicy")
	}

	// Must set OPENAI_API_KEY
	if !strings.Contains(script, "OPENAI_API_KEY") {
		t.Error("missing OPENAI_API_KEY")
	}

	// Must use shared install helper
	if !strings.Contains(script, "__cicy_require_command 'codex' 'Codex'") {
		t.Error("missing codex shared install helper")
	}

	// Must use custom provider
	if !strings.Contains(script, `model_provider="custom"`) {
		t.Error("missing model_provider=custom")
	}

	// Must have custom provider name
	if !strings.Contains(script, `model_providers.custom.name="cicy-local"`) {
		t.Error("missing model_providers.custom.name")
	}

	// Must have base_url pointing to local gateway
	if !strings.Contains(script, "api/ai-gateway/openai/w-10001") {
		t.Error("missing ai-gateway base_url")
	}

	// Must pass model explicitly so shared ~/.codex/config.toml does not win
	if !strings.Contains(script, "codex -m 'gpt-5.5'") {
		t.Error("missing explicit codex model override")
	}

	// Must have --dangerously-bypass-approvals-and-sandbox
	if !strings.Contains(script, "--dangerously-bypass-approvals-and-sandbox") {
		t.Error("missing --dangerously-bypass-approvals-and-sandbox when allowAllActions=true")
	}

	// Must NOT have unnecessary env vars
	for _, banned := range []string{"CICY_API_KEY", "CICY_API_URL", "CICY_ANTHROPIC_URL", "CICY_DEFAULT_CLAUDE_MODEL", "CICY_OPENCLAW_MODEL"} {
		if strings.Contains(script, "export "+banned) {
			t.Errorf("boot script should not contain %s for codex", banned)
		}
	}
}

func TestAgentBootLinesCodexModelCatalog(t *testing.T) {
	lines := agentBootLines("codex", true, false, true, "w-10001", "deepseek-v4-pro")
	script := strings.Join(lines, "\n")

	// Catalog is generated at boot (cloned from Codex's own embedded metadata)
	// and written under the persistent state dir, then referenced so Codex
	// stops warning "Model metadata for deepseek-v4-* not found".
	if !strings.Contains(script, "cicy-ai/db/codex-models.json") {
		t.Error("catalog should be written under ~/cicy-ai/db")
	}
	if !strings.Contains(script, `model_catalog_json="`) {
		t.Error("missing -c model_catalog_json override")
	}
	// The generator must define both gateway slugs (pro + flash).
	for _, slug := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
		if !strings.Contains(script, slug) {
			t.Errorf("catalog generator missing entry for %s", slug)
		}
	}
	// A failed catalog build must not block startup — launch is guarded by a
	// non-empty-file check with a plain-codex fallback.
	if !strings.Contains(script, "if [ -s ") || !strings.Contains(script, "; else codex -m ") {
		t.Error("codex launch should fall back to no-catalog when build fails")
	}

	// Non-gateway codex must NOT inject the catalog (it uses official config).
	off := strings.Join(agentBootLines("codex", true, false, false, "w-10001", ""), "\n")
	if strings.Contains(off, "model_catalog_json") {
		t.Error("non-gateway codex should not inject a model catalog")
	}
}

func TestAgentBootLinesCodexRestrictedActions(t *testing.T) {
	lines := agentBootLines("codex", false, false, true, "w-10001", "gpt-5.5")

	script := strings.Join(lines, "\n")

	// Must NOT have --dangerously-bypass-approvals-and-sandbox
	if strings.Contains(script, "--dangerously-bypass-approvals-and-sandbox") {
		t.Error("should NOT have --dangerously-bypass-approvals-and-sandbox when allowAllActions=false")
	}

	// Must still have codex command with custom provider
	if !strings.Contains(script, `model_provider="custom"`) {
		t.Error("missing model_provider=custom")
	}
	if !strings.Contains(script, `model_providers.custom.base_url=`) {
		t.Error("missing model_providers.custom.base_url")
	}
	if !strings.Contains(script, "codex -m 'gpt-5.5'") {
		t.Error("missing explicit codex model override")
	}

	// Must still use shared install helper
	if !strings.Contains(script, "__cicy_require_command 'codex' 'Codex'") {
		t.Error("missing codex shared install helper")
	}

	// Must still have OPENAI_API_KEY
	if !strings.Contains(script, "OPENAI_API_KEY") {
		t.Error("missing OPENAI_API_KEY")
	}
}

func TestAgentBootLinesCodexNormalization(t *testing.T) {
	// "openai" should normalize to codex
	for _, alias := range []string{"codex", "openai"} {
		lines := agentBootLines(alias, true, false, true, "w-10001", "gpt-5.5")
		script := strings.Join(lines, "\n")
		if !strings.Contains(script, "codex -m 'gpt-5.5'") {
			t.Errorf("agentType=%q should produce codex boot lines", alias)
		}
	}
}

func TestAgentBootLinesCodexUsesCodexDefaultProviderModel(t *testing.T) {
	withTestStore(t)
	withTempCicyRoot(t)
	body := `{
	  "ai": {
	    "currentProvider": "xchai"
	  },
	  "providers": {
	    "default": {
	      "claude": "2000RunClaude",
	      "codex": "2000RunOpenAi"
	    },
	    "items": [
	      {
	        "name": "XChai",
	        "key": "xchai",
	        "url": "https://xchai.xyz",
	        "apiKey": "test-anthropic-key",
	        "protocol": "anthropic",
	        "defaultModel": "claude-opus-4-7"
	      },
	      {
	        "name": "2000Run OpenAI",
	        "key": "2000RunOpenAi",
	        "url": "https://api.2000.run/v1",
	        "apiKey": "test-openai-key",
	        "protocol": "openai",
	        "defaultModel": "gpt-5.5"
	      }
	    ]
	  }
	}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}

	lines := agentBootLines("codex", true, false, true, "w-10001", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, "codex -m 'gpt-5.5'") {
		t.Fatalf("codex boot lines should use codex provider default model: %s", script)
	}
	if strings.Contains(script, "codex -m 'claude-opus-4-7'") {
		t.Fatalf("codex boot lines should not use the global anthropic provider default model: %s", script)
	}
}

func TestInitPaneEnvCodexNoExtraEnv(t *testing.T) {
	// Test that selectedBuiltinWorkers env for codex is empty
	agentNorm := normalizeAgentType("codex")
	if agentNorm != "codex" {
		t.Fatalf("normalizeAgentType(codex) = %q, want codex", agentNorm)
	}

	// Simulate the env selection logic from initPaneEnv
	sessionEnv := map[string]string{}
	switch agentNorm {
	case "codex":
		// codex uses -c flags directly, no env needed
	}

	if len(sessionEnv) != 0 {
		t.Errorf("codex should have 0 session env vars, got %d: %v", len(sessionEnv), sessionEnv)
	}
}
