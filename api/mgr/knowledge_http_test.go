package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capturedKnowledgeNotification struct {
	pane string
	text string
}

func captureKnowledgeNotifications(t *testing.T) *[]capturedKnowledgeNotification {
	t.Helper()
	previous := deliverKnowledgeAgentMessageFn
	got := []capturedKnowledgeNotification{}
	deliverKnowledgeAgentMessageFn = func(pane, text string) {
		got = append(got, capturedKnowledgeNotification{pane: pane, text: text})
	}
	t.Cleanup(func() { deliverKnowledgeAgentMessageFn = previous })
	return &got
}

func knowledgeReq(t *testing.T, handler http.HandlerFunc, method, target string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// End-to-end over the HTTP handlers (no daemon): POST add → GET list?status=
// pending → PATCH promote → GET recall(canon) hits, mirroring the A acceptance.
func TestKnowledgeHTTPFlow(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	// POST add → pending + id.
	code, body := knowledgeReq(t, handleKnowledge, "POST", "/api/knowledge", M{
		"title": "Restart runbook",
		"body":  "Run dev.py --quick --preview to rebuild and restart 8008.",
		"tags":  "deploy ops",
	})
	if code != 200 {
		t.Fatalf("POST add code=%d body=%v", code, body)
	}
	id, _ := body["id"].(string)
	if id == "" || body["status"] != "pending" {
		t.Fatalf("add response wrong: %v", body)
	}

	// GET list?status=pending → finds it.
	code, list := knowledgeReq(t, handleKnowledge, "GET", "/api/knowledge?status=pending", nil)
	if code != 200 {
		t.Fatalf("GET list code=%d", code)
	}
	if arr, _ := list["knowledge"].([]interface{}); len(arr) != 1 {
		t.Fatalf("pending list len=%d, want 1", len(arr))
	}

	// PATCH promote → canon.
	code, pr := knowledgeReq(t, handleKnowledgeByID, "PATCH", "/api/knowledge/"+id, M{"action": "promote", "verified_by": "w-10131"})
	if code != 200 || pr["status"] != "canon" {
		t.Fatalf("promote code=%d body=%v", code, pr)
	}

	// GET recall over canon by keyword → hit.
	code, rc := knowledgeReq(t, handleKnowledge, "GET", "/api/knowledge?status=canon&q=restart", nil)
	if code != 200 {
		t.Fatalf("recall code=%d", code)
	}
	arr, _ := rc["knowledge"].([]interface{})
	if len(arr) != 1 {
		t.Fatalf("recall hits=%d, want 1", len(arr))
	}

	// PATCH supersede requires superseded_by.
	code, _ = knowledgeReq(t, handleKnowledgeByID, "PATCH", "/api/knowledge/"+id, M{"action": "supersede"})
	if code != http.StatusBadRequest {
		t.Fatalf("supersede without superseded_by should 400, got %d", code)
	}

	// Unknown id → 404.
	code, _ = knowledgeReq(t, handleKnowledgeByID, "GET", "/api/knowledge/does-not-exist", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown id should 404, got %d", code)
	}

	// POST missing body → 400.
	code, _ = knowledgeReq(t, handleKnowledge, "POST", "/api/knowledge", M{"title": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("missing body should 400, got %d", code)
	}
}

func TestKnowledgeHTTPAddNotifiesLatestSelectedSpecialist(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	notifications := captureKnowledgeNotifications(t)

	code, body := knowledgeReq(t, handleKnowledgeSpecialist, "POST", "/api/knowledge/specialist", M{"pane": "w-30099"})
	if code != http.StatusOK || body["pane"] != "w-30099:main.0" {
		t.Fatalf("select first specialist code=%d body=%v", code, body)
	}
	code, added := knowledgeReq(t, handleKnowledge, "POST", "/api/knowledge", M{
		"title": "Restart runbook",
		"body":  "Restart the service with the versioned binary.",
	})
	if code != http.StatusOK {
		t.Fatalf("add first knowledge code=%d body=%v", code, added)
	}
	if len(*notifications) != 1 {
		t.Fatalf("first pending add notifications=%d, want 1", len(*notifications))
	}
	first := (*notifications)[0]
	firstID, _ := added["id"].(string)
	if first.pane != "w-30099:main.0" || !strings.Contains(first.text, firstID) || !strings.Contains(first.text, "Restart runbook") {
		t.Fatalf("first notification=%+v id=%q", first, firstID)
	}

	code, body = knowledgeReq(t, handleKnowledgeSpecialist, "POST", "/api/knowledge/specialist", M{"pane": "w-30100"})
	if code != http.StatusOK || body["pane"] != "w-30100:main.0" {
		t.Fatalf("select second specialist code=%d body=%v", code, body)
	}
	code, added = knowledgeReq(t, handleKnowledge, "POST", "/api/knowledge", M{
		"title": "Proxy runbook",
		"body":  "Validate the proxy before publishing it.",
	})
	if code != http.StatusOK {
		t.Fatalf("add second knowledge code=%d body=%v", code, added)
	}
	if len(*notifications) != 2 || (*notifications)[1].pane != "w-30100:main.0" {
		t.Fatalf("notifications after specialist switch=%+v", *notifications)
	}
}

func TestKnowledgeHTTPDraftDoesNotNotifySpecialist(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	notifications := captureKnowledgeNotifications(t)

	code, body := knowledgeReq(t, handleKnowledge, "POST", "/api/knowledge", M{
		"title":  "Unfinished note",
		"body":   "This still needs evidence.",
		"status": "draft",
	})
	if code != http.StatusOK || body["status"] != "draft" {
		t.Fatalf("add draft code=%d body=%v", code, body)
	}
	if len(*notifications) != 0 {
		t.Fatalf("draft notifications=%+v, want none", *notifications)
	}
}
