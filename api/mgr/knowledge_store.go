// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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
	Summary      string `json:"summary"` // one-line catalog blurb (for pointer/recall view)
	Tags         string `json:"tags"`
	SourcePane   string `json:"source_pane"`
	SourceKind   string `json:"source_kind"`
	OriginRef    string `json:"origin_ref"`
	Status       string `json:"status"`             // resolved maturity: draft|pending|canon|rejected|deprecated (folder-derived, overridable by frontmatter `status:`)
	Declared     string `json:"declared,omitempty"` // the frontmatter `status:` value (a maturity flag set in-place); when present it OVERRIDES the folder-derived status
	Domain       string `json:"domain"`             // canon folder (empty for inbox/archive)
	Path         string `json:"path"`
	VerifiedBy   string `json:"verified_by"`
	SupersededBy string `json:"superseded_by"`
	CreatedAt    string `json:"created_at"`
}

const (
	knowledgeStatusDraft      = "draft"      // 未成稿: author's WIP. NOT served by recall (canon-only). Specialist doesn't govern it.
	knowledgeStatusPending    = "pending"    // 待审: in _inbox, awaiting governance review.
	knowledgeStatusCanon      = "canon"      // 已确立事实: in a domain folder. recall serves these.
	knowledgeStatusRejected   = "rejected"   // 已弃: in _archive.
	knowledgeStatusDeprecated = "deprecated" // 已废弃: once-canon, now superseded; excluded from recall.
	knowledgeDefaultDomain    = "general"
)

// knowledgeNormalizeStatus maps a frontmatter `status:` value (incl. a few CN/EN
// aliases) to a canonical maturity, or "" if unrecognized (→ ignored, folder wins).
func knowledgeNormalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "draft", "草案", "未成稿", "wip":
		return knowledgeStatusDraft
	case "pending", "proposed", "待审", "待评审":
		return knowledgeStatusPending
	case "canon", "accepted", "stable", "已落地", "已确立":
		return knowledgeStatusCanon
	case "deprecated", "superseded", "已废弃", "过时":
		return knowledgeStatusDeprecated
	case "rejected", "已弃":
		return knowledgeStatusRejected
	}
	return ""
}

// knowledgeInPlaceStatus reports whether a status is a maturity FLAG the author
// may set in-place via frontmatter (without moving the file through the
// location-governed pipeline). draft/deprecated are demotions a doc can carry
// while still living in any folder; pending/canon/rejected are LOCATION states
// (governed by add/promote/reject), so they aren't settable as a bare flag.
func knowledgeInPlaceStatus(s string) bool {
	return s == knowledgeStatusDraft || s == knowledgeStatusDeprecated
}

// ── paths ───────────────────────────────────────────────────────────────

func knowledgeRootDir() string    { return filepath.Join(cicyRootDir, "knowledge") }
func knowledgeInboxDir() string   { return filepath.Join(knowledgeRootDir(), "_inbox") }
func knowledgeArchiveDir() string { return filepath.Join(knowledgeRootDir(), "_archive") }
func knowledgeDraftsDir() string  { return filepath.Join(knowledgeRootDir(), "_drafts") }
func knowledgeDocsDir() string    { return filepath.Join(knowledgeRootDir(), "docs") }

// knowledgeEnsureRoot creates the store skeleton so writes succeed and the fs
// root shows up in the FileExplorer even before the first entry.
func knowledgeEnsureRoot() error {
	loadKnowledgeGitTokenEnv()
	for _, d := range []string{knowledgeRootDir(), knowledgeInboxDir(), knowledgeArchiveDir(), knowledgeDraftsDir(), knowledgeDocsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// On a brand new install the store isn't a git repo yet — seed the embedded
	// README/.gitignore + git init + first commit (idempotent; fast no-op once
	// the repo exists). Best-effort, never blocks the store.
	knowledgeEnsureGitRepo()
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
	if d := knowledgeNormalizeStatus(k.Declared); d != "" {
		b.WriteString("status: " + d + "\n")
	}
	if t := knowledgeOneLine(k.Tags); t != "" {
		b.WriteString("tags: " + t + "\n")
	}
	if s := knowledgeOneLine(k.Summary); s != "" {
		b.WriteString("summary: " + s + "\n")
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
		Summary:      fm["summary"],
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
	// A frontmatter `status:` maturity flag OVERRIDES the folder-derived status, so
	// a doc can sit in a topic folder yet read as draft/deprecated (and thus drop
	// out of canon-only recall). Domain (topic) is unaffected — filing ≠ maturity.
	if d := knowledgeNormalizeStatus(fm["status"]); d != "" {
		k.Declared = d
		k.Status = d
	}
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
	case "_drafts":
		return knowledgeStatusDraft, ""
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

// insertKnowledge writes a new entry and returns the slug (id). By default it
// lands as a PENDING proposal in _inbox/<slug>.md; if k.Status == "draft" it
// lands in _drafts/<slug>.md instead (未成稿: the author's WIP, which recall —
// canon-only — won't serve and the specialist won't govern). If k.ID is set it's
// used as a stable slug (the memory hook passes one so repeated writes to the
// same memory file overwrite the same proposal); otherwise a unique slug is
// derived from the title. If an entry with the slug already exists in a GOVERNED
// location (anything but the destination), the write is skipped so a reviewed
// entry is never clobbered or re-proposed.
func insertKnowledge(k knowledgeRow) (string, error) {
	if err := knowledgeEnsureRoot(); err != nil {
		return "", err
	}
	asDraft := knowledgeNormalizeStatus(k.Status) == knowledgeStatusDraft
	destStatus := knowledgeStatusPending
	destDir := knowledgeInboxDir()
	if asDraft {
		destStatus = knowledgeStatusDraft
		destDir = knowledgeDraftsDir()
	}
	slug := strings.TrimSpace(k.ID)
	if slug == "" {
		slug = knowledgeUniqueSlug(knowledgeSlugify(firstNonEmpty(k.Title, "untitled")))
	} else {
		slug = knowledgeSlugify(slug)
	}
	if existing, ok := knowledgeFindByID(slug); ok && existing.Status != destStatus {
		return slug, nil // already governed elsewhere — don't clobber/re-propose
	}
	if strings.TrimSpace(k.CreatedAt) == "" {
		k.CreatedAt = time.Now().Format(time.RFC3339)
	}
	k.ID = slug
	k.Declared = "" // location (folder) carries the status for a fresh entry; no in-place flag
	path := filepath.Join(destDir, slug+".md")
	if err := os.WriteFile(path, []byte(knowledgeRenderFile(k)), 0o644); err != nil {
		return slug, err
	}
	switch {
	case strings.TrimSpace(k.SourceKind) == "memory-hook":
		knowledgeGitCommit(fmt.Sprintf("knowledge: inbox %s (memory-hook from %s)", slug, shortPaneID(k.SourcePane)), k.SourcePane)
	case asDraft:
		knowledgeGitCommit(fmt.Sprintf("knowledge: draft %s (by %s)", slug, knowledgeGitAuthor(k.SourcePane)), k.SourcePane)
	default:
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

// setKnowledgeStatus sets (or, with "", clears) an in-place maturity flag on an
// entry WITHOUT moving it through the location pipeline. Only draft/deprecated are
// valid flags — they demote an entry out of canon-only recall while it stays
// filed under its topic; pending/canon/rejected are LOCATION states, changed via
// add/promote/reject, not this call.
func setKnowledgeStatus(id, status, verifiedBy string) error {
	row, ok := knowledgeFindByID(id)
	if !ok {
		return os.ErrNotExist
	}
	norm := knowledgeNormalizeStatus(status)
	if strings.TrimSpace(status) != "" && !knowledgeInPlaceStatus(norm) {
		return fmt.Errorf("status must be draft|deprecated (or empty to clear); pending/canon/rejected are set by add/promote/reject")
	}
	row.Declared = norm // "" clears
	if v := normPaneID(strings.TrimSpace(verifiedBy)); v != "" {
		row.VerifiedBy = v
	}
	if err := os.WriteFile(row.Path, []byte(knowledgeRenderFile(row)), 0o644); err != nil {
		return err
	}
	label := norm
	if label == "" {
		label = "active"
	}
	knowledgeGitCommit(fmt.Sprintf("knowledge: status %s → %s (by %s)", id, label, knowledgeGitAuthor(verifiedBy)), verifiedBy)
	return nil
}

func knowledgeMove(id, destDir string, fmUpdates map[string]string) error {
	row, ok := knowledgeFindByID(id)
	if !ok {
		return os.ErrNotExist
	}
	row.Declared = "" // a governance move asserts the destination folder's status; drop any in-place flag
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
			hay := strings.ToLower(k.Title + "\n" + k.Tags + "\n" + k.Summary + "\n" + k.Body)
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
		// Pointer/index view: drop bodies so recall over a large KB returns a
		// small catalog (id/title/tags/summary/domain) instead of every full
		// entry — the caller then reads only the hits via GET /api/knowledge/<id>.
		if v := q.Get("view"); v == "index" || v == "pointer" {
			for i := range rows {
				rows[i].Body = ""
			}
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
		// status=draft → land in _drafts/ (未成稿); anything else → _inbox/ (pending).
		initStatus := knowledgeStatusPending
		if knowledgeNormalizeStatus(getString(req, "status")) == knowledgeStatusDraft {
			initStatus = knowledgeStatusDraft
		}
		id, err := insertKnowledge(knowledgeRow{
			Title:      title,
			Body:       body,
			Summary:    getString(req, "summary"),
			Tags:       getString(req, "tags"),
			SourcePane: getString(req, "source_pane"),
			SourceKind: getString(req, "source_kind"),
			OriginRef:  getString(req, "origin_ref"),
			Status:     initStatus,
		})
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if initStatus == knowledgeStatusPending {
			notifyKnowledgeSpecialistPending(id, title, getString(req, "source_pane"))
		}
		J(w, M{"id": id, "status": initStatus})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

// handleKnowledgeSpecialist gets/sets which pane governs the knowledge store.
// GET → {pane, default}. POST {pane} pins it (empty pane clears → default).
// Config-file backed (global.json), so it's NOT tied to a DB role query.
func handleKnowledgeSpecialist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		J(w, M{"pane": knowledgeSpecialistPaneID(), "default": knowledgeSpecialistDefaultPane})
	case http.MethodPost:
		var req M
		readBody(r, &req)
		pane, _ := req["pane"].(string)
		if err := setKnowledgeSpecialistPane(pane); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"pane": knowledgeSpecialistPaneID(), "default": knowledgeSpecialistDefaultPane})
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
		case "status", "set-status":
			// In-place maturity flag (draft|deprecated, or "" to clear) — no move.
			st := getString(req, "status")
			if err := setKnowledgeStatus(id, st, verifiedBy); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
			resolved := knowledgeNormalizeStatus(st)
			if resolved == "" {
				if k, ok := knowledgeFindByID(id); ok {
					resolved = k.Status
				}
			}
			J(w, M{"id": id, "status": resolved})
		default:
			httpErr(w, http.StatusBadRequest, "action must be promote|reject|supersede|status")
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
