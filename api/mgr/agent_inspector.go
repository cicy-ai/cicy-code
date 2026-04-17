package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	agentInspectorDefaultReplyInstruction = "always reply in chinese"
	agentInspectorHistoryDefaultLimit     = 24
	agentInspectorHistoryMaxLimit         = 100
	agentInspectorTmuxHistoryCaptureStart = -400
)

var agentInspectorHistoryMergeWindow = 20 * time.Minute
var agentInspectorCodexLeftRe = regexp.MustCompile(`(\d+)%\s+left`)

type agentInspectorTextFile struct {
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at"`
}

type agentInspectorRuntimeMemoryFile struct {
	Enabled   bool   `json:"enabled"`
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at"`
}

type agentInspectorPromptRule struct {
	Scope           string `json:"scope"`
	Key             string `json:"key,omitempty"`
	Label           string `json:"label,omitempty"`
	Content         string `json:"content"`
	Enabled         bool   `json:"enabled"`
	InjectOnRequest bool   `json:"inject_on_request"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Available       bool   `json:"available"`
}

type agentInspectorPromptRules struct {
	Global      agentInspectorPromptRule `json:"global"`
	Project     agentInspectorPromptRule `json:"project"`
	Agent       agentInspectorPromptRule `json:"agent"`
	ProjectKey  string                   `json:"project_key,omitempty"`
	Overlay     string                   `json:"overlay_preview,omitempty"`
	OverlayPath string                   `json:"overlay_path,omitempty"`
	UpdatedAt   string                   `json:"updated_at,omitempty"`
}

type agentInspectorHistoryItem struct {
	ID         int      `json:"id"`
	Q          string   `json:"q,omitempty"`
	A          string   `json:"a,omitempty"`
	QTime      string   `json:"qTime,omitempty"`
	ATime      string   `json:"aTime,omitempty"`
	Model      string   `json:"model,omitempty"`
	Thinking   string   `json:"thinking,omitempty"`
	ToolNames  []string `json:"tool_names,omitempty"`
	MatchField string   `json:"match_field,omitempty"`
	Snippet    string   `json:"snippet,omitempty"`
	MergeCount int      `json:"merge_count,omitempty"`
}

type agentInspectorHistoryPage struct {
	Query   string                      `json:"query"`
	Total   int                         `json:"total"`
	Offset  int                         `json:"offset"`
	Limit   int                         `json:"limit"`
	HasMore bool                        `json:"has_more"`
	Managed bool                        `json:"managed"`
	Reason  string                      `json:"reason,omitempty"`
	Items   []agentInspectorHistoryItem `json:"items"`
}

type agentHistorySyncItem struct {
	ID          string                 `json:"id"`
	Seq         int                    `json:"seq"`
	Role        string                 `json:"role"`
	Text        string                 `json:"text,omitempty"`
	ContentJSON map[string]interface{} `json:"content_json,omitempty"`
	TS          string                 `json:"ts,omitempty"`
	TurnID      string                 `json:"turn_id,omitempty"`
	Status      string                 `json:"status,omitempty"`
	Model       string                 `json:"model,omitempty"`
}

func agentInspectorPaneRegistered(agentID string) bool {
	var count int
	shortID := shortPaneID(agentID)
	fullID := normPaneID(agentID)
	if err := store.QueryRow(
		"SELECT COUNT(*) FROM agent_config WHERE pane_id IN (?,?,?)",
		shortID,
		fullID,
		shortID+":main.0",
	).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func agentInspectorPanePID(agentID string) int {
	out, err := runTmux("display-message", "-p", "-t", normPaneID(agentID), "#{pane_pid}")
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(out))
	return pid
}

func agentInspectorPaneRuntimeManaged(agentID string) bool {
	if !agentInspectorPaneRegistered(agentID) {
		return false
	}
	pid := agentInspectorPanePID(agentID)
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil || len(data) == 0 {
		return false
	}
	shortID := shortPaneID(agentID)
	var hasAgentID bool
	var hasAgentShortID bool
	var shortIDMatched bool
	for _, raw := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(raw, "X_AGENT_ID=") {
			hasAgentID = strings.TrimSpace(strings.TrimPrefix(raw, "X_AGENT_ID=")) != ""
			continue
		}
		if strings.HasPrefix(raw, "X_AGENT_SHORT_ID=") {
			hasAgentShortID = strings.TrimSpace(strings.TrimPrefix(raw, "X_AGENT_SHORT_ID=")) != ""
			shortIDMatched = strings.TrimSpace(strings.TrimPrefix(raw, "X_AGENT_SHORT_ID=")) == shortID
		}
	}
	return hasAgentID && hasAgentShortID && shortIDMatched
}

func agentInspectorHistoryUnavailableReason(agentID string) string {
	if !agentInspectorPaneRegistered(agentID) {
		return "当前 pane 不是受管 agent，会话历史无法和 tmux 当前内容对齐。"
	}
	if !agentInspectorPaneRuntimeManaged(agentID) {
		return "当前 pane 虽然保留了 agent 记录，但 tmux 里跑的是脱管会话，history 无法和当前内容对齐。"
	}
	return ""
}

func agentInspectorStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "thinking":
		return "Thinking 思考中"
	case "streaming", "working", "tool_call":
		return "Working 工作中"
	case "completed", "done", "idle":
		return "Idle 空闲"
	case "wait_auth":
		return "Wait Auth 等待授权"
	case "compacting":
		return "Compacting 压缩中"
	case "error", "failed":
		return "Failed 失败"
	default:
		if strings.TrimSpace(status) == "" {
			return "Idle 空闲"
		}
		return status
	}
}

func agentInspectorStatusIsActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "idle", "completed", "done", "error", "failed":
		return false
	default:
		return true
	}
}

func agentInspectorReplyHasActivePrimaryRequest(reply aiGatewayReplySnapshot) bool {
	for _, request := range aiGatewayFilterPrimaryRequests(reply.HTTPRequests) {
		if strings.EqualFold(strings.TrimSpace(request.Status), "streaming") {
			return true
		}
	}
	return false
}

func agentInspectorSnapshotFresh(current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) bool {
	now := time.Now().UTC()
	latest := time.Time{}
	for _, raw := range []string{current.UpdatedAt, reply.UpdatedAt, current.StartedAt, reply.StartedAt} {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		if ts.After(latest) {
			latest = ts
		}
	}
	if latest.IsZero() {
		return false
	}
	return now.Sub(latest) <= 5*time.Minute
}

func agentInspectorStatusMap(snapshot aiGatewayReplySnapshot) M {
	items := make([]M, 0, len(snapshot.StatusMap.Items))
	for _, item := range snapshot.StatusMap.Items {
		items = append(items, M{
			"kind":   item.Kind,
			"label":  agentInspectorStatusLabel(aiGatewayFirstNonEmpty(item.Label, item.Kind)),
			"active": item.Active,
			"count":  item.Count,
		})
	}
	return M{
		"primary": aiGatewayFirstNonEmpty(snapshot.StatusMap.Primary, snapshot.Status),
		"items":   items,
	}
}

func agentInspectorGatewayStatus(current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) string {
	statusMap := aiGatewayBuildStatusMap(current, reply)
	return aiGatewayFirstNonEmpty(statusMap.Primary, reply.Status, current.Status, "idle")
}

func agentInspectorUsageTotals(reply aiGatewayReplySnapshot) (int, int, int) {
	return aiGatewayReplyTotals(reply)
}

func agentInspectorLatestPrimaryRequest(reply aiGatewayReplySnapshot) *aiGatewayRequestSpan {
	items := aiGatewayFilterPrimaryRequests(reply.HTTPRequests)
	if len(items) == 0 {
		return nil
	}
	latest := items[len(items)-1]
	return &latest
}

func agentInspectorCurrentUsageMap(reply aiGatewayReplySnapshot) map[string]interface{} {
	if len(reply.LastUsage) > 0 {
		return aiGatewayCloneAnyMap(reply.LastUsage)
	}
	if latest := agentInspectorLatestPrimaryRequest(reply); latest != nil {
		if len(latest.Usage) > 0 {
			return aiGatewayCloneAnyMap(latest.Usage)
		}
	}
	if len(reply.Usage) > 0 {
		return aiGatewayCloneAnyMap(reply.Usage)
	}
	return nil
}

func agentInspectorCurrentUsageMetrics(reply aiGatewayReplySnapshot) (int, int, int, float64, bool) {
	if reply.LastInputTokens > 0 || reply.LastOutputTokens > 0 || reply.LastTotalTokens > 0 || reply.LastCostCredit > 0 {
		return reply.LastInputTokens, reply.LastOutputTokens, reply.LastTotalTokens, reply.LastCostCredit, true
	}
	usage := agentInspectorCurrentUsageMap(reply)
	if len(usage) == 0 {
		return 0, 0, 0, 0, false
	}
	inputTokens, outputTokens, totalTokens := aiGatewayUsageTotals(usage)
	costCredit := aiGatewayEstimateCostCredit(aiGatewayFirstNonEmpty(reply.Models...), usage)
	if inputTokens > 0 || outputTokens > 0 || totalTokens > 0 || costCredit > 0 {
		return inputTokens, outputTokens, totalTokens, costCredit, true
	}
	return 0, 0, 0, 0, false
}

func agentInspectorLiveContextUsage(agentID string) interface{} {
	out, err := runTmux("capture-pane", "-t", normPaneID(agentID), "-p")
	if err != nil {
		return nil
	}
	matches := agentInspectorCodexLeftRe.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return nil
	}
	left, err := strconv.Atoi(matches[len(matches)-1][1])
	if err != nil {
		return nil
	}
	used := 100 - left
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return used
}

func agentInspectorNotesPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "inspector_notes.json")
}

func agentInspectorRuntimeMemoryPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "runtime_memory.json")
}

func agentInspectorPromptRulesPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "prompt_rules.json")
}

func agentInspectorPaneKeys(agentID string) []string {
	shortID := shortPaneID(agentID)
	fullID := normPaneID(agentID)
	return []string{fullID, shortID, shortID + ":main.0"}
}

func agentInspectorLoadPaneConfig(agentID string) (string, string, string) {
	keys := agentInspectorPaneKeys(agentID)
	var workspace, configJSON, sourceRef string
	_ = store.QueryRow(
		`SELECT COALESCE(workspace, ''), COALESCE(config, '{}'), COALESCE(source_ref, '')
		 FROM agent_config
		 WHERE pane_id IN (?,?,?)
		 ORDER BY CASE
		   WHEN pane_id=? THEN 0
		   WHEN pane_id=? THEN 1
		   ELSE 2
		 END
		 LIMIT 1`,
		keys[0], keys[1], keys[2],
		keys[0], keys[2],
	).Scan(&workspace, &configJSON, &sourceRef)
	return workspace, configJSON, sourceRef
}

func agentInspectorProjectKey(agentID string) string {
	workspace, configJSON, sourceRef := agentInspectorLoadPaneConfig(agentID)
	var cfg struct {
		Projects []string `json:"projects"`
		Project  string   `json:"project"`
	}
	if strings.TrimSpace(configJSON) != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg)
	}
	if len(cfg.Projects) > 0 && strings.TrimSpace(cfg.Projects[0]) != "" {
		return strings.TrimSpace(cfg.Projects[0])
	}
	if strings.TrimSpace(cfg.Project) != "" {
		return strings.TrimSpace(cfg.Project)
	}
	if strings.TrimSpace(sourceRef) != "" {
		return strings.TrimSpace(sourceRef)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	clean := filepath.Clean(workspace)
	if matched, _ := regexp.MatchString(`/workers/w-\d+$`, clean); matched {
		return ""
	}
	return clean
}

func agentInspectorLoadPromptRule(scopeType string, scopeKey string) agentInspectorPromptRule {
	rule := agentInspectorPromptRule{
		Scope:     scopeType,
		Key:       scopeKey,
		Available: strings.TrimSpace(scopeKey) != "",
	}
	if !rule.Available {
		return rule
	}
	var enabled, inject int
	err := store.QueryRow(
		"SELECT COALESCE(content, ''), COALESCE(enabled, 0), COALESCE(inject_on_request, 0), COALESCE(updated_at, '') FROM prompt_rules WHERE scope_type=? AND scope_key=?",
		scopeType,
		scopeKey,
	).Scan(&rule.Content, &enabled, &inject, &rule.UpdatedAt)
	if err != nil {
		return rule
	}
	rule.Enabled = enabled != 0
	rule.InjectOnRequest = inject != 0
	return rule
}

func agentInspectorSavePromptRule(scopeType string, scopeKey string, rule agentInspectorPromptRule) (agentInspectorPromptRule, error) {
	rule.Scope = scopeType
	rule.Key = scopeKey
	rule.Available = strings.TrimSpace(scopeKey) != ""
	if !rule.Available {
		return rule, nil
	}
	rule.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := store.Exec(
		`INSERT INTO prompt_rules (scope_type, scope_key, content, enabled, inject_on_request, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope_type, scope_key) DO UPDATE SET
		   content=excluded.content,
		   enabled=excluded.enabled,
		   inject_on_request=excluded.inject_on_request,
		   updated_at=excluded.updated_at`,
		scopeType,
		scopeKey,
		rule.Content,
		boolToInt(rule.Enabled),
		boolToInt(rule.InjectOnRequest),
		rule.UpdatedAt,
	)
	return rule, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func agentInspectorReadJSON(path string, v interface{}) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func agentInspectorWriteJSON(path string, v interface{}) error {
	return aiGatewayWriteJSONAtomic(path, v)
}

func agentInspectorLoadNotes(agentID string) agentInspectorTextFile {
	shortID := shortPaneID(agentID)
	fullID := normPaneID(agentID)
	var notes agentInspectorTextFile
	err := store.QueryRow(
		`SELECT COALESCE(inspector_notes, ''), COALESCE(inspector_notes_updated_at, '')
		 FROM agent_config
		 WHERE pane_id IN (?,?,?)
		 ORDER BY CASE
		   WHEN pane_id=? THEN 0
		   WHEN pane_id=? THEN 1
		   ELSE 2
		 END
		 LIMIT 1`,
		fullID,
		shortID,
		shortID+":main.0",
		fullID,
		shortID+":main.0",
	).Scan(&notes.Content, &notes.UpdatedAt)
	if err == nil {
		return notes
	}
	legacy := agentInspectorTextFile{}
	if readErr := agentInspectorReadJSON(agentInspectorNotesPath(agentID), &legacy); readErr == nil {
		return legacy
	}
	return agentInspectorTextFile{}
}

func agentInspectorSaveNotes(agentID string, content string) (agentInspectorTextFile, error) {
	shortID := shortPaneID(agentID)
	fullID := normPaneID(agentID)
	record := agentInspectorTextFile{
		Content:   content,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	res, err := store.Exec(
		`UPDATE agent_config
		 SET inspector_notes=?, inspector_notes_updated_at=?, updated_at=datetime('now')
		 WHERE pane_id IN (?,?,?)`,
		record.Content,
		record.UpdatedAt,
		fullID,
		shortID,
		shortID+":main.0",
	)
	if err != nil {
		return agentInspectorTextFile{}, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return agentInspectorTextFile{}, os.ErrNotExist
	}
	return record, nil
}

func agentInspectorLoadRuntimeMemory(agentID string) agentInspectorRuntimeMemoryFile {
	memory := agentInspectorRuntimeMemoryFile{}
	if err := agentInspectorReadJSON(agentInspectorRuntimeMemoryPath(agentID), &memory); err != nil {
		return agentInspectorRuntimeMemoryFile{}
	}
	return memory
}

func agentInspectorRuntimeMemoryFromRule(rule agentInspectorPromptRule) agentInspectorRuntimeMemoryFile {
	return agentInspectorRuntimeMemoryFile{
		Enabled:   rule.Enabled,
		Content:   rule.Content,
		UpdatedAt: rule.UpdatedAt,
	}
}

func agentInspectorRuleLabel(scopeType string, scopeKey string) string {
	switch scopeType {
	case "global":
		return "全局"
	case "project":
		if strings.TrimSpace(scopeKey) == "" {
			return "项目"
		}
		return scopeKey
	case "agent":
		return shortPaneID(scopeKey)
	default:
		return scopeType
	}
}

func agentInspectorPromptRulesBundle(agentID string) agentInspectorPromptRules {
	projectKey := agentInspectorProjectKey(agentID)
	global := agentInspectorLoadPromptRule("global", "global")
	project := agentInspectorLoadPromptRule("project", projectKey)
	agent := agentInspectorLoadPromptRule("agent", shortPaneID(agentID))
	if strings.TrimSpace(agent.Content) == "" && !agent.Enabled && !agent.InjectOnRequest {
		legacy := agentInspectorLoadRuntimeMemory(agentID)
		if strings.TrimSpace(legacy.Content) != "" || legacy.Enabled {
			agent.Content = legacy.Content
			agent.Enabled = legacy.Enabled
			agent.InjectOnRequest = legacy.Enabled
			agent.UpdatedAt = legacy.UpdatedAt
			agent.Available = true
			agent.Scope = "agent"
			agent.Key = shortPaneID(agentID)
		}
	}
	global.Label = agentInspectorRuleLabel("global", global.Key)
	project.Label = agentInspectorRuleLabel("project", project.Key)
	agent.Label = agentInspectorRuleLabel("agent", agent.Key)
	overlay := agentInspectorPromptOverlayFromRules(agentID, global, project, agent)
	bundle := agentInspectorPromptRules{
		Global:      global,
		Project:     project,
		Agent:       agent,
		ProjectKey:  projectKey,
		Overlay:     overlay,
		OverlayPath: agentInspectorPromptRulesPath(agentID),
	}
	for _, ts := range []string{global.UpdatedAt, project.UpdatedAt, agent.UpdatedAt} {
		if strings.TrimSpace(ts) != "" && ts > bundle.UpdatedAt {
			bundle.UpdatedAt = ts
		}
	}
	return bundle
}

func agentInspectorPromptOverlayFromRules(agentID string, global agentInspectorPromptRule, project agentInspectorPromptRule, agent agentInspectorPromptRule) string {
	parts := []string{agentInspectorDefaultReplyInstruction}
	appendRule := func(tag string, rule agentInspectorPromptRule) {
		if !rule.Available || !rule.Enabled || !rule.InjectOnRequest || strings.TrimSpace(rule.Content) == "" {
			return
		}
		parts = append(parts, "<"+tag+">\n"+strings.TrimSpace(rule.Content)+"\n</"+tag+">")
	}
	appendRule("global-memory", global)
	appendRule("project-memory", project)
	appendRule("agent-memory", agent)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func agentInspectorSyncPromptRuleFiles(agentID string) {
	bundle := agentInspectorPromptRulesBundle(agentID)
	_ = agentInspectorWriteJSON(agentInspectorPromptRulesPath(agentID), bundle)
}

func agentInspectorLoadCurrent(agentID string) aiGatewayCurrentSnapshot {
	current := aiGatewayCurrentSnapshot{}
	if err := agentInspectorReadJSON(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), &current); err != nil {
		return aiGatewayCurrentSnapshot{}
	}
	return current
}

func agentInspectorLoadReply(agentID string) aiGatewayReplySnapshot {
	reply := aiGatewayReplySnapshot{}
	if err := agentInspectorReadJSON(filepath.Join(aiGatewayHistoryDir(agentID), "reply.json"), &reply); err != nil {
		return aiGatewayReplySnapshot{}
	}
	return reply
}

func agentInspectorHistoryQuestionFromItem(item map[string]interface{}) string {
	if len(item) == 0 || aiGatewayString(item["role"]) != "user" || aiGatewayItemIsToolResult(item) {
		return ""
	}
	if content := strings.TrimSpace(aiGatewayString(item["content"])); content != "" {
		return aiGatewaySanitizeUserQuestion(content)
	}
	if contentParts := aiGatewaySlice(item["content"]); len(contentParts) > 0 {
		parts := make([]string, 0, len(contentParts))
		for _, part := range contentParts {
			if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return aiGatewaySanitizeUserQuestion(strings.Join(parts, "\n"))
		}
	}
	if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
		return aiGatewaySanitizeUserQuestion(text)
	}
	return ""
}

func agentInspectorHistoryAnswerFromItem(item map[string]interface{}) string {
	if len(item) == 0 || aiGatewayString(item["role"]) != "assistant" {
		return ""
	}
	parts := []string{}
	for _, rawPart := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(rawPart)
		if len(part) == 0 {
			if text := strings.TrimSpace(aiGatewayContentPartToText(rawPart)); text != "" {
				parts = append(parts, text)
			}
			continue
		}
		partType := aiGatewayString(part["type"])
		text := strings.TrimSpace(aiGatewayContentPartToText(part))
		if text == "" {
			continue
		}
		if partType == "" || partType == "text" || partType == "output_text" {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		return strings.TrimSpace(aiGatewayJoinUniqueText(parts))
	}
	if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
		return text
	}
	return ""
}

func agentInspectorMergeHistoryToolStep(record *aiGatewayMessageRecord, byID map[string]int, step map[string]interface{}) {
	if record == nil || len(step) == 0 {
		return
	}
	name := strings.TrimSpace(aiGatewayString(step["tool_name"]))
	input := strings.TrimSpace(aiGatewayString(step["input"]))
	output := strings.TrimSpace(aiGatewayString(step["output"]))
	if name == "" && input == "" && output == "" {
		return
	}
	id := strings.TrimSpace(aiGatewayString(step["tool_id"]))
	mergeAt := func(index int) {
		if index < 0 || index >= len(record.ToolCalls) {
			return
		}
		current := &record.ToolCalls[index]
		if current.Name == "" {
			current.Name = name
		}
		if current.Input == "" {
			current.Input = input
		} else if input != "" && current.Input != input {
			current.Input = aiGatewayCompactText(aiGatewayJoinUniqueText([]string{current.Input, input}), 12000)
		}
		if current.Output == "" {
			current.Output = output
		} else if output != "" && current.Output != output {
			current.Output = aiGatewayCompactText(aiGatewayJoinUniqueText([]string{current.Output, output}), 12000)
		}
	}
	if id != "" {
		if index, ok := byID[id]; ok {
			mergeAt(index)
			return
		}
		record.ToolCalls = append(record.ToolCalls, aiGatewayMessageToolCall{
			Name:   name,
			Input:  input,
			Output: output,
		})
		byID[id] = len(record.ToolCalls) - 1
		return
	}
	for index := range record.ToolCalls {
		call := record.ToolCalls[index]
		if strings.TrimSpace(call.Name) != name {
			continue
		}
		if input != "" && strings.TrimSpace(call.Input) != "" && strings.TrimSpace(call.Input) != input {
			continue
		}
		if output != "" && strings.TrimSpace(call.Output) != "" && strings.TrimSpace(call.Output) != output {
			continue
		}
		mergeAt(index)
		return
	}
	record.ToolCalls = append(record.ToolCalls, aiGatewayMessageToolCall{
		Name:   name,
		Input:  input,
		Output: output,
	})
}

func agentInspectorBuildSnapshotHistory(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) []aiGatewayMessageRecord {
	items := aiGatewaySlice(current.History)
	records := make([]aiGatewayMessageRecord, 0, 64)
	var currentRecord *aiGatewayMessageRecord
	currentToolByID := map[string]int{}
	flush := func() {
		if currentRecord == nil {
			return
		}
		currentRecord.Q = strings.TrimSpace(currentRecord.Q)
		currentRecord.A = strings.TrimSpace(currentRecord.A)
		currentRecord.Thinking = strings.TrimSpace(currentRecord.Thinking)
		currentRecord.ToolCalls = aiGatewayCompactMessageToolCalls(currentRecord.ToolCalls)
		if currentRecord.Q != "" || currentRecord.A != "" || currentRecord.Thinking != "" || len(currentRecord.ToolCalls) > 0 {
			records = append(records, *currentRecord)
		}
		currentRecord = nil
		currentToolByID = map[string]int{}
	}
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		if question := agentInspectorHistoryQuestionFromItem(item); question != "" {
			flush()
			currentRecord = &aiGatewayMessageRecord{Q: question}
			continue
		}
		if currentRecord == nil {
			continue
		}
		if answer := agentInspectorHistoryAnswerFromItem(item); answer != "" {
			currentRecord.A = strings.TrimSpace(aiGatewayJoinUniqueText([]string{currentRecord.A, answer}))
		}
		for _, step := range aiGatewayToolCallsFromMessage(item) {
			agentInspectorMergeHistoryToolStep(currentRecord, currentToolByID, step)
		}
		for _, step := range aiGatewayToolResultsFromMessage(item) {
			agentInspectorMergeHistoryToolStep(currentRecord, currentToolByID, step)
		}
		for _, step := range aiGatewayToolCallsFromInputItem(item) {
			agentInspectorMergeHistoryToolStep(currentRecord, currentToolByID, step)
		}
		for _, step := range aiGatewayToolResultsFromInputItem(item) {
			agentInspectorMergeHistoryToolStep(currentRecord, currentToolByID, step)
		}
	}
	flush()

	latestQuestion := aiGatewayCurrentQuestion(current)
	latestAnswer := aiGatewaySanitizeMessageAnswer(reply.Answer)
	latestThinking := strings.TrimSpace(reply.Thinking)
	latestModel := aiGatewayFirstNonEmpty(aiGatewayReplyPrimaryModel(reply), strings.TrimSpace(current.Model))
	latestQTime := aiGatewayMessageQuestionTime(current, reply)
	latestATime := aiGatewayMessageAnswerTime(reply)
	latestToolCalls := aiGatewayBuildMessageToolCalls(nil, reply)
	if events, err := aiGatewayReadReplyEvents(agentID); err == nil {
		latestToolCalls = aiGatewayBuildMessageToolCalls(aiGatewayFilterEventsByTurn(events, reply.TurnID), reply)
	}
	if strings.TrimSpace(latestQuestion) == "" && strings.TrimSpace(latestAnswer) == "" && strings.TrimSpace(latestThinking) == "" && len(latestToolCalls) == 0 {
		return records
	}
	latestRecord := aiGatewayMessageRecord{
		Q:         latestQuestion,
		A:         latestAnswer,
		QTime:     latestQTime,
		ATime:     latestATime,
		Model:     latestModel,
		Thinking:  latestThinking,
		ToolCalls: latestToolCalls,
	}
	if len(records) == 0 {
		return append(records, latestRecord)
	}
	last := &records[len(records)-1]
	if agentInspectorNormalizeQuestion(last.Q) == agentInspectorNormalizeQuestion(latestRecord.Q) {
		if strings.TrimSpace(last.A) == "" {
			last.A = latestRecord.A
		} else if strings.TrimSpace(latestRecord.A) != "" {
			last.A = strings.TrimSpace(aiGatewayJoinUniqueText([]string{last.A, latestRecord.A}))
		}
		if strings.TrimSpace(last.QTime) == "" {
			last.QTime = latestRecord.QTime
		}
		if strings.TrimSpace(latestRecord.ATime) != "" {
			last.ATime = latestRecord.ATime
		}
		if strings.TrimSpace(latestRecord.Model) != "" {
			last.Model = latestRecord.Model
		}
		if strings.TrimSpace(latestRecord.Thinking) != "" {
			last.Thinking = strings.TrimSpace(aiGatewayJoinUniqueText([]string{last.Thinking, latestRecord.Thinking}))
		}
		last.ToolCalls = agentInspectorMergeToolCalls(last.ToolCalls, latestRecord.ToolCalls)
		return records
	}
	return append(records, latestRecord)
}

func agentInspectorSnapshotHistoryRecords(agentID string) []aiGatewayMessageRecord {
	current := agentInspectorLoadCurrent(agentID)
	reply := agentInspectorLoadReply(agentID)
	return agentInspectorCollapseHistoryRecords(agentInspectorBuildSnapshotHistory(agentID, current, reply))
}

func agentInspectorTrimHistoryText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := 0
	end := len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	out := make([]string, 0, end-start)
	prevBlank := false
	for _, line := range lines[start:end] {
		if strings.TrimSpace(line) == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, strings.TrimRight(line, " "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func agentInspectorPaneLineIsNoise(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "gpt-") && strings.Contains(trimmed, "% left") {
		return true
	}
	if strings.Contains(trimmed, "context left") && strings.Contains(trimmed, "gpt-") {
		return true
	}
	if strings.Contains(trimmed, "Working (") || strings.Contains(trimmed, "esc to interrupt") {
		return true
	}
	if strings.Contains(trimmed, "/ps to view") || strings.Contains(trimmed, "/stop to close") {
		return true
	}
	if strings.HasPrefix(trimmed, "Token usage:") {
		return true
	}
	if strings.HasPrefix(trimmed, "To continue this session, run codex resume") {
		return true
	}
	if strings.HasPrefix(trimmed, "Press enter to confirm") {
		return true
	}
	return false
}

func agentInspectorTmuxPromptLine(line string) (string, bool) {
	left := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(left, "› ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(left, "› ")), true
}

func agentInspectorLoadTmuxMessages(agentID string) []aiGatewayMessageRecord {
	out, err := runTmux("capture-pane", "-t", normPaneID(agentID), "-p", "-S", strconv.Itoa(agentInspectorTmuxHistoryCaptureStart))
	if err != nil {
		return []aiGatewayMessageRecord{}
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	records := make([]aiGatewayMessageRecord, 0, 24)
	for i := 0; i < len(lines); {
		q0, ok := agentInspectorTmuxPromptLine(lines[i])
		if !ok {
			i++
			continue
		}
		qParts := []string{q0}
		i++
		for i < len(lines) {
			if _, ok := agentInspectorTmuxPromptLine(lines[i]); ok {
				break
			}
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || agentInspectorPaneLineIsNoise(lines[i]) {
				break
			}
			qParts = append(qParts, strings.TrimSpace(lines[i]))
			i++
		}
		answerLines := make([]string, 0, 16)
		for i < len(lines) {
			if _, ok := agentInspectorTmuxPromptLine(lines[i]); ok {
				break
			}
			if !agentInspectorPaneLineIsNoise(lines[i]) {
				answerLines = append(answerLines, lines[i])
			}
			i++
		}
		record := aiGatewayMessageRecord{
			Q: agentInspectorTrimHistoryText(strings.Join(qParts, "\n")),
			A: agentInspectorTrimHistoryText(strings.Join(answerLines, "\n")),
		}
		if strings.TrimSpace(record.Q) == "" {
			continue
		}
		records = append(records, record)
	}
	if n := len(records); n > 0 {
		last := records[n-1]
		if strings.TrimSpace(last.Q) != "" && strings.TrimSpace(last.A) == "" {
			records = records[:n-1]
		}
	}
	return records
}

func agentInspectorHistoryData(agentID string) ([]aiGatewayMessageRecord, bool, string) {
	runtimeManaged := agentInspectorPaneRuntimeManaged(agentID)
	current := agentInspectorLoadCurrent(agentID)
	reply := agentInspectorLoadReply(agentID)
	snapshotMessages := agentInspectorCollapseHistoryRecords(agentInspectorBuildSnapshotHistory(agentID, current, reply))
	if len(snapshotMessages) > 0 {
		if runtimeManaged || agentInspectorSnapshotFresh(current, reply) {
			return snapshotMessages, runtimeManaged, ""
		}
	}
	if runtimeManaged {
		return []aiGatewayMessageRecord{}, true, ""
	}
	messages := agentInspectorCollapseHistoryRecords(agentInspectorLoadTmuxMessages(agentID))
	if len(messages) > 0 {
		return messages, false, "当前 pane 是脱管会话，以下 history 基于 tmux 当前可见内容解析。"
	}
	if len(snapshotMessages) > 0 {
		return snapshotMessages, false, "当前 pane 的 history 基于 gateway current/reply 快照，tmux 当前会话可能已脱管。"
	}
	return []aiGatewayMessageRecord{}, false, agentInspectorHistoryUnavailableReason(agentID)
}

func agentInspectorToolNames(toolCalls []aiGatewayMessageToolCall) []string {
	names := []string{}
	seen := map[string]struct{}{}
	for _, call := range toolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func agentInspectorCompactText(text string, limit int) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n\n", "\n")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func agentInspectorCanonicalQuestion(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	qIndex := -1
	for idx, line := range lines {
		if strings.TrimSpace(line) == "Q" {
			qIndex = idx
			break
		}
	}
	if qIndex < 0 || qIndex+1 >= len(lines) {
		return text
	}
	var parts []string
	for _, line := range lines[qIndex+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" && len(parts) > 0 {
			break
		}
		switch trimmed {
		case "A", "模型", "工具", "q", "a":
			if len(parts) > 0 {
				return strings.TrimSpace(strings.Join(parts, "\n"))
			}
			return text
		}
		if strings.HasPrefix(trimmed, "模型:") || strings.HasPrefix(trimmed, "模型：") {
			break
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return text
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func agentInspectorMessageTime(record aiGatewayMessageRecord) time.Time {
	for _, raw := range []string{record.ATime, record.QTime} {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, value); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func agentInspectorNormalizeQuestion(text string) string {
	parts := strings.Fields(strings.ToLower(agentInspectorCanonicalQuestion(text)))
	return strings.TrimSpace(strings.Join(parts, " "))
}

func agentInspectorShouldMergeHistoryRecord(prev aiGatewayMessageRecord, next aiGatewayMessageRecord) bool {
	prevQ := agentInspectorNormalizeQuestion(prev.Q)
	nextQ := agentInspectorNormalizeQuestion(next.Q)
	if prevQ == "" || nextQ == "" || prevQ != nextQ {
		return false
	}
	prevTime := agentInspectorMessageTime(prev)
	nextTime := agentInspectorMessageTime(next)
	if prevTime.IsZero() || nextTime.IsZero() {
		return true
	}
	diff := nextTime.Sub(prevTime)
	if diff < 0 {
		diff = -diff
	}
	return diff <= agentInspectorHistoryMergeWindow
}

func agentInspectorMergeToolCalls(base []aiGatewayMessageToolCall, extra []aiGatewayMessageToolCall) []aiGatewayMessageToolCall {
	if len(base) == 0 {
		return append([]aiGatewayMessageToolCall(nil), extra...)
	}
	out := append([]aiGatewayMessageToolCall(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range out {
		key := strings.Join([]string{strings.TrimSpace(item.Name), strings.TrimSpace(item.Input), strings.TrimSpace(item.Output)}, "|")
		seen[key] = struct{}{}
	}
	for _, item := range extra {
		key := strings.Join([]string{strings.TrimSpace(item.Name), strings.TrimSpace(item.Input), strings.TrimSpace(item.Output)}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func agentInspectorCollapseHistoryRecords(messages []aiGatewayMessageRecord) []aiGatewayMessageRecord {
	if len(messages) <= 1 {
		return append([]aiGatewayMessageRecord(nil), messages...)
	}
	collapsed := make([]aiGatewayMessageRecord, 0, len(messages))
	for _, record := range messages {
		record.ToolCalls = append([]aiGatewayMessageToolCall(nil), record.ToolCalls...)
		if len(collapsed) == 0 {
			collapsed = append(collapsed, record)
			continue
		}
		last := &collapsed[len(collapsed)-1]
		if !agentInspectorShouldMergeHistoryRecord(*last, record) {
			collapsed = append(collapsed, record)
			continue
		}
		if strings.TrimSpace(last.Q) == "" {
			last.Q = record.Q
		}
		if strings.TrimSpace(last.QTime) == "" {
			last.QTime = record.QTime
		}
		if strings.TrimSpace(record.A) != "" {
			last.A = record.A
		}
		if strings.TrimSpace(record.ATime) != "" {
			last.ATime = record.ATime
		}
		if strings.TrimSpace(record.Model) != "" {
			last.Model = record.Model
		}
		if strings.TrimSpace(record.Thinking) != "" {
			last.Thinking = record.Thinking
		}
		last.ToolCalls = agentInspectorMergeToolCalls(last.ToolCalls, record.ToolCalls)
	}
	return collapsed
}

func agentInspectorMatchHistory(record aiGatewayMessageRecord, query string) (string, string, bool) {
	if query == "" {
		snippet := record.Q
		field := "q"
		if strings.TrimSpace(snippet) == "" {
			snippet = record.A
			field = "a"
		}
		if strings.TrimSpace(snippet) == "" {
			snippet = record.Thinking
			field = "thinking"
		}
		return field, agentInspectorCompactText(snippet, 220), true
	}

	normalized := strings.ToLower(strings.TrimSpace(query))
	fields := []struct {
		name  string
		value string
	}{
		{name: "q", value: record.Q},
		{name: "a", value: record.A},
		{name: "thinking", value: record.Thinking},
	}
	for _, call := range record.ToolCalls {
		fields = append(fields,
			struct {
				name  string
				value string
			}{name: "tool_name", value: call.Name},
			struct {
				name  string
				value string
			}{name: "tool_input", value: call.Input},
			struct {
				name  string
				value string
			}{name: "tool_output", value: call.Output},
		)
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" {
			continue
		}
		if strings.Contains(strings.ToLower(value), normalized) {
			return field.name, agentInspectorCompactText(value, 220), true
		}
	}
	return "", "", false
}

func agentInspectorBuildSyncItems(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) []agentHistorySyncItem {
	records := agentInspectorCollapseHistoryRecords(agentInspectorBuildSnapshotHistory(agentID, current, reply))
	items := make([]agentHistorySyncItem, 0, len(records)*2)
	seq := 0
	conversationID := strings.TrimSpace(current.ConversationID)
	makeID := func(role string, index int, ts string) string {
		base := conversationID
		if base == "" {
			base = strings.TrimSpace(reply.TurnID)
		}
		if base == "" {
			base = agentID
		}
		if strings.TrimSpace(ts) != "" {
			return base + ":" + strconv.Itoa(index) + ":" + role + ":" + strings.TrimSpace(ts)
		}
		return base + ":" + strconv.Itoa(index) + ":" + role
	}
	for _, record := range records {
		q := strings.TrimSpace(record.Q)
		if q != "" {
			seq++
			items = append(items, agentHistorySyncItem{
				ID:     makeID("user", seq, record.QTime),
				Seq:    seq,
				Role:   "user",
				Text:   q,
				TS:     strings.TrimSpace(record.QTime),
				TurnID: strings.TrimSpace(reply.TurnID),
				Status: "done",
			})
		}

		turnItems := make([]struct {
			kind  string
			index int
			text  string
			call  aiGatewayMessageToolCall
		}, 0, len(record.ToolCalls)+2)

		thinking := strings.TrimSpace(record.Thinking)
		if thinking != "" {
			turnItems = append(turnItems, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "thinking", index: -1, text: thinking})
		}
		for _, call := range record.ToolCalls {
			if strings.TrimSpace(call.Name) == "" {
				continue
			}
			turnItems = append(turnItems, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "tool", index: call.Index, call: call})
		}
		a := strings.TrimSpace(record.A)
		if a != "" {
			turnItems = append(turnItems, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "assistant", index: 1 << 30, text: a})
		}

		sort.SliceStable(turnItems, func(i, j int) bool {
			left := turnItems[i]
			right := turnItems[j]
			if left.index == right.index {
				return i < j
			}
			if left.index <= 0 {
				return true
			}
			if right.index <= 0 {
				return false
			}
			return left.index < right.index
		})

		for _, item := range turnItems {
			switch item.kind {
			case "thinking":
				seq++
				items = append(items, agentHistorySyncItem{
					ID:     makeID("thinking", seq, record.ATime),
					Seq:    seq,
					Role:   "thinking",
					Text:   item.text,
					TS:     strings.TrimSpace(record.ATime),
					TurnID: strings.TrimSpace(reply.TurnID),
					Status: "done",
					Model:  strings.TrimSpace(record.Model),
				})
			case "tool":
				seq++
				content := map[string]interface{}{
					"name":   strings.TrimSpace(item.call.Name),
					"input":  strings.TrimSpace(item.call.Input),
					"output": strings.TrimSpace(item.call.Output),
				}
				items = append(items, agentHistorySyncItem{
					ID:          makeID("tool", seq, record.ATime),
					Seq:         seq,
					Role:        "tool",
					Text:        strings.TrimSpace(item.call.Name),
					ContentJSON: content,
					TS:          strings.TrimSpace(record.ATime),
					TurnID:      strings.TrimSpace(reply.TurnID),
					Status:      "done",
					Model:       strings.TrimSpace(record.Model),
				})
			case "assistant":
				seq++
				items = append(items, agentHistorySyncItem{
					ID:     makeID("assistant", seq, record.ATime),
					Seq:    seq,
					Role:   "assistant",
					Text:   item.text,
					TS:     strings.TrimSpace(record.ATime),
					TurnID: strings.TrimSpace(reply.TurnID),
					Status: "done",
					Model:  strings.TrimSpace(record.Model),
				})
			}
		}
	}
	return items
}

func handleAgentHistorySyncByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/history-sync/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	current := agentInspectorLoadCurrent(paneID)
	reply := agentInspectorLoadReply(paneID)
	items := agentInspectorBuildSyncItems(paneID, current, reply)
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	limit := agentInspectorHistoryDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > agentInspectorHistoryMaxLimit {
				parsed = agentInspectorHistoryMaxLimit
			}
			limit = parsed
		}
	}
	conversationID := strings.TrimSpace(current.ConversationID)
	baseComplete := true
	start := 0
	if cursor != "" {
		matched := false
		for index, item := range items {
			if item.ID == cursor {
				start = index + 1
				matched = true
				break
			}
		}
		if !matched {
			baseComplete = false
			if len(items) > limit {
				start = len(items) - limit
			}
		}
	} else if len(items) > limit {
		start = len(items) - limit
		baseComplete = false
	}
	if start < 0 {
		start = 0
	}
	nextItems := items[start:]
	nextCursor := ""
	if len(nextItems) > 0 {
		nextCursor = nextItems[len(nextItems)-1].ID
	} else if len(items) > 0 {
		nextCursor = items[len(items)-1].ID
	}
	J(w, M{
		"agent_id":       paneID,
		"conversation_id": conversationID,
		"cursor":         nextCursor,
		"base_complete":  baseComplete,
		"items":          nextItems,
		"reply": M{
			"turn_id":     strings.TrimSpace(reply.TurnID),
			"status":      strings.TrimSpace(reply.Status),
			"answer":      strings.TrimSpace(reply.Answer),
			"thinking":    strings.TrimSpace(reply.Thinking),
			"updated_at":  strings.TrimSpace(reply.UpdatedAt),
			"model":       strings.TrimSpace(aiGatewayReplyPrimaryModel(reply)),
			"tool_calls":  reply.ToolCalls,
		},
	})
}

func agentInspectorBuildToolDisplay(call aiGatewayMessageToolCall) M {
	name := strings.TrimSpace(call.Name)
	rawInput := strings.TrimSpace(call.Input)
	rawOutput := strings.TrimSpace(call.Output)
	tool := M{"name": name}

	parsedArg := rawInput
	if rawInput != "" {
		var inputObj map[string]interface{}
		if json.Unmarshal([]byte(rawInput), &inputObj) == nil && len(inputObj) > 0 {
			if extracted := strings.TrimSpace(extractArg(inputObj, name)); extracted != "" {
				parsedArg = extracted
			}
		}
	}
	if parsedArg != "" {
		tool["arg"] = parsedArg
	} else if name != "" {
		tool["arg"] = name
	}

	if rawOutput != "" {
		switch {
		case strings.Contains(rawOutput, "\n---\n"):
			parts := strings.SplitN(rawOutput, "\n---\n", 2)
			tool["diff"] = M{"old": strings.TrimSpace(parts[0]), "new": strings.TrimSpace(parts[1])}
		case strings.Contains(rawOutput, "@@ ") || strings.HasPrefix(rawOutput, "--- ") || strings.HasPrefix(rawOutput, "+++ "):
			tool["diff"] = M{"new": rawOutput}
		default:
			tool["result"] = rawOutput
		}
	}
	return tool
}

func agentInspectorBuildChatTurns(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) []M {
	records := agentInspectorCollapseHistoryRecords(agentInspectorBuildSnapshotHistory(agentID, current, reply))
	turns := make([]M, 0, len(records))
	liveTurnID := strings.TrimSpace(reply.TurnID)
	currentQuestion := agentInspectorNormalizeQuestion(aiGatewayCurrentQuestion(current))
	status := strings.ToLower(strings.TrimSpace(aiGatewayFirstNonEmpty(reply.Status, current.Status, "idle")))
	currentInputTokens, currentOutputTokens, currentTotalTokens, currentCostCredit, hasCurrentUsage := agentInspectorCurrentUsageMetrics(reply)
	_ = currentInputTokens
	_ = currentOutputTokens
	_ = currentTotalTokens
	for _, record := range records {
		steps := make([]M, 0, 4)
		thinking := strings.TrimSpace(record.Thinking)
		a := strings.TrimSpace(record.A)

		timeline := make([]struct {
			kind  string
			index int
			text  string
			call  aiGatewayMessageToolCall
		}, 0, len(record.ToolCalls)+2)
		if thinking != "" {
			timeline = append(timeline, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "text", index: -1, text: thinking})
		}
		for _, call := range record.ToolCalls {
			if strings.TrimSpace(call.Name) == "" {
				continue
			}
			timeline = append(timeline, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "tool", index: call.Index, call: call})
		}
		if a != "" {
			timeline = append(timeline, struct {
				kind  string
				index int
				text  string
				call  aiGatewayMessageToolCall
			}{kind: "text", index: 1 << 30, text: a})
		}

		sort.SliceStable(timeline, func(i, j int) bool {
			left := timeline[i]
			right := timeline[j]
			if left.index == right.index {
				return i < j
			}
			if left.index <= 0 {
				return true
			}
			if right.index <= 0 {
				return false
			}
			return left.index < right.index
		})

		appendTextStep := func(text string) {
			text = strings.TrimSpace(text)
			if text == "" {
				return
			}
			if len(steps) > 0 {
				if lastType, _ := steps[len(steps)-1]["type"].(string); lastType == "text" {
					if prevText, _ := steps[len(steps)-1]["text"].(string); strings.TrimSpace(prevText) != "" {
						steps[len(steps)-1]["text"] = strings.TrimSpace(prevText + "\n\n" + text)
						return
					}
				}
			}
			steps = append(steps, M{"type": "text", "text": text})
		}

		for _, item := range timeline {
			switch item.kind {
			case "text":
				appendTextStep(item.text)
			case "tool":
				steps = append(steps, M{"type": "tool", "tools": []M{agentInspectorBuildToolDisplay(item.call)}})
			}
		}
		question := agentInspectorCanonicalQuestion(record.Q)
		if strings.TrimSpace(question) == "" {
			continue
		}
		startTS := int64(0)
		if value := strings.TrimSpace(record.QTime); value != "" {
			if ts, err := time.Parse(time.RFC3339, value); err == nil {
				startTS = ts.Unix()
			}
		}
		endTS := startTS
		if value := strings.TrimSpace(record.ATime); value != "" {
			if ts, err := time.Parse(time.RFC3339, value); err == nil {
				endTS = ts.Unix()
			}
		}
		if endTS == 0 {
			endTS = startTS
		}
		turnStatus := "done"
		if a != "" {
			turnStatus = "text"
		} else if len(record.ToolCalls) > 0 || thinking != "" {
			turnStatus = "tool_use"
		}
		credit := 0.0
		if liveTurnID != "" && agentInspectorNormalizeQuestion(question) == currentQuestion {
			switch status {
			case "thinking", "working", "tool_call", "tool_use":
				turnStatus = "tool_use"
			case "streaming":
				turnStatus = "streaming"
			case "completed", "done", "idle", "text":
				if a != "" {
					turnStatus = "text"
				} else if len(record.ToolCalls) > 0 || thinking != "" {
					turnStatus = "tool_use"
				} else {
					turnStatus = "pending"
				}
			default:
				if a == "" && len(record.ToolCalls) == 0 && thinking == "" {
					turnStatus = "pending"
				}
			}
			if hasCurrentUsage {
				credit = currentCostCredit
			} else if reply.CostCredit > 0 {
				credit = reply.CostCredit
			}
		}
		turns = append(turns, M{
			"q":        question,
			"a":        a,
			"steps":    steps,
			"status":   turnStatus,
			"ts":       endTS,
			"start_ts": startTS,
			"credit":   credit,
			"model":    strings.TrimSpace(aiGatewayFirstNonEmpty(record.Model, aiGatewayReplyPrimaryModel(reply), current.Model)),
		})
	}
	return turns
}

func handleAgentHistoryViewByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/history-view/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	current := agentInspectorLoadCurrent(paneID)
	reply := agentInspectorLoadReply(paneID)
	J(w, M{
		"pane_id": paneID,
		"data":    agentInspectorBuildChatTurns(paneID, current, reply),
	})
}

func agentInspectorBuildHistory(agentID string, query string, limit int, offset int) agentInspectorHistoryPage {
	if limit <= 0 {
		limit = agentInspectorHistoryDefaultLimit
	}
	if limit > agentInspectorHistoryMaxLimit {
		limit = agentInspectorHistoryMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	messages, runtimeManaged, reason := agentInspectorHistoryData(agentID)
	if !runtimeManaged && len(messages) == 0 && reason != "" {
		return agentInspectorHistoryPage{
			Query:   query,
			Total:   0,
			Offset:  offset,
			Limit:   limit,
			HasMore: false,
			Items:   []agentInspectorHistoryItem{},
			Managed: runtimeManaged,
			Reason:  reason,
		}
	}
	matched := make([]agentInspectorHistoryItem, 0, len(messages))
	for idx := len(messages) - 1; idx >= 0; idx-- {
		record := messages[idx]
		matchField, snippet, ok := agentInspectorMatchHistory(record, query)
		if !ok {
			continue
		}
		matched = append(matched, agentInspectorHistoryItem{
			ID:         idx,
			Q:          agentInspectorCanonicalQuestion(record.Q),
			A:          record.A,
			QTime:      record.QTime,
			ATime:      record.ATime,
			Model:      record.Model,
			Thinking:   record.Thinking,
			ToolNames:  agentInspectorToolNames(record.ToolCalls),
			MatchField: matchField,
			Snippet:    snippet,
			MergeCount: 1,
		})
	}
	matched = agentInspectorDedupHistoryItems(matched)
	matched = agentInspectorFilterActiveHistoryItem(agentID, matched)
	total := len(matched)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]agentInspectorHistoryItem(nil), matched[offset:end]...)
	return agentInspectorHistoryPage{
		Query:   query,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
		HasMore: end < total,
		Managed: runtimeManaged,
		Reason:  reason,
		Items:   items,
	}
}

func agentInspectorMergeStrings(current string, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fallback
}

func agentInspectorMergeHistoryItemToolNames(base []string, extra []string) []string {
	if len(base) == 0 {
		return append([]string(nil), extra...)
	}
	out := append([]string(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range out {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
	}
	for _, item := range extra {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func agentInspectorDedupHistoryItems(items []agentInspectorHistoryItem) []agentInspectorHistoryItem {
	if len(items) <= 1 {
		return items
	}
	out := make([]agentInspectorHistoryItem, 0, len(items))
	indexByQuestion := map[string]int{}
	for _, item := range items {
		key := agentInspectorNormalizeQuestion(item.Q)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(item.Snippet))
		}
		if key == "" {
			out = append(out, item)
			continue
		}
		existingIdx, ok := indexByQuestion[key]
		if !ok {
			indexByQuestion[key] = len(out)
			out = append(out, item)
			continue
		}
		existing := &out[existingIdx]
		existing.A = agentInspectorMergeStrings(existing.A, item.A)
		existing.ATime = agentInspectorMergeStrings(existing.ATime, item.ATime)
		existing.Model = agentInspectorMergeStrings(existing.Model, item.Model)
		existing.Thinking = agentInspectorMergeStrings(existing.Thinking, item.Thinking)
		existing.MatchField = agentInspectorMergeStrings(existing.MatchField, item.MatchField)
		existing.Snippet = agentInspectorMergeStrings(existing.Snippet, item.Snippet)
		existing.ToolNames = agentInspectorMergeHistoryItemToolNames(existing.ToolNames, item.ToolNames)
		existing.MergeCount++
	}
	return out
}

func agentInspectorQuestionsOverlap(a string, b string) bool {
	left := agentInspectorNormalizeQuestion(a)
	right := agentInspectorNormalizeQuestion(b)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	shorter := left
	longer := right
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	if len([]rune(shorter)) < 12 {
		return false
	}
	return strings.Contains(longer, shorter)
}

func agentInspectorQuestionContained(a string, b string) bool {
	left := agentInspectorNormalizeQuestion(a)
	right := agentInspectorNormalizeQuestion(b)
	if left == "" || right == "" {
		return false
	}
	return strings.Contains(left, right) || strings.Contains(right, left)
}

func agentInspectorFilterActiveHistoryItem(agentID string, items []agentInspectorHistoryItem) []agentInspectorHistoryItem {
	if len(items) == 0 {
		return items
	}
	current := agentInspectorLoadCurrent(agentID)
	reply := agentInspectorLoadReply(agentID)
	if !agentInspectorStatusIsActive(aiGatewayFirstNonEmpty(reply.Status, current.Status)) {
		return items
	}
	activeQuestion := agentInspectorNormalizeQuestion(aiGatewayCurrentQuestion(current))
	if activeQuestion == "" {
		return items
	}
	out := make([]agentInspectorHistoryItem, 0, len(items))
	for _, item := range items {
		if agentInspectorQuestionsOverlap(item.Q, activeQuestion) || agentInspectorQuestionContained(item.Q, activeQuestion) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func agentInspectorOverview(agentID string) M {
	registered := agentInspectorPaneRegistered(agentID)
	runtimeManaged := agentInspectorPaneRuntimeManaged(agentID)
	paneStatus := getPaneStatus(agentID)
	current := agentInspectorLoadCurrent(agentID)
	reply := agentInspectorLoadReply(agentID)
	rawMessages := agentInspectorBuildSnapshotHistory(agentID, current, reply)
	messages, _, historyReason := agentInspectorHistoryData(agentID)
	notes := agentInspectorLoadNotes(agentID)
	promptRules := agentInspectorPromptRulesBundle(agentID)
	systemPrompt := ""
	if body, err := os.ReadFile(aiGatewaySystemPromptPath(agentID)); err == nil {
		systemPrompt = string(body)
	}
	latestMessage := aiGatewayMessageRecord{}
	if len(messages) > 0 {
		latestMessage = messages[len(messages)-1]
	}
	latestHTTP := aiGatewayRequestSpan{}
	if count := len(reply.HTTPRequests); count > 0 {
		latestHTTP = reply.HTTPRequests[count-1]
	}
	latestToolNames := agentInspectorToolNames(latestMessage.ToolCalls)
	currentUsageRaw := agentInspectorCurrentUsageMap(reply)
	currentInputTokens, currentOutputTokens, currentTotalTokens, currentCostCredit, currentUsageReady := agentInspectorCurrentUsageMetrics(reply)
	cumulativeInputTokens, cumulativeOutputTokens, cumulativeTotalTokens := agentInspectorUsageTotals(reply)
	cumulativeCostCredit := reply.CostCredit
	derivedStatus := agentInspectorGatewayStatus(current, reply)
	liveStatus := ""
	if paneStatus != nil && paneStatus.Status != nil {
		liveStatus = strings.TrimSpace(*paneStatus.Status)
	}
	if strings.TrimSpace(liveStatus) != "" {
		switch strings.ToLower(liveStatus) {
		case "idle", "completed", "done", "failed", "error", "wait_auth", "compacting":
			derivedStatus = liveStatus
		case "thinking", "working", "streaming", "tool_call":
			if !agentInspectorSnapshotFresh(current, reply) {
				derivedStatus = liveStatus
			}
		default:
			if !runtimeManaged && !agentInspectorSnapshotFresh(current, reply) {
				derivedStatus = liveStatus
			}
		}
	}
	var contextUsage interface{}
	if paneStatus != nil && paneStatus.CtxUsage != nil {
		contextUsage = *paneStatus.CtxUsage
	} else {
		contextUsage = agentInspectorLiveContextUsage(agentID)
	}
	var overviewInputTokens interface{}
	var overviewOutputTokens interface{}
	var overviewTotalTokens interface{}
	var overviewCostCredit interface{}
	if currentUsageReady {
		overviewInputTokens = currentInputTokens
		overviewOutputTokens = currentOutputTokens
		overviewTotalTokens = currentTotalTokens
		overviewCostCredit = currentCostCredit
	}
	currentQuestion := aiGatewayCurrentQuestion(current)
	return M{
		"pane_id":                  agentID,
		"managed":                  runtimeManaged,
		"registered":               registered,
		"history_reason":           historyReason,
		"status":                   derivedStatus,
		"status_label":             agentInspectorStatusLabel(derivedStatus),
		"status_map":               agentInspectorStatusMap(aiGatewayReplySnapshot{Status: derivedStatus, StatusMap: aiGatewayBuildStatusMap(current, reply)}),
		"provider":                 aiGatewayFirstNonEmpty(current.Provider, latestMessage.Model),
		"model":                    aiGatewayFirstNonEmpty(current.Model, latestMessage.Model),
		"history_source":           current.HistorySource,
		"started_at":               aiGatewayFirstNonEmpty(reply.StartedAt, current.StartedAt),
		"current_updated_at":       current.UpdatedAt,
		"reply_updated_at":         reply.UpdatedAt,
		"answer_preview":           agentInspectorCompactText(reply.Answer, 240),
		"thinking_preview":         agentInspectorCompactText(reply.Thinking, 200),
		"tool_call_count":          len(reply.ToolCalls),
		"http_request_count":       len(reply.HTTPRequests),
		"request_count":            reply.RequestCount,
		"active_request_count":     len(current.ActiveRequestIDs),
		"conversation_count":       len(current.ConversationIDs),
		"input_tokens":             overviewInputTokens,
		"output_tokens":            overviewOutputTokens,
		"total_tokens":             overviewTotalTokens,
		"context_usage":            contextUsage,
		"cost_credit":              overviewCostCredit,
		"current_usage_raw":        currentUsageRaw,
		"usage_pending":            !currentUsageReady && agentInspectorStatusIsActive(derivedStatus),
		"usage_scope":              "current_request",
		"cumulative_input_tokens":  cumulativeInputTokens,
		"cumulative_output_tokens": cumulativeOutputTokens,
		"cumulative_total_tokens":  cumulativeTotalTokens,
		"cumulative_cost_credit":   cumulativeCostCredit,
		"message_count":            len(messages),
		"history_total":            len(messages),
		"raw_message_count":        len(rawMessages),
		"system_prompt_preview":    agentInspectorCompactText(systemPrompt, 220),
		"overlay_preview":          agentInspectorCompactText(promptRules.Overlay, 220),
		"latest_q":                 latestMessage.Q,
		"latest_a":                 latestMessage.A,
		"latest_q_time":            latestMessage.QTime,
		"latest_a_time":            latestMessage.ATime,
		"latest_tool_names":        latestToolNames,
		"latest_tool_name":         aiGatewayFirstNonEmpty(strings.Join(latestToolNames, ", "), ""),
		"latest_http_url":          latestHTTP.URL,
		"latest_http_status":       latestHTTP.Status,
		"latest_http_updated_at":   latestHTTP.UpdatedAt,
		"notes_updated_at":         notes.UpdatedAt,
		"notes_preview":            agentInspectorCompactText(notes.Content, 160),
		"history_available":        len(messages) > 0 || (runtimeManaged && (strings.TrimSpace(currentQuestion) != "" || strings.TrimSpace(reply.Answer) != "")),
		"runtime_memory_enabled":   promptRules.Agent.Enabled,
		"runtime_memory_updated":   promptRules.Agent.UpdatedAt,
		"runtime_memory_preview":   agentInspectorCompactText(promptRules.Agent.Content, 160),
	}
}

func agentInspectorBuildPromptOverlay(agentID string) string {
	rules := agentInspectorPromptRulesBundle(agentID)
	return rules.Overlay
}

func agentInspectorUpsertPromptText(existing string, injected string) string {
	existing = strings.TrimSpace(existing)
	injected = strings.TrimSpace(injected)
	if injected == "" {
		return existing
	}
	if existing == "" {
		return injected
	}
	if strings.Contains(strings.ToLower(existing), strings.ToLower(injected)) {
		return existing
	}
	return injected + "\n\n" + existing
}

func agentInspectorDeveloperInputItem(text string) M {
	return M{
		"role": "developer",
		"content": []M{
			{
				"type": "input_text",
				"text": text,
			},
		},
	}
}

func agentInspectorSystemMessage(text string) M {
	return M{
		"role":    "system",
		"content": text,
	}
}

func agentInspectorPromptExistsInItems(items []interface{}, injected string) bool {
	needle := strings.ToLower(strings.TrimSpace(injected))
	if needle == "" {
		return false
	}
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(aiGatewayString(item["role"])))
		if role != "system" && role != "developer" {
			continue
		}
		content := strings.ToLower(strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"])))
		if strings.Contains(content, needle) {
			return true
		}
	}
	return false
}

func agentInspectorInjectPrompt(body map[string]interface{}, provider string, agentID string) map[string]interface{} {
	injected := agentInspectorBuildPromptOverlay(agentID)
	if strings.TrimSpace(injected) == "" {
		return body
	}
	if body == nil {
		return map[string]interface{}{"instructions": injected}
	}

	switch current := body["instructions"].(type) {
	case string:
		body["instructions"] = agentInspectorUpsertPromptText(current, injected)
		return body
	}

	if provider == "anthropic" {
		switch current := body["system"].(type) {
		case string:
			body["system"] = agentInspectorUpsertPromptText(current, injected)
			return body
		case []interface{}:
			body["system"] = append([]interface{}{injected}, current...)
			return body
		}
		body["system"] = injected
		return body
	}

	if messages := aiGatewaySlice(body["messages"]); len(messages) > 0 {
		if agentInspectorPromptExistsInItems(messages, injected) {
			return body
		}
		body["messages"] = append([]interface{}{agentInspectorSystemMessage(injected)}, messages...)
		return body
	}

	if inputItems := aiGatewaySlice(body["input"]); len(inputItems) > 0 {
		if agentInspectorPromptExistsInItems(inputItems, injected) {
			return body
		}
		body["input"] = append([]interface{}{agentInspectorDeveloperInputItem(injected)}, inputItems...)
		return body
	}

	body["instructions"] = injected
	return body
}

func agentInspectorRewriteRequestBody(provider string, agentID string, requestBody []byte) []byte {
	trimmed := strings.TrimSpace(string(requestBody))
	if trimmed == "" {
		payload := map[string]interface{}{}
		payload = agentInspectorInjectPrompt(payload, provider, agentID)
		body, err := json.Marshal(payload)
		if err != nil {
			return requestBody
		}
		return body
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		return requestBody
	}
	payload = agentInspectorInjectPrompt(payload, provider, agentID)
	body, err := json.Marshal(payload)
	if err != nil {
		return requestBody
	}
	return body
}

func handleAgentInspectorByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/inspector/")
	path = strings.Trim(path, "/")
	if path == "" {
		httpErr(w, 404, "not found")
		return
	}

	parts := strings.Split(path, "/")
	paneID := normPaneID(parts[0])
	shortID := shortPaneID(paneID)
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		offset, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("offset")))
		promptRules := agentInspectorPromptRulesBundle(shortID)
		agentInspectorSyncPromptRuleFiles(shortID)
		J(w, M{
			"pane_id":        shortID,
			"overview":       agentInspectorOverview(shortID),
			"history":        agentInspectorBuildHistory(shortID, query, limit, offset),
			"notes":          agentInspectorLoadNotes(shortID),
			"runtime_memory": agentInspectorRuntimeMemoryFromRule(promptRules.Agent),
			"prompt_rules":   promptRules,
		})
		return
	case r.Method == http.MethodPut && action == "notes":
		var req struct {
			Content string `json:"content"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		record, err := agentInspectorSaveNotes(shortID, req.Content)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "notes": record})
		return
	case r.Method == http.MethodPut && action == "runtime-memory":
		var req struct {
			Content string `json:"content"`
			Enabled *bool  `json:"enabled"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		current := agentInspectorPromptRulesBundle(shortID).Agent
		enabled := current.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		saved, err := agentInspectorSavePromptRule("agent", shortID, agentInspectorPromptRule{
			Content:         req.Content,
			Enabled:         enabled,
			InjectOnRequest: enabled,
		})
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		agentInspectorSyncPromptRuleFiles(shortID)
		J(w, M{
			"success":        true,
			"runtime_memory": agentInspectorRuntimeMemoryFromRule(saved),
			"prompt_rules":   agentInspectorPromptRulesBundle(shortID),
		})
		return
	case r.Method == http.MethodPut && action == "prompt-rules":
		var req struct {
			Global  *agentInspectorPromptRule `json:"global"`
			Project *agentInspectorPromptRule `json:"project"`
			Agent   *agentInspectorPromptRule `json:"agent"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}

		bundle := agentInspectorPromptRulesBundle(shortID)
		if req.Global != nil {
			if _, err := agentInspectorSavePromptRule("global", "global", agentInspectorPromptRule{
				Content:         req.Global.Content,
				Enabled:         req.Global.Enabled,
				InjectOnRequest: req.Global.InjectOnRequest,
			}); err != nil {
				httpErr(w, 500, err.Error())
				return
			}
		}
		if req.Project != nil && bundle.Project.Available {
			if _, err := agentInspectorSavePromptRule("project", bundle.Project.Key, agentInspectorPromptRule{
				Content:         req.Project.Content,
				Enabled:         req.Project.Enabled,
				InjectOnRequest: req.Project.InjectOnRequest,
			}); err != nil {
				httpErr(w, 500, err.Error())
				return
			}
		}
		if req.Agent != nil {
			if _, err := agentInspectorSavePromptRule("agent", shortID, agentInspectorPromptRule{
				Content:         req.Agent.Content,
				Enabled:         req.Agent.Enabled,
				InjectOnRequest: req.Agent.InjectOnRequest,
			}); err != nil {
				httpErr(w, 500, err.Error())
				return
			}
		}
		agentInspectorSyncPromptRuleFiles(shortID)
		next := agentInspectorPromptRulesBundle(shortID)
		J(w, M{
			"success":        true,
			"prompt_rules":   next,
			"runtime_memory": agentInspectorRuntimeMemoryFromRule(next.Agent),
		})
		return
	default:
		httpErr(w, 404, "not found")
		return
	}
}
