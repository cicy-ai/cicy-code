package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// useHubCredential points the transport at a hub-mode credential file.
func useHubCredential(t *testing.T, origin, token string) {
	t.Helper()
	oldDBDir := cicyDBDir
	cicyDBDir = filepath.Join(t.TempDir(), "db")
	t.Cleanup(func() { cicyDBDir = oldDBDir })
	if err := os.MkdirAll(cicyDBDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cred := cicyCloudCredential{Email: "alice@example.com", InstanceID: "code-aaaaaaaaaaaaaaaa", TeamID: "box",
		Token: token, Origin: origin, Mode: cicyCloudModeHub}
	if err := saveCiCyCloudCredential(cred); err != nil {
		t.Fatal(err)
	}
}

func TestHubModeMapsWorkerRoutesOntoHub(t *testing.T) {
	var hits atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer cwh_token" {
			t.Errorf("authorization = %q", got)
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/register":
			var tele M
			if err := json.NewDecoder(r.Body).Decode(&tele); err != nil || tele["platform"] == nil || tele["cpuCores"] == nil {
				t.Errorf("register must carry telemetry, got %#v (err %v)", tele, err)
			}
			_ = json.NewEncoder(w).Encode(M{"success": true, "ticket": "p.s", "wsUrl": "wss://hub.test/ws", "exp": 1})
		case "POST /api/heartbeat":
			var body M
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["tunnelUrl"] != "https://t.trycloudflare.com" || body["cpuCores"] == nil {
				t.Errorf("heartbeat must forward tunnel + telemetry, got %#v", body)
			}
			_ = json.NewEncoder(w).Encode(M{"success": true})
		case "GET /api/instances":
			_ = json.NewEncoder(w).Encode(M{"success": true, "owner": "alice@example.com", "instances": []M{
				{"instanceId": "code-aaaaaaaaaaaaaaaa", "name": "box", "online": true, "self": true, "cpuModel": "i5", "cpuCores": 12, "memoryTotalMB": 15891, "arch": "amd64",
					"proxyHost": "box.hub.test", "proxyAvailable": true, "resources": M{"cpu_usage_pct": 6.5},
					"agents": []M{{"agentId": "w-1001", "title": "Master", "agentType": "claude", "online": true, "model": "opus"}}},
				{"instanceId": "code-bbbbbbbbbbbbbbbb", "name": "", "online": false},
			}})
		default:
			t.Errorf("unexpected hub route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer hub.Close()
	// Any request reaching the cloud origin is a failure in hub mode.
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("cloud origin must not be called in hub mode: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer cloud.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", cloud.URL)
	useHubCredential(t, hub.URL, "cwh_token")

	var ticket struct {
		Ticket string `json:"ticket"`
		WSURL  string `json:"wsUrl"`
	}
	if err := cloudJSON(http.MethodPost, "/api/code/ws-ticket", "cwh_token", M{}, &ticket); err != nil {
		t.Fatalf("ws-ticket: %v", err)
	}
	if ticket.Ticket != "p.s" || ticket.WSURL != "wss://hub.test/ws" {
		t.Fatalf("ticket = %#v", ticket)
	}

	var instances struct {
		Instances []M `json:"instances"`
	}
	if err := cloudJSON(http.MethodGet, "/api/code/instances", "cwh_token", nil, &instances); err != nil {
		t.Fatalf("instances: %v", err)
	}
	if instances.Instances[0]["cpuModel"] != "i5" || instances.Instances[0]["cpuCores"] != float64(12) || instances.Instances[0]["arch"] != "amd64" {
		t.Fatalf("telemetry not mapped: %#v", instances.Instances[0])
	}
	if instances.Instances[0]["proxyHost"] != "box.hub.test" || instances.Instances[0]["proxyAvailable"] != float64(1) || instances.Instances[0]["resources"].(map[string]any)["cpu_usage_pct"] != 6.5 {
		t.Fatalf("gateway fields not mapped: %#v", instances.Instances[0])
	}
	if len(instances.Instances) != 2 || instances.Instances[0]["teamId"] != "box" || instances.Instances[0]["status"] != "online" ||
		instances.Instances[1]["teamId"] != "code-bbbbbbbb" || instances.Instances[1]["status"] != "offline" {
		t.Fatalf("instances = %#v", instances.Instances)
	}

	var agents struct {
		Agents []M `json:"agents"`
	}
	if err := cloudJSON(http.MethodGet, "/api/code/agents", "cwh_token", nil, &agents); err != nil {
		t.Fatalf("agents: %v", err)
	}
	if len(agents.Agents) != 1 {
		t.Fatalf("agents = %#v", agents.Agents)
	}
	a := agents.Agents[0]
	if a["instanceId"] != "code-aaaaaaaaaaaaaaaa" || a["agentId"] != "w-1001" || a["teamId"] != "box" || a["title"] != "Master" || a["instanceOnline"] != true {
		t.Fatalf("agent row = %#v", a)
	}

	// Heartbeat forwards to the hub; worker-only routes are no-ops or explicit websocket-only errors.
	if err := cloudJSON(http.MethodPost, "/api/code/instances/heartbeat", "cwh_token", M{"tunnelUrl": "https://t.trycloudflare.com", "tunnelToken": "tok"}, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := cloudJSON(http.MethodPost, "/api/code/agents", "cwh_token", M{"agents": []M{}}, nil); err != nil {
		t.Fatalf("agent directory should be a no-op: %v", err)
	}
	var poll struct {
		Messages []M `json:"messages"`
	}
	if err := cloudJSON(http.MethodGet, "/api/code/messages/poll?ack=msg-x", "cwh_token", nil, &poll); err != nil || len(poll.Messages) != 0 {
		t.Fatalf("poll: %v %#v", err, poll)
	}
	if err := cloudJSON(http.MethodPost, "/api/code/messages", "cwh_token", M{}, nil); err == nil || !strings.Contains(err.Error(), "websocket") {
		t.Fatalf("http send must be refused in hub mode, got %v", err)
	}
	if hits.Load() != 4 {
		t.Fatalf("hub hits = %d, want 4 (register, instances ×2, heartbeat)", hits.Load())
	}
}

func TestHubModeOnlyAppliesToTheHubToken(t *testing.T) {
	var cloudHits atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudHits.Add(1)
		_ = json.NewEncoder(w).Encode(M{"success": true, "instances": []M{}})
	}))
	defer cloud.Close()
	t.Setenv("CICY_CLOUD_ORIGIN", cloud.URL)
	useHubCredential(t, "https://hub.invalid", "cwh_token")
	// A different token (e.g. an older cloud account) keeps using cicy-cloud.
	if err := cloudJSON(http.MethodGet, "/api/code/instances", "cloud_token", nil, nil); err != nil {
		t.Fatalf("cloud call: %v", err)
	}
	if cloudHits.Load() != 1 {
		t.Fatalf("cloud hits = %d", cloudHits.Load())
	}
	if hubOriginForToken("") != "" || hubOriginForToken("cwh_token") != "https://hub.invalid" {
		t.Fatal("hubOriginForToken mismatch")
	}
}
