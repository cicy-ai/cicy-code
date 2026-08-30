package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const proxyNodesFixture = `# test config
mixed-port: 9001
proxies:
  # first node
  - name: a
    type: ss
    server: a.example
    port: 1
    cipher: aes-128-gcm
    password: x
  - name: b
    type: ss
    server: b.example
    port: 2
    cipher: aes-128-gcm
    password: y
proxy-groups:
  - name: g1
    type: select
    proxies: [a, b, DIRECT]
  - name: g2
    type: select
    proxies:
      - a
rules:
  - MATCH,g1
`

func proxyNodesEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CICY_MIHOMO_BIN", "/nonexistent/cicy-mihomo") // no validation binary, no reload
	dir := filepath.Join(home, "cicy-ai", "db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mihomo.yaml")
	if err := os.WriteFile(p, []byte(proxyNodesFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func proxyNodesCall(t *testing.T, method, path string, body any) (int, M) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	if strings.HasPrefix(path, "/api/proxy/groups/members") {
		handleProxyGroupMembers(rec, req)
	} else {
		handleProxyNodes(rec, req)
	}
	var out M
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestProxyNodesCRUDKeepsGroupsInSync(t *testing.T) {
	p := proxyNodesEnv(t)

	code, out := proxyNodesCall(t, http.MethodGet, "/api/proxy/nodes", nil)
	if code != 200 || len(out["nodes"].([]any)) != 2 || len(out["groups"].([]any)) != 2 {
		t.Fatalf("list: %d %#v", code, out)
	}

	// create → joins every group
	code, out = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "name: c\ntype: ss\nserver: c.example\nport: 3\ncipher: aes-128-gcm\npassword: z\n"})
	if code != 200 {
		t.Fatalf("create: %d %#v", code, out)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "# test config") || !strings.Contains(s, "# first node") {
		t.Fatalf("comments lost:\n%s", s)
	}
	if !strings.Contains(s, "proxies: [a, b, DIRECT, c]") || !strings.Contains(s, "- a\n      - c") {
		t.Fatalf("new node not in both groups:\n%s", s)
	}
	// duplicate → 409
	if code, _ = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "name: c\ntype: ss\n"}); code != 409 {
		t.Fatalf("duplicate create: %d", code)
	}
	// group name as node → 409; garbage → 400
	if code, _ = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "name: g1\ntype: ss\n"}); code != 409 {
		t.Fatalf("group-named node: %d", code)
	}
	if code, _ = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "just text"}); code != 400 {
		t.Fatalf("garbage: %d", code)
	}

	// rename b → bb: groups follow
	code, out = proxyNodesCall(t, http.MethodPut, "/api/proxy/nodes", M{"name": "b", "yaml": "name: bb\ntype: ss\nserver: b.example\nport: 2\ncipher: aes-128-gcm\npassword: y\n"})
	if code != 200 || out["renamed"] != true {
		t.Fatalf("rename: %d %#v", code, out)
	}
	data, _ = os.ReadFile(p)
	if !strings.Contains(string(data), "proxies: [a, bb, DIRECT, c]") {
		t.Fatalf("rename not applied to group:\n%s", data)
	}

	// set members of g2 explicitly
	code, out = proxyNodesCall(t, http.MethodPut, "/api/proxy/groups/members", M{"group": "g2", "proxies": []string{"c", "DIRECT"}})
	if code != 200 {
		t.Fatalf("members: %d %#v", code, out)
	}
	if code, _ = proxyNodesCall(t, http.MethodPut, "/api/proxy/groups/members", M{"group": "g2", "proxies": []string{"nope"}}); code != 400 {
		t.Fatalf("unknown member: %d", code)
	}
	if code, _ = proxyNodesCall(t, http.MethodPut, "/api/proxy/groups/members", M{"group": "g2", "proxies": []string{}}); code != 400 {
		t.Fatalf("empty group: %d", code)
	}

	// delete c → removed from proxies and every group
	code, out = proxyNodesCall(t, http.MethodDelete, "/api/proxy/nodes?name=c", nil)
	if code != 200 {
		t.Fatalf("delete: %d %#v", code, out)
	}
	data, _ = os.ReadFile(p)
	s = string(data)
	if strings.Contains(s, "c.example") || strings.Contains(s, ", c]") || strings.Contains(s, "- c\n") {
		t.Fatalf("delete left traces:\n%s", s)
	}
	// g2 is now [DIRECT]; deleting the only member of a group is refused
	if code, _ = proxyNodesCall(t, http.MethodPut, "/api/proxy/groups/members", M{"group": "g2", "proxies": []string{"a"}}); code != 200 {
		t.Fatalf("members a: %d", code)
	}
	if code, _ = proxyNodesCall(t, http.MethodDelete, "/api/proxy/nodes?name=a", nil); code != 409 {
		t.Fatalf("delete sole member should be refused: %d", code)
	}
	if code, _ = proxyNodesCall(t, http.MethodDelete, "/api/proxy/nodes?name=zzz", nil); code != 404 {
		t.Fatalf("delete missing: %d", code)
	}
}
