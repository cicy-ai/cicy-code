// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The dispatch convention (CLAUDE.md) is: `cicy-agent msg` carries ONLY a todo
// id + a one-line title, and ALL the detail — goal, acceptance criteria, files —
// lives in the todo. Before this, Todo had no Body field at all: the CLI's
// --body was silently dropped and every brief written into a todo was lost.
// These tests pin the round-trip so that can't regress.

const todoBrief = "## 目标\n把 X 改成 Y。\n\n## 验收\n- [ ] 测试绿\n- [ ] 无回归\n\n相关文件: api/mgr/todo.go"

// addBodyTodo posts a todo with a brief and returns its id.
func addBodyTodo(t *testing.T, title, body string) string {
	t.Helper()
	code, resp := callTodo(t, handleTodoAdd, "POST", "/api/todo/add", "w-1001",
		map[string]string{"title": title, "body": body})
	if code != 200 {
		t.Fatalf("POST /api/todo/add = %d, want 200 (%v)", code, resp)
	}
	todo, _ := resp["todo"].(map[string]interface{})
	if todo == nil {
		t.Fatalf("no todo in response: %v", resp)
	}
	id, _ := todo["id"].(string)
	if id == "" {
		t.Fatalf("no id in response: %v", resp)
	}
	return id
}

func todoBodyOf(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	todo, _ := resp["todo"].(map[string]interface{})
	body, _ := todo["body"].(string)
	return body
}

func TestTodoAddPersistsBody(t *testing.T) {
	setupTodoTest(t)

	code, resp := callTodo(t, handleTodoAdd, "POST", "/api/todo/add", "w-1001",
		map[string]string{"title": "带 brief 的任务", "body": todoBrief})
	if code != 200 {
		t.Fatalf("POST = %d, want 200 (%v)", code, resp)
	}
	if got := todoBodyOf(t, resp); got != todoBrief {
		t.Fatalf("body = %q, want %q — the brief was dropped", got, todoBrief)
	}
}

// Read it back through the list endpoint, i.e. off disk — the brief must
// survive the YAML round-trip, not just live in the POST response.
func TestTodoBodySurvivesReload(t *testing.T) {
	setupTodoTest(t)
	id := addBodyTodo(t, "重载后 brief 还在吗", todoBrief)

	code, resp := callTodo(t, handleTodoList, "GET", "/api/todo", "w-1001", nil)
	if code != 200 {
		t.Fatalf("GET = %d, want 200", code)
	}
	todos, _ := resp["todos"].([]interface{})
	for _, raw := range todos {
		td, _ := raw.(map[string]interface{})
		if td["id"] == id {
			if body, _ := td["body"].(string); body != todoBrief {
				t.Fatalf("after reload body = %q, want %q", body, todoBrief)
			}
			return
		}
	}
	t.Fatalf("todo %s not found in list", id)
}

func TestTodoPatchUpdatesBody(t *testing.T) {
	setupTodoTest(t)
	id := addBodyTodo(t, "改 brief", "旧的 brief")

	const updated = "新的 brief:验收标准变了"
	code, resp := callTodo(t, handleTodoByID, "PATCH", "/api/todo/"+id, "w-1001",
		map[string]string{"body": updated})
	if code != 200 {
		t.Fatalf("PATCH = %d, want 200 (%v)", code, resp)
	}
	if got := todoBodyOf(t, resp); got != updated {
		t.Fatalf("body = %q, want %q", got, updated)
	}
	todo, _ := resp["todo"].(map[string]interface{})
	if title, _ := todo["title"].(string); title != "改 brief" {
		t.Fatalf("patching body clobbered the title: %q", title)
	}
}

// A status transition must not wipe the brief — the whole point of `test` is to
// hand the work to a reviewer, who then needs the acceptance criteria.
func TestTodoStatusChangeKeepsBody(t *testing.T) {
	setupTodoTest(t)
	id := addBodyTodo(t, "交接给验收方", todoBrief)

	code, resp := callTodo(t, handleTodoByID, "PATCH", "/api/todo/"+id, "w-1001",
		map[string]string{"status": "test"})
	if code != 200 {
		t.Fatalf("PATCH = %d, want 200 (%v)", code, resp)
	}
	todo, _ := resp["todo"].(map[string]interface{})
	if status, _ := todo["status"].(string); status != "test" {
		t.Fatalf("status = %q, want test", status)
	}
	if got := todoBodyOf(t, resp); got != todoBrief {
		t.Fatalf("status change wiped the brief: body = %q", got)
	}
}

// -q must search the brief, not just the title: the detail lives in Body, so a
// title-only search can't find the todo that actually describes the thing.
func TestTodoSearchMatchesBody(t *testing.T) {
	setupTodoTest(t)
	addBodyTodo(t, "标题里没有关键词", "brief 里提到了 outbound-only 反向拨号")

	code, resp := callTodo(t, handleTodoList, "GET", "/api/todo?q=outbound-only", "w-1001", nil)
	if code != 200 {
		t.Fatalf("GET = %d, want 200", code)
	}
	todos, _ := resp["todos"].([]interface{})
	for _, raw := range todos {
		td, _ := raw.(map[string]interface{})
		if body, _ := td["body"].(string); strings.Contains(body, "outbound-only") {
			return
		}
	}
	t.Fatal("-q did not match a keyword that exists only in the brief")
}
