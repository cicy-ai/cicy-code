package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
