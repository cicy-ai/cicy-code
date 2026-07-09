package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalRegistryLifecycle drives the host-side handler directly against the
// ALWAYS-ON registry semantics: status reports always_on + a generated token,
// publish/unpublish manage the shared dir, the mounted /registry handler serves
// /v1/skills with the Bearer token, and start/stop are back-compat no-ops.
func TestLocalRegistryLifecycle(t *testing.T) {
	// Full isolation (HOME + cicy path vars incl. global.json). Setting only HOME
	// left cicyGlobalJSONPath on the real ~/cicy-ai/global.json, so the test read
	// the operator's real skill_registry_token instead of exercising generation —
	// green on a configured dev box, red on a fresh CI runner.
	withTempCicyRoot(t)
	// Boot-time init generates the read token into global.json (the /start action
	// is a back-compat no-op that only reads it). Run it so a fresh isolated env
	// has a token, matching real startup.
	ensureLocalRegistry()

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
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		handleLocalRegistry(w, req)
		return w
	}

	// start is a back-compat no-op; it must succeed and return the status shape.
	w := call("POST", "/api/local-registry/start", `{"port":18799}`)
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var st map[string]any
	json.Unmarshal(w.Body.Bytes(), &st)
	if st["always_on"] != true {
		t.Fatalf("expected always_on=true, got %v", st["always_on"])
	}
	tok, _ := st["token"].(string)
	if tok == "" {
		t.Fatalf("expected a generated token")
	}

	// publish the skill by path
	w = call("POST", "/api/local-registry/publish", `{"path":"`+skill+`"}`)
	if w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}

	// the mounted /registry handler is live in-process — list skills with the token
	req := httptest.NewRequest("GET", localRegMountPrefix+"/v1/skills", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	serveLocalRegistry(rec, req)
	if rec.Code != 200 {
		t.Fatalf("mounted /v1/skills: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"demo"`) {
		t.Fatalf("published skill missing from /v1/skills: %s", rec.Body.String())
	}

	// unpublish removes it
	w = call("POST", "/api/local-registry/unpublish", `{"name":"demo"}`)
	if w.Code != 200 {
		t.Fatalf("unpublish: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("GET", localRegMountPrefix+"/v1/skills", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	serveLocalRegistry(rec, req)
	if rec.Code != 200 {
		t.Fatalf("mounted /v1/skills after unpublish: want 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"demo"`) {
		t.Fatalf("skill still listed after unpublish: %s", rec.Body.String())
	}

	// stop is a no-op and still returns status 200
	w = call("POST", "/api/local-registry/stop", "")
	if w.Code != 200 {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
}
