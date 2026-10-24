// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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

func keystoreCall(t *testing.T, h http.HandlerFunc, method, target, body string) (int, M, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	var out M
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestKeystoreNeverListsValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const secret = "sk-super-secret-123"

	code, _, _ := keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"OPENAI_KEY","value":"`+secret+`","note":"OpenAI prod"}`)
	if code != 200 {
		t.Fatalf("create: %d", code)
	}
	code, _, raw := keystoreCall(t, handleKeystore, "GET", "/api/keystore", "")
	if code != 200 || strings.Contains(raw, secret) {
		t.Fatalf("list must not contain the value (code %d): %s", code, raw)
	}
	if !strings.Contains(raw, `"has_value":true`) || !strings.Contains(raw, "OpenAI prod") {
		t.Fatalf("list should report key, note, has_value: %s", raw)
	}
	// Mutating responses echo the list too — still no value.
	_, _, raw = keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"OPENAI_KEY","note":"renamed note"}`)
	if strings.Contains(raw, secret) {
		t.Fatalf("update response leaked the value: %s", raw)
	}

	path := filepath.Join(home, "cicy-ai", "db", "keystore.json")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("keystore.json mode = %o, want 600", st.Mode().Perm())
	}
}

func TestKeystoreUpdateRenameDeleteResolve(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if code, _, _ := keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"bad-key","value":"x"}`); code != 400 {
		t.Fatalf("invalid key name: got %d, want 400", code)
	}
	if code, _, _ := keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"NO_VALUE","note":"n"}`); code != 400 {
		t.Fatalf("new key without value: got %d, want 400", code)
	}
	keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"A","value":"v1","note":"first"}`)

	// Note-only update keeps the stored value.
	keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"A","note":"second"}`)
	_, out, _ := keystoreCall(t, handleKeystoreResolve, "POST", "/api/keystore/resolve", `{"keys":["A"]}`)
	if v, _ := out["values"].(map[string]any)["A"].(string); v != "v1" {
		t.Fatalf("value after note-only update = %q, want v1", v)
	}

	// Rename carries the value; the old key is gone.
	if code, _, _ := keystoreCall(t, handleKeystore, "POST", "/api/keystore", `{"key":"B","old_key":"A","note":"moved"}`); code != 200 {
		t.Fatalf("rename failed: %d", code)
	}
	if code, _, _ := keystoreCall(t, handleKeystoreResolve, "POST", "/api/keystore/resolve", `{"keys":["A"]}`); code != 404 {
		t.Fatalf("old key still resolves after rename: %d", code)
	}
	_, out, _ = keystoreCall(t, handleKeystoreResolve, "POST", "/api/keystore/resolve", `{"keys":["B"]}`)
	if v, _ := out["values"].(map[string]any)["B"].(string); v != "v1" {
		t.Fatalf("renamed value = %q, want v1", v)
	}

	// Any missing key fails the whole resolve.
	if code, _, _ := keystoreCall(t, handleKeystoreResolve, "POST", "/api/keystore/resolve", `{"keys":["B","NOPE"]}`); code != 404 {
		t.Fatalf("partial resolve must fail: %d", code)
	}

	if code, _, _ := keystoreCall(t, handleKeystore, "DELETE", "/api/keystore?key=B", ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	_, _, raw := keystoreCall(t, handleKeystore, "GET", "/api/keystore", "")
	if strings.Contains(raw, `"B"`) {
		t.Fatalf("deleted key still listed: %s", raw)
	}
}
