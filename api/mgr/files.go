// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Native files API.
//
// Replaces code-server with a lightweight Go fs layer. All public symbols
// use "agent" terminology; "pane" must not appear in this file.
//
// Routes registered in main.go:
//
//	GET  /api/fs/list   ?agent_id&path&hidden
//	GET  /api/fs/read   ?agent_id&path
//	POST /api/fs/write  ?agent_id   body={path,content,expected_mtime}
//	GET  /api/fs/stat   ?agent_id&path
//
// Path safety: every fs handler funnels through resolveSafePath, which
// rejects absolute paths, ".." escapes, and symlinks pointing outside the
// agent's workspace folder.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- errors --------------------------------------------------------------

var (
	errMissingAgentID            = errors.New("missing_agent_id")
	errInvalidRoot               = errors.New("invalid_root")
	errAgentWorkspaceUnavailable = errors.New("agent_workspace_unavailable")
	errPathOutsideWorkspace      = errors.New("path_outside_workspace")
	errPathAbsoluteForbidden     = errors.New("path_absolute_forbidden")
	errPathSymlinkEscape         = errors.New("path_symlink_escape")
	errPathWriteForbidden        = errors.New("path_write_forbidden")
	errFileTooLarge              = errors.New("file_too_large")
	errFileNotRegular            = errors.New("file_not_regular")
	errNotADirectory             = errors.New("not_a_directory")
)

// --- limits --------------------------------------------------------------

const (
	fsDefaultReadMaxBytes   = 5 * 1024 * 1024
	fsDefaultWriteMaxBytes  = 5 * 1024 * 1024
	fsDefaultListMaxEntries = 5000
)

func fsReadMaxBytes() int64 {
	return envInt64("CICY_FS_READ_MAX_BYTES", fsDefaultReadMaxBytes)
}

func fsWriteMaxBytes() int64 {
	return envInt64("CICY_FS_WRITE_MAX_BYTES", fsDefaultWriteMaxBytes)
}

func fsListMaxEntries() int {
	return int(envInt64("CICY_FS_LIST_MAX_ENTRIES", fsDefaultListMaxEntries))
}

func envInt64(key string, fallback int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// Default folders that are kept in the listing but UI is expected to leave
// them collapsed by default. Heavy or noisy directories.
var fsDefaultBlacklist = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".cache":       true,
}

// --- agent workspace lookup ----------------------------------------------

// normalizeAgentID accepts either a short id ("w-1001") or a fully
// qualified id ("w-1001:main.0"). Returns the fully qualified form, which
// is what agent_config indexes by.
func normalizeAgentID(id string) string {
	v := strings.TrimSpace(id)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, ":") {
		return v + ":main.0"
	}
	return v
}

// agentWorkspace returns the absolute, existing workspace folder bound to
// the given agent. Errors map to specific HTTP codes via fsHTTPCode.
func agentWorkspace(agentID string) (string, error) {
	id := normalizeAgentID(agentID)
	if id == "" {
		return "", errMissingAgentID
	}
	var ws string
	store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", id).Scan(&ws)
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return "", errAgentWorkspaceUnavailable
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		ws = strings.Replace(ws, "~", home, 1)
	}
	ws = os.ExpandEnv(ws)
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", errAgentWorkspaceUnavailable
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errAgentWorkspaceUnavailable
	}
	return abs, nil
}

// --- path safety ---------------------------------------------------------

// resolveSafePath turns a request path into an absolute path. Relative paths
// resolve against the agent's workspace; the confinement boundary, however, is
// the user's whole HOME dir — the editor is allowed to open/edit files across
// agent workspaces, projects and fork handoff summaries, not just its own
// workspace. Only paths that escape home (e.g. /etc) are rejected.
//
// Rules:
//   - empty / "." / "./" => workspace root
//   - relative paths     => joined onto workspace
//   - absolute paths     => accepted if they resolve INSIDE home
//   - anything (incl. a symlink target) escaping home => rejected
//
// For non-existent paths (typical on write of a new file), the parent
// directory's real path is verified instead.
// resolveSafePath resolves a WRITE/mutate path and confines it to the agent's
// workspace or the user's home. Used by rename / mkdir / touch / upload / write.
// Symlink escapes are rejected here — a mutating op must not reach outside the
// boundary even through an in-workspace symlink.
func resolveSafePath(workspace, requested string) (string, error) {
	return resolveFsPath(workspace, requested, true)
}

// resolveReadPath resolves a READ / list / open path with NO workspace
// confinement: the operator's file explorer may open anything the OS grants it
// read access to. The gate is OS read permission alone — the file API is behind
// the operator's auth token, and any agent living in a pane already has a shell,
// so a read boundary here contains nothing and only breaks the explorer (e.g. a
// symlink or an absolute path that points outside ~). Writes still go through
// resolveSafePath and stay confined.
func resolveReadPath(workspace, requested string) (string, error) {
	return resolveFsPath(workspace, requested, false)
}

func resolveFsPath(workspace, requested string, confine bool) (string, error) {
	clean := strings.TrimSpace(requested)
	if clean == "" || clean == "." || clean == "./" {
		return workspace, nil
	}
	home, _ := os.UserHomeDir()
	// Expand a leading ~ to the user's home dir so "~/.claude.json" opens (the
	// editor passes the literal path; without this it resolves relative to the
	// workspace and 404s).
	if home != "" && (clean == "~" || strings.HasPrefix(clean, "~/")) {
		clean = filepath.Join(home, strings.TrimPrefix(clean[1:], "/"))
	}
	clean = strings.TrimPrefix(clean, "./")
	var joined string
	if filepath.IsAbs(clean) {
		joined = filepath.Clean(clean)
	} else {
		joined = filepath.Clean(filepath.Join(workspace, clean))
	}
	// Confinement (writes only): allow anything inside the agent's own workspace
	// OR anywhere under the user's home dir. The home allowance lifts the
	// per-workspace editor limit (cross-agent files, projects, fork summaries);
	// keeping the workspace allowance covers workspaces mounted outside home.
	escapes := func(base, target string) bool {
		if base == "" {
			return true
		}
		rel, err := filepath.Rel(base, target)
		return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	outside := func(target string) bool {
		return escapes(workspace, target) && escapes(home, target)
	}
	if confine && outside(joined) {
		return "", errPathOutsideWorkspace
	}
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if confine {
			if realParent, perr := filepath.EvalSymlinks(filepath.Dir(joined)); perr == nil && outside(realParent) {
				return "", errPathSymlinkEscape
			}
		}
		return joined, nil
	}
	if confine && outside(real) {
		return "", errPathSymlinkEscape
	}
	return real, nil
}

// resolveSafeLeaf validates that `requested` lives inside the workspace WITHOUT
// following a final-component symlink, returning the literal leaf path. Delete
// and rename must act on the link node itself, not its target: resolveSafePath
// EvalSymlinks the whole path, so deleting a symlink with it would remove what
// the link points at and leave the link dangling. The parent chain is still
// resolved so a symlinked parent dir can't smuggle the leaf outside.
func resolveSafeLeaf(workspace, requested string) (string, error) {
	clean := strings.TrimSpace(requested)
	if clean == "" || clean == "." || clean == "./" {
		return "", errPathOutsideWorkspace
	}
	clean = strings.TrimPrefix(clean, "./")
	var joined string
	if filepath.IsAbs(clean) {
		joined = filepath.Clean(clean)
	} else {
		joined = filepath.Clean(filepath.Join(workspace, clean))
	}
	rel, err := filepath.Rel(workspace, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errPathOutsideWorkspace
	}
	// The leaf is left unresolved (so a leaf symlink is acted on as a link), but
	// the parent chain must still stay inside the workspace.
	if realParent, perr := filepath.EvalSymlinks(filepath.Dir(joined)); perr == nil {
		if rel2, rerr := filepath.Rel(workspace, realParent); rerr != nil ||
			rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
			return "", errPathSymlinkEscape
		}
	}
	return joined, nil
}

// isProtectedWritePath blocks writes to dirs we never want the UI to touch.
func isProtectedWritePath(workspace, abs string) bool {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil {
		return true
	}
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		switch p {
		case ".git", "node_modules":
			return true
		}
	}
	base := filepath.Base(abs)
	if strings.Contains(base, ".cicy-tmp-") || strings.HasSuffix(base, ".cicy-tmp") {
		return true
	}
	return false
}

// --- HTTP plumbing -------------------------------------------------------

func fsHTTPCode(err error) int {
	switch {
	case errors.Is(err, errMissingAgentID),
		errors.Is(err, errInvalidRoot):
		return http.StatusBadRequest
	case errors.Is(err, errAgentWorkspaceUnavailable):
		return http.StatusNotFound
	case errors.Is(err, errPathOutsideWorkspace),
		errors.Is(err, errPathAbsoluteForbidden),
		errors.Is(err, errPathSymlinkEscape),
		errors.Is(err, errPathWriteForbidden):
		return http.StatusForbidden
	case errors.Is(err, errFileTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errFileNotRegular),
		errors.Is(err, errNotADirectory):
		return http.StatusBadRequest
	case os.IsNotExist(err):
		return http.StatusNotFound
	case os.IsPermission(err):
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

func fsErr(w http.ResponseWriter, err error) {
	code := fsHTTPCode(err)
	msg := err.Error()
	// Don't leak absolute filesystem paths from os.* errors.
	switch {
	case os.IsNotExist(err):
		msg = "not_found"
	case os.IsPermission(err):
		msg = "permission_denied"
	case code == http.StatusInternalServerError:
		msg = "internal_error"
	}
	httpErr(w, code, msg)
}

// fsResolve pulls agent_id from the query, looks up the agent's workspace,
// and resolves the requested path under it. This is a READ resolver (search /
// list / open) — no workspace confinement; OS read permission is the gate.
func fsResolve(r *http.Request, requested string) (abs, workspace string, err error) {
	workspace, err = agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		return "", "", err
	}
	abs, err = resolveReadPath(workspace, requested)
	return abs, workspace, err
}

// fsRootInfo is the public shape returned by /api/fs/roots — one entry per
// allowed top-level folder the UI may browse.
type fsRootInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// fsRoots returns the absolute base paths the UI is allowed to browse for a
// given agent. "workspace" is always present; the others are stable per-user
// HOME-anchored paths that the explorer renders as separate sections, à la
// VS Code multi-root workspaces. Roots whose directory doesn't exist on disk
// are skipped — the UI simply doesn't render that section.
//
// Writes / deletes / renames stay scoped to the workspace root only; the
// other roots are read-only as far as the HTTP surface is concerned, so an
// agent UI sharing this server cannot exfiltrate or mutate files outside
// the workspace by guessing root IDs.
func fsRoots(agentID string) ([]fsRootInfo, error) {
	workspace, err := agentWorkspace(agentID)
	if err != nil {
		return nil, err
	}
	out := []fsRootInfo{{ID: "workspace", Label: "Workspace", Path: workspace}}
	home, _ := os.UserHomeDir()
	if home == "" {
		return out, nil
	}
	candidates := []struct {
		id, label, sub string
	}{
		{"knowledge", "知识库", "cicy-ai/knowledge"},
		{"memory", "Memory", "cicy-ai/memory"},
		{"cicy-ai", "cicy-ai", "cicy-ai"},
		{"projects", "Projects", "projects"},
		{"home", "Home", ""},
	}
	for _, c := range candidates {
		p := home
		if c.sub != "" {
			p = filepath.Join(home, c.sub)
		}
		// Hide a root whose base equals the workspace — avoids duplicate
		// sections when the agent's workspace literally is $HOME or
		// ~/projects (rare but possible during local dev).
		if filepath.Clean(p) == filepath.Clean(workspace) {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, fsRootInfo{ID: c.id, Label: c.label, Path: p})
		}
	}
	return out, nil
}

// fsResolveRoot is fsResolve generalized to any allowed root. The "root"
// query param selects which base path the request is anchored against.
// Defaults to "workspace" for backward compatibility.
func fsResolveRoot(r *http.Request, requested string) (abs, base string, err error) {
	return fsResolveRootMode(r, requested, true)
}

// fsResolveRootRead is fsResolveRoot for READ handlers (list / read / stat /
// download): it skips workspace confinement so the explorer can open any
// OS-readable path. Writes must keep using fsResolveRoot.
func fsResolveRootRead(r *http.Request, requested string) (abs, base string, err error) {
	return fsResolveRootMode(r, requested, false)
}

func fsResolveRootMode(r *http.Request, requested string, confine bool) (abs, base string, err error) {
	q := r.URL.Query()
	rootID := q.Get("root")
	if rootID == "" {
		rootID = "workspace"
	}
	roots, err := fsRoots(q.Get("agent_id"))
	if err != nil {
		return "", "", err
	}
	for _, root := range roots {
		if root.ID == rootID {
			abs, err = resolveFsPath(root.Path, requested, confine)
			return abs, root.Path, err
		}
	}
	return "", "", errInvalidRoot
}

// fsRootBase returns the absolute base directory for the request's "root"
// query param (default "workspace"). Used by handlers that need the root path
// itself (e.g. delete via resolveSafeLeaf) rather than a resolved sub-path.
func fsRootBase(r *http.Request) (string, error) {
	q := r.URL.Query()
	rootID := q.Get("root")
	if rootID == "" {
		rootID = "workspace"
	}
	roots, err := fsRoots(q.Get("agent_id"))
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if root.ID == rootID {
			return root.Path, nil
		}
	}
	return "", errInvalidRoot
}

// handleFsRoots — GET /api/fs/roots?agent_id=…
// Lists the available roots for the explorer's section bar.
func handleFsRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	roots, err := fsRoots(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	J(w, M{"roots": roots})
}

// --- list ---------------------------------------------------------------

type fsEntry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	Mtime     int64  `json:"mtime"`
	Mode      string `json:"mode"`
	IsSymlink bool   `json:"is_symlink,omitempty"`
}

type fsListResponse struct {
	Path      string    `json:"path"`
	Entries   []fsEntry `json:"entries"`
	Truncated bool      `json:"truncated,omitempty"`
}

func handleFsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	q := r.URL.Query()
	showHidden := q.Get("hidden") == "1"

	abs, base, err := fsResolveRootRead(r, q.Get("path"))
	if err != nil {
		fsErr(w, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	if !st.IsDir() {
		fsErr(w, errNotADirectory)
		return
	}

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		fsErr(w, err)
		return
	}

	max := fsListMaxEntries()
	out := fsListResponse{
		Path:    workspaceRel(base, abs),
		Entries: make([]fsEntry, 0, len(dirEntries)),
	}
	for _, de := range dirEntries {
		name := de.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, infoErr := de.Info()
		var (
			size  int64
			mtime int64
			mode  string
		)
		if infoErr == nil {
			size = info.Size()
			mtime = info.ModTime().Unix()
			mode = info.Mode().String()
		}
		isDir := de.IsDir()
		isSymlink := infoErr == nil && info.Mode()&fs.ModeSymlink != 0
		// Symlinks: DirEntry.Info()/IsDir() describe the link itself, not its
		// target — that's why links used to be hidden. Follow the link with
		// os.Stat so a link-to-dir is navigable and a link-to-file is sized like
		// its target; mark it so the UI can show it as a link. A broken or
		// out-of-workspace link still lists (a click on the latter 403s in
		// resolveSafePath, same as before).
		if isSymlink {
			if tinfo, terr := os.Stat(filepath.Join(abs, name)); terr == nil {
				isDir = tinfo.IsDir()
				size = tinfo.Size()
				mtime = tinfo.ModTime().Unix()
			}
		}
		out.Entries = append(out.Entries, fsEntry{
			Name:      name,
			IsDir:     isDir,
			Size:      size,
			Mtime:     mtime,
			Mode:      mode,
			IsSymlink: isSymlink,
		})
		if len(out.Entries) >= max {
			out.Truncated = true
			break
		}
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		if out.Entries[i].IsDir != out.Entries[j].IsDir {
			return out.Entries[i].IsDir
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	J(w, out)
}

// --- read ---------------------------------------------------------------

func handleFsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	abs, _, err := fsResolveRootRead(r, r.URL.Query().Get("path"))
	if err != nil {
		fsErr(w, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	if !st.Mode().IsRegular() {
		fsErr(w, errFileNotRegular)
		return
	}
	if st.Size() > fsReadMaxBytes() {
		fsErr(w, errFileTooLarge)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	headLen := 512
	if len(data) < headLen {
		headLen = len(data)
	}
	mime := http.DetectContentType(data[:headLen])
	textual := strings.HasPrefix(mime, "text/") ||
		strings.Contains(mime, "json") ||
		strings.Contains(mime, "javascript") ||
		strings.Contains(mime, "xml")

	w.Header().Set("X-File-Mtime", strconv.FormatInt(st.ModTime().Unix(), 10))
	w.Header().Set("X-File-Size", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("X-File-Mime", mime)
	if textual {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-File-Encoding", "utf-8")
		w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Encoding", "base64")
	w.Write([]byte(base64.StdEncoding.EncodeToString(data)))
}

// --- write --------------------------------------------------------------

type fsWriteRequest struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	ExpectedMtime int64  `json:"expected_mtime,omitempty"`
}

type fsWriteResponse struct {
	Mtime int64 `json:"mtime"`
	Size  int64 `json:"size"`
}

func handleFsWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsWriteRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if int64(len(req.Content)) > fsWriteMaxBytes() {
		fsErr(w, errFileTooLarge)
		return
	}
	// Resolve against the requested root (workspace/projects/skills/home) so
	// files under any root are writable, mirroring the read path.
	abs, base, err := fsResolveRoot(r, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	if isProtectedWritePath(base, abs) {
		fsErr(w, errPathWriteForbidden)
		return
	}

	if st, statErr := os.Stat(abs); statErr == nil {
		if !st.Mode().IsRegular() {
			fsErr(w, errFileNotRegular)
			return
		}
		if req.ExpectedMtime > 0 && st.ModTime().Unix() != req.ExpectedMtime {
			httpErr(w, http.StatusConflict,
				fmt.Sprintf("mtime_mismatch:%d", st.ModTime().Unix()))
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fsErr(w, err)
		return
	}
	tmp := abs + fsTempSuffix()
	if err := os.WriteFile(tmp, []byte(req.Content), 0o644); err != nil {
		os.Remove(tmp)
		fsErr(w, err)
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		fsErr(w, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	knowledgeMaybeCommitFsWrite(r, "edit", req.Path)
	J(w, fsWriteResponse{Mtime: st.ModTime().Unix(), Size: st.Size()})
}

func fsTempSuffix() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ".cicy-tmp-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return ".cicy-tmp-" + hex.EncodeToString(buf[:])
}

// --- stat ---------------------------------------------------------------

type fsStatResponse struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Mode  string `json:"mode"`
	Mime  string `json:"mime,omitempty"`
}

func handleFsStat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	abs, base, err := fsResolveRootRead(r, r.URL.Query().Get("path"))
	if err != nil {
		fsErr(w, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	resp := fsStatResponse{
		Name:  filepath.Base(abs),
		Path:  workspaceRel(base, abs),
		IsDir: st.IsDir(),
		Size:  st.Size(),
		Mtime: st.ModTime().Unix(),
		Mode:  st.Mode().String(),
	}
	if !st.IsDir() && st.Mode().IsRegular() && st.Size() > 0 {
		if f, err := os.Open(abs); err == nil {
			head := make([]byte, 512)
			n, _ := f.Read(head)
			f.Close()
			resp.Mime = http.DetectContentType(head[:n])
		}
	}
	J(w, resp)
}

// --- helpers ------------------------------------------------------------

func workspaceRel(workspace, abs string) string {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// --- runtime flags ------------------------------------------------------

// useNativeFiles tells the frontend whether to render the native FilesView
// or fall back to the legacy code-server iframe. Env override:
//   CICY_USE_NATIVE_FILES=0  → disabled
//   CICY_USE_NATIVE_FILES=1  → enabled (default)
func useNativeFiles() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CICY_USE_NATIVE_FILES")))
	if v == "" {
		return true
	}
	return v != "0" && v != "false" && v != "off" && v != "no"
}

func handleRuntimeFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	J(w, M{
		"use_native_files": useNativeFiles(),
	})
}

// --- download / upload --------------------------------------------------

// handleFsDownload streams a file as application/octet-stream with a
// Content-Disposition header so the browser saves it instead of trying to
// preview it. Distinct from /api/fs/read which always returns text or
// base64 — useful for large binaries (no base64 inflation) and to give
// images a "save as" affordance.
func handleFsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	// Root-aware: honor ?root= so downloads work for any allowed root, matching
	// the explorer's full context menu. Defaults to the workspace root.
	abs, _, err := fsResolveRootRead(r, r.URL.Query().Get("path"))
	if err != nil {
		fsErr(w, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	if !st.Mode().IsRegular() {
		fsErr(w, errFileNotRegular)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		fsErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("X-File-Mtime", strconv.FormatInt(st.ModTime().Unix(), 10))
	if r.URL.Query().Get("inline") == "1" {
		// Inline preview (the editor's <img>/<audio>/<video>): serve the real
		// media Content-Type so the element renders/streams, with inline (not
		// attachment) disposition. Range/seek + Content-Length are owned by
		// http.ServeContent. Explicit type map first — a stripped container may
		// lack a system mime table; "" lets ServeContent sniff by extension.
		if ct := inlinePreviewContentType(abs); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`inline; filename="%s"`, sanitizeFilename(filepath.Base(abs))))
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filepath.Base(abs))))
	}
	http.ServeContent(w, r, filepath.Base(abs), st.ModTime(), f)
}

// inlinePreviewContentType maps a file extension to the media Content-Type used
// for the editor's inline preview (?inline=1). Empty when the extension isn't a
// known previewable image/audio/video — the caller then lets ServeContent sniff.
func inlinePreviewContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".avif":
		return "image/avif"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".ogv":
		return "video/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".opus":
		return "audio/opus"
	default:
		return ""
	}
}

// sanitizeFilename strips characters that would break a Content-Disposition
// header. Quotes, newlines, and backslashes are the dangerous ones.
func sanitizeFilename(name string) string {
	r := strings.NewReplacer(`"`, "_", `\`, "_", "\n", "_", "\r", "_")
	return r.Replace(name)
}

// handleFsUpload accepts a multipart upload (form field "file") and writes
// it to the workspace path supplied via the ?path= query. Refuses overwrite
// unless ?overwrite=1.
func handleFsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	q := r.URL.Query()
	overwrite := q.Get("overwrite") == "1"
	maxBytes := fsWriteMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_multipart")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpErr(w, http.StatusBadRequest, "missing_file_field")
		return
	}
	defer file.Close()

	// Root-aware: honor ?root= so uploads can target any allowed root (projects
	// / skills / home), matching the explorer's full context menu. Defaults to
	// the workspace root.
	workspace, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	target := strings.TrimSpace(q.Get("path"))
	if target == "" || strings.HasSuffix(target, "/") {
		// Directory destination: keep the uploaded filename, sanitized.
		target = filepath.Join(target, sanitizeFilename(hdr.Filename))
	}
	abs, err := resolveSafePath(workspace, target)
	if err != nil {
		fsErr(w, err)
		return
	}
	if abs == workspace {
		httpErr(w, http.StatusBadRequest, "path_required")
		return
	}
	if isProtectedWritePath(workspace, abs) {
		fsErr(w, errPathWriteForbidden)
		return
	}
	if hdr.Size > maxBytes {
		fsErr(w, errFileTooLarge)
		return
	}
	if _, statErr := os.Stat(abs); statErr == nil && !overwrite {
		httpErr(w, http.StatusConflict, "exists")
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fsErr(w, err)
		return
	}
	tmp := abs + fsTempSuffix()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		fsErr(w, err)
		return
	}
	// Cap-enforced copy so a lying Content-Length can't still write past max.
	written, copyErr := io.Copy(out, io.LimitReader(file, maxBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmp)
		if copyErr != nil {
			fsErr(w, copyErr)
		} else {
			fsErr(w, closeErr)
		}
		return
	}
	if written > maxBytes {
		os.Remove(tmp)
		fsErr(w, errFileTooLarge)
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		fsErr(w, err)
		return
	}
	st, _ := os.Stat(abs)
	rel := workspaceRel(workspace, abs)
	resp := M{
		"success": true,
		"path":    rel,
		"size":    written,
	}
	if st != nil {
		resp["mtime"] = st.ModTime().Unix()
	}
	// docs/ is gitignored, so document uploads there produce no commit (expected);
	// uploads elsewhere under the knowledge root are committed.
	knowledgeMaybeCommitFsWrite(r, "upload", rel)
	J(w, resp)
}

// --- rename / delete / mkdir / touch ------------------------------------

type fsRenameRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func handleFsRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsRenameRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	workspace, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	from, err := resolveSafePath(workspace, req.From)
	if err != nil {
		fsErr(w, err)
		return
	}
	to, err := resolveSafePath(workspace, req.To)
	if err != nil {
		fsErr(w, err)
		return
	}
	if from == workspace || to == workspace {
		httpErr(w, http.StatusBadRequest, "cannot_rename_root")
		return
	}
	if isProtectedWritePath(workspace, from) || isProtectedWritePath(workspace, to) {
		fsErr(w, errPathWriteForbidden)
		return
	}
	if _, statErr := os.Stat(from); statErr != nil {
		fsErr(w, statErr)
		return
	}
	if _, statErr := os.Stat(to); statErr == nil {
		httpErr(w, http.StatusConflict, "destination_exists")
		return
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		fsErr(w, err)
		return
	}
	if err := os.Rename(from, to); err != nil {
		fsErr(w, err)
		return
	}
	knowledgeMaybeCommitFsWrite(r, "rename", req.From+" → "+req.To)
	J(w, M{
		"success": true,
		"from":    workspaceRel(workspace, from),
		"to":      workspaceRel(workspace, to),
	})
}

type fsDeleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

func handleFsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsDeleteRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	base, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	// resolveSafeLeaf (not resolveSafePath): delete must act on the path itself,
	// not a symlink's target — otherwise deleting a link removes what it points
	// at and orphans the link.
	abs, err := resolveSafeLeaf(base, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	if abs == base {
		httpErr(w, http.StatusBadRequest, "cannot_delete_root")
		return
	}
	if isProtectedWritePath(base, abs) {
		fsErr(w, errPathWriteForbidden)
		return
	}
	// Lstat, not Stat: a symlink (even one pointing at a directory) is a single
	// link node — remove the link, never recurse into or delete its target.
	st, statErr := os.Lstat(abs)
	if statErr != nil {
		fsErr(w, statErr)
		return
	}
	if st.IsDir() {
		// Require explicit recursive flag for non-empty directories so a
		// stray double-click can't take out a tree.
		entries, _ := os.ReadDir(abs)
		if len(entries) > 0 && !req.Recursive {
			httpErr(w, http.StatusConflict, "directory_not_empty")
			return
		}
		if err := os.RemoveAll(abs); err != nil {
			fsErr(w, err)
			return
		}
	} else {
		if err := os.Remove(abs); err != nil {
			fsErr(w, err)
			return
		}
	}
	knowledgeMaybeCommitFsWrite(r, "delete", req.Path)
	J(w, M{"success": true, "path": workspaceRel(base, abs)})
}

type fsMkdirRequest struct {
	Path string `json:"path"`
}

func handleFsMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsMkdirRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	workspace, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	abs, err := resolveSafePath(workspace, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	if abs == workspace {
		httpErr(w, http.StatusBadRequest, "path_required")
		return
	}
	if isProtectedWritePath(workspace, abs) {
		fsErr(w, errPathWriteForbidden)
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fsErr(w, err)
		return
	}
	J(w, M{"success": true, "path": workspaceRel(workspace, abs)})
}

type fsTouchRequest struct {
	Path string `json:"path"`
}

func handleFsTouch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsTouchRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	workspace, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	abs, err := resolveSafePath(workspace, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	if abs == workspace {
		httpErr(w, http.StatusBadRequest, "path_required")
		return
	}
	if isProtectedWritePath(workspace, abs) {
		fsErr(w, errPathWriteForbidden)
		return
	}
	if _, statErr := os.Stat(abs); statErr == nil {
		httpErr(w, http.StatusConflict, "exists")
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fsErr(w, err)
		return
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		fsErr(w, err)
		return
	}
	f.Close()
	knowledgeMaybeCommitFsWrite(r, "create", req.Path)
	J(w, M{"success": true, "path": workspaceRel(workspace, abs)})
}

// --- favorites ----------------------------------------------------------

// Favorites are persisted as a small JSON file under the workspace's .cicy/
// dir — keeps per-agent UI state together under .cicy/ instead of littering the
// workspace root, lives near the actual files, and avoids per-host central state.
const fsFavoritesFile = ".cicy/favorites.json"

// Legacy location (workspace root). Read as a fallback and removed on the next
// save so existing favorites migrate into .cicy/ without being lost.
const fsFavoritesFileLegacy = ".cicy-favorites.json"

type fsFavorite struct {
	Path  string `json:"path"`  // workspace-relative
	Name  string `json:"name"`  // display label
	Added int64  `json:"added"` // unix seconds
}

type fsFavoritesFileShape struct {
	Items []fsFavorite `json:"items"`
}

func favoritesPath(workspace string) string {
	return filepath.Join(workspace, fsFavoritesFile)
}

func favoritesPathLegacy(workspace string) string {
	return filepath.Join(workspace, fsFavoritesFileLegacy)
}

func loadFavorites(workspace string) (*fsFavoritesFileShape, error) {
	p := favoritesPath(workspace)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			// Fall back to the legacy workspace-root file if it's still around
			// (migrated to .cicy/ on the next save).
			if legacy, lerr := os.ReadFile(favoritesPathLegacy(workspace)); lerr == nil {
				data = legacy
			} else {
				return &fsFavoritesFileShape{Items: []fsFavorite{}}, nil
			}
		} else {
			return nil, err
		}
	}
	var out fsFavoritesFileShape
	if err := json.Unmarshal(data, &out); err != nil {
		return &fsFavoritesFileShape{Items: []fsFavorite{}}, nil
	}
	if out.Items == nil {
		out.Items = []fsFavorite{}
	}
	return &out, nil
}

func saveFavorites(workspace string, f *fsFavoritesFileShape) error {
	p := favoritesPath(workspace)
	if f.Items == nil {
		f.Items = []fsFavorite{}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + fsTempSuffix()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	// Migrated into .cicy/ — drop the legacy workspace-root file if present.
	_ = os.Remove(favoritesPathLegacy(workspace))
	return nil
}

func handleFsFavoritesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	favs, err := loadFavorites(workspace)
	if err != nil {
		fsErr(w, err)
		return
	}
	J(w, favs)
}

type fsFavoritesMutateRequest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

func handleFsFavoritesAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsFavoritesMutateRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	// Validate path is in workspace; reject favorites pointing outside.
	abs, err := resolveSafePath(workspace, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	rel := workspaceRel(workspace, abs)
	if rel == "" {
		httpErr(w, http.StatusBadRequest, "cannot_favorite_root")
		return
	}
	favs, err := loadFavorites(workspace)
	if err != nil {
		fsErr(w, err)
		return
	}
	for _, f := range favs.Items {
		if f.Path == rel {
			J(w, M{"success": true, "duplicate": true, "favorites": favs})
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(rel)
	}
	favs.Items = append(favs.Items, fsFavorite{
		Path:  rel,
		Name:  name,
		Added: time.Now().Unix(),
	})
	if err := saveFavorites(workspace, favs); err != nil {
		fsErr(w, err)
		return
	}
	J(w, M{"success": true, "favorites": favs})
}

func handleFsFavoritesRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsFavoritesMutateRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	rel := strings.TrimSpace(req.Path)
	favs, err := loadFavorites(workspace)
	if err != nil {
		fsErr(w, err)
		return
	}
	out := favs.Items[:0]
	for _, f := range favs.Items {
		if f.Path != rel {
			out = append(out, f)
		}
	}
	favs.Items = out
	if err := saveFavorites(workspace, favs); err != nil {
		fsErr(w, err)
		return
	}
	J(w, M{"success": true, "favorites": favs})
}

// --- send-path ----------------------------------------------------------

// handleFsSendPath broadcasts a "code.send_path" event to every chat client
// connected to the given agent, mirroring the legacy code-server bridge but
// without requiring a code-server page context. The page that issued the call
// supplies its page_client_id so Workspace.tsx's code.send_path handler can
// filter the event back to the right tab; the optional range turns the
// broadcast path into "path:l:c-l:c" so the agent's prompt carries the
// selection just like the VSIX flow did.
type fsSendPathRequest struct {
	Path          string `json:"path"`
	FileName      string `json:"file_name,omitempty"`
	PageClientID  string `json:"page_client_id,omitempty"`
	SelectionText string `json:"selection_text,omitempty"`
	Range         *struct {
		StartLine      int `json:"startLine"`
		StartCharacter int `json:"startCharacter"`
		EndLine        int `json:"endLine"`
		EndCharacter   int `json:"endCharacter"`
	} `json:"range,omitempty"`
}

func handleFsSendPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req fsSendPathRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		httpErr(w, http.StatusBadRequest, "missing_path")
		return
	}
	// Resolve against the request's "root" (workspace | projects | skills | home),
	// not always the agent workspace — otherwise an extra-root node's relative path
	// gets joined onto the workspace and the agent receives a wrong absolute path.
	// Mirrors every other root-aware fs op (list/read/rename/delete).
	workspace, err := fsRootBase(r)
	if err != nil {
		fsErr(w, err)
		return
	}
	requested := req.Path
	if filepath.IsAbs(requested) {
		rel, relErr := filepath.Rel(workspace, requested)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			fsErr(w, errPathOutsideWorkspace)
			return
		}
		requested = rel
	}
	abs, err := resolveSafePath(workspace, requested)
	if err != nil {
		fsErr(w, err)
		return
	}
	pathForAgent := abs
	if req.Range != nil &&
		req.Range.StartLine > 0 && req.Range.StartCharacter > 0 &&
		req.Range.EndLine > 0 && req.Range.EndCharacter > 0 {
		pathForAgent = fmt.Sprintf("%s:%d:%d-%d:%d",
			abs,
			req.Range.StartLine, req.Range.StartCharacter,
			req.Range.EndLine, req.Range.EndCharacter)
	}
	// Route the event back to the exact page that issued the call. We
	// previously broadcast across the whole agent bucket, but only one
	// page handles "send to my tmux" so direct delivery avoids the fanout
	// and dodges the master_agent_id vs short-id normalisation mismatch
	// that silently dropped messages.
	pageClientID := strings.TrimSpace(req.PageClientID)
	evt := ChatEvent{Type: "code.send_path", Data: M{
		"path":           pathForAgent,
		"fileName":       strings.TrimSpace(req.FileName),
		"selectionText":  req.SelectionText,
		"range":          req.Range,
		"page_client_id": pageClientID,
	}}
	if pageClientID != "" {
		if !hub.sendToClient(pageClientID, evt) {
			// Page disconnected between issuing the request and the
			// broadcast — surface so the UI knows nothing happened.
			httpErr(w, http.StatusNotFound, "page_client_not_connected")
			return
		}
	} else {
		// Fallback: legacy callers without page_client_id still get the old
		// broadcast behaviour (short-id bucket key).
		agentID := normalizeChatAgentValue(r.URL.Query().Get("agent_id"))
		hub.broadcast(agentID, evt)
	}
	J(w, M{"success": true, "path": pathForAgent})
}
