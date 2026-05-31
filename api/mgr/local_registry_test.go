package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalRegistryLifecycle drives the host-side handler directly: start an
// in-process registry on a real port, publish a skill, read status, then stop.
func TestLocalRegistryLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// minimal valid skill dir
	skill := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(filepath.Join(skill, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(skill, "manifest.json"),
		[]byte(`{"name":"demo","version":"1.0.0","title":"Demo","entry":"bin/demo"}`), 0o644)
	os.WriteFile(filepath.Join(skill, "bin", "demo"), []byte("#!/bin/sh\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Demo"), 0o644)

	call := func(method, path, body string) *httptest.ResponseRecorder {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		} else {
			rdr = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, rdr)
		w := httptest.NewRecorder()
		handleLocalRegistry(w, req)
		return w
	}

	// ensure stopped at the end
	defer call("POST", "/api/local-registry/stop", "")

	// start on an unlikely-free port
	w := call("POST", "/api/local-registry/start", `{"port":18799}`)
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var st map[string]any
	json.Unmarshal(w.Body.Bytes(), &st)
	if st["running"] != true {
		t.Fatalf("expected running=true, got %v", st["running"])
	}
	if tok, _ := st["token"].(string); tok == "" {
		t.Errorf("expected a generated token")
	}

	// publish the skill
	w = call("POST", "/api/local-registry/publish", `{"path":"`+skill+`"}`)
	if w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}

	// status should now list the skill
	w = call("GET", "/api/local-registry", "")
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &st)
	skills, _ := st["skills"].([]any)
	if len(skills) != 1 {
		t.Fatalf("expected 1 published skill, got %d (%s)", len(skills), w.Body.String())
	}

	// the server is actually listening — hit it over HTTP with the token
	tok := st["token"].(string)
	port := int(st["port"].(float64))
	req, _ := http.NewRequest("GET", "http://127.0.0.1:18799/v1/skills", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live GET (port %d): %v", port, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("live /v1/skills: want 200, got %d", resp.StatusCode)
	}

	// config persisted with enabled=true
	cfgData, _ := os.ReadFile(localRegistryConfigPath())
	if !strings.Contains(string(cfgData), `"enabled": true`) {
		t.Errorf("config not persisted enabled: %s", cfgData)
	}
}
