package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The team knowledge store is FILE-backed: the files under ~/cicy-ai/knowledge
// ARE the source of truth, so humans and agents can govern it directly (and it
// shows up as a normal fs root in the FileExplorer). Governance is expressed by
// WHERE a file lives, not a DB status column:
//
//	~/cicy-ai/knowledge/
//	├── KNOWLEDGE.md      ← index (like MEMORY.md) — not an entry
//	├── <domain>/<slug>.md ← canon (promoted)
//	├── _inbox/<slug>.md   ← pending (hook proposals + harvest orphans)
//	├── _archive/<slug>.md ← rejected / superseded
//	└── docs/              ← uploaded enterprise docs (not entries)
//
// Each entry is a markdown file: frontmatter (name/tags/source/source_pane/
// origin_ref/date/verified_by/superseded_by) + body. recall is a keyword/tag
// grep over that markdown — deliberately NOT vector/RAG.

type knowledgeRow struct {
	ID           string `json:"id"` // slug = filename stem (unique across the store)
	Title        string `json:"title"`
	Body         string `json:"body"`
	Tags         string `json:"tags"`
	SourcePane   string `json:"source_pane"`
	SourceKind   string `json:"source_kind"`
	OriginRef    string `json:"origin_ref"`
	Status       string `json:"status"` // derived from folder: pending|canon|rejected
	Domain       string `json:"domain"` // canon folder (empty for inbox/archive)
	Path         string `json:"path"`
	VerifiedBy   string `json:"verified_by"`
	SupersededBy string `json:"superseded_by"`
	CreatedAt    string `json:"created_at"`
}

const (
	knowledgeStatusPending  = "pending"
	knowledgeStatusCanon    = "canon"
	knowledgeStatusRejected = "rejected"
	knowledgeDefaultDomain  = "general"
)

// ── paths ───────────────────────────────────────────────────────────────

func knowledgeRootDir() string    { return filepath.Join(cicyRootDir, "knowledge") }
func knowledgeInboxDir() string   { return filepath.Join(knowledgeRootDir(), "_inbox") }
func knowledgeArchiveDir() string { return filepath.Join(knowledgeRootDir(), "_archive") }
func knowledgeDocsDir() string    { return filepath.Join(knowledgeRootDir(), "docs") }

// knowledgeEnsureRoot creates the store skeleton so writes succeed and the fs
// root shows up in the FileExplorer even before the first entry.
func knowledgeEnsureRoot() error {
	for _, d := range []string{knowledgeRootDir(), knowledgeInboxDir(), knowledgeArchiveDir(), knowledgeDocsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ── slug / frontmatter ──────────────────────────────────────────────────

func knowledgeSlugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len([]rune(out)) > 80 {
		out = strings.Trim(string([]rune(out)[:80]), "-")
	}
	if out == "" {
		out = "k-" + aiGatewayShortID()
	}
	return out
}

func knowledgeOneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func knowledgeRenderFile(k knowledgeRow) string {
	kind := strings.TrimSpace(k.SourceKind)
	if kind == "" {
		kind = "manual"
	}
	date := strings.TrimSpace(k.CreatedAt)
	if date == "" {
		date = time.Now().Format(time.RFC3339)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + knowledgeOneLine(k.Title) + "\n")
	if t := knowledgeOneLine(k.Tags); t != "" {
		b.WriteString("tags: " + t + "\n")
	}
	b.WriteString("source: " + kind + "\n")
	if p := strings.TrimSpace(k.SourcePane); p != "" {
		b.WriteString("source_pane: " + p + "\n")
	}
	if o := knowledgeOneLine(k.OriginRef); o != "" {
		b.WriteString("origin_ref: " + o + "\n")
	}
	b.WriteString("date: " + date + "\n")
	if v := strings.TrimSpace(k.VerifiedBy); v != "" {
		b.WriteString("verified_by: " + v + "\n")
	}
	if s := strings.TrimSpace(k.SupersededBy); s != "" {
		b.WriteString("superseded_by: " + s + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(k.Body)
	if !strings.HasSuffix(k.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// knowledgeParseFile reads one entry file: its frontmatter + body, with status
// and domain inferred from where it lives relative to the knowledge root.
func knowledgeParseFile(path string) (knowledgeRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return knowledgeRow{}, err
	}
	fm, body := knowledgeSplitFrontmatter(string(raw))
	k := knowledgeRow{
		ID:           strings.TrimSuffix(filepath.Base(path), ".md"),
		Title:        firstNonEmpty(fm["name"], strings.TrimSuffix(filepath.Base(path), ".md")),
		Body:         body,
		Tags:         fm["tags"],
		SourceKind:   firstNonEmpty(fm["source"], "manual"),
		SourcePane:   fm["source_pane"],
		OriginRef:    fm["origin_ref"],
		VerifiedBy:   fm["verified_by"],
		SupersededBy: fm["superseded_by"],
		CreatedAt:    fm["date"],
		Path:         path,
	}
	rel, _ := filepath.Rel(knowledgeRootDir(), path)
	k.Status, k.Domain = knowledgeStatusForRel(filepath.ToSlash(rel))
	return k, nil
}

// knowledgeSplitFrontmatter parses a leading --- … --- block into a flat
// key→value map and returns the remaining body. Tolerates a missing block.
func knowledgeSplitFrontmatter(content string) (map[string]string, string) {
	fm := map[string]string{}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fm, content
	}
	for i := 1; i < end; i++ {
		line := lines[i]
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if key != "" {
				fm[key] = val
			}
		}
	}
	body := strings.Join(lines[end+1:], "\n")
	return fm, strings.TrimLeft(body, "\n")
}

func knowledgeStatusForRel(rel string) (status, domain string) {
	top := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		top = rel[:i]
	}
	switch top {
	case "_inbox":
		return knowledgeStatusPending, ""
	case "_archive":
		return knowledgeStatusRejected, ""
	default:
		return knowledgeStatusCanon, top
	}
}

// ── scan / lookup ───────────────────────────────────────────────────────

// knowledgeScanAll walks the store and returns every entry (skips docs/, the
// KNOWLEDGE.md index, and any non-.md or root-level file).
func knowledgeScanAll() []knowledgeRow {
	root := knowledgeRootDir()
	var out []knowledgeRow
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && d.Name() == "docs" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		// entries live in a subfolder; root-level files (KNOWLEDGE.md) are not entries.
		if !strings.Contains(rel, "/") {
			return nil
		}
		if row, perr := knowledgeParseFile(p); perr == nil {
			out = append(out, row)
		}
		return nil
	})
	return out
}

func knowledgeFindByID(id string) (knowledgeRow, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return knowledgeRow{}, false
	}
	for _, row := range knowledgeScanAll() {
		if row.ID == id {
			return row, true
		}
	}
	return knowledgeRow{}, false
}

func knowledgeUniqueSlug(base string) string {
	if _, ok := knowledgeFindByID(base); !ok {
		return base
	}
	for i := 2; ; i++ {
		cand := base + "-" + strconv.Itoa(i)
		if _, ok := knowledgeFindByID(cand); !ok {
			return cand
		}
	}
}

// ── write / governance ──────────────────────────────────────────────────

// insertKnowledge writes a PENDING proposal to _inbox/<slug>.md and returns the
// slug (id). If k.ID is set it's used as a stable slug (the memory hook passes
// one so repeated writes to the same memory file overwrite the same proposal);
// otherwise a unique slug is derived from the title. If an entry with the slug
// already exists OUTSIDE _inbox (already governed), the write is skipped so a
// reviewed entry is never clobbered or re-proposed.
func insertKnowledge(k knowledgeRow) (string, error) {
	if err := knowledgeEnsureRoot(); err != nil {
		return "", err
	}
	slug := strings.TrimSpace(k.ID)
	if slug == "" {
		slug = knowledgeUniqueSlug(knowledgeSlugify(firstNonEmpty(k.Title, "untitled")))
	} else {
		slug = knowledgeSlugify(slug)
	}
	if existing, ok := knowledgeFindByID(slug); ok && existing.Status != knowledgeStatusPending {
		return slug, nil // already governed elsewhere — don't clobber/re-propose
	}
	if strings.TrimSpace(k.CreatedAt) == "" {
		k.CreatedAt = time.Now().Format(time.RFC3339)
	}
	k.ID = slug
	path := filepath.Join(knowledgeInboxDir(), slug+".md")
	if err := os.WriteFile(path, []byte(knowledgeRenderFile(k)), 0o644); err != nil {
		return slug, err
	}
	if strings.TrimSpace(k.SourceKind) == "memory-hook" {
		knowledgeGitCommit(fmt.Sprintf("knowledge: inbox %s (memory-hook from %s)", slug, shortPaneID(k.SourcePane)), k.SourcePane)
	} else {
		knowledgeGitCommit(fmt.Sprintf("knowledge: add %s (by %s)", slug, knowledgeGitAuthor(k.SourcePane)), k.SourcePane)
	}
	return slug, nil
}

// promoteKnowledge moves an entry into a canon domain folder, stamping the
// reviewing pane. domain defaults to "general".
func promoteKnowledge(id, domain, verifiedBy string) error {
	domain = knowledgeSlugify(firstNonEmpty(domain, knowledgeDefaultDomain))
	if err := knowledgeMove(id, filepath.Join(knowledgeRootDir(), domain), map[string]string{"verified_by": verifiedBy}); err != nil {
		return err
	}
	knowledgeGitCommit(fmt.Sprintf("knowledge: promote %s → %s (by %s)", id, domain, knowledgeGitAuthor(verifiedBy)), verifiedBy)
	return nil
}

// rejectKnowledge moves an entry into _archive, stamping the reviewing pane.
func rejectKnowledge(id, verifiedBy string) error {
	if err := knowledgeMove(id, knowledgeArchiveDir(), map[string]string{"verified_by": verifiedBy}); err != nil {
		return err
	}
	knowledgeGitCommit(fmt.Sprintf("knowledge: reject %s (by %s)", id, knowledgeGitAuthor(verifiedBy)), verifiedBy)
	return nil
}

// supersedeKnowledge archives oldID, recording the entry that replaces it.
func supersedeKnowledge(oldID, newID, verifiedBy string) error {
	if err := knowledgeMove(oldID, knowledgeArchiveDir(), map[string]string{"verified_by": verifiedBy, "superseded_by": strings.TrimSpace(newID)}); err != nil {
		return err
	}
	knowledgeGitCommit(fmt.Sprintf("knowledge: supersede %s → %s (by %s)", oldID, strings.TrimSpace(newID), knowledgeGitAuthor(verifiedBy)), verifiedBy)
	return nil
}

func knowledgeMove(id, destDir string, fmUpdates map[string]string) error {
	row, ok := knowledgeFindByID(id)
	if !ok {
		return os.ErrNotExist
	}
	if v, ok := fmUpdates["verified_by"]; ok {
		row.VerifiedBy = normPaneID(strings.TrimSpace(v))
	}
	if v, ok := fmUpdates["superseded_by"]; ok {
		row.SupersededBy = strings.TrimSpace(v)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	newPath := filepath.Join(destDir, row.ID+".md")
	if err := os.WriteFile(newPath, []byte(knowledgeRenderFile(row)), 0o644); err != nil {
		return err
	}
	if filepath.Clean(newPath) != filepath.Clean(row.Path) {
		_ = os.Remove(row.Path)
	}
	return nil
}

// ── query / recall ──────────────────────────────────────────────────────

type knowledgeFilter struct {
	Status string
	Tag    string
	Q      string
	Domain string
	Limit  int
}

func listKnowledge(f knowledgeFilter) ([]knowledgeRow, error) {
	rows := knowledgeScanAll()
	status := strings.TrimSpace(f.Status)
	tag := strings.ToLower(strings.TrimSpace(f.Tag))
	q := strings.ToLower(strings.TrimSpace(f.Q))
	domain := strings.TrimSpace(f.Domain)

	out := make([]knowledgeRow, 0, len(rows))
	for _, k := range rows {
		if status != "" && k.Status != status {
			continue
		}
		if domain != "" && k.Domain != domain {
			continue
		}
		if tag != "" && !knowledgeHasTag(k.Tags, tag) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(k.Title + "\n" + k.Tags + "\n" + k.Body)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, k)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func knowledgeHasTag(tags, want string) bool {
	for _, t := range strings.FieldsFunc(strings.ToLower(tags), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if t == want {
			return true
		}
	}
	return false
}

func getKnowledge(id string) (knowledgeRow, bool, error) {
	k, ok := knowledgeFindByID(id)
	return k, ok, nil
}

// ── HTTP handlers (routes unchanged; file-backed) ───────────────────────

func handleKnowledge(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		rows, err := listKnowledge(knowledgeFilter{
			Status: q.Get("status"),
			Tag:    q.Get("tag"),
			Q:      q.Get("q"),
			Domain: q.Get("domain"),
		})
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"knowledge": rows})
	case http.MethodPost:
		var req M
		readBody(r, &req)
		title, _ := req["title"].(string)
		body, _ := req["body"].(string)
		if strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "" {
			httpErr(w, http.StatusBadRequest, "title and body required")
			return
		}
		id, err := insertKnowledge(knowledgeRow{
			Title:      title,
			Body:       body,
			Tags:       getString(req, "tags"),
			SourcePane: getString(req, "source_pane"),
			SourceKind: getString(req, "source_kind"),
			OriginRef:  getString(req, "origin_ref"),
		})
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"id": id, "status": knowledgeStatusPending})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

func handleKnowledgeByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Path[len("/api/knowledge/"):])
	if id == "" {
		handleKnowledge(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		k, ok, _ := getKnowledge(id)
		if !ok {
			httpErr(w, http.StatusNotFound, "knowledge "+id+" not found")
			return
		}
		J(w, k)
	case http.MethodPatch:
		var req M
		readBody(r, &req)
		if _, ok := knowledgeFindByID(id); !ok {
			httpErr(w, http.StatusNotFound, "knowledge "+id+" not found")
			return
		}
		action := strings.ToLower(strings.TrimSpace(getString(req, "action")))
		verifiedBy := getString(req, "verified_by")
		switch action {
		case "promote":
			if err := promoteKnowledge(id, getString(req, "domain"), verifiedBy); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			J(w, M{"id": id, "status": knowledgeStatusCanon})
		case "reject":
			if err := rejectKnowledge(id, verifiedBy); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			J(w, M{"id": id, "status": knowledgeStatusRejected})
		case "supersede":
			newID := strings.TrimSpace(getString(req, "superseded_by"))
			if newID == "" {
				httpErr(w, http.StatusBadRequest, "superseded_by required for supersede")
				return
			}
			if err := supersedeKnowledge(id, newID, verifiedBy); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			J(w, M{"id": id, "status": knowledgeStatusRejected, "superseded_by": newID})
		default:
			httpErr(w, http.StatusBadRequest, "action must be promote|reject|supersede")
		}
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET or PATCH")
	}
}

// getString reads a string field from a decoded JSON object, tolerating absence.
func getString(m M, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
