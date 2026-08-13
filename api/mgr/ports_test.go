// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func withPortsTestDB(t *testing.T) {
	old := cicyDBDir
	cicyDBDir = filepath.Join(t.TempDir(), "db")
	t.Cleanup(func() { cicyDBDir = old })
}

func TestPublishedPortsPersistAndRejectManagementPort(t *testing.T) {
	withPortsTestDB(t)
	if err := savePublishedPorts([]publishedPort{{Port: 34567, Name: "Preview", Visibility: "private"}}); err != nil {
		t.Fatal(err)
	}
	got := loadPublishedPorts()
	if len(got) != 1 || got[0].Port != 34567 || got[0].Visibility != "private" {
		t.Fatalf("unexpected ports: %#v", got)
	}
	management, _ := strconv.Atoi(resolvePort())
	if validPublishedPort(management) {
		t.Fatalf("management port %d must never be publishable", management)
	}
}

func TestPublishedPortProxyRequiresInstanceProxyAndStripsCredentials(t *testing.T) {
	withPortsTestDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "" || r.Header.Get("cookie") != "" || r.Header.Get("x-cicy-instance-proxy") != "" {
			t.Errorf("credentials leaked upstream: %#v", r.Header)
		}
		w.Header().Set("location", "http://127.0.0.1:"+r.Host[strings.LastIndex(r.Host, ":")+1:]+"/next")
		w.Header().Set("set-cookie", "sid=ok; Domain=127.0.0.1; Path=/; HttpOnly")
		_ = json.NewEncoder(w).Encode(M{"path": r.URL.Path, "query": r.URL.RawQuery})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	if err := savePublishedPorts([]publishedPort{{Port: port, Name: "Test", Visibility: "public"}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://local/_cicy/ports/"+strconv.Itoa(port)+"/hello?q=1", nil)
	req.Header.Set("authorization", "Bearer secret")
	req.Header.Set("cookie", "private=1")
	denied := httptest.NewRecorder()
	handlePublishedPortProxy(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("direct request status=%d", denied.Code)
	}

	req.Header.Set("x-cicy-instance-proxy", "1")
	ok := httptest.NewRecorder()
	handlePublishedPortProxy(ok, req)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"path":"/hello"`) {
		t.Fatalf("proxy status=%d body=%s", ok.Code, ok.Body.String())
	}
	if ok.Header().Get("location") != "https://local/next" {
		t.Fatalf("absolute loopback redirect was not rewritten: %q", ok.Header().Get("location"))
	}
	if strings.Contains(strings.ToLower(ok.Header().Get("set-cookie")), "domain=") {
		t.Fatalf("loopback cookie domain leaked: %q", ok.Header().Get("set-cookie"))
	}
}

func TestClosedPortIsRejected(t *testing.T) {
	withPortsTestDB(t)
	if got := normalizePortVisibility("closed"); got != "" {
		t.Fatalf("closed visibility must be rejected, got %q", got)
	}
}

func TestOfflinePublishedPortIsPruned(t *testing.T) {
	withPortsTestDB(t)
	if err := savePublishedPorts([]publishedPort{{Port: 34569, Name: "Stopped", Visibility: "private"}}); err != nil {
		t.Fatal(err)
	}
	if got := pruneOfflinePublishedPorts(); len(got) != 0 {
		t.Fatalf("offline ports were not pruned: %#v", got)
	}
	if got := loadPublishedPorts(); len(got) != 0 {
		t.Fatalf("offline ports remain on disk: %#v", got)
	}
}

func TestHTTPPortProbeDistinguishesHTTPFromRawTCP(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>Test app</title>"))
	}))
	defer httpServer.Close()
	u, _ := url.Parse(httpServer.URL)
	httpPort, _ := strconv.Atoi(u.Port())
	if !isLoopbackHTTP(httpPort) {
		t.Fatalf("HTTP listener on %d was not detected", httpPort)
	}

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rawPort := raw.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := raw.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	if isLoopbackHTTP(rawPort) {
		t.Fatalf("raw TCP listener on %d must not be reported as HTTP", rawPort)
	}
}

func TestHTTPPortProbeRejectsAPIsErrorsAndDevTools(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{name: "json api", status: 200, contentType: "application/json", body: `{"ok":true}`},
		{name: "root not found", status: 404, contentType: "text/html", body: "<html>not found</html>"},
		{name: "electron devtools", status: 200, contentType: "text/html", body: "<html>Content shell remote debugging</html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			port, _ := strconv.Atoi(u.Port())
			if isLoopbackHTTP(port) {
				t.Fatalf("%s must not be auto-detected", tt.name)
			}
		})
	}
}
