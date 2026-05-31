package skillcmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildTestRegistry stores one skill into a temp store and returns an
// httptest server in front of it.
func buildTestRegistry(t *testing.T, readToken, adminToken string) (*httptest.Server, *regStore) {
	t.Helper()
	dir := t.TempDir()
	store := newRegStore(dir)

	m := &Manifest{
		Name:        "demo",
		Version:     "1.0.0",
		Title:       "Demo",
		Description: "a demo skill",
		Category:    "dev",
		Author:      "tester",
		License:     "MIT",
		Runtime:     ManifestRuntime{Node: ">=18"},
		Entry:       "bin/demo",
	}
	zip := []byte("PK-fake-zip-bytes")
	m.Publish = &ManifestPublish{
		PublishedAt: "2026-01-01T00:00:00Z",
		SHA256:      sha256Hex(zip),
		Size:        int64(len(zip)),
		Source:      ManifestSource{Type: "local"},
	}
	if err := store.writeSkill(m, zip, map[string]string{"skill_md": "# Demo"}); err != nil {
		t.Fatalf("writeSkill: %v", err)
	}
	srv := newRegServer(store, readToken, adminToken, "")
	return httptest.NewServer(srv.handler()), store
}

func get(t *testing.T, url, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func TestRegistryReadAuth(t *testing.T) {
	ts, _ := buildTestRegistry(t, "SECRET", "ADMIN")
	defer ts.Close()

	// health is open
	if resp, _ := get(t, ts.URL+"/v1/health", ""); resp.StatusCode != 200 {
		t.Errorf("health: want 200, got %d", resp.StatusCode)
	}
	// list without token → 401
	if resp, _ := get(t, ts.URL+"/v1/skills", ""); resp.StatusCode != 401 {
		t.Errorf("list no-token: want 401, got %d", resp.StatusCode)
	}
	// wrong token → 403
	if resp, _ := get(t, ts.URL+"/v1/skills", "NOPE"); resp.StatusCode != 403 {
		t.Errorf("list wrong-token: want 403, got %d", resp.StatusCode)
	}
	// correct token → 200
	if resp, _ := get(t, ts.URL+"/v1/skills", "SECRET"); resp.StatusCode != 200 {
		t.Errorf("list good-token: want 200, got %d", resp.StatusCode)
	}
}

func TestRegistryOpenWhenNoToken(t *testing.T) {
	ts, _ := buildTestRegistry(t, "", "")
	defer ts.Close()
	if resp, _ := get(t, ts.URL+"/v1/skills", ""); resp.StatusCode != 200 {
		t.Errorf("open list: want 200, got %d", resp.StatusCode)
	}
}

func TestRegistryDetailRewritesDownloadURL(t *testing.T) {
	ts, _ := buildTestRegistry(t, "", "")
	defer ts.Close()

	_, body := get(t, ts.URL+"/v1/skills/demo", "")
	var env struct {
		Data SkillDetail `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	want := ts.URL + "/v1/skills/demo/1.0.0/download"
	if got := env.Data.Manifest.Publish.DownloadURL; got != want {
		t.Errorf("download_url: want %s, got %s", want, got)
	}
}

func TestRegistryDownloadServesZip(t *testing.T) {
	ts, store := buildTestRegistry(t, "", "")
	defer ts.Close()

	resp, body := get(t, ts.URL+"/v1/skills/demo/1.0.0/download", "")
	if resp.StatusCode != 200 {
		t.Fatalf("download: want 200, got %d", resp.StatusCode)
	}
	m, _, _ := store.readManifest("demo", "1.0.0")
	if sha256Hex(body) != m.Publish.SHA256 {
		t.Errorf("downloaded bytes sha mismatch")
	}
}

func TestRegistryLatestAndVersions(t *testing.T) {
	ts, _ := buildTestRegistry(t, "", "")
	defer ts.Close()

	// latest alias resolves
	if resp, _ := get(t, ts.URL+"/v1/skills/demo/latest", ""); resp.StatusCode != 200 {
		t.Errorf("latest: want 200, got %d", resp.StatusCode)
	}
	// versions list
	_, body := get(t, ts.URL+"/v1/skills/demo/versions", "")
	var env struct {
		Data struct {
			Latest   string         `json:"latest"`
			Versions []versionEntry `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Latest != "1.0.0" || len(env.Data.Versions) != 1 {
		t.Errorf("versions: got latest=%q n=%d", env.Data.Latest, len(env.Data.Versions))
	}
}

func TestRegistryYankRequiresAdmin(t *testing.T) {
	ts, _ := buildTestRegistry(t, "SECRET", "ADMIN")
	defer ts.Close()

	del := func(token string) int {
		req, _ := http.NewRequest("DELETE", ts.URL+"/v1/admin/skills/demo/1.0.0", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := del(""); code != 401 {
		t.Errorf("yank no-token: want 401, got %d", code)
	}
	if code := del("SECRET"); code != 403 { // read token is not the admin token
		t.Errorf("yank read-token: want 403, got %d", code)
	}
	if code := del("ADMIN"); code != 200 {
		t.Errorf("yank admin-token: want 200, got %d", code)
	}
	// after yank, the skill drops out of the listing
	_, body := get(t, ts.URL+"/v1/skills", "SECRET")
	var env struct {
		Data SkillListResp `json:"data"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Data.Total != 0 {
		t.Errorf("after yank: want 0 skills, got %d", env.Data.Total)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0", "1.0.0-rc1", 1}, // release > prerelease
		{"1.0.0-rc2", "1.0.0-rc1", 1},
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		if (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) || (got == 0) != (c.want == 0) {
			t.Errorf("compareSemver(%q,%q)=%d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}
