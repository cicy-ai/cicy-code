package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// setupTodoTest installs an isolated cicy root, store, and inserts a master
// (w-1001) + a worker (w-10025) agent_config row. Returns nothing — tests use
// the global handlers directly.
func setupTodoTest(t *testing.T) {
	t.Helper()
	withTempCicyRoot(t)
	withTestStore(t)
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-1001:main.0", "Master", "/cicy/workers/w-1001", "", "{}", "master", "", "claude", true, true,
	); err != nil {
		t.Fatalf("insert master: %v", err)
	}
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-10025:main.0", "Worker", "/cicy/workers/w-10025", "", "{}", "worker", "", "kiro-cli", true, true,
	); err != nil {
		t.Fatalf("insert worker: %v", err)
	}
}

// callTodo runs a handler with the given pane id in the X-Agent-Show-Id
// header and returns status + decoded body.
func callTodo(t *testing.T, handler http.HandlerFunc, method, target, requesterPane string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	if requesterPane != "" {
		req.Header.Set("X-Agent-Show-Id", requesterPane)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	out := map[string]interface{}{}
	if rr.Body.Len() > 0 {
		// payloads may be either JSON objects or arrays; decode into a map
		// when possible, otherwise stuff the raw text into "_raw".
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			out["_raw"] = rr.Body.String()
		}
	}
	return rr.Code, out
}

// addTodo helper — POST /api/todo/add
func addTodo(t *testing.T, requesterPane, title string, paneOverride string) string {
	t.Helper()
	body := map[string]interface{}{"title": title}
	if paneOverride != "" {
		body["pane_id"] = paneOverride
	}
	code, resp := callTodo(t, handleTodoAdd, "POST", "/api/todo/add", requesterPane, body)
	if code != 200 {
		t.Fatalf("addTodo as %s: status=%d body=%v", requesterPane, code, resp)
	}
	todo, _ := resp["todo"].(map[string]interface{})
	id, _ := todo["id"].(string)
	if id == "" {
		t.Fatalf("addTodo: missing id in response: %v", resp)
	}
	return id
}

func listTodos(t *testing.T, requesterPane string, query string) []map[string]interface{} {
	t.Helper()
	target := "/api/todo/list"
	if query != "" {
		target += "?" + query
	}
	code, resp := callTodo(t, handleTodoList, "GET", target, requesterPane, nil)
	if code != 200 {
		t.Fatalf("listTodos as %s: status=%d body=%v", requesterPane, code, resp)
	}
	raw, _ := resp["todos"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestTodoAdd_StampsRequesterPaneByDefault(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-10025", "worker task", "")
	if id == "" {
		t.Fatalf("expected non-empty id")
	}

	// Master sees it with pane_id = w-10025.
	all := listTodos(t, "w-1001", "all_agents=true")
	if len(all) != 1 || all[0]["pane_id"] != "w-10025" {
		t.Fatalf("master list: %v", all)
	}
}

func TestTodoAdd_MasterCanCreateOnBehalfOfWorker(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-1001", "for w-10025", "w-10025")
	if id == "" {
		t.Fatalf("expected non-empty id")
	}

	all := listTodos(t, "w-1001", "all_agents=true")
	if len(all) != 1 || all[0]["pane_id"] != "w-10025" {
		t.Fatalf("expected pane_id w-10025, got: %v", all)
	}
}

func TestTodoAdd_WorkerCannotCreateForOtherWorker(t *testing.T) {
	setupTodoTest(t)

	body := map[string]interface{}{"title": "sneaky", "pane_id": "w-1001"}
	code, resp := callTodo(t, handleTodoAdd, "POST", "/api/todo/add", "w-10025", body)
	if code != 403 {
		t.Fatalf("expected 403, got %d: %v", code, resp)
	}
}

func TestTodoList_WorkerSeesOnlyOwn(t *testing.T) {
	setupTodoTest(t)

	addTodo(t, "w-10025", "task A", "")
	addTodo(t, "w-1001", "master task", "")
	addTodo(t, "w-1001", "for other worker", "w-20000") // unknown worker but allowed: pane_id stamped

	w25 := listTodos(t, "w-10025", "")
	if len(w25) != 1 {
		t.Fatalf("worker should see exactly 1 todo, got %d: %v", len(w25), w25)
	}
	if w25[0]["pane_id"] != "w-10025" {
		t.Fatalf("worker saw wrong pane: %v", w25[0])
	}
}

func TestTodoList_MasterSeesAll(t *testing.T) {
	setupTodoTest(t)

	addTodo(t, "w-10025", "worker task", "")
	addTodo(t, "w-1001", "master task", "")

	all := listTodos(t, "w-1001", "all_agents=true")
	if len(all) != 2 {
		t.Fatalf("master should see 2 todos, got %d: %v", len(all), all)
	}
}

func TestTodoList_MasterCanFilterByPane(t *testing.T) {
	setupTodoTest(t)

	addTodo(t, "w-10025", "worker task", "")
	addTodo(t, "w-1001", "master task", "")

	w25Only := listTodos(t, "w-1001", "pane_id=w-10025")
	if len(w25Only) != 1 || w25Only[0]["pane_id"] != "w-10025" {
		t.Fatalf("filter to w-10025: %v", w25Only)
	}
}

func TestTodoList_RequesterHeaderRequired(t *testing.T) {
	setupTodoTest(t)
	// No header → 400. We deliberately do NOT default to master to prevent
	// silent fallback writes landing under w-1001.
	code, _ := callTodo(t, handleTodoList, "GET", "/api/todo/list", "", nil)
	if code != 400 {
		t.Fatalf("expected 400 without header, got %d", code)
	}
}

func TestTodoAdd_RequesterHeaderRequired(t *testing.T) {
	setupTodoTest(t)
	code, _ := callTodo(t, handleTodoAdd, "POST", "/api/todo/add", "",
		map[string]interface{}{"title": "anonymous"})
	if code != 400 {
		t.Fatalf("expected 400 without header, got %d", code)
	}
	// Confirm no todo was written.
	all := listTodos(t, "w-1001", "all_agents=true")
	if len(all) != 0 {
		t.Fatalf("anonymous add should have been rejected, but found todos: %v", all)
	}
}

func TestTodoPatch_WorkerCannotModifyOthers(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-1001", "master only", "")

	// Worker tries to PATCH master's todo via the public endpoint.
	req := httptest.NewRequest("PATCH", "/api/todo/"+id, strings.NewReader(`{"status":"done"}`))
	req.Header.Set("X-Agent-Show-Id", "w-10025")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleTodoByID(rr, req)
	if rr.Code != 403 {
		t.Fatalf("expected 403 for cross-worker patch, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestTodoPatch_OwnerCanModify(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-10025", "my task", "")
	req := httptest.NewRequest("PATCH", "/api/todo/"+id, strings.NewReader(`{"status":"done"}`))
	req.Header.Set("X-Agent-Show-Id", "w-10025")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleTodoByID(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Todo.Status != "done" {
		t.Fatalf("status not updated: %+v", resp.Todo)
	}
}

func TestTodoPatch_MasterCanModifyAny(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-10025", "worker task", "")
	req := httptest.NewRequest("PATCH", "/api/todo/"+id, strings.NewReader(`{"status":"done"}`))
	req.Header.Set("X-Agent-Show-Id", "w-1001")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleTodoByID(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestTodoDelete_Authz(t *testing.T) {
	setupTodoTest(t)

	id := addTodo(t, "w-10025", "worker task", "")

	// Master may delete any.
	req := httptest.NewRequest("DELETE", "/api/todo/"+id, nil)
	req.Header.Set("X-Agent-Show-Id", "w-1001")
	rr := httptest.NewRecorder()
	handleTodoByID(rr, req)
	if rr.Code != 200 {
		t.Fatalf("master delete: %d body=%s", rr.Code, rr.Body.String())
	}

	// Re-add as worker.
	id = addTodo(t, "w-10025", "worker task", "")

	// Other worker cannot delete.
	req2 := httptest.NewRequest("DELETE", "/api/todo/"+id, nil)
	req2.Header.Set("X-Agent-Show-Id", "w-20000")
	rr2 := httptest.NewRecorder()
	handleTodoByID(rr2, req2)
	if rr2.Code != 403 {
		t.Fatalf("expected 403 for cross-worker delete, got %d", rr2.Code)
	}

	// Owner can delete.
	req3 := httptest.NewRequest("DELETE", "/api/todo/"+id, nil)
	req3.Header.Set("X-Agent-Show-Id", "w-10025")
	rr3 := httptest.NewRecorder()
	handleTodoByID(rr3, req3)
	if rr3.Code != 200 {
		t.Fatalf("owner delete: %d body=%s", rr3.Code, rr3.Body.String())
	}
}

func TestTodoCounts_ScopedByRequester(t *testing.T) {
	setupTodoTest(t)

	addTodo(t, "w-10025", "a", "")
	addTodo(t, "w-10025", "b", "")
	addTodo(t, "w-1001", "c", "")

	// Worker counts only own.
	code, resp := callTodo(t, handleTodoCounts, "GET", "/api/todo/counts", "w-10025", nil)
	if code != 200 {
		t.Fatalf("worker counts: %d", code)
	}
	if resp["all"].(float64) != 2 {
		t.Fatalf("worker counts.all = %v, want 2", resp["all"])
	}

	// Master counts all.
	code, resp = callTodo(t, handleTodoCounts, "GET", "/api/todo/counts", "w-1001", nil)
	if code != 200 {
		t.Fatalf("master counts: %d", code)
	}
	if resp["all"].(float64) != 3 {
		t.Fatalf("master counts.all = %v, want 3", resp["all"])
	}

	// Master with pane filter.
	code, resp = callTodo(t, handleTodoCounts, "GET", "/api/todo/counts?pane_id=w-10025", "w-1001", nil)
	if code != 200 {
		t.Fatalf("master+filter counts: %d", code)
	}
	if resp["all"].(float64) != 2 {
		t.Fatalf("master+filter counts.all = %v, want 2", resp["all"])
	}
}

func TestTodoStorage_AllInMasterWorkspace(t *testing.T) {
	setupTodoTest(t)

	addTodo(t, "w-10025", "from worker", "")
	addTodo(t, "w-1001", "from master", "")

	// The single todos.yaml lives under master's workspace.
	masterWs := paneWorkspace("w-1001")
	if masterWs == "" {
		t.Fatalf("paneWorkspace(w-1001) empty")
	}
	todos, err := loadTodos(masterWs)
	if err != nil {
		t.Fatalf("loadTodos master: %v", err)
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos in master workspace, got %d", len(todos))
	}

	// Worker workspace must NOT have a todos file.
	workerWs := paneWorkspace("w-10025")
	workerTodos, _ := loadTodos(workerWs)
	if len(workerTodos) != 0 {
		t.Fatalf("worker workspace should be empty, got %d", len(workerTodos))
	}
}

func TestNormaliseTodoTimestamps(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "python style → RFC3339",
			in:   "created_at: 2026-05-25 02:59:48+00:00\n",
			want: "created_at: 2026-05-25T02:59:48+00:00\n",
		},
		{
			name: "already RFC3339 — unchanged",
			in:   "created_at: 2026-05-25T02:59:48+00:00\n",
			want: "created_at: 2026-05-25T02:59:48+00:00\n",
		},
		{
			name: "multiple timestamps in one document",
			in: "todos:\n- id: a\n  created_at: 2026-01-01 10:20:30+00:00\n  updated_at: 2026-02-02 11:22:33+00:00\n",
			want: "todos:\n- id: a\n  created_at: 2026-01-01T10:20:30+00:00\n  updated_at: 2026-02-02T11:22:33+00:00\n",
		},
		{
			name: "timestamps without TZ also handled",
			in:   "created_at: 2026-05-25 02:59:48Z\n",
			want: "created_at: 2026-05-25T02:59:48Z\n",
		},
		{
			name: "non-timestamp text untouched",
			in:   "title: hello world 12 34\n",
			want: "title: hello world 12 34\n",
		},
	}
	for _, c := range cases {
		got := string(normaliseTodoTimestamps([]byte(c.in)))
		if got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}

func TestLoadTodos_AcceptsPythonTimestampFormat(t *testing.T) {
	setupTodoTest(t)
	ws := paneWorkspace(primaryWorkerSession)
	if ws == "" {
		t.Skip("master workspace not configured")
	}
	if err := os.MkdirAll(workspaceRuntimeDir(ws), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a todos.yaml with the Python (space-separated) timestamp form.
	raw := []byte(`todos:
- id: t-1
  title: hello
  status: todo
  pane_id: w-10025
  created_at: 2026-05-25 02:59:48+00:00
  updated_at: 2026-05-25 02:59:48+00:00
`)
	if err := os.WriteFile(todoFilePath(ws), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	todos, err := loadTodos(ws)
	if err != nil {
		t.Fatalf("loadTodos with python ts: %v", err)
	}
	// The legacy "t-1" id is migrated to the stable integer "1" on load.
	if len(todos) != 1 || todos[0].ID != "1" {
		t.Fatalf("expected 1 todo with migrated id 1, got %v", todos)
	}
	if todos[0].CreatedAt.IsZero() {
		t.Fatalf("CreatedAt parsed as zero: %+v", todos[0])
	}
}

func TestMigrateTodoIDs_AssignsStableIntegers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	todos := []Todo{
		{ID: "t-300-aaaa", Title: "third", CreatedAt: now.Add(2 * time.Hour)},
		{ID: "t-100-bbbb", Title: "first", CreatedAt: now},
		{ID: "5", Title: "already numeric", CreatedAt: now.Add(time.Hour)},
		{ID: "t-200-cccc", Title: "second", CreatedAt: now.Add(90 * time.Minute)},
	}
	if !migrateTodoIDs(todos) {
		t.Fatalf("expected migration to report a change")
	}
	// Legacy ids are numbered by created_at ascending, continuing after the
	// highest existing numeric id (5). The pre-existing numeric id is untouched.
	byTitle := map[string]string{}
	for _, td := range todos {
		byTitle[td.Title] = td.ID
	}
	want := map[string]string{
		"already numeric": "5",
		"first":           "6",
		"second":          "7",
		"third":           "8",
	}
	for title, id := range want {
		if byTitle[title] != id {
			t.Fatalf("title %q: want id %s, got %s (all=%v)", title, id, byTitle[title], byTitle)
		}
	}
	// Idempotent: a second pass changes nothing.
	if migrateTodoIDs(todos) {
		t.Fatalf("expected second migration pass to be a no-op")
	}
	// nextTodoID continues after the max.
	if got := nextTodoID(todos); got != "9" {
		t.Fatalf("nextTodoID: want 9, got %s", got)
	}
}
