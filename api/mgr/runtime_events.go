package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type runtimeEvent struct {
	EventID           string      `json:"event_id"`
	WorkspaceID       string      `json:"workspace_id,omitempty"`
	EmployeeID        string      `json:"employee_id,omitempty"`
	WorkItemID        string      `json:"work_item_id,omitempty"`
	ArtifactID        string      `json:"artifact_id,omitempty"`
	HandoffID         string      `json:"handoff_id,omitempty"`
	TaskID            interface{} `json:"task_id,omitempty"`
	SessionID         string      `json:"session_id"`
	RuntimeInstanceID string      `json:"runtime_instance_id,omitempty"`
	Type              string      `json:"type"`
	TS                string      `json:"ts"`
	Payload           interface{} `json:"payload,omitempty"`
}

type runtimeEventHub struct {
	mu           sync.RWMutex
	eventsBySess map[string][]runtimeEvent
	seq          int64
}

var rtEvents = &runtimeEventHub{eventsBySess: map[string][]runtimeEvent{}}

func appendRuntimeEvent(sessionID, eventType string, payload interface{}) runtimeEvent {
	sessionID = shortPaneID(normPaneID(sessionID))
	ctx := lookupRuntimeEventContext(sessionID)
	rtEvents.mu.Lock()
	defer rtEvents.mu.Unlock()
	rtEvents.seq++
	evt := runtimeEvent{
		EventID:           "rt_evt_" + strconv.FormatInt(rtEvents.seq, 10),
		WorkspaceID:       firstNonEmpty(ctx.WorkspaceID, "workspace-cicy-virtual-employees"),
		EmployeeID:        ctx.EmployeeID,
		WorkItemID:        ctx.WorkItemID,
		ArtifactID:        ctx.ArtifactID,
		HandoffID:         ctx.HandoffID,
		SessionID:         sessionID,
		RuntimeInstanceID: ctx.InstanceID,
		TaskID:            ctx.TaskID,
		Type:              normalizeRuntimeEventType(eventType),
		TS:                time.Now().Format(time.RFC3339),
		Payload:           payload,
	}
	rtEvents.eventsBySess[sessionID] = append(rtEvents.eventsBySess[sessionID], evt)
	if len(rtEvents.eventsBySess[sessionID]) > 200 {
		rtEvents.eventsBySess[sessionID] = rtEvents.eventsBySess[sessionID][len(rtEvents.eventsBySess[sessionID])-200:]
	}
	_ = appendSharedEvent(M{
		"id":          fmt.Sprintf("event-%d", time.Now().UnixNano()),
		"workspaceId": firstNonEmpty(evt.WorkspaceID, "workspace-cicy-virtual-employees"),
		"actor":       firstNonEmpty(evt.EmployeeID, "w-10004"),
		"type":        "runtime_event",
		"summary":     evt.Type + " @ " + evt.SessionID,
		"payload": M{
			"event_id":            evt.EventID,
			"type":                evt.Type,
			"session_id":          evt.SessionID,
			"runtime_instance_id": evt.RuntimeInstanceID,
			"task_id":             evt.TaskID,
			"work_item_id":        evt.WorkItemID,
			"artifact_id":         evt.ArtifactID,
			"handoff_id":          evt.HandoffID,
			"payload":             evt.Payload,
		},
		"ts": evt.TS,
	})
	return evt
}

func listRuntimeEvents(sessionID string, since string) []runtimeEvent {
	sessionID = shortPaneID(normPaneID(sessionID))
	rtEvents.mu.RLock()
	defer rtEvents.mu.RUnlock()
	items := rtEvents.eventsBySess[sessionID]
	if since == "" {
		out := make([]runtimeEvent, len(items))
		copy(out, items)
		return out
	}
	var out []runtimeEvent
	for _, item := range items {
		if item.EventID > since {
			out = append(out, item)
		}
	}
	return out
}

func bootstrapRuntimeEvents(sessionID string) []runtimeEvent {
	sessionID = shortPaneID(normPaneID(sessionID))
	var out []runtimeEvent
	if st := getPaneStatus(sessionID); st != nil {
		payload := M{}
		if st.Status != nil {
			payload["status"] = *st.Status
		}
		if st.Title != nil {
			payload["title"] = *st.Title
		}
		out = append(out, appendRuntimeEvent(sessionID, "session_status_changed", payload))
	}
	items, err := listQueueItems(sessionID, "", "")
	if err == nil {
		for _, item := range items {
			if stringValue(item["result_summary"]) == "" && item["result_payload"] == nil {
				continue
			}
			for _, art := range runtimeArtifactsFromTask(item) {
				out = append(out, appendRuntimeEvent(sessionID, "artifact_emitted", art))
			}
			break
		}
	}
	return out
}

func getPaneStatus(sessionID string) *paneSt {
	raw := redisDo("GET", "pane_status_map")
	if raw == "" {
		return nil
	}
	m := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	for _, key := range []string{normPaneID(sessionID), sessionID} {
		if item, ok := m[key]; ok {
			var st paneSt
			if json.Unmarshal(item, &st) == nil {
				return &st
			}
		}
	}
	return nil
}

type runtimeEventContext struct {
	InstanceID  string
	TaskID      interface{}
	WorkspaceID string
	WorkItemID  string
	ArtifactID  string
	HandoffID   string
	EmployeeID  string
}

func lookupRuntimeEventContext(sessionID string) runtimeEventContext {
	paneID := normPaneID(sessionID)
	ctx := runtimeEventContext{}
	var machineID sql.NullInt64
	_ = store.QueryRow("SELECT machine_id FROM agent_config WHERE pane_id=?", paneID).Scan(&machineID)
	if machineID.Valid {
		ctx.InstanceID = strconv.FormatInt(machineID.Int64, 10)
	}
	var taskID sql.NullInt64
	var workspaceID, workItemID, artifactID, handoffID, employeeID sql.NullString
	_ = store.QueryRow("SELECT id, workspace_id, work_item_id, artifact_id, handoff_id, employee_id FROM agent_queue WHERE target_pane_id=? ORDER BY id DESC LIMIT 1", shortPaneID(paneID)).
		Scan(&taskID, &workspaceID, &workItemID, &artifactID, &handoffID, &employeeID)
	if taskID.Valid {
		ctx.TaskID = taskID.Int64
	}
	ctx.WorkspaceID = workspaceID.String
	ctx.WorkItemID = workItemID.String
	ctx.ArtifactID = artifactID.String
	ctx.HandoffID = handoffID.String
	ctx.EmployeeID = employeeID.String
	return ctx
}

func normalizeRuntimeEventType(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case "status_change":
		return "session_status_changed"
	case "worker_idle":
		return "task_completed"
	case "http_log":
		return "tool_result"
	default:
		return eventType
	}
}
