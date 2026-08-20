// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func callGroupsHandler(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	if path == "/api/groups" {
		handleGroups(rec, req)
	} else {
		handleGroupByID(rec, req)
	}
	return rec
}

func decodeGroupResponse(t *testing.T, rec *httptest.ResponseRecorder) M {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result M
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func TestDefaultGroupIsPersistedAndCanManageAgents(t *testing.T) {
	withTestStore(t)

	listed := decodeGroupResponse(t, callGroupsHandler(t, "GET", "/api/groups", ""))
	groups, ok := listed["groups"].([]interface{})
	if !ok || len(groups) != 1 {
		t.Fatalf("groups = %#v, want the seeded default group", listed["groups"])
	}
	defaultGroup := groups[0].(map[string]interface{})
	if defaultGroup["is_default"] != true {
		t.Fatalf("default group flag = %#v", defaultGroup)
	}
	seeded := map[string]bool{}
	for _, raw := range defaultGroup["pane_ids"].([]interface{}) {
		seeded[raw.(string)] = true
	}
	if !seeded["w-101:main.0"] || !seeded["w-102:main.0"] || len(seeded) != 2 {
		t.Fatalf("default group seed = %#v", defaultGroup["pane_ids"])
	}

	groupID := int(defaultGroup["id"].(float64))
	decodeGroupResponse(t, callGroupsHandler(t, "PATCH", fmt.Sprintf("/api/groups/%d", groupID), `{"name":"My default","is_pinned":true}`))
	renamed := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	if renamed["name"] != "My default" || renamed["name_customized"] != true || renamed["is_pinned"] != true {
		t.Fatalf("default project rename/pin = %#v", renamed)
	}
	decodeGroupResponse(t, callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", groupID), ""))
	detail := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	panes := detail["panes"].([]interface{})
	if len(panes) != 3 {
		t.Fatalf("default project panes after add = %#v", panes)
	}

	other := decodeGroupResponse(t, callGroupsHandler(t, "POST", "/api/groups", `{"name":"Other"}`))
	otherID := int(other["id"].(float64))
	decodeGroupResponse(t, callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", otherID), ""))
	moved := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", otherID), ""))
	if panes := moved["panes"].([]interface{}); len(panes) != 1 || panes[0].(map[string]interface{})["pane_id"] != "w-123:main.0" {
		t.Fatalf("moved project panes = %#v", moved["panes"])
	}
	previous := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	if panes := previous["panes"].([]interface{}); len(panes) != 2 {
		t.Fatalf("previous project retained moved Agent: %#v", previous["panes"])
	}
	decodeGroupResponse(t, callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0?mode=add", groupID), ""))
	added := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	if panes := added["panes"].([]interface{}); len(panes) != 3 {
		t.Fatalf("add mode did not retain both project memberships: %#v", added["panes"])
	}
	stillInOther := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", otherID), ""))
	if panes := stillInOther["panes"].([]interface{}); len(panes) != 1 {
		t.Fatalf("add mode removed the original membership: %#v", stillInOther["panes"])
	}

	if _, err := store.Exec(`INSERT INTO agent_config (pane_id, title, agent_type, active) VALUES ('w-123:main.0', 'Shared Agent', 'codex', 1)`); err != nil {
		t.Fatalf("seed shared agent: %v", err)
	}
	allPanesRecorder := httptest.NewRecorder()
	handlePanes(allPanesRecorder, httptest.NewRequest("GET", "/api/tmux/panes", nil))
	allPanes := decodeGroupResponse(t, allPanesRecorder)["panes"].([]interface{})
	sharedCount := 0
	for _, raw := range allPanes {
		if raw.(map[string]interface{})["pane_id"] == "w-123:main.0" {
			sharedCount++
		}
	}
	if sharedCount != 1 {
		t.Fatalf("multi-project agent appeared %d times in global pane list: %#v", sharedCount, allPanes)
	}

	denied := callGroupsHandler(t, "DELETE", fmt.Sprintf("/api/groups/%d", groupID), "")
	if denied.Code != 400 {
		t.Fatalf("delete default status = %d body=%s", denied.Code, denied.Body.String())
	}
}

func TestGroupsCRUD(t *testing.T) {
	withTestStore(t)

	created := decodeGroupResponse(t, callGroupsHandler(t, "POST", "/api/groups", `{"name":"Launch","description":"Release project"}`))
	groupID := int(created["id"].(float64))

	listed := decodeGroupResponse(t, callGroupsHandler(t, "GET", "/api/groups", ""))
	groups, ok := listed["groups"].([]interface{})
	if !ok || len(groups) != 2 {
		t.Fatalf("groups = %#v, want default + created group", listed["groups"])
	}
	createdGroup := groups[1].(map[string]interface{})
	if createdGroup["name"] != "Launch" || createdGroup["is_default"] != false || createdGroup["created_at"] == "" || createdGroup["updated_at"] == "" {
		t.Fatalf("unexpected listed group: %#v", createdGroup)
	}

	decodeGroupResponse(t, callGroupsHandler(t, "PATCH", fmt.Sprintf("/api/groups/%d", groupID), `{"name":"Launch 2"}`))
	decodeGroupResponse(t, callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", groupID), ""))

	detail := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	if detail["name"] != "Launch 2" || int(detail["id"].(float64)) != groupID {
		t.Fatalf("unexpected group detail: %#v", detail)
	}
	panes, ok := detail["panes"].([]interface{})
	if !ok || len(panes) != 1 || panes[0].(map[string]interface{})["pane_id"] != "w-123:main.0" {
		t.Fatalf("unexpected panes: %#v", detail["panes"])
	}

	decodeGroupResponse(t, callGroupsHandler(t, "DELETE", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", groupID), ""))
	detail = decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	if panes, ok := detail["panes"].([]interface{}); !ok || len(panes) != 0 {
		t.Fatalf("panes after remove = %#v", detail["panes"])
	}

	decodeGroupResponse(t, callGroupsHandler(t, "DELETE", fmt.Sprintf("/api/groups/%d", groupID), ""))
	missing := callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), "")
	if missing.Code != 404 {
		t.Fatalf("deleted group status = %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestPinnedGroupsSortBeforeUnpinnedGroups(t *testing.T) {
	withTestStore(t)

	first := decodeGroupResponse(t, callGroupsHandler(t, "POST", "/api/groups", `{"name":"First"}`))
	second := decodeGroupResponse(t, callGroupsHandler(t, "POST", "/api/groups", `{"name":"Second"}`))
	secondID := int(second["id"].(float64))
	decodeGroupResponse(t, callGroupsHandler(t, "PATCH", fmt.Sprintf("/api/groups/%d", secondID), `{"is_pinned":true}`))

	listed := decodeGroupResponse(t, callGroupsHandler(t, "GET", "/api/groups", ""))
	groups := listed["groups"].([]interface{})
	if groups[0].(map[string]interface{})["id"] != second["id"] || groups[0].(map[string]interface{})["is_pinned"] != true {
		t.Fatalf("pinned project was not first: %#v", groups)
	}
	if first["is_pinned"] != false {
		t.Fatalf("new project should be unpinned: %#v", first)
	}
}
