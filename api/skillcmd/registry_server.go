package skillcmd

// registry_server.go — HTTP server for a self-hosted private skill registry.
//
// Speaks the same wire protocol as the public Cloudflare Worker
// (workers/skills-registry) so the existing Go client talks to it unchanged:
// the {ok,data,error} envelope, the /v1/* routes, CORS headers, and status
// codes all match. Two intentional differences from the Worker:
//
//   - GET /v1/skills/:name/:version/download returns the zip bytes directly
//     (200 application/zip) instead of a 302 to GitHub — this server stores
//     and serves its own assets.
//   - publish.download_url is rewritten per-request to point back at THIS
//     server's host (or --public-url), so clients reach the /download endpoint
//     on the same origin they queried — no hardcoded host needed.

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type regServer struct {
	store      *regStore
	readToken  string // "" = open (rely on network isolation)
	adminToken string // "" = admin endpoints disabled
	publicURL  string // "" = derive scheme+host from each request
	nowFn      func() string
}

func newRegServer(store *regStore, readToken, adminToken, publicURL string) *regServer {
	return &regServer{
		store:      store,
		readToken:  readToken,
		adminToken: adminToken,
		publicURL:  strings.TrimRight(publicURL, "/"),
		nowFn:      func() string { return time.Now().UTC().Format(time.RFC3339) },
	}
}

// ── envelope + CORS ──────────────────────────────────────────────────────────

func setCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	h.Set("Access-Control-Max-Age", "86400")
}

func writeOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(RegistryEnvelope{OK: true, Data: data})
}

func writeErr(w http.ResponseWriter, code, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(RegistryEnvelope{
		OK:    false,
		Error: &RegistryAPIError{Code: code, Message: msg},
	})
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func tokenEqual(provided, want string) bool {
	return subtle.ConstantTimeCompare([]byte(provided), []byte(want)) == 1
}

// requireAdmin checks the admin token; returns true if the request may proceed.
func (s *regServer) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.adminToken == "" {
		writeErr(w, "FORBIDDEN", "admin endpoints disabled (no --admin-token)", 403)
		return false
	}
	tok := bearerToken(r)
	if tok == "" {
		writeErr(w, "UNAUTHORIZED", "Authorization: Bearer <token> required", 401)
		return false
	}
	if !tokenEqual(tok, s.adminToken) {
		writeErr(w, "FORBIDDEN", "invalid admin token", 403)
		return false
	}
	return true
}

// ── handler wiring ───────────────────────────────────────────────────────────

func (s *regServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.hHealth)
	mux.HandleFunc("GET /v1/skills", s.hList)
	mux.HandleFunc("GET /v1/skills/{name}", s.hDetail)
	mux.HandleFunc("GET /v1/skills/{name}/versions", s.hVersions)
	mux.HandleFunc("GET /v1/skills/{name}/{version}", s.hManifest)
	mux.HandleFunc("GET /v1/skills/{name}/{version}/download", s.hDownload)
	mux.HandleFunc("GET /v1/skills/{name}/{version}/files/{file}", s.hFiles)
	mux.HandleFunc("GET /v1/categories", s.hCategories)
	mux.HandleFunc("POST /v1/admin/publish", s.hPublish)
	mux.HandleFunc("DELETE /v1/admin/skills/{name}/{version}", s.hYank)
	return s.middleware(mux)
}

// middleware applies CORS, handles OPTIONS preflight, and enforces the read
// token on read endpoints (everything under /v1 except /v1/health and the
// /v1/admin/* endpoints, which carry their own admin auth).
func (s *regServer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		p := r.URL.Path
		needsRead := s.readToken != "" &&
			strings.HasPrefix(p, "/v1/") &&
			p != "/v1/health" &&
			!strings.HasPrefix(p, "/v1/admin/")
		if needsRead {
			tok := bearerToken(r)
			if tok == "" {
				writeErr(w, "UNAUTHORIZED", "Authorization: Bearer <token> required", 401)
				return
			}
			if !tokenEqual(tok, s.readToken) {
				writeErr(w, "FORBIDDEN", "invalid token", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// baseURL returns the origin clients should use to reach this server: the
// configured --public-url, else derived from the request (proxy-aware).
func (s *regServer) baseURL(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = xf
	}
	return scheme + "://" + r.Host
}

// resolveVersion turns "latest" into the latest non-yanked version.
func (s *regServer) resolveVersion(name, ver string) string {
	if ver == "latest" {
		return s.store.latest(name)
	}
	return ver
}

// fillDownloadURL points the manifest's publish.download_url at this server's
// own /download endpoint (assets are stored & served locally).
func (s *regServer) fillDownloadURL(m *Manifest, r *http.Request) {
	if m.Publish == nil {
		return
	}
	m.Publish.DownloadURL = s.baseURL(r) +
		"/v1/skills/" + m.Name + "/" + m.Version + "/download"
}

// ── read handlers ────────────────────────────────────────────────────────────

func (s *regServer) hHealth(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]interface{}{
		"status":         "ok",
		"schema_version": "1",
		"self_hosted":    true,
		"time":           s.nowFn(),
	})
}

func (s *regServer) hList(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	cat := r.URL.Query().Get("category")
	agent := r.URL.Query().Get("agent")
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)

	var all []SkillSummary
	for _, name := range s.store.catalog() {
		lv := s.store.latest(name)
		if lv == "" {
			continue // fully yanked
		}
		m, _, err := s.store.readManifest(name, lv)
		if err != nil {
			continue
		}
		if !matchesFilters(m, q, cat, agent) {
			continue
		}
		all = append(all, summaryOf(m))
	}
	total := len(all)
	lo := offset
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + limit
	if limit <= 0 || hi > len(all) {
		hi = len(all)
	}
	writeOK(w, SkillListResp{
		Skills: all[lo:hi],
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

func (s *regServer) hDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	lv := s.store.latest(name)
	if lv == "" {
		writeErr(w, "NOT_FOUND", "skill not found: "+name, 404)
		return
	}
	m, files, err := s.store.readManifest(name, lv)
	if err != nil {
		writeErr(w, "NOT_FOUND", "skill not found: "+name, 404)
		return
	}
	s.fillDownloadURL(m, r)
	writeOK(w, SkillDetail{Manifest: *m, Files: files})
}

func (s *regServer) hVersions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vs := s.store.versions(name)
	if vs == nil {
		writeErr(w, "NOT_FOUND", "skill not found: "+name, 404)
		return
	}
	writeOK(w, map[string]interface{}{
		"name":     name,
		"latest":   s.store.latest(name),
		"versions": vs,
	})
}

func (s *regServer) hManifest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ver := s.resolveVersion(name, r.PathValue("version"))
	if ver == "" {
		writeErr(w, "NOT_FOUND", "skill not found: "+name, 404)
		return
	}
	m, files, err := s.store.readManifest(name, ver)
	if err != nil {
		writeErr(w, "NOT_FOUND", "version not found: "+name+"@"+ver, 404)
		return
	}
	if m.Yanked {
		writeErr(w, "GONE", "version yanked: "+name+"@"+ver, 410)
		return
	}
	s.fillDownloadURL(m, r)
	writeOK(w, SkillDetail{Manifest: *m, Files: files})
}

func (s *regServer) hDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ver := s.resolveVersion(name, r.PathValue("version"))
	if ver == "" {
		writeErr(w, "NOT_FOUND", "version not found: "+name+"@"+r.PathValue("version"), 404)
		return
	}
	m, _, err := s.store.readManifest(name, ver)
	if err != nil {
		writeErr(w, "NOT_FOUND", "version not found: "+name+"@"+ver, 404)
		return
	}
	if m.Yanked {
		writeErr(w, "GONE", "version yanked: "+name+"@"+ver, 410)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, s.store.zipPath(name, ver))
}

var validFileKeys = map[string]bool{
	"skill_md": true, "help_md": true, "tools_md": true, "readme": true,
}

func (s *regServer) hFiles(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	file := r.PathValue("file")
	if !validFileKeys[file] {
		writeErr(w, "BAD_REQUEST", "file must be one of skill_md,help_md,tools_md,readme", 400)
		return
	}
	ver := s.resolveVersion(name, r.PathValue("version"))
	if ver == "" {
		writeErr(w, "NOT_FOUND", "skill not found: "+name, 404)
		return
	}
	_, files, err := s.store.readManifest(name, ver)
	if err != nil {
		writeErr(w, "NOT_FOUND", "version not found: "+name+"@"+ver, 404)
		return
	}
	content, ok := files[file]
	if !ok {
		writeErr(w, "NOT_FOUND", "file not found: "+name+"@"+ver+"/"+file, 404)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}

func (s *regServer) hCategories(w http.ResponseWriter, r *http.Request) {
	cats := s.store.categories()
	type catEntry struct {
		Name   string   `json:"name"`
		Count  int      `json:"count"`
		Skills []string `json:"skills"`
	}
	var out []catEntry
	for name, skills := range cats {
		out = append(out, catEntry{Name: name, Count: len(skills), Skills: skills})
	}
	// stable order by category name
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	writeOK(w, map[string]interface{}{"categories": out})
}

// ── admin handlers ───────────────────────────────────────────────────────────

type regPublishReq struct {
	Manifest *Manifest         `json:"manifest"`
	Files    map[string]string `json:"files,omitempty"`
	ZipB64   string            `json:"zip_b64,omitempty"`
}

func (s *regServer) hPublish(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req regPublishReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, "BAD_JSON", "invalid JSON body: "+err.Error(), 400)
		return
	}
	if req.Manifest == nil {
		writeErr(w, "BAD_MANIFEST", "manifest required", 422)
		return
	}
	if msg := validateRegManifest(req.Manifest); msg != "" {
		writeErr(w, "BAD_MANIFEST", msg, 422)
		return
	}
	var zipBytes []byte
	if req.ZipB64 != "" {
		b, err := base64.StdEncoding.DecodeString(req.ZipB64)
		if err != nil {
			writeErr(w, "BAD_ZIP", "zip_b64 not valid base64: "+err.Error(), 422)
			return
		}
		zipBytes = b
	}
	// stamp publish metadata for the stored copy (download_url stays empty;
	// it's filled per-request on read).
	if req.Manifest.Publish == nil {
		req.Manifest.Publish = &ManifestPublish{}
	}
	if zipBytes != nil {
		req.Manifest.Publish.SHA256 = sha256Hex(zipBytes)
		req.Manifest.Publish.Size = int64(len(zipBytes))
	}
	if req.Manifest.Publish.PublishedAt == "" {
		req.Manifest.Publish.PublishedAt = s.nowFn()
	}
	req.Manifest.Publish.DownloadURL = ""
	if req.Manifest.Publish.Source.Type == "" {
		req.Manifest.Publish.Source.Type = "local"
	}
	if err := s.store.writeSkill(req.Manifest, zipBytes, req.Files); err != nil {
		writeErr(w, "INTERNAL", err.Error(), 500)
		return
	}
	writeOK(w, map[string]interface{}{
		"name":    req.Manifest.Name,
		"version": req.Manifest.Version,
	})
}

func (s *regServer) hYank(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	ver := r.PathValue("version")
	ok, err := s.store.yank(name, ver)
	if err != nil {
		writeErr(w, "INTERNAL", err.Error(), 500)
		return
	}
	if !ok {
		writeErr(w, "NOT_FOUND", name+"@"+ver+" not found", 404)
		return
	}
	writeOK(w, map[string]interface{}{
		"name":    name,
		"version": ver,
		"yanked":  true,
		"latest":  s.store.latest(name),
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func summaryOf(m *Manifest) SkillSummary {
	ca := m.CompatibleAgents
	if ca == nil {
		ca = []string{"*"}
	}
	sum := SkillSummary{
		Name:             m.Name,
		Version:          m.Version,
		Title:            m.Title,
		Description:      m.Description,
		Category:         m.Category,
		Tags:             m.Tags,
		Author:           m.Author,
		License:          m.License,
		CompatibleAgents: ca,
	}
	if m.Publish != nil {
		sum.Size = m.Publish.Size
		sum.PublishedAt = m.Publish.PublishedAt
	}
	if sum.Tags == nil {
		sum.Tags = []string{}
	}
	return sum
}

func matchesFilters(m *Manifest, q, cat, agent string) bool {
	if cat != "" && m.Category != cat {
		return false
	}
	if agent != "" {
		ok := false
		for _, a := range m.CompatibleAgents {
			if a == "*" || a == agent {
				ok = true
				break
			}
		}
		if len(m.CompatibleAgents) == 0 {
			ok = true // default ['*']
		}
		if !ok {
			return false
		}
	}
	if q != "" {
		hay := strings.ToLower(m.Name + " " + m.Title + " " + m.Description + " " + strings.Join(m.Tags, " "))
		if !strings.Contains(hay, q) {
			return false
		}
	}
	return true
}

// validateRegManifest does light validation for self-hosted publish. Unlike
// the public Worker, download_url is NOT required (assets are local) and may
// be empty/http.
func validateRegManifest(m *Manifest) string {
	if m.Name == "" {
		return "name required"
	}
	if !isValidSemver(m.Version) {
		return "invalid version: " + m.Version
	}
	if m.Title == "" {
		return "title required"
	}
	if m.Entry == "" || !strings.HasPrefix(m.Entry, "bin/") {
		return `entry must start with "bin/"`
	}
	return ""
}
