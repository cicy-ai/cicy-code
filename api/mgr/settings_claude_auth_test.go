// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleClaudeAuthImportRestoresDecodedJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := []byte("{\n  \"auth_mode\": \"chatgpt\",\n  \"tokens\": {\"access_token\": \"new-token\"}\n}\n")
	body := `{"base64":"` + base64.StdEncoding.EncodeToString(want) + `"}`
	recorder := httptest.NewRecorder()

	handleClaudeAuthImport(recorder, httptest.NewRequest(http.MethodPost, "/api/settings/claude-auth", strings.NewReader(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatalf("read restored auth.json: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("restored auth.json = %q, want exact decoded bytes %q", got, want)
	}
}

func TestHandleClaudeAuthImportRejectsInvalidInputWithoutChangingAuth(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid base64", body: `{"base64":"not-base64!"}`},
		{name: "invalid json", body: `{"base64":"` + base64.StdEncoding.EncodeToString([]byte("not-json")) + `"}`},
		{name: "json array", body: `{"base64":"` + base64.StdEncoding.EncodeToString([]byte(`[]`)) + `"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".claude", ".credentials.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			const original = `{"tokens":{"access_token":"keep-me"}}`
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatalf("seed auth.json: %v", err)
			}

			recorder := httptest.NewRecorder()
			handleClaudeAuthImport(recorder, httptest.NewRequest(http.MethodPost, "/api/settings/claude-auth", strings.NewReader(tt.body)))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read auth.json after rejection: %v", err)
			}
			if string(got) != original {
				t.Fatalf("auth.json changed after rejected input: %q", got)
			}
		})
	}
}
