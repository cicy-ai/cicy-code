// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withMobileBridge(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv(mobileBridgeURLVar, srv.URL)
	t.Setenv(mobileBridgeTokenVar, "test-secret")
	return srv
}

func decodeMobileResult(t *testing.T, result string) M {
	t.Helper()
	var out M
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result is not JSON: %q: %v", result, err)
	}
	return out
}

func TestMobileBridgeTreeAndAuthentication(t *testing.T) {
	withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/accessibility/tree" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"nodes":[]}}`))
	})

	result := mobileBridgeCall(context.Background(), map[string]interface{}{"action": "tree"})
	if got := decodeMobileResult(t, result)["ok"]; got != true {
		t.Fatalf("result = %s", result)
	}
}

func TestMobileBridgeActionForwarding(t *testing.T) {
	actions := []string{"click", "input", "scroll", "back", "home", "launch"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/accessibility/action" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				var body M
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["action"] != action {
					t.Errorf("action = %#v, want %q", body["action"], action)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			input := map[string]interface{}{"action": action, "text": "hello", "package": "com.example.app"}
			if got := decodeMobileResult(t, mobileBridgeCall(context.Background(), input))["ok"]; got != true {
				t.Fatalf("ok = %#v", got)
			}
		})
	}
}

func TestMobileBridgeRejectsNonLoopbackAndRedirects(t *testing.T) {
	t.Setenv(mobileBridgeURLVar, "http://example.com:1234")
	t.Setenv(mobileBridgeTokenVar, "secret")
	if mobileBridgeConfigured() {
		t.Fatal("non-loopback bridge must not be configured")
	}
	if code := decodeMobileResult(t, mobileBridgeCall(context.Background(), map[string]interface{}{"action": "tree"}))["code"]; code != "bridge_unavailable" {
		t.Fatalf("code = %#v", code)
	}

	withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/steal", http.StatusFound)
	})
	if code := decodeMobileResult(t, mobileBridgeCall(context.Background(), map[string]interface{}{"action": "tree"}))["code"]; code != "bridge_http_error" {
		t.Fatalf("redirect code = %#v", code)
	}
}

func TestMobileBridgeTimeoutAndResponseLimit(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		if code := decodeMobileResult(t, mobileBridgeCall(ctx, map[string]interface{}{"action": "tree"}))["code"]; code != "timeout" {
			t.Fatalf("code = %#v", code)
		}
	})

	t.Run("response limit", func(t *testing.T) {
		withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", mobileBridgeMaxResponse+1)))
		})
		if code := decodeMobileResult(t, mobileBridgeCall(context.Background(), map[string]interface{}{"action": "tree"}))["code"]; code != "response_too_large" {
			t.Fatalf("code = %#v", code)
		}
	})
}

func TestMobileToolAPIOnlyGatingAndDispatch(t *testing.T) {
	t.Setenv("CICY_RUNTIME_MODE", "api-only")
	t.Setenv(mobileBridgeURLVar, "")
	t.Setenv(mobileBridgeTokenVar, "")
	if cfg := resolveLiteConfig("w-mobile", t.TempDir()); cfg.enabledTools["mobile"] {
		t.Fatal("mobile tool enabled without bridge credentials")
	}

	withMobileBridge(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":"clicked"}`))
	})
	cfg := resolveLiteConfig("w-mobile", t.TempDir())
	if !cfg.enabledTools["mobile"] {
		t.Fatal("mobile tool not enabled for configured API-only runtime")
	}
	result := cicyRunTool(context.Background(), "w-mobile", "mobile", map[string]interface{}{"action": "click"}, cfg)
	if got := decodeMobileResult(t, result)["ok"]; got != true {
		t.Fatalf("dispatch result = %s", result)
	}

	t.Setenv("CICY_RUNTIME_MODE", "")
	if cfg := resolveLiteConfig("w-mobile", t.TempDir()); cfg.enabledTools["mobile"] {
		t.Fatal("mobile tool enabled outside API-only runtime")
	}
}

func TestMobileToolDefinitionExists(t *testing.T) {
	for _, def := range cicyAllToolDefs() {
		if def["name"] == "mobile" {
			return
		}
	}
	t.Fatal("mobile tool definition missing")
}
