// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Todo is one task. ALL todos live in the master pane's workspace
// (`<masterWs>/.cicy/todos.yaml`) regardless of who created them. The PaneID
// field records which worker owns the todo; workers can only read/mutate
// todos with PaneID == their own short pane id, while the master pane sees
// every todo and may filter by PaneID.
//
// Status state machine: todo → test → done, with dropped as a terminal "abandoned" state.
type Todo struct {
	ID        string    `yaml:"id" json:"id"`
	Title     string    `yaml:"title" json:"title"`
	Status    string    `yaml:"status" json:"status"`
	PaneID    string    `yaml:"pane_id,omitempty" json:"pane_id,omitempty"`
	CreatorID string    `yaml:"creator_id,omitempty" json:"creator_id,omitempty"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
}

type todoFile struct {
	Todos []Todo `yaml:"todos"`
}

var (
	todoMu          sync.Mutex
	todoValidStatus = map[string]bool{
		"todo": true, "test": true, "done": true, "dropped": true,
	}
)

// ── workspace + identity helpers ───────────────────────────────────────────

// masterWorkspaceForTodo returns the workspace directory of the master pane
// (w-1001). All todo storage routes through this single workspace.
func masterWorkspaceForTodo() string {
	return paneWorkspace(primaryWorkerSession)
}

// isMasterPaneID reports whether the given short or full pane id refers to
// the master pane (w-1001).
func isMasterPaneID(paneID string) bool {
	return shortPaneID(normPaneID(strings.TrimSpace(paneID))) == primaryWorkerSession
}

// requesterPaneID returns the short pane id of the caller, derived from the
// X-Agent-Show-Id header. Returns empty string when the header is absent —
// callers MUST provide it. We deliberately do NOT fall back to the master
// pane: a silent default makes it too easy for a script or buggy client
// without the header to create todos under w-1001 by accident.
func requesterPaneID(r *http.Request) string {
	for _, h := range []string{"X-Agent-Show-Id", "X-Agent-Show-ID", "X_AGENT_SHOW_ID"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return shortPaneID(normPaneID(v))
		}
	}
	return ""
}

func todoFilePath(workspace string) string {
	// Single store relocated to ~/cicy-ai/db/todos.yaml (was
	// <masterWs>/.cicy/todos.yaml) so it lives alongside the other syncable
	// config under ~/cicy-ai/db. The workspace arg is now unused but kept for
	// call-site compatibility.
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "db", "todos.yaml")
}

func loadTodos(workspace string) ([]Todo, error) {
	path := todoFilePath(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Todo{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Todo{}, nil
	}
	// Normalise timestamps written by the Python migration script:
	// "2026-05-25 02:59:48+00:00" → "2026-05-25T02:59:48+00:00"
	data = normaliseTodoTimestamps(data)
	var f todoFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Todos == nil {
		return []Todo{}, nil
	}
	// One-time migration: legacy random ids ("t-<unix>-<hex>") are replaced
	// with stable, monotonic integer ids. Idempotent — once every id is a
	// positive integer this is a no-op and nothing is rewritten.
	changed := migrateTodoIDs(f.Todos)
	// The "doing" status was retired (lifecycle is now todo → test → done).
	// Fold any leftover "doing" todos back to "todo" so they stay visible
	// instead of falling into an unknown bucket. Idempotent.
	if migrateDoingStatus(f.Todos) {
		changed = true
	}
	if changed {
		_ = saveTodos(workspace, f.Todos)
	}
	return f.Todos, nil
}

// migrateDoingStatus rewrites the retired "doing" status to "todo". Returns
// true if at least one todo changed.
func migrateDoingStatus(todos []Todo) bool {
	changed := false
	for i := range todos {
		if todos[i].Status == "doing" {
			todos[i].Status = "todo"
			changed = true
		}
	}
	return changed
}

// migrateTodoIDs assigns sequential integer ids to any todo whose id is not
// already a positive integer (legacy "t-<unix>-<hex>" ids). Legacy todos are
// numbered by created_at ascending, continuing after the highest existing
// numeric id so any previously-assigned numbers stay stable. Returns true if
// at least one id changed.
func migrateTodoIDs(todos []Todo) bool {
	max := 0
	var legacy []int
	for i, t := range todos {
		if n, err := strconv.Atoi(t.ID); err == nil && n > 0 {
			if n > max {
				max = n
			}
		} else {
			legacy = append(legacy, i)
		}
	}
	if len(legacy) == 0 {
		return false
	}
	sort.SliceStable(legacy, func(a, b int) bool {
		return todos[legacy[a]].CreatedAt.Before(todos[legacy[b]].CreatedAt)
	})
	for _, i := range legacy {
		max++
		todos[i].ID = strconv.Itoa(max)
	}
	return true
}

// normaliseTodoTimestamps replaces "YYYY-MM-DD HH:MM:SS" with
// "YYYY-MM-DDTHH:MM:SS" so the Go yaml parser can decode time.Time.
// Python's yaml.safe_dump emits datetime objects in the space-separated
// form which Go's yaml.v3 cannot parse and which is also ambiguous YAML
// (the value contains colons, triggering "mapping values are not allowed
// in this context").
var todoTsRE = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}:\d{2})`)

func normaliseTodoTimestamps(data []byte) []byte {
	return todoTsRE.ReplaceAll(data, []byte("${1}T${2}"))
}

func saveTodos(workspace string, todos []Todo) error {
	dir := workspaceRuntimeDir(workspace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if todos == nil {
		todos = []Todo{}
	}
	data, err := yaml.Marshal(todoFile{Todos: todos})
	if err != nil {
		return err
	}
	final := todoFilePath(workspace)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// nextTodoID returns the next auto-incrementing integer id as a string,
// computed as max(existing numeric ids)+1. All todos live in one master file,
// so a single global counter is sufficient and ids never collide or shift.
func nextTodoID(todos []Todo) string {
	max := 0
	for _, t := range todos {
		if n, err := strconv.Atoi(t.ID); err == nil && n > max {
			max = n
		}
	}
	return strconv.Itoa(max + 1)
}

// resolveTodoID matches a user-supplied id or unique prefix.
// Returns the index, or an error describing the ambiguity.
func resolveTodoID(todos []Todo, idOrPrefix string) (int, error) {
	idOrPrefix = strings.TrimSpace(idOrPrefix)
	if idOrPrefix == "" {
		return -1, fmt.Errorf("id required")
	}
	exact := -1
	matches := []int{}
	for i, t := range todos {
		if t.ID == idOrPrefix {
			exact = i
		}
		if strings.HasPrefix(t.ID, idOrPrefix) {
			matches = append(matches, i)
		}
	}
	if exact >= 0 {
		return exact, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return -1, fmt.Errorf("todo not found: %s", idOrPrefix)
	}
	return -1, fmt.Errorf("ambiguous id %q matches %d todos", idOrPrefix, len(matches))
}

// sortTodos in-place: todo first, then test, done, dropped; within each
// bucket by updated_at desc.
func sortTodos(todos []Todo) {
	rank := func(s string) int {
		switch s {
		case "todo":
			return 0
		case "test":
			return 1
		case "done":
			return 2
		case "dropped":
			return 3
		}
		return 4
	}
	sort.SliceStable(todos, func(i, j int) bool {
		ri, rj := rank(todos[i].Status), rank(todos[j].Status)
		if ri != rj {
			return ri < rj
		}
		return todos[i].UpdatedAt.After(todos[j].UpdatedAt)
	})
}

// requireMasterWorkspaceForTodo loads (and ensures we have a path to) the
// master workspace, returning false on error after writing the HTTP response.
func requireMasterWorkspaceForTodo(w http.ResponseWriter) (string, bool) {
	ws := masterWorkspaceForTodo()
	if ws == "" {
		httpErr(w, 500, "master workspace ("+primaryWorkerSession+") not configured")
		return "", false
	}
	return ws, true
}

// ── handlers ───────────────────────────────────────────────────────────────

// GET /api/todo/list?[pane_id=<filter>][&status=][&q=][&all_agents=true]
//
// Authorization model:
//   - Requester is identified via the X-Agent-Show-Id header.
//   - Master pane (w-1001) sees every todo; the optional pane_id query
//     parameter narrows the result to one worker. all_agents=true is an
//     alias for "no filter, return everything" and is the default for
//     master.
//   - Any other requester is treated as a worker and the result is
//     restricted to todos whose PaneID equals the requester's pane id,
//     regardless of the pane_id query parameter.
func handleTodoList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	q := r.URL.Query()
	requester := requesterPaneID(r)
	if requester == "" {
		httpErr(w, 400, "X-Agent-Show-Id header required")
		return
	}
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	kw := strings.ToLower(strings.TrimSpace(q.Get("q")))
	paneFilter := shortPaneID(normPaneID(strings.TrimSpace(q.Get("pane_id"))))
	allAgents := strings.ToLower(strings.TrimSpace(q.Get("all_agents"))) == "true"

	todoMu.Lock()
	defer todoMu.Unlock()

	ws, ok := requireMasterWorkspaceForTodo(w)
	if !ok {
		return
	}
	all, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	out := all[:0:0]
	for _, t := range all {
		// Authorization filter: workers see only their own.
		if !isMasterPaneID(requester) {
			if t.PaneID != requester {
				continue
			}
		} else {
			// Master can optionally filter by pane.
			if !allAgents && paneFilter != "" && t.PaneID != paneFilter {
				continue
			}
		}
		if status != "" && status != "all" && t.Status != status {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(t.Title), kw) {
			continue
		}
		out = append(out, t)
	}
	sortTodos(out)
	J(w, M{"todos": out})
}

// GET /api/todo/counts
//
// Returns counts scoped to the requester (master = global, worker = own).
func handleTodoCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	requester := requesterPaneID(r)
	if requester == "" {
		httpErr(w, 400, "X-Agent-Show-Id header required")
		return
	}
	q := r.URL.Query()
	paneFilter := shortPaneID(normPaneID(strings.TrimSpace(q.Get("pane_id"))))

	todoMu.Lock()
	defer todoMu.Unlock()

	ws, ok := requireMasterWorkspaceForTodo(w)
	if !ok {
		return
	}
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	counts := M{"all": 0, "todo": 0, "test": 0, "done": 0, "dropped": 0}
	for _, t := range todos {
		if !isMasterPaneID(requester) {
			if t.PaneID != requester {
				continue
			}
		} else if paneFilter != "" && t.PaneID != paneFilter {
			continue
		}
		counts["all"] = counts["all"].(int) + 1
		if _, ok := counts[t.Status]; ok {
			counts[t.Status] = counts[t.Status].(int) + 1
		}
	}
	J(w, counts)
}

// POST /api/todo/add  body: {title, [pane_id], [creator_id]}
//
// The new todo is always saved into the master workspace. PaneID is set to:
//   - the requester's pane id (default), or
//   - the body's pane_id when the requester is the master pane (lets the UI
//     create a todo on behalf of a specific worker).
func handleTodoAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		PaneID    string `json:"pane_id"`
		Title     string `json:"title"`
		CreatorID string `json:"creator_id"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid json")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		httpErr(w, 400, "title required")
		return
	}
	requester := requesterPaneID(r)
	if requester == "" {
		httpErr(w, 400, "X-Agent-Show-Id header required")
		return
	}

	target := requester
	bodyPane := shortPaneID(normPaneID(strings.TrimSpace(req.PaneID)))
	if bodyPane != "" && bodyPane != requester {
		if !isMasterPaneID(requester) {
			httpErr(w, 403, "only master pane can create todos for other workers")
			return
		}
		target = bodyPane
	}

	todoMu.Lock()
	defer todoMu.Unlock()

	ws, ok := requireMasterWorkspaceForTodo(w)
	if !ok {
		return
	}
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	t := Todo{
		ID:        nextTodoID(todos),
		Title:     req.Title,
		Status:    "todo",
		PaneID:    target,
		CreatorID: strings.TrimSpace(req.CreatorID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	todos = append(todos, t)
	if err := saveTodos(ws, todos); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"todo": t})
}

// PATCH /api/todo/{id}    body: {status?, title?, [pane_id]}
// DELETE /api/todo/{id}
//
// Authorization: the requester must be the master pane, or the todo's
// PaneID must equal the requester's pane id. The pane_id body/query param
// is accepted but is no longer used to choose a workspace.
func handleTodoByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/todo/")
	if id == "" || strings.Contains(id, "/") {
		httpErr(w, 400, "invalid todo id")
		return
	}
	switch r.Method {
	case "PATCH":
		handleTodoPatch(w, r, id)
	case "DELETE":
		handleTodoDelete(w, r, id)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleTodoPatch(w http.ResponseWriter, r *http.Request, idOrPrefix string) {
	var req struct {
		PaneID   string  `json:"pane_id"`
		Status   *string `json:"status"`
		Title    *string `json:"title"`
		Assignee *string `json:"assignee"` // reassign ownership (master only)
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid json")
		return
	}
	if req.Status == nil && req.Title == nil && req.Assignee == nil {
		httpErr(w, 400, "no fields to update")
		return
	}
	var assignee string
	if req.Assignee != nil {
		assignee = shortPaneID(normPaneID(strings.TrimSpace(*req.Assignee)))
		if assignee == "" {
			httpErr(w, 400, "assignee cannot be empty")
			return
		}
	}
	if req.Status != nil {
		s := strings.ToLower(strings.TrimSpace(*req.Status))
		if !todoValidStatus[s] {
			httpErr(w, 400, "invalid status: "+s)
			return
		}
		*req.Status = s
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			httpErr(w, 400, "title cannot be empty")
			return
		}
		*req.Title = t
	}
	requester := requesterPaneID(r)
	if requester == "" {
		httpErr(w, 400, "X-Agent-Show-Id header required")
		return
	}

	todoMu.Lock()
	defer todoMu.Unlock()
	ws, ok := requireMasterWorkspaceForTodo(w)
	if !ok {
		return
	}
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	idx, err := resolveTodoID(todos, idOrPrefix)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	if !isMasterPaneID(requester) && todos[idx].PaneID != requester {
		httpErr(w, 403, "todo belongs to another worker")
		return
	}
	// Reassigning ownership to another worker is a master-only operation.
	if req.Assignee != nil {
		if !isMasterPaneID(requester) {
			httpErr(w, 403, "only master pane can reassign todos")
			return
		}
		todos[idx].PaneID = assignee
	}
	if req.Status != nil {
		todos[idx].Status = *req.Status
	}
	if req.Title != nil {
		todos[idx].Title = *req.Title
	}
	todos[idx].UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := saveTodos(ws, todos); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"todo": todos[idx]})
}

func handleTodoDelete(w http.ResponseWriter, r *http.Request, idOrPrefix string) {
	requester := requesterPaneID(r)
	if requester == "" {
		httpErr(w, 400, "X-Agent-Show-Id header required")
		return
	}
	todoMu.Lock()
	defer todoMu.Unlock()
	ws, ok := requireMasterWorkspaceForTodo(w)
	if !ok {
		return
	}
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	idx, err := resolveTodoID(todos, idOrPrefix)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	if !isMasterPaneID(requester) && todos[idx].PaneID != requester {
		httpErr(w, 403, "todo belongs to another worker")
		return
	}
	removed := todos[idx]
	todos = append(todos[:idx], todos[idx+1:]...)
	if err := saveTodos(ws, todos); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"ok": true, "id": removed.ID})
}
