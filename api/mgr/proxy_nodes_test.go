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
	t.Setenv("CICY_MIHOMO_BIN", "/nonexistent/cicy-mihomo")  // no validation binary, no reload
	t.Setenv("CICY_MIHOMO_CONTROLLER", "http://127.0.0.1:1") // controller "not running" → no reload attempted
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
	if use, ok := out["groups"].([]any)[0].(map[string]any)["use"].([]any); !ok || use == nil {
		t.Fatalf("group.use must be an empty array, not null: %#v", out["groups"])
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

// The exit-IP probe must never touch a user group: it provisions its own
// listener + group + rule once, keeps the group in step with proxies:, and
// the private names stay out of the node/group listings.
func TestProbePathIsProvisionedOnceAndHidden(t *testing.T) {
	p := proxyNodesEnv(t)
	port, err := ensureMihomoProbePath()
	if err != nil || port == 0 {
		t.Fatalf("provision: port=%d err=%v", port, err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	for _, want := range []string{"name: cicy-probe\n", "name: cicy-probe-group\n", "- IN-NAME,cicy-probe,cicy-probe-group\n"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Index(s, "IN-NAME,cicy-probe,") > strings.Index(s, "MATCH,g1") {
		t.Fatalf("probe rule must come first:\n%s", s)
	}
	// idempotent
	port2, err := ensureMihomoProbePath()
	if err != nil || port2 != port {
		t.Fatalf("second call: port=%d err=%v", port2, err)
	}
	if strings.Count(string(mustRead(t, p)), "name: cicy-probe-group") != 1 {
		t.Fatal("probe group duplicated")
	}
	// hidden from the file-backed listing
	code, out := proxyNodesCall(t, http.MethodGet, "/api/proxy/nodes", nil)
	if code != 200 {
		t.Fatal(code)
	}
	for _, g := range out["groups"].([]any) {
		if g.(map[string]any)["name"] == "cicy-probe-group" {
			t.Fatal("probe group must be hidden")
		}
	}
	// a new node joins the probe group too, but the response doesn't mention it
	code, out = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "name: c\ntype: ss\nserver: c.example\nport: 3\ncipher: aes-128-gcm\npassword: z\n"})
	if code != 200 {
		t.Fatalf("create: %d %#v", code, out)
	}
	for _, g := range out["groups"].([]any) {
		if g == "cicy-probe-group" {
			t.Fatal("joined-groups must not list the probe group")
		}
	}
	if !strings.Contains(string(mustRead(t, p)), "      - c\n") {
		t.Fatalf("probe group did not receive the new node:\n%s", mustRead(t, p))
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChainsAreRelayGroupsThatFollowNodesAndGroups(t *testing.T) {
	p := proxyNodesEnv(t)
	call := func(method, path string, body any) (int, M) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		handleProxyChains(rec, httptest.NewRequest(method, path, &buf))
		var out M
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	// needs ≥2 hops, hops must be nodes
	if code, _ := call(http.MethodPost, "/api/proxy/chains", M{"name": "us-via-a", "hops": []string{"a"}}); code != 400 {
		t.Fatalf("1 hop: %d", code)
	}
	if code, out := call(http.MethodPost, "/api/proxy/chains", M{"name": "us-via-a", "hops": []string{"a", "g1"}}); code != 400 {
		t.Fatalf("group hop: %d %#v", code, out)
	}
	code, out := call(http.MethodPost, "/api/proxy/chains", M{"name": "us-via-a", "hops": []string{"a", "b"}})
	if code != 200 {
		t.Fatalf("create: %d %#v", code, out)
	}
	s := string(mustRead(t, p))
	if !strings.Contains(s, "type: relay") || !strings.Contains(s, "proxies: [a, b]") {
		t.Fatalf("relay missing:\n%s", s)
	}
	if !strings.Contains(s, "proxies: [a, b, DIRECT, us-via-a]") {
		t.Fatalf("chain must join select groups:\n%s", s)
	}
	// listed as a chain, not as a group
	code, out = proxyNodesCall(t, http.MethodGet, "/api/proxy/nodes", nil)
	if code != 200 || len(out["chains"].([]any)) != 1 || len(out["groups"].([]any)) != 2 {
		t.Fatalf("list: %d %#v", code, out)
	}
	// a new node is not pushed into the relay
	if code, out = proxyNodesCall(t, http.MethodPost, "/api/proxy/nodes", M{"yaml": "name: c\ntype: ss\nserver: c.example\nport: 3\ncipher: aes-128-gcm\npassword: z\n"}); code != 200 {
		t.Fatalf("node create: %d %#v", code, out)
	}
	if strings.Contains(string(mustRead(t, p)), "proxies: [a, b, c]") {
		t.Fatal("node joined the relay chain")
	}
	// rename + new route
	code, out = call(http.MethodPut, "/api/proxy/chains", M{"name": "us-via-a", "newName": "us-via-c", "hops": []string{"c", "b"}})
	if code != 200 || out["renamed"] != true {
		t.Fatalf("update: %d %#v", code, out)
	}
	s = string(mustRead(t, p))
	if !strings.Contains(s, "proxies: [c, b]") || !strings.Contains(s, "DIRECT, us-via-c, c]") || strings.Contains(s, "us-via-a") {
		t.Fatalf("rename not applied:\n%s", s)
	}
	// delete removes it everywhere
	if code, _ = call(http.MethodDelete, "/api/proxy/chains?name=us-via-c", nil); code != 200 {
		t.Fatal(code)
	}
	if strings.Contains(string(mustRead(t, p)), "us-via-c") {
		t.Fatal("chain left traces")
	}
}
