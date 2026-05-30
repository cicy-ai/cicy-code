package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func writeProvidersGlobalJSON(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
}

func readProvidersGlobalJSON(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(cicyGlobalJSONPath)
	if err != nil {
		t.Fatalf("read global.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode global.json: %v", err)
	}
	return cfg
}

const seedProvidersJSON = `{
  "api_token": "keep-me",
  "proxy_token": "keep-proxy",
  "providers": {
    "default": { "codex": "oai", "claude": "ant" },
    "items": [
      { "name": "OpenAI", "key": "oai", "url": "https://oai.example/v1", "apiKey": "sk-oai", "protocol": "openai", "defaultModel": "gpt-5.5", "defaultModels": { "codex": "gpt-5.4" }, "models": ["gpt-5.5", "gpt-5.4"] },
      { "name": "Anthropic", "key": "ant", "url": "https://ant.example", "apiKey": "sk-ant", "protocol": "anthropic", "defaultModel": "claude-opus-4-7" }
    ]
  }
}`

func TestHandleProvidersGet(t *testing.T) {
	withTempCicyRoot(t)
	writeProvidersGlobalJSON(t, seedProvidersJSON)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/providers", nil)
	handleProviders(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	defaults, _ := resp["defaults"].(map[string]any)
	if anyString(defaults["codex"]) != "oai" || anyString(defaults["claude"]) != "ant" {
		t.Fatalf("unexpected defaults: %v", defaults)
	}
	items, _ := resp["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d", len(items))
	}
}

func TestHandleProvidersCreateAndPersist(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	writeProvidersGlobalJSON(t, seedProvidersJSON)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(`{"key":"new-oai","name":"New OAI","url":"https://new.example/v1/","protocol":"openai","apiKey":"sk-new","defaultModel":"gpt-5.5","defaultModels":{"codex":"gpt-5.4","junk":""},"models":["gpt-5.5"," "],"modelMapping":{"gpt-4":"gpt-5.5"," ":"x"}}`))
	handleProviders(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	cfg := readProvidersGlobalJSON(t)
	if anyString(cfg["api_token"]) != "keep-me" {
		t.Fatalf("api_token not preserved: %v", cfg["api_token"])
	}
	if anyString(cfg["proxy_token"]) != "keep-proxy" {
		t.Fatalf("proxy_token not preserved: %v", cfg["proxy_token"])
	}
	provider, ok := loadProviderByKey("new-oai")
	if !ok || provider == nil {
		t.Fatalf("new-oai not persisted")
	}
	if provider.URL != "https://new.example/v1" {
		t.Fatalf("url not normalized: %q", provider.URL)
	}
	if provider.DefaultModels["codex"] != "gpt-5.4" {
		t.Fatalf("defaultModels not stored: %v", provider.DefaultModels)
	}
	if _, ok := provider.DefaultModels["junk"]; ok {
		t.Fatalf("empty defaultModels key should be dropped: %v", provider.DefaultModels)
	}
	if len(provider.Models) != 1 || provider.Models[0] != "gpt-5.5" {
		t.Fatalf("models not normalized: %v", provider.Models)
	}
	if provider.ModelMapping["gpt-4"] != "gpt-5.5" || len(provider.ModelMapping) != 1 {
		t.Fatalf("modelMapping not normalized: %v", provider.ModelMapping)
	}

	// duplicate key -> 409
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/providers", strings.NewReader(`{"key":"oai","url":"https://x/v1","protocol":"openai"}`))
	handleProviders(rec2, req2)
	if rec2.Code != 409 {
		t.Fatalf("expected 409 on duplicate, got %d", rec2.Code)
	}

	// bad protocol -> 400
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/api/providers", strings.NewReader(`{"key":"bad","url":"https://x","protocol":"weird"}`))
	handleProviders(rec3, req3)
	if rec3.Code != 400 {
		t.Fatalf("expected 400 on bad protocol, got %d", rec3.Code)
	}
}

func TestHandleProviderPatchPreservesUnknownFields(t *testing.T) {
	withTempCicyRoot(t)
	writeProvidersGlobalJSON(t, seedProvidersJSON)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/providers/oai", strings.NewReader(`{"defaultModel":"gpt-5.6","defaultModels":{"codex":"gpt-5.5","claude":"claude-opus-4-7"}}`))
	handleProvidersSub(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	provider, ok := loadProviderByKey("oai")
	if !ok {
		t.Fatalf("oai missing after patch")
	}
	if provider.DefaultModel != "gpt-5.6" {
		t.Fatalf("defaultModel not updated: %q", provider.DefaultModel)
	}
	if provider.APIKey != "sk-oai" {
		t.Fatalf("apiKey not preserved: %q", provider.APIKey)
	}
	if provider.Protocol != "openai" {
		t.Fatalf("protocol not preserved: %q", provider.Protocol)
	}
	if providerDefaultModelForAgentType(provider, "codex") != "gpt-5.5" {
		t.Fatalf("codex default model resolution wrong: %v", provider.DefaultModels)
	}
	if providerDefaultModelForAgentType(provider, "claude") != "claude-opus-4-7" {
		t.Fatalf("claude default model resolution wrong: %v", provider.DefaultModels)
	}

	// key change rejected
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PATCH", "/api/providers/oai", strings.NewReader(`{"key":"renamed","url":"https://x/v1","protocol":"openai"}`))
	handleProvidersSub(rec2, req2)
	if rec2.Code != 400 {
		t.Fatalf("expected 400 on key change, got %d", rec2.Code)
	}
}

func TestHandleProviderDefaultsUpdate(t *testing.T) {
	withTempCicyRoot(t)
	writeProvidersGlobalJSON(t, seedProvidersJSON)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/providers/defaults", strings.NewReader(`{"default":{"claude":"ant","opencode":"oai"}}`))
	handleProvidersSub(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if loadDefaultProviderKeyForAgentType("claude") != "ant" {
		t.Fatalf("claude default not updated")
	}
	if loadDefaultProviderKeyForAgentType("opencode") != "oai" {
		t.Fatalf("opencode default not added")
	}

	// protocol mismatch: claude must use an anthropic provider, not the openai one -> 400
	recM := httptest.NewRecorder()
	reqM := httptest.NewRequest("PUT", "/api/providers/defaults", strings.NewReader(`{"default":{"claude":"oai"}}`))
	handleProvidersSub(recM, reqM)
	if recM.Code != 400 {
		t.Fatalf("expected 400 on protocol mismatch (claude+openai), got %d body=%s", recM.Code, recM.Body.String())
	}
	// codex must use an openai provider, not the anthropic one -> 400
	recM2 := httptest.NewRecorder()
	reqM2 := httptest.NewRequest("PUT", "/api/providers/defaults", strings.NewReader(`{"default":{"codex":"ant"}}`))
	handleProvidersSub(recM2, reqM2)
	if recM2.Code != 400 {
		t.Fatalf("expected 400 on protocol mismatch (codex+anthropic), got %d body=%s", recM2.Code, recM2.Body.String())
	}

	// unknown provider key -> 400
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PUT", "/api/providers/defaults", strings.NewReader(`{"default":{"codex":"does-not-exist"}}`))
	handleProvidersSub(rec2, req2)
	if rec2.Code != 400 {
		t.Fatalf("expected 400 on unknown key, got %d", rec2.Code)
	}
}

func TestHandleProviderDeleteReferenceChecks(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	writeProvidersGlobalJSON(t, seedProvidersJSON)

	// referenced by providers.default.codex -> 409
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/providers/oai", nil)
	handleProvidersSub(rec, req)
	if rec.Code != 409 {
		t.Fatalf("expected 409 (default ref), got %d body=%s", rec.Code, rec.Body.String())
	}

	// clear defaults referencing oai, then make a pane reference it
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PUT", "/api/providers/defaults", strings.NewReader(`{"default":{"codex":""}}`))
	handleProvidersSub(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("clear default status = %d body=%s", rec2.Code, rec2.Body.String())
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-12345:main.0", "Codex", 12345, "/tmp/w-12345", "", `{"runtime_ai":{"provider_name":"oai"}}`, "worker", "", "codex", true, true,
	); err != nil {
		t.Fatalf("insert pane: %v", err)
	}
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("DELETE", "/api/providers/oai", nil)
	handleProvidersSub(rec3, req3)
	if rec3.Code != 409 {
		t.Fatalf("expected 409 (pane ref), got %d body=%s", rec3.Code, rec3.Body.String())
	}
	var refResp map[string]any
	_ = json.Unmarshal(rec3.Body.Bytes(), &refResp)
	refs, _ := refResp["references"].([]any)
	if len(refs) == 0 {
		t.Fatalf("expected references in 409 body, got %v", refResp)
	}

	// remove the pane reference, then delete succeeds
	if _, err := store.Exec("UPDATE agent_config SET config='{}' WHERE pane_id=?", "w-12345:main.0"); err != nil {
		t.Fatalf("update pane: %v", err)
	}
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest("DELETE", "/api/providers/oai", nil)
	handleProvidersSub(rec4, req4)
	if rec4.Code != 200 {
		t.Fatalf("expected 200 on unreferenced delete, got %d body=%s", rec4.Code, rec4.Body.String())
	}
	if _, ok := loadProviderByKey("oai"); ok {
		t.Fatalf("oai still present after delete")
	}
}

func TestHandleProviderTestDraftValidation(t *testing.T) {
	withTempCicyRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/providers/test", strings.NewReader(`{"url":"","protocol":"openai"}`))
	handleProvidersSub(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("expected ok=false for empty url, got %v", result)
	}
}

func TestApplyModelMappingResolution(t *testing.T) {
	p := &providerConfig{ModelMapping: map[string]string{
		"claude-opus-4-8": "claude-opus-4-7", // exact
		"claude-sonnet*":  "deepseek-v4-flash", // prefix
		"claude-haiku*":   "deepseek-v4-flash",
	}}
	cases := map[string]string{
		"claude-opus-4-8":   "claude-opus-4-7",   // exact wins
		"claude-opus-4-7":   "claude-opus-4-7",   // unmapped -> unchanged
		"claude-sonnet-4-6": "deepseek-v4-flash", // prefix
		"claude-sonnet-4-5": "deepseek-v4-flash",
		"claude-haiku-4-5":  "deepseek-v4-flash",
		"deepseek-v4-pro":   "deepseek-v4-pro", // unrelated -> unchanged
	}
	for in, want := range cases {
		if got := p.applyModelMapping(in); got != want {
			t.Errorf("applyModelMapping(%q) = %q, want %q", in, got, want)
		}
	}
	// Wildcard "" catch-all + longest-prefix-wins.
	p2 := &providerConfig{ModelMapping: map[string]string{
		"":              "deepseek-v4-pro",
		"claude-sonnet*": "x",
		"claude-sonnet-4*": "y", // longer prefix should win
	}}
	if got := p2.applyModelMapping("claude-sonnet-4-6"); got != "y" {
		t.Errorf("longest-prefix: got %q want y", got)
	}
	if got := p2.applyModelMapping("anything-else"); got != "deepseek-v4-pro" {
		t.Errorf("wildcard catch-all: got %q want deepseek-v4-pro", got)
	}
}
