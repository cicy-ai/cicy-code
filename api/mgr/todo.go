package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Todo is one task in <workspace>/.cicy/todos.yaml.
//
// Status state machine: todo → doing → done, with dropped as a terminal "abandoned" state.
type Todo struct {
	ID        string    `yaml:"id" json:"id"`
	Title     string    `yaml:"title" json:"title"`
	Status    string    `yaml:"status" json:"status"`
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
		"todo": true, "doing": true, "done": true, "dropped": true,
	}
)

func todoFilePath(workspace string) string {
	return filepath.Join(workspaceRuntimeDir(workspace), "todos.yaml")
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
	var f todoFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Todos == nil {
		return []Todo{}, nil
	}
	return f.Todos, nil
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
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func newTodoID() string {
	var b [2]byte
	rand.Read(b[:])
	return fmt.Sprintf("t-%d-%s", time.Now().Unix(), hex.EncodeToString(b[:]))
}

// resolveTodoID matches a user-supplied id or unique prefix.
// Returns the full id, the index, or an error describing the ambiguity.
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

// sortTodos in-place: doing first, then todo, done, dropped; within each bucket by updated_at desc.
func sortTodos(todos []Todo) {
	rank := func(s string) int {
		switch s {
		case "doing":
			return 0
		case "todo":
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

// resolveTodoPane picks the pane id to act on. Precedence: explicit query/body
// `pane_id` (when non-empty) > `X-Agent-Show-Id` header. The header is what the
// CLI uses to address other agents' todos (e.g. `cicy-todo w-10001`).
func resolveTodoPane(r *http.Request, fromQueryOrBody string) string {
	if s := strings.TrimSpace(fromQueryOrBody); s != "" {
		return s
	}
	for _, h := range []string{"X-Agent-Show-Id", "X-Agent-Show-ID", "X_AGENT_SHOW_ID"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

func resolvePaneWorkspaceForTodo(w http.ResponseWriter, paneID string) (string, bool) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		httpErr(w, 400, "pane_id required (query pane_id= or header X-Agent-Show-Id)")
		return "", false
	}
	ws := paneWorkspace(paneID)
	if ws == "" {
		httpErr(w, 404, "workspace not found for pane "+shortPaneID(normPaneID(paneID)))
		return "", false
	}
	return ws, true
}

// GET /api/todo/list?pane_id=&status=&q=&all_agents=true
func handleTodoList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	q := r.URL.Query()
	paneID := resolveTodoPane(r, q.Get("pane_id"))
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	kw := strings.ToLower(strings.TrimSpace(q.Get("q")))
	allAgents := strings.ToLower(strings.TrimSpace(q.Get("all_agents"))) == "true"

	todoMu.Lock()
	defer todoMu.Unlock()

	// all_agents: collect from all known workspaces under the master pane.
	if allAgents {
		masterWs, ok := resolvePaneWorkspaceForTodo(w, paneID)
		if !ok {
			return
		}
		// Gather todos from master workspace + all child worker dirs.
		type TodoWithPane struct {
			Todo
			PaneID string `json:"pane_id,omitempty"`
		}
		var combined []TodoWithPane
		masterTodos, _ := loadTodos(masterWs)
		for _, t := range masterTodos {
			combined = append(combined, TodoWithPane{Todo: t, PaneID: paneID})
		}
		// child workspaces are symlinks in <masterWs>/workers/
		workersDir := filepath.Join(masterWs, "workers")
		if entries, err := os.ReadDir(workersDir); err == nil {
			for _, e := range entries {
				childWs, err := filepath.EvalSymlinks(filepath.Join(workersDir, e.Name()))
				if err != nil {
					childWs = filepath.Join(workersDir, e.Name())
				}
				childTodos, _ := loadTodos(childWs)
				for _, t := range childTodos {
					combined = append(combined, TodoWithPane{Todo: t, PaneID: e.Name()})
				}
			}
		}
		// filter
		out := combined[:0]
		for _, t := range combined {
			if status != "" && status != "all" && t.Status != status {
				continue
			}
			if kw != "" && !strings.Contains(strings.ToLower(t.Title), kw) {
				continue
			}
			out = append(out, t)
		}
		// sort
		sort.SliceStable(out, func(i, j int) bool {
			ri := map[string]int{"doing": 0, "todo": 1, "done": 2, "dropped": 3}
			riv, rjv := ri[out[i].Status], ri[out[j].Status]
			if riv != rjv {
				return riv < rjv
			}
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		})
		J(w, M{"todos": out})
		return
	}

	ws, ok := resolvePaneWorkspaceForTodo(w, paneID)
	if !ok {
		return
	}
	all, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := make([]Todo, 0, len(all))
	for _, t := range all {
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

// GET /api/todo/counts?pane_id=
func handleTodoCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	ws, ok := resolvePaneWorkspaceForTodo(w, resolveTodoPane(r, r.URL.Query().Get("pane_id")))
	if !ok {
		return
	}
	todoMu.Lock()
	defer todoMu.Unlock()
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	counts := M{"all": len(todos), "todo": 0, "doing": 0, "done": 0, "dropped": 0}
	for _, t := range todos {
		if _, ok := counts[t.Status]; ok {
			counts[t.Status] = counts[t.Status].(int) + 1
		}
	}
	J(w, counts)
}

// POST /api/todo/add  body: {pane_id, title}
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
	ws, ok := resolvePaneWorkspaceForTodo(w, resolveTodoPane(r, req.PaneID))
	if !ok {
		return
	}
	todoMu.Lock()
	defer todoMu.Unlock()
	todos, err := loadTodos(ws)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	t := Todo{
		ID:        newTodoID(),
		Title:     req.Title,
		Status:    "todo",
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

// PATCH /api/todo/{id}   body: {pane_id, status?, title?}
// DELETE /api/todo/{id}?pane_id=
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
		PaneID string  `json:"pane_id"`
		Status *string `json:"status"`
		Title  *string `json:"title"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid json")
		return
	}
	if req.Status == nil && req.Title == nil {
		httpErr(w, 400, "no fields to update")
		return
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
	ws, ok := resolvePaneWorkspaceForTodo(w, resolveTodoPane(r, req.PaneID))
	if !ok {
		return
	}
	todoMu.Lock()
	defer todoMu.Unlock()
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
	ws, ok := resolvePaneWorkspaceForTodo(w, resolveTodoPane(r, r.URL.Query().Get("pane_id")))
	if !ok {
		return
	}
	todoMu.Lock()
	defer todoMu.Unlock()
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
	removed := todos[idx]
	todos = append(todos[:idx], todos[idx+1:]...)
	if err := saveTodos(ws, todos); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"ok": true, "id": removed.ID})
}
