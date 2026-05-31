package skillcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Registry is a thin HTTP client for a skills registry (public or private).
type Registry struct {
	Name    string // source label ("public", "team-a", ...)
	BaseURL string
	Token   string // bearer token for private registries ("" = none)
	HTTP    *http.Client
}

func NewRegistry() *Registry {
	return &Registry{
		Name:    "public",
		BaseURL: registryURL(),
		Token:   strings.TrimSpace(os.Getenv("CICY_SKILLS_REGISTRY_TOKEN")),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// newRegistryFromSource builds a client for one configured source.
func newRegistryFromSource(s registrySource) *Registry {
	return &Registry{
		Name:    s.Name,
		BaseURL: s.URL,
		Token:   s.Token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// clientRegistries returns one client per configured source, in precedence
// order (later wins on name collision).
func clientRegistries() []*Registry {
	srcs := effectiveSources()
	out := make([]*Registry, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, newRegistryFromSource(s))
	}
	return out
}

// fetchJSON GETs the URL and decodes the envelope { ok, data, error }.
func (r *Registry) fetchJSON(path string, q url.Values, out interface{}) error {
	u := strings.TrimRight(r.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	var env struct {
		OK    bool             `json:"ok"`
		Data  json.RawMessage  `json:"data"`
		Error *RegistryAPIError `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("parse response: %w (body=%.200s)", err, string(body))
	}
	if !env.OK || env.Error != nil {
		if env.Error != nil {
			return fmt.Errorf("registry error: %s — %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("registry returned ok=false")
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode data: %w", err)
		}
	}
	return nil
}

// ListSkills GET /v1/skills with optional filters.
func (r *Registry) ListSkills(q, category, agent string, limit, offset int) (*SkillListResp, error) {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if category != "" {
		v.Set("category", category)
	}
	if agent != "" {
		v.Set("agent", agent)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprint(limit))
	}
	if offset > 0 {
		v.Set("offset", fmt.Sprint(offset))
	}
	var out SkillListResp
	if err := r.fetchJSON("/v1/skills", v, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDetail GET /v1/skills/:name (latest version).
func (r *Registry) GetDetail(name string) (*SkillDetail, error) {
	var out SkillDetail
	if err := r.fetchJSON("/v1/skills/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetVersion GET /v1/skills/:name/:version.
func (r *Registry) GetVersion(name, version string) (*SkillDetail, error) {
	var out SkillDetail
	if err := r.fetchJSON(
		"/v1/skills/"+url.PathEscape(name)+"/"+url.PathEscape(version), nil, &out,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadURL returns the manifest's publish.download_url, falling back to
// the registry redirect endpoint if missing.
func (r *Registry) DownloadURL(m *Manifest) string {
	if m.Publish != nil && m.Publish.DownloadURL != "" {
		return m.Publish.DownloadURL
	}
	return strings.TrimRight(r.BaseURL, "/") +
		"/v1/skills/" + url.PathEscape(m.Name) + "/" + url.PathEscape(m.Version) + "/download"
}
