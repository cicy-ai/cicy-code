package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHandleUpdatePaneChangesRoleTemplateWithoutRewritingGuidance(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	if err := os.MkdirAll(roleDir("assistant"), 0755); err != nil {
		t.Fatalf("mkdir role: %v", err)
	}
	if err := os.WriteFile(roleTemplatePath("assistant"), []byte("# assistant role\n"), 0644); err != nil {
		t.Fatalf("write role: %v", err)
	}
	workspace := t.TempDir()
	guidancePath := workspace + "/AGENTS.md"
	if err := os.WriteFile(guidancePath, []byte("custom guidance"), 0644); err != nil {
		t.Fatalf("write existing guidance: %v", err)
	}
	if _, err := store.Exec(`INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, agent_type, project_template, role_template) VALUES (?,?,?,?,?,?,?,?,?)`,
		"w-10027:main.0", "Codex", workspace, "", `{}`, "worker", "codex", "default", "knowledge-specialist"); err != nil {
		t.Fatalf("insert pane: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/tmux/panes/w-10027", strings.NewReader(`{"role_template":"assistant"}`))
	handleUpdatePane(rec, req, "w-10027")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var roleTemplate string
	if err := store.QueryRow("SELECT role_template FROM agent_config WHERE pane_id=?", "w-10027:main.0").Scan(&roleTemplate); err != nil {
		t.Fatalf("read role template: %v", err)
	}
	if roleTemplate != "assistant" {
		t.Fatalf("role_template = %q, want assistant", roleTemplate)
	}
	content, err := os.ReadFile(guidancePath)
	if err != nil {
		t.Fatalf("read rewritten guidance: %v", err)
	}
	if string(content) != "custom guidance" {
		t.Fatalf("guidance changed while updating role template: %s", content)
	}
}

func TestHandleUpdatePaneRejectsUnknownRoleTemplate(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	if err := os.MkdirAll(roleDir("assistant"), 0755); err != nil {
		t.Fatalf("mkdir role: %v", err)
	}
	if err := os.WriteFile(roleTemplatePath("assistant"), []byte("# assistant role\n"), 0644); err != nil {
		t.Fatalf("write role: %v", err)
	}
	workspace := t.TempDir()
	if _, err := store.Exec(`INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, agent_type, role_template) VALUES (?,?,?,?,?,?,?,?)`,
		"w-10027:main.0", "Codex", workspace, "", `{}`, "worker", "codex", "assistant"); err != nil {
		t.Fatalf("insert pane: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/tmux/panes/w-10027", strings.NewReader(`{"role_template":"does-not-exist"}`))
	handleUpdatePane(rec, req, "w-10027")
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var roleTemplate string
	_ = store.QueryRow("SELECT role_template FROM agent_config WHERE pane_id=?", "w-10027:main.0").Scan(&roleTemplate)
	if roleTemplate != "assistant" {
		t.Fatalf("role_template changed after rejection: %q", roleTemplate)
	}
}

func TestHandleGetPaneIncludesStructuredRuntimeAI(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	body := `{
  "providers": {
    "default": {
      "codex": "openai-default",
      "claude": "anthropic-default"
    },
    "items": [
      {
        "name": "OpenAI Default",
        "key": "openai-default",
        "url": "https://openai.example/v1",
        "apiKey": "test-openai",
        "protocol": "openai",
        "defaultModel": "gpt-5.5",
        "models": ["gpt-5.5", "gpt-5.4"]
      },
      {
        "name": "Anthropic Default",
        "key": "anthropic-default",
        "url": "https://anthropic.example",
        "apiKey": "test-anthropic",
        "protocol": "anthropic",
        "defaultModel": "claude-opus-4-7",
        "models": ["claude-opus-4-7"]
      }
    ]
  }
}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-10027:main.0", "Codex", "/tmp/w-10027", "", `{"runtime_ai":{"provider_name":"openai-default"}}`, "worker", "gpt-5.4", "codex", true, true,
	); err != nil {
		t.Fatalf("insert pane: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tmux/panes/w-10027", nil)
	handleGetPane(rec, req, "w-10027")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	runtimeAI, _ := resp["runtime_ai"].(map[string]any)
	if strings.TrimSpace(anyString(runtimeAI["provider_name"])) != "openai-default" {
		t.Fatalf("runtime_ai.provider_name = %v", runtimeAI)
	}
	options, _ := resp["runtime_ai_provider_options"].([]any)
	if len(options) != 1 {
		t.Fatalf("runtime_ai_provider_options len = %d, want 1; got=%v", len(options), resp["runtime_ai_provider_options"])
	}
	option, _ := options[0].(map[string]any)
	if anyString(option["key"]) != "openai-default" {
		t.Fatalf("provider option key = %v", option)
	}
	defaultSummary, _ := resp["runtime_ai_default"].(map[string]any)
	if anyString(defaultSummary["provider_name"]) != "openai-default" {
		t.Fatalf("runtime_ai_default = %v", defaultSummary)
	}
}

func TestHandleUpdatePaneAcceptsStructuredRuntimeAI(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	body := `{
  "providers": {
    "default": {
      "codex": "openai-default"
    },
    "items": [
      {
        "name": "OpenAI Default",
        "key": "openai-default",
        "url": "https://openai.example/v1",
        "apiKey": "test-openai",
        "protocol": "openai",
        "defaultModel": "gpt-5.5"
      }
    ]
  }
}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-10027:main.0", "Codex", "/tmp/w-10027", "", `{}`, "worker", "", "codex", true, true,
	); err != nil {
		t.Fatalf("insert pane: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/tmux/panes/w-10027", strings.NewReader(`{"runtime_ai":{"provider_name":"openai-default"}}`))
	handleUpdatePane(rec, req, "w-10027")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var config string
	if err := store.QueryRow("SELECT config FROM agent_config WHERE pane_id=?", "w-10027:main.0").Scan(&config); err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(config, `"provider_name":"openai-default"`) {
		t.Fatalf("config = %s", config)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PATCH", "/api/tmux/panes/w-10027", strings.NewReader(`{"runtime_ai":null}`))
	handleUpdatePane(rec, req, "w-10027")
	if rec.Code != 200 {
		t.Fatalf("clear status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if err := store.QueryRow("SELECT config FROM agent_config WHERE pane_id=?", "w-10027:main.0").Scan(&config); err != nil {
		t.Fatalf("read cleared config: %v", err)
	}
	if strings.Contains(config, "runtime_ai") {
		t.Fatalf("config should clear runtime_ai, got %s", config)
	}
}

func anyString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func TestSubmitEnterRetryLimitKeepsCodexRecoveryBounded(t *testing.T) {
	if got := submitEnterRetryLimitForAgent("codex"); got != 1 {
		t.Fatalf("codex retry limit = %d, want one visibility-guarded retry", got)
	}
	if got := submitEnterRetryLimitForAgent("claude"); got != submitEnterRetryLimit {
		t.Fatalf("claude retry limit = %d, want %d", got, submitEnterRetryLimit)
	}
}
