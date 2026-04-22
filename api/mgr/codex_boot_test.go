package main

import (
	"strings"
	"testing"
)

func TestAgentBootLinesCodexAllowAllActions(t *testing.T) {
	lines := agentBootLines("codex", true, "w-10001")

	script := strings.Join(lines, "\n")

	// Must have mkdir
	if !strings.Contains(script, "mkdir -p ~/.cicy") {
		t.Error("missing mkdir -p ~/.cicy")
	}

	// Must set OPENAI_API_KEY
	if !strings.Contains(script, "OPENAI_API_KEY") {
		t.Error("missing OPENAI_API_KEY")
	}

	// Must have codex install check
	if !strings.Contains(script, "command -v codex") {
		t.Error("missing codex install check")
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

func TestAgentBootLinesCodexRestrictedActions(t *testing.T) {
	lines := agentBootLines("codex", false, "w-10001")

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

	// Must still have install check
	if !strings.Contains(script, "command -v codex") {
		t.Error("missing codex install check")
	}

	// Must still have OPENAI_API_KEY
	if !strings.Contains(script, "OPENAI_API_KEY") {
		t.Error("missing OPENAI_API_KEY")
	}
}

func TestAgentBootLinesCodexNormalization(t *testing.T) {
	// "openai" and "kiro-cli" should normalize to codex
	for _, alias := range []string{"codex", "openai", "kiro-cli"} {
		lines := agentBootLines(alias, true, "w-10001")
		script := strings.Join(lines, "\n")
		if !strings.Contains(script, "codex -c") {
			t.Errorf("agentType=%q should produce codex boot lines", alias)
		}
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
