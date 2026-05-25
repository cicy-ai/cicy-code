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
	"errors"
	"fmt"
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

// normalizeAgentID accepts either a short id ("w-10001") or a fully
// qualified id ("w-10001:main.0"). Returns the fully qualified form, which
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

// resolveSafePath turns a workspace-relative path into an absolute path
// guaranteed to live inside the workspace.
//
// Rules:
//   - empty / "." / "./" => workspace root
//   - absolute paths     => rejected (errPathAbsoluteForbidden)
//   - ".." escapes       => rejected (errPathOutsideWorkspace)
//   - symlinks whose real target lies outside workspace => rejected
//
// For non-existent paths (typical on write of a new file), the parent
// directory's real path is verified instead.
func resolveSafePath(workspace, requested string) (string, error) {
	clean := strings.TrimSpace(requested)
	if clean == "" || clean == "." || clean == "./" {
		return workspace, nil
	}
	clean = strings.TrimPrefix(clean, "./")
	if filepath.IsAbs(clean) {
		return "", errPathAbsoluteForbidden
	}
	joined := filepath.Clean(filepath.Join(workspace, clean))
	rel, err := filepath.Rel(workspace, joined)
	if err != nil {
		return "", errPathOutsideWorkspace
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errPathOutsideWorkspace
	}
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		// New file: verify the existing parent's real path is in workspace.
		parent := filepath.Dir(joined)
		if realParent, perr := filepath.EvalSymlinks(parent); perr == nil {
			if rel2, err2 := filepath.Rel(workspace, realParent); err2 != nil ||
				rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
				return "", errPathSymlinkEscape
			}
		}
		return joined, nil
	}
	if relReal, err := filepath.Rel(workspace, real); err != nil ||
		relReal == ".." || strings.HasPrefix(relReal, ".."+string(filepath.Separator)) {
		return "", errPathSymlinkEscape
	}
	return real, nil
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
	case errors.Is(err, errMissingAgentID):
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
// and resolves the requested path under it.
func fsResolve(r *http.Request, requested string) (abs, workspace string, err error) {
	workspace, err = agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		return "", "", err
	}
	abs, err = resolveSafePath(workspace, requested)
	return abs, workspace, err
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

	abs, workspace, err := fsResolve(r, q.Get("path"))
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
		Path:    workspaceRel(workspace, abs),
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
			sym   bool
		)
		if infoErr == nil {
			size = info.Size()
			mtime = info.ModTime().Unix()
			mode = info.Mode().String()
			sym = info.Mode()&fs.ModeSymlink != 0
		}
		out.Entries = append(out.Entries, fsEntry{
			Name:      name,
			IsDir:     de.IsDir(),
			Size:      size,
			Mtime:     mtime,
			Mode:      mode,
			IsSymlink: sym,
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
	abs, _, err := fsResolve(r, r.URL.Query().Get("path"))
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
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	abs, err := resolveSafePath(workspace, req.Path)
	if err != nil {
		fsErr(w, err)
		return
	}
	if isProtectedWritePath(workspace, abs) {
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
	abs, workspace, err := fsResolve(r, r.URL.Query().Get("path"))
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
		Path:  workspaceRel(workspace, abs),
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

// --- send-path ----------------------------------------------------------

// handleFsSendPath broadcasts a "code.send_path" event to every chat client
// connected to the given agent, mirroring the legacy code-server bridge but
// without requiring a code-server page context.
type fsSendPathRequest struct {
	Path     string `json:"path"`
	FileName string `json:"file_name,omitempty"`
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
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	// Accept either a workspace-relative path or a path already absolute
	// inside the workspace.
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
	agentID := normalizeAgentID(r.URL.Query().Get("agent_id"))
	hub.broadcast(agentID, ChatEvent{Type: "code.send_path", Data: M{
		"path":     abs,
		"fileName": strings.TrimSpace(req.FileName),
	}})
	J(w, M{"success": true, "path": abs})
}
