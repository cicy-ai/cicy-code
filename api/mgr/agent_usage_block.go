// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// handleAgentUsageBlockByPane returns the FULL text of one history message
// block (current.json messages[idx]). The 用量分析「最大的几段」rows only carry a
// truncated preview, so this backs a per-block download of the complete
// content. GET /api/agents/usage-block/<paneID>?idx=N
func handleAgentUsageBlockByPane(w http.ResponseWriter, r *http.Request) {
	paneID := shortPaneID(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agents/usage-block/"), "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	idx, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("idx")))
	if err != nil || idx < 0 {
		httpErr(w, http.StatusBadRequest, "valid idx required")
		return
	}
	dir := aiGatewayHistoryDir(paneID)
	current := usageAnalysisReadJSON(filepath.Join(dir, "current.json"))
	body, _ := current["body"].(map[string]interface{})
	messages, _ := bodyField(body, "messages").([]interface{})
	role, text, ok := usageBlockFullText(messages, idx)
	if !ok {
		httpErr(w, http.StatusNotFound, "block not found")
		return
	}
	J(w, M{"pane_id": paneID, "idx": idx, "role": role, "text": text, "bytes": len(text)})
}

// usageBlockFullText reconstructs the complete text of messages[idx], mirroring
// the kinds the analysis recognizes (text / tool_use / tool_result / thinking),
// but untruncated. Multi-block messages are concatenated in order.
func usageBlockFullText(messages []interface{}, idx int) (string, string, bool) {
	if idx < 0 || idx >= len(messages) {
		return "", "", false
	}
	m, _ := messages[idx].(map[string]interface{})
	if m == nil {
		return "", "", false
	}
	role := asString(m["role"])
	switch c := m["content"].(type) {
	case string:
		return role, c, true
	case []interface{}:
		parts := []string{}
		for _, bi := range c {
			b, _ := bi.(map[string]interface{})
			if b == nil {
				continue
			}
			switch asString(b["type"]) {
			case "text":
				parts = append(parts, asString(b["text"]))
			case "tool_use":
				inp, _ := json.MarshalIndent(b["input"], "", "  ")
				parts = append(parts, "● tool_use: "+asString(b["name"])+"\n"+string(inp))
			case "tool_result":
				parts = append(parts, toolResultText(b["content"]))
			case "thinking":
				parts = append(parts, asString(b["thinking"]))
			default:
				raw, _ := json.MarshalIndent(b, "", "  ")
				parts = append(parts, string(raw))
			}
		}
		return role, strings.Join(parts, "\n\n"), true
	}
	return role, "", true
}
