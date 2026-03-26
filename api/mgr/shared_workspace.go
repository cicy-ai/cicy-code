package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func sharedWorkspaceRoot() string {
	return "/Users/ton/projects/cicy-team/shared-workspace"
}

func sharedWorkspacePath(parts ...string) string {
	all := append([]string{sharedWorkspaceRoot()}, parts...)
	return filepath.Join(all...)
}

func handleSharedWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var data M
	if err := readJSONFile(sharedWorkspacePath("workspace.json"), &data); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"workspace": data})
}

func handleSharedWorkItems(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shared-workspace/work-items"), "/")
	switch r.Method {
	case "GET":
		if id == "" {
			items, err := readJSONDir(sharedWorkspacePath("work-items"))
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			J(w, M{"work_items": items})
			return
		}
		var item M
		if err := readJSONFile(sharedWorkspacePath("work-items", id+".json"), &item); err != nil {
			httpErr(w, 404, "work item not found")
			return
		}
		J(w, M{"work_item": item})
	case "POST":
		var item M
		if err := readBody(r, &item); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		if stringValue(item["id"]) == "" {
			httpErr(w, 400, "id required")
			return
		}
		merged := mergeSharedWorkItem(item)
		if err := writeSharedJSON(filepath.Join("work-items", stringValue(merged["id"])+".json"), merged); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(merged["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": "w-10004", "type": "work_item_updated", "summary": firstNonEmpty(stringValue(merged["title"]), stringValue(merged["id"])), "payload": M{"workItemId": stringValue(merged["id"])}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "work_item": merged})
	case "PATCH":
		if id == "" {
			httpErr(w, 400, "id required")
			return
		}
		var patch M
		if err := readBody(r, &patch); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		current := M{}
		_ = readJSONFile(sharedWorkspacePath("work-items", id+".json"), &current)
		current["id"] = id
		for k, v := range patch {
			current[k] = v
		}
		merged := mergeSharedWorkItem(current)
		if err := writeSharedJSON(filepath.Join("work-items", id+".json"), merged); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(merged["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": "w-10004", "type": "work_item_updated", "summary": firstNonEmpty(stringValue(merged["title"]), id), "payload": M{"workItemId": id}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "work_item": merged})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleSharedArtifacts(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shared-workspace/artifacts"), "/")
	switch r.Method {
	case "GET":
		if id == "" {
			items, err := readMarkdownDir(sharedWorkspacePath("artifacts"))
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			J(w, M{"artifacts": items})
			return
		}
		path := sharedWorkspacePath("artifacts", id+".md")
		if _, err := os.Stat(path); err != nil {
			httpErr(w, 404, "artifact not found")
			return
		}
		body, err := os.ReadFile(path)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"artifact": M{"id": id, "path": path, "content": string(body)}})
	case "POST":
		var item M
		if err := readBody(r, &item); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		item = mergeSharedArtifact(item)
		artifactID := stringValue(item["id"])
		if artifactID == "" {
			httpErr(w, 400, "id required")
			return
		}
		if err := writeSharedMarkdown(filepath.Join("artifacts", artifactID+".md"), stringValue(item["content"])); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(item["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": firstNonEmpty(stringValue(item["producer"]), "w-10004"), "type": "artifact_updated", "summary": firstNonEmpty(stringValue(item["title"]), artifactID), "payload": M{"artifacts": []string{artifactID}, "workItemId": stringValue(item["relatedWorkItemId"])}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "artifact": item})
	case "PATCH":
		if id == "" {
			httpErr(w, 400, "id required")
			return
		}
		var patch M
		if err := readBody(r, &patch); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		current := M{"id": id}
		path := sharedWorkspacePath("artifacts", id+".md")
		if body, err := os.ReadFile(path); err == nil {
			current["content"] = string(body)
		}
		for k, v := range patch {
			current[k] = v
		}
		item := mergeSharedArtifact(current)
		if err := writeSharedMarkdown(filepath.Join("artifacts", id+".md"), stringValue(item["content"])); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(item["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": firstNonEmpty(stringValue(item["producer"]), "w-10004"), "type": "artifact_updated", "summary": firstNonEmpty(stringValue(item["title"]), id), "payload": M{"artifacts": []string{id}, "workItemId": stringValue(item["relatedWorkItemId"])}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "artifact": item})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleSharedHandoffs(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shared-workspace/handoffs"), "/")
	switch r.Method {
	case "GET":
		if id == "" {
			items, err := readJSONDir(sharedWorkspacePath("handoffs"))
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			J(w, M{"handoffs": items})
			return
		}
		var item M
		if err := readJSONFile(sharedWorkspacePath("handoffs", id+".json"), &item); err != nil {
			httpErr(w, 404, "handoff not found")
			return
		}
		J(w, M{"handoff": item})
	case "POST":
		var item M
		if err := readBody(r, &item); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		item = mergeSharedHandoff(item)
		handoffID := stringValue(item["id"])
		if handoffID == "" {
			httpErr(w, 400, "id required")
			return
		}
		if err := writeSharedJSON(filepath.Join("handoffs", handoffID+".json"), item); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(item["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": firstNonEmpty(stringValue(item["from"]), "w-10004"), "type": "handoff_updated", "summary": firstNonEmpty(stringValue(item["summary"]), handoffID), "payload": M{"handoff": handoffID, "workItemId": stringValue(item["workItemId"]), "artifactIds": item["artifactIds"]}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "handoff": item})
	case "PATCH":
		if id == "" {
			httpErr(w, 400, "id required")
			return
		}
		var patch M
		if err := readBody(r, &patch); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		current := M{}
		_ = readJSONFile(sharedWorkspacePath("handoffs", id+".json"), &current)
		current["id"] = id
		for k, v := range patch {
			current[k] = v
		}
		item := mergeSharedHandoff(current)
		if err := writeSharedJSON(filepath.Join("handoffs", id+".json"), item); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		_ = appendSharedEvent(M{"id": fmt.Sprintf("event-%d", time.Now().UnixNano()), "workspaceId": firstNonEmpty(stringValue(item["workspaceId"]), "workspace-cicy-virtual-employees"), "actor": firstNonEmpty(stringValue(item["from"]), "w-10004"), "type": "handoff_updated", "summary": firstNonEmpty(stringValue(item["summary"]), id), "payload": M{"handoff": id, "workItemId": stringValue(item["workItemId"]), "artifactIds": item["artifactIds"]}, "ts": time.Now().UTC().Format(time.RFC3339)})
		J(w, M{"success": true, "handoff": item})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleSharedEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	file, err := os.Open(sharedWorkspacePath("events", "events.ndjson"))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer file.Close()
	var events []M
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item M
		if json.Unmarshal([]byte(line), &item) == nil {
			events = append(events, item)
		}
	}
	if events == nil {
		events = []M{}
	}
	J(w, M{"events": events})
}

func readJSONFile(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readJSONDir(dir string) ([]M, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := []M{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var item M
		if err := readJSONFile(filepath.Join(dir, entry.Name()), &item); err == nil {
			item["__file"] = entry.Name()
			items = append(items, item)
		}
	}
	return items, nil
}

func readMarkdownDir(dir string) ([]M, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := []M{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		items = append(items, M{"id": id, "path": path, "content": string(body)})
	}
	return items, nil
}

func writeSharedJSON(relPath string, data interface{}) error {
	path := sharedWorkspacePath(relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0644)
}

func writeSharedMarkdown(relPath string, content string) error {
	path := sharedWorkspacePath(relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func appendSharedEvent(event M) error {
	path := sharedWorkspacePath("events", "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = f.Write(append(body, '\n'))
	return err
}

func syncSharedWorkItem(task M) error {
	workItemID := stringValue(task["work_item_id"])
	workspaceID := firstNonEmpty(stringValue(task["workspace_id"]), "workspace-cicy-virtual-employees")
	if workItemID == "" {
		return nil
	}
	path := filepath.Join("work-items", workItemID+".json")
	current := M{}
	_ = readJSONFile(sharedWorkspacePath(path), &current)
	if current["id"] == nil {
		current["id"] = workItemID
	}
	if current["workspaceId"] == nil {
		current["workspaceId"] = workspaceID
	}
	if current["title"] == nil || stringValue(current["title"]) == "" {
		current["title"] = firstNonEmpty(stringValue(task["title"]), "Runtime task")
	}
	if current["goal"] == nil || stringValue(current["goal"]) == "" {
		current["goal"] = firstNonEmpty(stringValue(task["message"]), stringValue(task["title"]), "Runtime task sync")
	}
	if current["owner"] == nil || stringValue(current["owner"]) == "" {
		current["owner"] = firstNonEmpty(stringValue(task["created_by"]), "w-10004")
	}
	if current["kind"] == nil || stringValue(current["kind"]) == "" {
		current["kind"] = "runtime"
	}
	current["status"] = mapTaskStatusToWorkItemStatus(stringValue(task["status"]))
	current["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return writeSharedJSON(path, current)
}

func syncSharedArtifact(artifact M) error {
	artifactID := stringValue(artifact["artifact_id"])
	workspaceID := firstNonEmpty(stringValue(artifact["workspace_id"]), "workspace-cicy-virtual-employees")
	if artifactID == "" {
		return nil
	}
	mdPath := filepath.Join("artifacts", artifactID+".md")
	content := buildArtifactMarkdown(artifact)
	if err := writeSharedMarkdown(mdPath, content); err != nil {
		return err
	}
	return appendSharedEvent(M{
		"id":          fmt.Sprintf("event-%d", time.Now().UnixNano()),
		"workspaceId": workspaceID,
		"actor":       "w-10004",
		"type":        "artifact_updated",
		"summary":     firstNonEmpty(stringValue(artifact["summary"]), artifactID),
		"payload": M{
			"artifacts":  []string{artifactID},
			"workItemId": stringValue(artifact["work_item_id"]),
			"handoffId":  stringValue(artifact["handoff_id"]),
		},
		"ts": time.Now().UTC().Format(time.RFC3339),
	})
}

func syncSharedHandoff(task M) error {
	handoffID := stringValue(task["handoff_id"])
	workspaceID := firstNonEmpty(stringValue(task["workspace_id"]), "workspace-cicy-virtual-employees")
	if handoffID == "" {
		return nil
	}
	path := filepath.Join("handoffs", handoffID+".json")
	current := M{}
	_ = readJSONFile(sharedWorkspacePath(path), &current)
	if current["id"] == nil {
		current["id"] = handoffID
	}
	if current["workspaceId"] == nil {
		current["workspaceId"] = workspaceID
	}
	if current["from"] == nil || stringValue(current["from"]) == "" {
		current["from"] = firstNonEmpty(stringValue(task["created_by"]), "w-10004")
	}
	if current["to"] == nil || stringValue(current["to"]) == "" {
		current["to"] = "w-10002"
	}
	if current["workItemId"] == nil || stringValue(current["workItemId"]) == "" {
		current["workItemId"] = stringValue(task["work_item_id"])
	}
	current["summary"] = firstNonEmpty(stringValue(task["title"]), stringValue(task["message"]), "Runtime handoff")
	if aid := stringValue(task["artifact_id"]); aid != "" {
		current["artifactIds"] = []string{aid}
	}
	current["status"] = "pending"
	if current["createdAt"] == nil || stringValue(current["createdAt"]) == "" {
		current["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	return writeSharedJSON(path, current)
}

func buildArtifactMarkdown(artifact M) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(firstNonEmpty(stringValue(artifact["artifact_id"]), "runtime-artifact"))
	b.WriteString("\n\n")
	if s := stringValue(artifact["summary"]); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if payload := artifact["payload"]; payload != nil {
		b.WriteString("## Payload\n\n```json\n")
		body, _ := json.MarshalIndent(payload, "", "  ")
		b.Write(body)
		b.WriteString("\n```\n")
	}
	return b.String()
}

func mapTaskStatusToWorkItemStatus(status string) string {
	switch status {
	case "pending", "queued", "sent", "running":
		return "in_progress"
	case "completed":
		return "done"
	case "failed", "cancelled":
		return "blocked"
	default:
		return "in_progress"
	}
}

func mergeSharedWorkItem(item M) M {
	out := M{}
	for k, v := range item {
		out[k] = v
	}
	out["workspaceId"] = firstNonEmpty(stringValue(out["workspaceId"]), "workspace-cicy-virtual-employees")
	out["title"] = firstNonEmpty(stringValue(out["title"]), "Untitled Work Item")
	out["goal"] = firstNonEmpty(stringValue(out["goal"]), stringValue(out["title"]))
	out["owner"] = firstNonEmpty(stringValue(out["owner"]), "w-10004")
	out["status"] = firstNonEmpty(stringValue(out["status"]), "todo")
	out["kind"] = firstNonEmpty(stringValue(out["kind"]), "runtime")
	out["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	if out["collaborators"] == nil {
		out["collaborators"] = []string{}
	}
	if out["inputIds"] == nil {
		out["inputIds"] = []string{}
	}
	if out["outputIds"] == nil {
		out["outputIds"] = []string{}
	}
	return out
}

func mergeSharedHandoff(item M) M {
	out := M{}
	for k, v := range item {
		out[k] = v
	}
	out["workspaceId"] = firstNonEmpty(stringValue(out["workspaceId"]), "workspace-cicy-virtual-employees")
	out["from"] = firstNonEmpty(stringValue(out["from"]), "w-10004")
	out["to"] = firstNonEmpty(stringValue(out["to"]), "w-10002")
	out["summary"] = firstNonEmpty(stringValue(out["summary"]), "Runtime handoff")
	out["status"] = firstNonEmpty(stringValue(out["status"]), "pending")
	if out["artifactIds"] == nil {
		out["artifactIds"] = []string{}
	}
	if stringValue(out["createdAt"]) == "" {
		out["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	return out
}

func mergeSharedArtifact(item M) M {
	out := M{}
	for k, v := range item {
		out[k] = v
	}
	out["workspaceId"] = firstNonEmpty(stringValue(out["workspaceId"]), "workspace-cicy-virtual-employees")
	out["type"] = firstNonEmpty(stringValue(out["type"]), "report")
	out["title"] = firstNonEmpty(stringValue(out["title"]), stringValue(out["id"]), "Runtime Artifact")
	out["producer"] = firstNonEmpty(stringValue(out["producer"]), "w-10004")
	out["status"] = firstNonEmpty(stringValue(out["status"]), "final")
	out["content"] = firstNonEmpty(stringValue(out["content"]), "# "+firstNonEmpty(stringValue(out["id"]), "artifact")+"\n")
	out["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return out
}
