package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func handleCollabSteps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		handleCollabStepList(w, r)
	case "POST":
		handleCollabStepCreate(w, r)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleCollabStepByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/collab/steps/")
	id, err := strconv.Atoi(strings.Trim(idStr, "/"))
	if err != nil {
		httpErr(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "PATCH":
		handleQueueUpdate(w, r, id)
	case "GET":
		item, err := getQueueItem(id)
		if err != nil {
			httpErr(w, 404, "step not found")
			return
		}
		J(w, item)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleCollabWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		handleWorkflowCreate(w, r)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleCollabWorkflowByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/collab/workflows/"), "/")
	if id == "" {
		httpErr(w, 400, "workflow id required")
		return
	}
	var title, description, status, createdBy, createdAt, updatedAt sql.NullString
	var completedAt sql.NullString
	err := store.QueryRow("SELECT title, description, status, created_by, created_at, updated_at, completed_at FROM workflows WHERE id=?", id).
		Scan(&title, &description, &status, &createdBy, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		httpErr(w, 404, "workflow not found")
		return
	}
	steps, err := listQueueItems("", "", id)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{
		"workflow": M{
			"id":           id,
			"title":        title.String,
			"description":  description.String,
			"status":       status.String,
			"created_by":   createdBy.String,
			"created_at":   createdAt.String,
			"updated_at":   updatedAt.String,
			"completed_at": completedAt.String,
		},
		"steps": steps,
	})
}

func handleCollabStepCreate(w http.ResponseWriter, r *http.Request) {
	var req M
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	paneID := firstNonEmpty(stringValue(req["pane_id"]), stringValue(req["target_pane_id"]))
	message := stringValue(req["message"])
	if paneID == "" || message == "" {
		httpErr(w, 400, "target_pane_id and message required")
		return
	}
	stepType := firstNonEmpty(stringValue(req["type"]), "message")
	stepKind := firstNonEmpty(stringValue(req["step_kind"]), stepType)
	priority := intValue(req["priority"])
	title := stringValue(req["title"])
	workflowID := nullableInt(req["workflow_id"])
	parentID := nullableInt(req["parent_id"])
	stepIndex := intValue(req["step_index"])
	targetMachineID := nullableInt(req["target_machine_id"])
	createdBy := stringValue(req["created_by"])
	workspaceID := stringValue(req["workspace_id"])
	workItemID := stringValue(req["work_item_id"])
	handoffID := stringValue(req["handoff_id"])
	employeeID := stringValue(req["employee_id"])
	res, err := store.Exec(`INSERT INTO agent_queue (
		pane_id, message, type, priority, status, step_kind, workflow_id, parent_id, step_index, title,
		target_machine_id, target_pane_id, created_by, result_summary, result_payload,
		workspace_id, work_item_id, handoff_id, employee_id
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		normPaneID(paneID), message, stepType, priority, "pending", stepKind, workflowID, parentID, stepIndex, title,
		targetMachineID, shortPaneID(normPaneID(paneID)), createdBy, "", "",
		workspaceID, workItemID, handoffID, employeeID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	item, _ := getQueueItem(int(id))
	J(w, M{"success": true, "step": item})
}

func handleCollabStepList(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	status := r.URL.Query().Get("status")
	workflowID := r.URL.Query().Get("workflow_id")
	items, err := listQueueItems(pane, status, workflowID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"steps": items})
}

func handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		CreatedBy   string `json:"created_by"`
		Steps       []M    `json:"steps"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if req.Title == "" {
		httpErr(w, 400, "title required")
		return
	}
	res, err := store.Exec("INSERT INTO workflows (title, description, status, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?)",
		req.Title, req.Description, "pending", req.CreatedBy, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	workflowID, _ := res.LastInsertId()
	createdSteps := []M{}
	for i, step := range req.Steps {
		paneID := firstNonEmpty(stringValue(step["pane_id"]), stringValue(step["target_pane_id"]))
		message := stringValue(step["message"])
		if paneID == "" || message == "" {
			continue
		}
		stepType := firstNonEmpty(stringValue(step["type"]), "message")
		stepKind := firstNonEmpty(stringValue(step["step_kind"]), stepType)
		title := firstNonEmpty(stringValue(step["title"]), req.Title)
		priority := intValue(step["priority"])
		targetMachineID := nullableInt(step["target_machine_id"])
		stepRes, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, workflow_id, step_index, title,
			target_machine_id, target_pane_id, created_by, result_summary, result_payload
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(paneID), message, stepType, priority, "pending", stepKind, workflowID, i+1, title,
			targetMachineID, shortPaneID(normPaneID(paneID)), req.CreatedBy, "", "")
		if err != nil {
			continue
		}
		stepID, _ := stepRes.LastInsertId()
		item, _ := getQueueItem(int(stepID))
		createdSteps = append(createdSteps, item)
	}
	J(w, M{"success": true, "workflow_id": workflowID, "steps": createdSteps})
}

func listQueueItems(pane, status, workflowID string) ([]M, error) {
	query := `SELECT q.id, q.pane_id, q.message, q.type, q.status, q.priority, q.created_at, q.sent_at,
		q.step_kind, q.workflow_id, q.parent_id, q.step_index, q.title, q.result_summary, q.result_payload,
		q.target_machine_id, q.target_pane_id, q.created_by, q.completed_at, q.workspace_id, q.work_item_id,
		q.artifact_id, q.handoff_id, q.employee_id, m.label, ac.title
		FROM agent_queue q
		LEFT JOIN machines m ON q.target_machine_id=m.id
		LEFT JOIN agent_config ac ON ac.pane_id=q.pane_id
		WHERE 1=1`
	args := []interface{}{}
	if pane != "" {
		query += " AND q.pane_id=?"
		args = append(args, normPaneID(pane))
	}
	if status != "" {
		query += " AND q.status=?"
		args = append(args, status)
	}
	if workflowID != "" {
		query += " AND q.workflow_id=?"
		args = append(args, workflowID)
	}
	query += " ORDER BY q.workflow_id ASC, q.step_index ASC, q.priority DESC, q.id ASC"
	rows, err := store.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []M
	for rows.Next() {
		var id, priority int
		var paneID, message, typ, st string
		var createdAt, sentAt, stepKind, title, resultSummary, resultPayload, targetPaneID, createdBy sql.NullString
		var workspaceID, workItemID, artifactID, handoffID, employeeID sql.NullString
		var workflowIDValue, parentID, stepIndex, targetMachineID sql.NullInt64
		var completedAt, machineLabel, paneTitle sql.NullString
		rows.Scan(&id, &paneID, &message, &typ, &st, &priority, &createdAt, &sentAt,
			&stepKind, &workflowIDValue, &parentID, &stepIndex, &title, &resultSummary, &resultPayload,
			&targetMachineID, &targetPaneID, &createdBy, &completedAt, &workspaceID, &workItemID,
			&artifactID, &handoffID, &employeeID, &machineLabel, &paneTitle)
		item := M{
			"id":             id,
			"pane_id":        shortPaneID(paneID),
			"message":        message,
			"type":           typ,
			"status":         st,
			"priority":       priority,
			"step_kind":      stepKind.String,
			"step_index":     stepIndex.Int64,
			"title":          title.String,
			"result_summary": resultSummary.String,
			"result_payload": parseJSONOrString(resultPayload.String),
			"target_pane_id": targetPaneID.String,
			"created_by":     createdBy.String,
			"workspace_id":   workspaceID.String,
			"work_item_id":   workItemID.String,
			"artifact_id":    artifactID.String,
			"handoff_id":     handoffID.String,
			"employee_id":    employeeID.String,
			"machine_label":  machineLabel.String,
			"pane_title":     paneTitle.String,
		}
		if workflowIDValue.Valid {
			item["workflow_id"] = workflowIDValue.Int64
		}
		if parentID.Valid {
			item["parent_id"] = parentID.Int64
		}
		if targetMachineID.Valid {
			item["target_machine_id"] = targetMachineID.Int64
		}
		if createdAt.Valid {
			item["created_at"] = createdAt.String
		}
		if sentAt.Valid {
			item["sent_at"] = sentAt.String
		}
		if completedAt.Valid {
			item["completed_at"] = completedAt.String
		}
		items = append(items, item)
	}
	if items == nil {
		items = []M{}
	}
	return items, nil
}

func getQueueItem(id int) (M, error) {
	items, err := listQueueItems("", "", "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if intValue(item["id"]) == id {
			return item, nil
		}
	}
	return nil, sql.ErrNoRows
}

func parseJSONOrString(raw string) interface{} {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var v interface{}
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return raw
}

func intValue(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(t)
		return i
	default:
		return 0
	}
}

func nullableInt(v interface{}) interface{} {
	if intValue(v) == 0 {
		return nil
	}
	return intValue(v)
}
