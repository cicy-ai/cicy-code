package main

import "net/http"

type skillDefinition struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Description   string    `json:"description"`
	Icon          string    `json:"icon"`
	Mode          string    `json:"mode"`
	DefaultTarget string    `json:"default_target"`
	Template      string    `json:"template"`
	StepKind      string    `json:"step_kind,omitempty"`
	WorkflowSteps []stepDef `json:"workflow_steps,omitempty"`
}

type stepDef struct {
	Title    string `json:"title"`
	StepKind string `json:"step_kind"`
	Type     string `json:"type"`
	Template string `json:"template"`
}

func builtinSkills() []skillDefinition {
	return []skillDefinition{
		{ID: "review", Label: "代码审查", Description: "创建 review step", Icon: "🔍", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /review skill from gstack to do a pre-landing code review on the current branch", StepKind: "review"},
		{ID: "qa", Label: "QA 测试", Description: "创建 QA step", Icon: "🧪", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /qa skill from gstack to QA test the app", StepKind: "qa"},
		{ID: "ship", Label: "发布", Description: "创建 review → qa → ship workflow", Icon: "🚀", Mode: "create_workflow", DefaultTarget: "bound_worker", Template: "Use the /ship skill from gstack to run tests, review, and create a PR", WorkflowSteps: []stepDef{
			{Title: "Review", StepKind: "review", Type: "message", Template: "Use the /review skill from gstack to do a pre-landing code review on the current branch"},
			{Title: "QA", StepKind: "qa", Type: "message", Template: "Use the /qa skill from gstack to QA test the app"},
			{Title: "Ship", StepKind: "ship", Type: "message", Template: "Use the /ship skill from gstack to run tests, review, and create a PR"},
		}},
		{ID: "investigate", Label: "调试", Description: "创建 investigate step", Icon: "🔧", Mode: "create_step", DefaultTarget: "current_pane", Template: "Use the /investigate skill from gstack to systematically debug the current issue", StepKind: "task"},
		{ID: "office-hours", Label: "CEO 顾问", Description: "直接发送顾问 prompt", Icon: "🧠", Mode: "direct_prompt", DefaultTarget: "current_pane", Template: "Use the /office-hours skill from gstack"},
		{ID: "document-release", Label: "更新文档", Description: "创建文档更新 step", Icon: "📄", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /document-release skill from gstack to update all docs after shipping", StepKind: "task"},
	}
}

func handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	J(w, M{"skills": builtinSkills()})
}

func handleSkillRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		SkillID         string `json:"skill_id"`
		TargetPaneID    string `json:"target_pane_id"`
		TargetMachineID int    `json:"target_machine_id"`
		CurrentPaneID   string `json:"current_pane_id"`
		CreatedBy       string `json:"created_by"`
		Title           string `json:"title"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	var skill *skillDefinition
	for _, item := range builtinSkills() {
		if item.ID == req.SkillID {
			copy := item
			skill = &copy
			break
		}
	}
	if skill == nil {
		httpErr(w, 404, "skill not found")
		return
	}
	targetPaneID := firstNonEmpty(req.TargetPaneID, req.CurrentPaneID)
	if targetPaneID == "" {
		httpErr(w, 400, "target_pane_id required")
		return
	}
	switch skill.Mode {
	case "direct_prompt":
		res, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, title, target_machine_id, target_pane_id, created_by
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(targetPaneID), skill.Template, "message", 0, "pending", "message", firstNonEmpty(req.Title, skill.Label), nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		step, _ := getQueueItem(int(id))
		J(w, M{"success": true, "mode": skill.Mode, "step": step})
	case "create_step":
		stepReq := M{
			"pane_id":           targetPaneID,
			"target_pane_id":    targetPaneID,
			"message":           skill.Template,
			"type":              "message",
			"step_kind":         skill.StepKind,
			"title":             firstNonEmpty(req.Title, skill.Label),
			"target_machine_id": req.TargetMachineID,
			"created_by":        req.CreatedBy,
		}
		res, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, title, target_machine_id, target_pane_id, created_by
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(targetPaneID), skill.Template, "message", 0, "pending", skill.StepKind, firstNonEmpty(req.Title, skill.Label), nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		step, _ := getQueueItem(int(id))
		J(w, M{"success": true, "mode": skill.Mode, "step": step, "request": stepReq})
	case "create_workflow":
		workflowRes, err := store.Exec("INSERT INTO workflows (title, description, status, created_by, created_at, updated_at) VALUES (?,?,?,?,datetime('now'),datetime('now'))",
			firstNonEmpty(req.Title, skill.Label), skill.Description, "pending", req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		workflowID, _ := workflowRes.LastInsertId()
		steps := []M{}
		for i, step := range skill.WorkflowSteps {
			res, err := store.Exec(`INSERT INTO agent_queue (
				pane_id, message, type, priority, status, step_kind, workflow_id, step_index, title, target_machine_id, target_pane_id, created_by
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				normPaneID(targetPaneID), step.Template, step.Type, 0, "pending", step.StepKind, workflowID, i+1, step.Title, nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
			if err != nil {
				continue
			}
			id, _ := res.LastInsertId()
			item, _ := getQueueItem(int(id))
			steps = append(steps, item)
		}
		J(w, M{"success": true, "mode": skill.Mode, "workflow_id": workflowID, "steps": steps})
	default:
		httpErr(w, 400, "unsupported skill mode")
	}
}
