// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	containerReportedRegionOnce sync.Once
	containerReportedRegion     string
)

func handleRuntimeInstances(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		instances, err := listMachines()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"instances": instances})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleRuntimeInstanceRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		InstanceID     string                 `json:"instance_id"`
		InstanceKey    string                 `json:"instance_key"`
		InstanceLabel  string                 `json:"instance_label"`
		RuntimeKind    string                 `json:"runtime_kind"`
		ReportedRegion string                 `json:"reported_region"`
		Endpoint       string                 `json:"endpoint"`
		Token          string                 `json:"token"`
		Status         string                 `json:"status"`
		LastSeenAt     string                 `json:"last_seen_at"`
		Capabilities   map[string]interface{} `json:"capabilities"`
		Host           string                 `json:"host"`
		Port           int                    `json:"port"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	node := normalizeInstanceNode(machineConfigNode{
		ID:           firstNonEmpty(req.InstanceID, req.InstanceKey),
		MachineKey:   firstNonEmpty(req.InstanceKey, req.InstanceID),
		Label:        firstNonEmpty(req.InstanceLabel, req.InstanceKey),
		Host:         req.Host,
		Port:         req.Port,
		URL:          req.Endpoint,
		Token:        req.Token,
		Status:       req.Status,
		LastSeenAt:   req.LastSeenAt,
		Capabilities: req.Capabilities,
	})
	if node.Capabilities == nil {
		node.Capabilities = M{}
	}
	if req.RuntimeKind != "" {
		node.Capabilities["runtime_kind"] = req.RuntimeKind
	}
	id, err := upsertMachine(node)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !isContainerRuntime() {
		cfg := loadMachineConfig()
		updated := false
		for i := range cfg.Machines {
			if cfg.Machines[i].MachineKey == node.MachineKey {
				cfg.Machines[i] = node
				updated = true
				break
			}
		}
		if !updated {
			cfg.Machines = append(cfg.Machines, node)
		}
		if cfg.Default == "" {
			cfg.Default = node.MachineKey
		}
		if err := saveMachineConfig(cfg); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	instance, _ := findMachineByID(strconv.FormatInt(id, 10))
	J(w, M{"success": true, "instance": instance})
}

func normalizeReportedRegion(parts ...string) string {
	seen := map[string]bool{}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Join(strings.Fields(part), " ")
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, " ")
}

func fetchReportedRegionFromJSON(url string, countryKeys []string, regionKeys []string, cityKeys []string) string {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json,text/plain,*/*")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	pick := func(keys []string) string {
		for _, key := range keys {
			if value, ok := payload[key]; ok {
				if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	return normalizeReportedRegion(pick(countryKeys), pick(regionKeys), pick(cityKeys))
}

func fetchReportedRegionFromIPIP() string {
	req, err := http.NewRequest("GET", "https://myip.ipip.net", nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		return ""
	}
	text := strings.TrimSpace(raw.String())
	if text == "" {
		return ""
	}
	marker := "来自于："
	idx := strings.Index(text, marker)
	if idx < 0 {
		marker = "来自于:"
		idx = strings.Index(text, marker)
	}
	if idx < 0 {
		return ""
	}
	return normalizeReportedRegion(strings.TrimSpace(text[idx+len(marker):]))
}

func detectContainerReportedRegion() string {
	providers := []func() string{
		fetchReportedRegionFromIPIP,
		func() string {
			return fetchReportedRegionFromJSON(
				"https://ip.sb/geoip",
				[]string{"country", "country_name"},
				[]string{"region", "region_name"},
				[]string{"city"},
			)
		},
		func() string {
			return fetchReportedRegionFromJSON(
				"https://ipapi.co/json/",
				[]string{"country_name"},
				[]string{"region"},
				[]string{"city"},
			)
		},
	}
	for _, provider := range providers {
		if region := strings.TrimSpace(provider()); region != "" {
			return region
		}
	}
	return ""
}

func containerReportedRegionLabel() string {
	containerReportedRegionOnce.Do(func() {
		containerReportedRegion = detectContainerReportedRegion()
	})
	return strings.TrimSpace(containerReportedRegion)
}

func handleRuntimeSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/runtime/sessions/"), "/")
	if strings.HasSuffix(sessionID, "/events") {
		sessionID = strings.TrimSuffix(sessionID, "/events")
	}
	sessionID = strings.Trim(sessionID, "/")
	if sessionID == "" {
		httpErr(w, 400, "session id required")
		return
	}
	since := r.URL.Query().Get("since")
	switch r.Method {
	case "GET":
		items := listRuntimeEvents(sessionID, since)
		if len(items) == 0 && since == "" {
			items = bootstrapRuntimeEvents(sessionID)
		}
		J(w, M{"events": items})
	case "POST":
		var req struct {
			Type    string      `json:"type"`
			Payload interface{} `json:"payload"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		if req.Type == "" {
			httpErr(w, 400, "type required")
			return
		}
		evt := appendRuntimeEvent(sessionID, req.Type, req.Payload)
		J(w, M{"success": true, "event": evt})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleRuntimeTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		items, err := listQueueItems(r.URL.Query().Get("pane"), r.URL.Query().Get("status"), r.URL.Query().Get("workflow_id"))
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"tasks": runtimeTasksView(items)})
	case "POST":
		var req struct {
			Title            string `json:"title"`
			Kind             string `json:"kind"`
			WorkflowID       any    `json:"workflow_id"`
			TargetInstanceID any    `json:"target_instance_id"`
			TargetSessionID  string `json:"target_session_id"`
			CreatedBy        string `json:"created_by"`
			WorkspaceID      string `json:"workspace_id"`
			WorkItemID       string `json:"work_item_id"`
			HandoffID        string `json:"handoff_id"`
			EmployeeID       string `json:"employee_id"`
			Payload          struct {
				Message string `json:"message"`
			} `json:"payload"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		if req.TargetSessionID == "" || req.Payload.Message == "" {
			httpErr(w, 400, "target_session_id and payload.message required")
			return
		}
		res, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, workflow_id, title,
			target_machine_id, target_pane_id, created_by, workspace_id, work_item_id, handoff_id, employee_id
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(req.TargetSessionID), req.Payload.Message, "message", 0, "pending", firstNonEmpty(req.Kind, "task"), nullableInt(req.WorkflowID), req.Title,
			nullableInt(req.TargetInstanceID), req.TargetSessionID, req.CreatedBy, req.WorkspaceID, req.WorkItemID, req.HandoffID, req.EmployeeID)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		item, _ := getQueueItem(int(id))
		_ = syncSharedWorkItem(item)
		_ = syncSharedHandoff(item)
		J(w, M{"success": true, "task": runtimeTaskView(item)})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleRuntimeTaskByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/runtime/tasks/"), "/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		httpErr(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "GET":
		item, err := getQueueItem(id)
		if err != nil {
			httpErr(w, 404, "task not found")
			return
		}
		J(w, M{"task": runtimeTaskView(item)})
	case "PATCH":
		handleQueueUpdate(w, r, id)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleRuntimeArtifacts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		taskID := r.URL.Query().Get("task_id")
		if taskID == "" {
			httpErr(w, 400, "task_id required")
			return
		}
		id, err := strconv.Atoi(taskID)
		if err != nil {
			httpErr(w, 400, "invalid task_id")
			return
		}
		item, err := getQueueItem(id)
		if err != nil {
			httpErr(w, 404, "task not found")
			return
		}
		J(w, M{"artifacts": runtimeArtifactsFromTask(item)})
	case "POST":
		var req struct {
			TaskID      int         `json:"task_id"`
			Kind        string      `json:"kind"`
			Summary     string      `json:"summary"`
			Payload     interface{} `json:"payload"`
			ArtifactID  string      `json:"artifact_id"`
			WorkspaceID string      `json:"workspace_id"`
			WorkItemID  string      `json:"work_item_id"`
			HandoffID   string      `json:"handoff_id"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		payloadJSON := machineCapabilitiesJSON(req.Payload)
		_, err := store.Exec("UPDATE agent_queue SET result_summary=?, result_payload=?, completed_at=?, status=?, artifact_id=?, workspace_id=COALESCE(NULLIF(workspace_id,''), ?), work_item_id=COALESCE(NULLIF(work_item_id,''), ?), handoff_id=COALESCE(NULLIF(handoff_id,''), ?) WHERE id=?",
			req.Summary, payloadJSON, time.Now().Format(time.RFC3339), "completed", req.ArtifactID, req.WorkspaceID, req.WorkItemID, req.HandoffID, req.TaskID)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		item, _ := getQueueItem(req.TaskID)
		artifacts := runtimeArtifactsFromTask(item)
		artifact := M{}
		if len(artifacts) > 0 {
			artifact = artifacts[0]
		}
		if req.Kind != "" {
			artifact["kind"] = req.Kind
		}
		_ = syncSharedArtifact(artifact)
		_ = syncSharedWorkItem(item)
		_ = syncSharedHandoff(item)
		J(w, M{"success": true, "artifact": artifact})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func runtimeTasksView(items []M) []M {
	out := make([]M, 0, len(items))
	for _, item := range items {
		out = append(out, runtimeTaskView(item))
	}
	return out
}

func runtimeTaskView(item M) M {
	view := M{}
	for k, v := range item {
		view[k] = v
	}
	view["task_id"] = item["id"]
	view["kind"] = firstNonEmpty(stringValue(item["step_kind"]), stringValue(item["type"]))
	view["target_instance_id"] = item["target_machine_id"]
	view["target_session_id"] = firstNonEmpty(stringValue(item["target_pane_id"]), stringValue(item["pane_id"]))
	view["session_id"] = firstNonEmpty(stringValue(item["target_pane_id"]), stringValue(item["pane_id"]))
	view["artifact_count"] = len(runtimeArtifactsFromTask(item))
	view["workspace_id"] = stringValue(item["workspace_id"])
	view["work_item_id"] = stringValue(item["work_item_id"])
	view["handoff_id"] = stringValue(item["handoff_id"])
	view["employee_id"] = stringValue(item["employee_id"])
	return view
}

func runtimeArtifactsFromTask(item M) []M {
	summary := stringValue(item["result_summary"])
	payload := item["result_payload"]
	if summary == "" && payload == nil {
		return []M{}
	}
	taskID := intValue(item["id"])
	kind := "summary"
	artifactID := firstNonEmpty(stringValue(item["artifact_id"]), "task-"+strconv.Itoa(taskID)+"-summary")
	art := M{
		"artifact_id":  artifactID,
		"task_id":      taskID,
		"kind":         kind,
		"summary":      summary,
		"payload":      payload,
		"workspace_id": stringValue(item["workspace_id"]),
		"work_item_id": stringValue(item["work_item_id"]),
		"handoff_id":   stringValue(item["handoff_id"]),
	}
	if createdAt := stringValue(item["completed_at"]); createdAt != "" {
		art["created_at"] = createdAt
	} else if createdAt := stringValue(item["sent_at"]); createdAt != "" {
		art["created_at"] = createdAt
	} else if createdAt := stringValue(item["created_at"]); createdAt != "" {
		art["created_at"] = createdAt
	}
	return []M{art}
}
