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
	decodeGroupResponse(t, callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", groupID), ""))
	detail := decodeGroupResponse(t, callGroupsHandler(t, "GET", fmt.Sprintf("/api/groups/%d", groupID), ""))
	panes := detail["panes"].([]interface{})
	if len(panes) != 3 {
		t.Fatalf("default project panes after add = %#v", panes)
	}

	other := decodeGroupResponse(t, callGroupsHandler(t, "POST", "/api/groups", `{"name":"Other"}`))
	otherID := int(other["id"].(float64))
	conflict := callGroupsHandler(t, "POST", fmt.Sprintf("/api/groups/%d/panes/w-123:main.0", otherID), "")
	if conflict.Code != 409 {
		t.Fatalf("duplicate project membership status = %d body=%s", conflict.Code, conflict.Body.String())
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
