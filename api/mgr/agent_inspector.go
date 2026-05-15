package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	agentInspectorHistoryDefaultLimit     = 24
	agentInspectorHistoryMaxLimit         = 100
	agentInspectorTmuxHistoryCaptureStart = -400
)

var agentInspectorHistoryMergeWindow = 20 * time.Minute
var agentInspectorCodexLeftRe = regexp.MustCompile(`(\d+)%\s+left`)

type agentInspectorHistoryCacheEntry struct {
	key      string
	records  []aiGatewayMessageRecord
	managed  bool
	reason   string
	cachedAt time.Time
}

var agentInspectorHistoryCache struct {
	mu    sync.RWMutex
	items map[string]agentInspectorHistoryCacheEntry
}

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

type agentInspectorProviderRequestSection struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Text  string `json:"text,omitempty"`
	Items []M    `json:"items,omitempty"`
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
	parts := []string{}
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
	return aiGatewayLoadReplySnapshot(agentID)
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
			if text := strings.TrimSpace(aiGatewaySanitizeUserQuestion(aiGatewayContentPartToText(part))); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.TrimSpace(strings.Join(parts, "\n"))
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
	if strings.EqualFold(strings.TrimSpace(aiGatewayString(item["phase"])), "commentary") {
		return ""
	}
	hasThinkingOrToolUse := false
	for _, rawPart := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(rawPart)
		if len(part) == 0 {
			continue
		}
		partType := aiGatewayString(part["type"])
		if partType == "thinking" || partType == "tool_use" {
			hasThinkingOrToolUse = true
			break
		}
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
		if (partType == "" || partType == "text" || partType == "output_text") && !hasThinkingOrToolUse {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		return strings.TrimSpace(aiGatewayJoinUniqueText(parts))
	}
	if hasThinkingOrToolUse {
		return ""
	}
	if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
		return text
	}
	return ""
}

func agentInspectorHistoryThinkingFromItem(item map[string]interface{}) string {
	if len(item) == 0 || aiGatewayString(item["role"]) != "assistant" {
		return ""
	}
	isCommentaryPhase := strings.EqualFold(strings.TrimSpace(aiGatewayString(item["phase"])), "commentary")
	hasThinkingOrToolUse := false
	for _, rawPart := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(rawPart)
		if len(part) == 0 {
			continue
		}
		partType := aiGatewayString(part["type"])
		if partType == "thinking" || partType == "tool_use" {
			hasThinkingOrToolUse = true
			break
		}
	}
	if !isCommentaryPhase && !hasThinkingOrToolUse {
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
		switch partType {
		case "thinking":
			if text := strings.TrimSpace(aiGatewayString(part["thinking"])); text != "" {
				parts = append(parts, text)
			}
			continue
		case "", "text", "output_text":
			if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
				parts = append(parts, text)
			}
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
	items := aiGatewayCurrentHistoryItems(current)
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
		if thinking := agentInspectorHistoryThinkingFromItem(item); thinking != "" {
			currentRecord.Thinking = strings.TrimSpace(aiGatewayJoinUniqueText([]string{currentRecord.Thinking, thinking}))
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

func agentInspectorHistoryCacheKey(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, runtimeManaged bool) string {
	return fmt.Sprintf("%s|%t|%s|%s|%s|%s|%s|%d|%d|%s|%s",
		agentID,
		runtimeManaged,
		strings.TrimSpace(current.UpdatedAt),
		strings.TrimSpace(reply.UpdatedAt),
		strings.TrimSpace(current.Status),
		strings.TrimSpace(reply.Status),
		strings.TrimSpace(reply.TurnID),
		len(current.RequestIDs),
		len(current.ActiveRequestIDs),
		strings.TrimSpace(current.ConversationID),
		strings.TrimSpace(current.Model),
	)
}

func agentInspectorCloneHistoryRecords(records []aiGatewayMessageRecord) []aiGatewayMessageRecord {
	if len(records) == 0 {
		return []aiGatewayMessageRecord{}
	}
	out := make([]aiGatewayMessageRecord, 0, len(records))
	for _, record := range records {
		clone := record
		if len(record.ToolCalls) > 0 {
			clone.ToolCalls = append([]aiGatewayMessageToolCall(nil), record.ToolCalls...)
		}
		out = append(out, clone)
	}
	return out
}

func agentInspectorGetCachedHistory(agentID string, key string) ([]aiGatewayMessageRecord, bool, string, bool) {
	agentInspectorHistoryCache.mu.RLock()
	defer agentInspectorHistoryCache.mu.RUnlock()
	entry, ok := agentInspectorHistoryCache.items[agentID]
	if !ok || entry.key != key {
		return nil, false, "", false
	}
	return agentInspectorCloneHistoryRecords(entry.records), entry.managed, entry.reason, true
}

func agentInspectorStoreCachedHistory(agentID string, key string, records []aiGatewayMessageRecord, managed bool, reason string) {
	agentInspectorHistoryCache.mu.Lock()
	defer agentInspectorHistoryCache.mu.Unlock()
	if agentInspectorHistoryCache.items == nil {
		agentInspectorHistoryCache.items = map[string]agentInspectorHistoryCacheEntry{}
	}
	agentInspectorHistoryCache.items[agentID] = agentInspectorHistoryCacheEntry{
		key:      key,
		records:  agentInspectorCloneHistoryRecords(records),
		managed:  managed,
		reason:   reason,
		cachedAt: time.Now(),
	}
}

func agentInspectorSnapshotHistoryData(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, runtimeManaged bool) ([]aiGatewayMessageRecord, bool, string) {
	key := agentInspectorHistoryCacheKey(agentID, current, reply, runtimeManaged)
	if records, managed, reason, ok := agentInspectorGetCachedHistory(agentID, key); ok {
		return records, managed, reason
	}
	records := agentInspectorCollapseHistoryRecords(agentInspectorBuildSnapshotHistory(agentID, current, reply))
	reason := ""
	if len(records) > 0 {
		if runtimeManaged || agentInspectorSnapshotFresh(current, reply) {
			agentInspectorStoreCachedHistory(agentID, key, records, runtimeManaged, reason)
			return records, runtimeManaged, reason
		}
		reason = "当前 pane 的 history 基于 gateway current/reply 快照，tmux 当前会话可能已脱管。"
		agentInspectorStoreCachedHistory(agentID, key, records, false, reason)
		return records, false, reason
	}
	agentInspectorStoreCachedHistory(agentID, key, records, runtimeManaged, reason)
	return records, runtimeManaged, reason
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
	snapshotMessages, snapshotManaged, snapshotReason := agentInspectorSnapshotHistoryData(agentID, current, reply, runtimeManaged)
	if len(snapshotMessages) > 0 {
		if snapshotManaged || agentInspectorSnapshotFresh(current, reply) {
			return snapshotMessages, snapshotManaged, ""
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
		return snapshotMessages, false, snapshotReason
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

func agentInspectorBuildToolSummary(tool M) M {
	if len(tool) == 0 {
		return M{}
	}
	name := strings.TrimSpace(aiGatewayString(tool["name"]))
	out := M{
		"name": name,
	}
	return out
}

func agentInspectorCompactCurrentHistoryItems(items []M) []M {
	if len(items) == 0 {
		return items
	}
	out := make([]M, 0, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		next := aiGatewayCloneAnyMap(item)
		next["q"] = agentInspectorCompactText(aiGatewayString(next["q"]), 600)
		next["a"] = agentInspectorCompactText(aiGatewayString(next["a"]), 800)
		switch steps := next["steps"].(type) {
		case []M:
			next["steps"] = agentInspectorCompactCurrentHistorySteps(steps)
		case []interface{}:
			normalized := make([]M, 0, len(steps))
			for _, raw := range steps {
				if step := aiGatewayMap(raw); len(step) > 0 {
					normalized = append(normalized, step)
				}
			}
			next["steps"] = agentInspectorCompactCurrentHistorySteps(normalized)
		}
		normalizedSteps, _ := next["steps"].([]M)
		if len(normalizedSteps) > 0 {
			hasRenderableStep := false
			hasTextStep := false
			for _, step := range normalizedSteps {
				stepType := strings.TrimSpace(aiGatewayString(step["type"]))
				switch stepType {
				case "text":
					if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
						hasRenderableStep = true
						hasTextStep = true
					}
				case "thinking":
					if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
						hasRenderableStep = true
					}
				case "tool":
					if len(aiGatewaySlice(step["tools"])) > 0 {
						hasRenderableStep = true
					}
				}
			}
			if hasRenderableStep && hasTextStep {
				next["a"] = ""
			}
		}
		out = append(out, next)
	}
	return out
}

func agentInspectorCompactCurrentHistorySteps(steps []M) []M {
	if len(steps) == 0 {
		return []M{}
	}
	out := make([]M, 0, len(steps))
	for _, step := range steps {
		if len(step) == 0 {
			continue
		}
		next := aiGatewayCloneAnyMap(step)
		switch strings.TrimSpace(aiGatewayString(next["type"])) {
		case "text", "thinking":
			next["text"] = agentInspectorCompactText(aiGatewayString(next["text"]), 600)
		case "tool":
			switch tools := next["tools"].(type) {
			case []M:
				next["tools"] = agentInspectorCompactCurrentHistoryTools(tools)
			case []interface{}:
				normalized := make([]M, 0, len(tools))
				for _, raw := range tools {
					if tool := aiGatewayMap(raw); len(tool) > 0 {
						normalized = append(normalized, tool)
					}
				}
				next["tools"] = agentInspectorCompactCurrentHistoryTools(normalized)
			}
		}
		out = append(out, next)
	}
	return out
}

func agentInspectorCompactCurrentHistoryTools(tools []M) []M {
	if len(tools) == 0 {
		return []M{}
	}
	out := make([]M, 0, len(tools))
	for _, tool := range tools {
		if len(tool) == 0 {
			continue
		}
		out = append(out, agentInspectorBuildToolSummary(tool))
	}
	return out
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

func agentInspectorBuildCurrentOnlyRecords(current aiGatewayCurrentSnapshot) []aiGatewayMessageRecord {
	records := make([]aiGatewayMessageRecord, 0, 64)
	items := aiGatewayCurrentHistoryItems(current)
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
		if thinking := agentInspectorHistoryThinkingFromItem(item); thinking != "" {
			currentRecord.Thinking = strings.TrimSpace(aiGatewayJoinUniqueText([]string{currentRecord.Thinking, thinking}))
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
	return agentInspectorCollapseHistoryRecords(records)
}

func agentInspectorAppendCurrentHistoryTextStep(steps *[]M, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	*steps = append(*steps, M{"type": "text", "text": text})
}

func agentInspectorAppendCurrentHistoryThinkingStep(steps *[]M, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	*steps = append(*steps, M{"type": "thinking", "text": text})
}

func agentInspectorCurrentHistoryUpsertToolStep(steps *[]M, pending map[string]int, step map[string]interface{}) {
	if len(step) == 0 {
		return
	}
	id := strings.TrimSpace(aiGatewayString(step["tool_id"]))
	name := strings.TrimSpace(aiGatewayString(step["tool_name"]))
	input := strings.TrimSpace(aiGatewayString(step["input"]))
	output := strings.TrimSpace(aiGatewayString(step["output"]))
	call := aiGatewayMessageToolCall{Name: name, Input: input, Output: output}
	if id != "" {
		if idx, ok := pending[id]; ok && idx >= 0 && idx < len(*steps) {
			toolStep := (*steps)[idx]
			tools, _ := toolStep["tools"].([]M)
			if len(tools) > 0 {
				tool := tools[0]
				if strings.TrimSpace(tool["arg"].(string)) == "" && input != "" {
					merged := agentInspectorBuildToolDisplay(call)
					if value, ok := merged["arg"]; ok {
						tool["arg"] = value
					}
				}
				if output != "" {
					merged := agentInspectorBuildToolDisplay(call)
					if value, ok := merged["result"]; ok {
						tool["result"] = value
					}
					if value, ok := merged["diff"]; ok {
						tool["diff"] = value
					}
				}
				tools[0] = tool
				toolStep["tools"] = tools
				(*steps)[idx] = toolStep
				return
			}
		}
	}
	tool := agentInspectorBuildToolDisplay(call)
	*steps = append(*steps, M{"type": "tool", "tools": []M{tool}})
	if id != "" {
		pending[id] = len(*steps) - 1
	}
}

func agentInspectorCurrentHistoryShouldSkipClaudeText(text string) bool {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return true
	}
	if strings.Contains(lower, "updated task #") {
		return true
	}
	if strings.Contains(lower, "<tool_use_error>") || strings.Contains(lower, "<persisted-output>") {
		return true
	}
	if strings.Contains(lower, "shell cwd was reset") || strings.Contains(lower, "running…") || strings.Contains(lower, "timeout 10m") {
		return true
	}
	if strings.Contains(lower, "the file /home/") && strings.Contains(lower, "has been updated successfully") {
		return true
	}
	if strings.HasPrefix(trimmed, "● ") || strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "✻ ") || strings.HasPrefix(trimmed, "✽ ") {
		return true
	}
	return false
}

func agentInspectorCurrentHistoryAnswerFromSteps(steps []M) string {
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		if stepType, _ := step["type"].(string); stepType == "text" {
			if text, _ := step["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func agentInspectorCurrentHistoryShouldSkipUserText(text string) bool {
	text = aiGatewaySanitizeUserQuestion(text)
	if text == "" {
		return true
	}
	source, kind := aiGatewayClassifyQuestion(text)
	if source != "user" || kind == "suggestion_mode" {
		return true
	}
	return agentInspectorCurrentHistoryShouldSkipClaudeText(text)
}

func agentInspectorClaudeUserQuestion(item map[string]interface{}) string {
	if len(item) == 0 {
		return ""
	}
	if contentParts := aiGatewaySlice(item["content"]); len(contentParts) > 0 {
		parts := make([]string, 0, len(contentParts))
		for _, part := range contentParts {
			if text := strings.TrimSpace(aiGatewaySanitizeUserQuestion(aiGatewayContentPartToText(part))); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if text := strings.TrimSpace(aiGatewaySanitizeUserQuestion(aiGatewayFlattenPromptValue(item["content"]))); text != "" {
		return text
	}
	return strings.TrimSpace(aiGatewaySanitizeUserQuestion(aiGatewayContentPartToText(item)))
}

func agentInspectorClaudeAssistantMessageHasToolActivity(item map[string]interface{}) bool {
	for _, raw := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(raw)
		if len(part) == 0 {
			continue
		}
		partType := strings.TrimSpace(aiGatewayString(part["type"]))
		if partType == "thinking" || partType == "tool_use" || partType == "tool_result" || partType == "function_call_output" {
			return true
		}
	}
	return false
}

func agentInspectorClaudeHasLaterToolActivityInTurn(messages []map[string]interface{}, start int) bool {
	for i := start + 1; i < len(messages); i++ {
		item := messages[i]
		role := strings.TrimSpace(aiGatewayString(item["role"]))
		if role == "user" && !aiGatewayItemIsToolResult(item) {
			return false
		}
		if role == "user" && aiGatewayItemIsToolResult(item) {
			return true
		}
		if role == "assistant" && agentInspectorClaudeAssistantMessageHasToolActivity(item) {
			return true
		}
	}
	return false
}

func agentInspectorBuildClaudeTurns(messages []map[string]interface{}, model string) []M {
	turns := make([]M, 0, len(messages))
	lastAssistantIdx := -1
	lastAssistantSteps := []M{}
	pending := map[string]int{}
	for idx, item := range messages {
		if len(item) == 0 {
			continue
		}
		role := strings.TrimSpace(aiGatewayString(item["role"]))
		if role == "" {
			continue
		}
		if role == "user" && aiGatewayItemIsToolResult(item) {
			if lastAssistantIdx < 0 {
				continue
			}
			for _, step := range aiGatewayToolResultsFromMessage(item) {
				agentInspectorCurrentHistoryUpsertToolStep(&lastAssistantSteps, pending, step)
			}
			turns[lastAssistantIdx]["steps"] = lastAssistantSteps
			turns[lastAssistantIdx]["a"] = agentInspectorCurrentHistoryAnswerFromSteps(lastAssistantSteps)
			continue
		}
		if role == "user" {
			text := agentInspectorClaudeUserQuestion(item)
			if agentInspectorCurrentHistoryShouldSkipUserText(text) {
				continue
			}
			turns = append(turns, M{
				"role":     "user",
				"text":     text,
				"q":        text,
				"a":        "",
				"steps":    []M{},
				"status":   "done",
				"ts":       0,
				"start_ts": 0,
				"credit":   0,
				"model":    strings.TrimSpace(model),
			})
			lastAssistantIdx = -1
			lastAssistantSteps = nil
			pending = map[string]int{}
			continue
		}
		if role != "assistant" {
			continue
		}
		steps := make([]M, 0, 4)
		pending = map[string]int{}
		messageHasToolActivity := agentInspectorClaudeAssistantMessageHasToolActivity(item)
		hasLaterToolActivity := agentInspectorClaudeHasLaterToolActivityInTurn(messages, idx)
		for _, raw := range aiGatewaySlice(item["content"]) {
			part := aiGatewayMap(raw)
			if len(part) == 0 {
				if text := strings.TrimSpace(aiGatewayContentPartToText(raw)); text != "" && !agentInspectorCurrentHistoryShouldSkipClaudeText(text) {
					if hasLaterToolActivity {
						steps = append(steps, M{"type": "thinking", "text": text})
					} else {
						steps = append(steps, M{"type": "text", "text": text})
					}
				}
				continue
			}
			switch strings.TrimSpace(aiGatewayString(part["type"])) {
			case "thinking":
				if text := strings.TrimSpace(aiGatewayString(part["thinking"])); text != "" {
					steps = append(steps, M{"type": "thinking", "text": text})
				}
			case "tool_use":
				agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, M{
					"tool_id":   strings.TrimSpace(aiGatewayString(part["id"])),
					"tool_name": strings.TrimSpace(aiGatewayString(part["name"])),
					"input":     strings.TrimSpace(aiGatewayJSONString(part["input"])),
				})
			case "tool_result", "function_call_output":
				agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, M{
					"tool_id":   strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(part["tool_use_id"]), aiGatewayString(part["tool_id"]), aiGatewayString(part["call_id"]))),
					"tool_name": strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(part["name"]), aiGatewayString(part["tool_name"]))),
					"output":    strings.TrimSpace(aiGatewayFlattenPromptValue(part["content"])),
				})
			default:
				if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" && !agentInspectorCurrentHistoryShouldSkipClaudeText(text) {
					if messageHasToolActivity || hasLaterToolActivity {
						steps = append(steps, M{"type": "thinking", "text": text})
					} else {
						steps = append(steps, M{"type": "text", "text": text})
					}
				}
			}
		}
		if len(steps) == 0 {
			continue
		}
		turns = append(turns, M{
			"role":     "assistant",
			"text":     "",
			"q":        "",
			"a":        agentInspectorCurrentHistoryAnswerFromSteps(steps),
			"steps":    steps,
			"status":   "text",
			"ts":       0,
			"start_ts": 0,
			"credit":   0,
			"model":    strings.TrimSpace(model),
		})
		lastAssistantIdx = len(turns) - 1
		lastAssistantSteps = steps
	}
	return turns
}

func agentInspectorBuildCurrentOnlyClaudeTurns(current aiGatewayCurrentSnapshot) []M {
	body := aiGatewayMap(current.Body)
	return agentInspectorBuildClaudeTurns(aiGatewayExtractMessages(body), current.Model)
}

func agentInspectorBuildInputTurns(items []interface{}, model string) []M {
	turns := make([]M, 0, 64)
	var currentTurn M
	var steps []M
	pending := map[string]int{}
	flush := func() {
		if currentTurn == nil {
			return
		}
		currentTurn["steps"] = steps
		currentTurn["a"] = ""
		for _, step := range steps {
			if stepType, _ := step["type"].(string); stepType == "text" {
				if text, _ := step["text"].(string); strings.TrimSpace(text) != "" {
					if currentTurn["a"] == "" {
						currentTurn["a"] = strings.TrimSpace(text)
					} else {
						currentTurn["a"] = strings.TrimSpace(currentTurn["a"].(string) + "\n\n" + text)
					}
				}
			}
		}
		turns = append(turns, currentTurn)
		currentTurn = nil
		steps = nil
		pending = map[string]int{}
	}
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		if question := agentInspectorHistoryQuestionFromItem(item); question != "" {
			flush()
			currentTurn = M{
				"q":        question,
				"model":    strings.TrimSpace(model),
				"status":   "text",
				"ts":       0,
				"start_ts": 0,
				"credit":   0,
			}
			continue
		}
		if currentTurn == nil {
			continue
		}
		if thinking := agentInspectorHistoryThinkingFromItem(item); thinking != "" {
			agentInspectorAppendCurrentHistoryThinkingStep(&steps, thinking)
		}
		for _, step := range aiGatewayToolCallsFromMessage(item) {
			agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, step)
		}
		for _, step := range aiGatewayToolResultsFromMessage(item) {
			agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, step)
		}
		for _, step := range aiGatewayToolCallsFromInputItem(item) {
			agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, step)
		}
		for _, step := range aiGatewayToolResultsFromInputItem(item) {
			agentInspectorCurrentHistoryUpsertToolStep(&steps, pending, step)
		}
		if text := agentInspectorHistoryAnswerFromItem(item); text != "" {
			agentInspectorAppendCurrentHistoryTextStep(&steps, text)
		}
	}
	flush()
	return turns
}

func agentInspectorBuildCurrentOnlyInputTurns(current aiGatewayCurrentSnapshot) []M {
	return agentInspectorBuildInputTurns(aiGatewayCurrentHistoryItems(current), current.Model)
}

func agentInspectorBuildCurrentOnlyTurns(current aiGatewayCurrentSnapshot) []M {
	body := aiGatewayMap(current.Body)
	if len(aiGatewayExtractMessages(body)) > 0 {
		return agentInspectorBuildCurrentOnlyClaudeTurns(current)
	}
	return agentInspectorBuildCurrentOnlyInputTurns(current)
}

func agentInspectorShouldSkipCurrentSnapshot(current aiGatewayCurrentSnapshot) bool {
	body := aiGatewayMap(current.Body)
	if len(body) == 0 {
		return false
	}
	return aiGatewayShouldSkipInternalPrompt(aiGatewayBuildSystemPrompt(body))
}

func agentInspectorBuildCurrentOnlyPersistedTurns(current aiGatewayCurrentSnapshot) []M {
	if agentInspectorShouldSkipCurrentSnapshot(current) {
		return nil
	}
	body := aiGatewayMap(current.Body)
	if len(aiGatewayExtractMessages(body)) == 0 {
		return agentInspectorBuildCurrentOnlyInputTurns(current)
	}

	rawTurns := agentInspectorBuildCurrentOnlyClaudeTurns(current)
	persisted := make([]M, 0, len(rawTurns))
	var currentUserTurn M

	flushUserOnly := func() {
		if currentUserTurn == nil {
			return
		}
		persisted = append(persisted, currentUserTurn)
		currentUserTurn = nil
	}

	for _, turn := range rawTurns {
		role := strings.TrimSpace(aiGatewayString(turn["role"]))
		switch role {
		case "user":
			flushUserOnly()
			currentUserTurn = M{
				"q":        strings.TrimSpace(aiGatewayString(turn["q"])),
				"a":        "",
				"steps":    []M{},
				"status":   strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["status"]), "pending")),
				"ts":       turn["ts"],
				"start_ts": turn["start_ts"],
				"credit":   turn["credit"],
				"model":    strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["model"]), current.Model)),
			}
		case "assistant":
			if currentUserTurn == nil {
				continue
			}
			existingSteps, _ := currentUserTurn["steps"].([]M)
			nextSteps := make([]M, 0, len(existingSteps)+8)
			nextSteps = append(nextSteps, existingSteps...)
			if assistantSteps, ok := turn["steps"].([]M); ok {
				nextSteps = append(nextSteps, assistantSteps...)
			}
			currentUserTurn["steps"] = nextSteps
			currentUserTurn["a"] = agentInspectorCurrentHistoryAnswerFromSteps(nextSteps)
			currentUserTurn["status"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["status"]), aiGatewayString(currentUserTurn["status"]), "text"))
			if model := strings.TrimSpace(aiGatewayString(turn["model"])); model != "" {
				currentUserTurn["model"] = model
			}
			if startTS, ok := turn["start_ts"]; ok && startTS != nil {
				currentUserTurn["start_ts"] = startTS
			}
			if ts, ok := turn["ts"]; ok && ts != nil {
				currentUserTurn["ts"] = ts
			}
			if credit, ok := turn["credit"]; ok && credit != nil {
				currentUserTurn["credit"] = credit
			}
		}
	}
	flushUserOnly()
	return agentInspectorCollapseContinuationTurns(persisted)
}

func agentInspectorBuildPersistedTurnsFromFullCurrent(current aiGatewayCurrentSnapshot) []M {
	if agentInspectorShouldSkipCurrentSnapshot(current) {
		return nil
	}
	body := aiGatewayMap(current.Body)
	if len(aiGatewayExtractMessages(body)) > 0 {
		rawTurns := agentInspectorBuildClaudeTurns(aiGatewayExtractMessages(body), current.Model)
		persisted := make([]M, 0, len(rawTurns))
		var currentUserTurn M
		flushUserOnly := func() {
			if currentUserTurn == nil {
				return
			}
			persisted = append(persisted, currentUserTurn)
			currentUserTurn = nil
		}
		for _, turn := range rawTurns {
			role := strings.TrimSpace(aiGatewayString(turn["role"]))
			switch role {
			case "user":
				flushUserOnly()
				currentUserTurn = M{
					"q":        strings.TrimSpace(aiGatewayString(turn["q"])),
					"a":        "",
					"steps":    []M{},
					"status":   strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["status"]), "pending")),
					"ts":       turn["ts"],
					"start_ts": turn["start_ts"],
					"credit":   turn["credit"],
					"model":    strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["model"]), current.Model)),
				}
			case "assistant":
				if currentUserTurn == nil {
					continue
				}
				existingSteps, _ := currentUserTurn["steps"].([]M)
				nextSteps := make([]M, 0, len(existingSteps)+8)
				nextSteps = append(nextSteps, existingSteps...)
				if assistantSteps, ok := turn["steps"].([]M); ok {
					nextSteps = append(nextSteps, assistantSteps...)
				}
				currentUserTurn["steps"] = nextSteps
				currentUserTurn["a"] = agentInspectorCurrentHistoryAnswerFromSteps(nextSteps)
				currentUserTurn["status"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["status"]), aiGatewayString(currentUserTurn["status"]), "text"))
				if model := strings.TrimSpace(aiGatewayString(turn["model"])); model != "" {
					currentUserTurn["model"] = model
				}
			}
		}
		flushUserOnly()
		return agentInspectorCollapseContinuationTurns(persisted)
	}
	return agentInspectorCollapseContinuationTurns(agentInspectorBuildInputTurns(aiGatewayExtractInputItems(body), current.Model))
}

func agentInspectorHistoryStepCount(item M) int {
	switch steps := item["steps"].(type) {
	case []M:
		return len(steps)
	case []interface{}:
		return len(steps)
	default:
		return 0
	}
}

func agentInspectorTurnHasAssistantActivity(turn M) bool {
	if len(turn) == 0 {
		return false
	}
	if strings.TrimSpace(aiGatewayString(turn["a"])) != "" {
		return true
	}
	switch steps := turn["steps"].(type) {
	case []M:
		for _, step := range steps {
			switch strings.TrimSpace(aiGatewayString(step["type"])) {
			case "thinking":
				if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
					return true
				}
			case "tool":
				if len(aiGatewaySlice(step["tools"])) > 0 {
					return true
				}
			case "text":
				if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
					return true
				}
			}
		}
	case []interface{}:
		for _, raw := range steps {
			step := aiGatewayMap(raw)
			switch strings.TrimSpace(aiGatewayString(step["type"])) {
			case "thinking":
				if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
					return true
				}
			case "tool":
				if len(aiGatewaySlice(step["tools"])) > 0 {
					return true
				}
			case "text":
				if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
					return true
				}
			}
		}
	}
	return false
}

func agentInspectorIsContinuationPrompt(text string) bool {
	normalized := agentInspectorNormalizeQuestion(text)
	switch normalized {
	case "go", "continue", "go on", "keep going", "proceed", "继续", "继续吧", "继续一下", "继续执行", "接着":
		return true
	default:
		return false
	}
}

func agentInspectorCloneSteps(value interface{}) []M {
	switch steps := value.(type) {
	case []M:
		out := make([]M, 0, len(steps))
		for _, step := range steps {
			out = append(out, aiGatewayCloneAnyMap(step))
		}
		return out
	case []interface{}:
		out := make([]M, 0, len(steps))
		for _, raw := range steps {
			if step := aiGatewayMap(raw); len(step) > 0 {
				out = append(out, step)
			}
		}
		return out
	default:
		return nil
	}
}

func agentInspectorPreferContinuationTurn(base M, continuation M) M {
	merged := aiGatewayCloneAnyMap(base)
	if len(merged) == 0 {
		merged = M{}
	}
	for key, value := range continuation {
		switch key {
		case "q", "start_ts":
			continue
		case "steps":
			baseSteps := agentInspectorCloneSteps(base["steps"])
			nextSteps := agentInspectorCloneSteps(value)
			switch {
			case len(nextSteps) == 0:
			case len(baseSteps) == 0 || agentInspectorHistoryStepCount(continuation) >= agentInspectorHistoryStepCount(base):
				merged["steps"] = nextSteps
			}
		case "a":
			nextAnswer := strings.TrimSpace(aiGatewayString(value))
			baseAnswer := strings.TrimSpace(aiGatewayString(base["a"]))
			if nextAnswer != "" && (baseAnswer == "" || agentInspectorHistoryStepCount(continuation) >= agentInspectorHistoryStepCount(base) || len([]rune(nextAnswer)) >= len([]rune(baseAnswer))) {
				merged["a"] = nextAnswer
			}
		default:
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					merged[key] = typed
				}
			case nil:
			default:
				merged[key] = value
			}
		}
	}
	if _, ok := merged["steps"]; !ok {
		merged["steps"] = agentInspectorCloneSteps(base["steps"])
	}
	if strings.TrimSpace(aiGatewayString(merged["a"])) == "" {
		merged["a"] = strings.TrimSpace(aiGatewayString(base["a"]))
	}
	return merged
}

func agentInspectorCollapseContinuationTurns(turns []M) []M {
	if len(turns) <= 1 {
		return turns
	}
	out := make([]M, 0, len(turns))
	for _, turn := range turns {
		if len(out) == 0 {
			out = append(out, turn)
			continue
		}
		question := aiGatewayString(turn["q"])
		if agentInspectorIsContinuationPrompt(question) {
			out[len(out)-1] = agentInspectorPreferContinuationTurn(out[len(out)-1], turn)
			continue
		}
		out = append(out, turn)
	}
	return out
}

func agentInspectorHistoryUnix(item M, key string) int64 {
	switch value := item[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func agentInspectorShouldPreferLiveHistoryTurn(stored M, live M) bool {
	storedTS := agentInspectorHistoryUnix(stored, "ts")
	liveTS := agentInspectorHistoryUnix(live, "ts")
	if liveTS > storedTS {
		return true
	}
	if liveTS < storedTS {
		return false
	}
	if agentInspectorHistoryStepCount(live) > agentInspectorHistoryStepCount(stored) {
		return true
	}
	return len(strings.TrimSpace(aiGatewayString(live["a"]))) >= len(strings.TrimSpace(aiGatewayString(stored["a"])))
}

func agentInspectorMergeCurrentHistoryItems(items []M, liveTurns []M, limit int) []M {
	if len(liveTurns) == 0 {
		return items
	}
	live := liveTurns[len(liveTurns)-1]
	liveQ := agentInspectorNormalizeQuestion(aiGatewayString(live["q"]))
	if liveQ == "" {
		return items
	}
	if len(items) == 0 {
		return []M{live}
	}
	out := append([]M{}, items...)
	lastIdx := len(out) - 1
	lastQ := agentInspectorNormalizeQuestion(aiGatewayString(out[lastIdx]["q"]))
	if lastQ == liveQ {
		if agentInspectorShouldPreferLiveHistoryTurn(out[lastIdx], live) {
			out[lastIdx] = live
		}
		return out
	}
	out = append(out, live)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func agentInspectorAttachCurrentHistoryToolMeta(items []M, paneID string) []M {
	if len(items) == 0 {
		return items
	}
	out := make([]M, 0, len(items))
	for _, item := range items {
		next := aiGatewayCloneAnyMap(item)
		historyID := int64(0)
		switch value := next["history_id"].(type) {
		case int64:
			historyID = value
		case int:
			historyID = int64(value)
		case float64:
			historyID = int64(value)
		}
		steps := agentInspectorCloneSteps(next["steps"])
		for stepIndex, step := range steps {
			if strings.TrimSpace(aiGatewayString(step["type"])) != "tool" {
				continue
			}
			tools := make([]M, 0, 1)
			appendTool := func(toolIndex int, tool M) {
				if len(tool) == 0 {
					return
				}
				toolNext := aiGatewayCloneAnyMap(tool)
				toolNext["step_index"] = stepIndex
				toolNext["tool_index"] = toolIndex
				id := ""
				if historyID > 0 {
					toolNext["history_id"] = historyID
					id = fmt.Sprintf("history:%d:%d:%d", historyID, stepIndex, toolIndex)
				} else {
					id = fmt.Sprintf("live:%s:%d:%d", paneID, stepIndex, toolIndex)
				}
				toolNext["id"] = id
				tools = append(tools, toolNext)
			}
			switch raw := step["tools"].(type) {
			case []M:
				for toolIndex, tool := range raw {
					appendTool(toolIndex, tool)
				}
			case []interface{}:
				for toolIndex, rawTool := range raw {
					appendTool(toolIndex, aiGatewayMap(rawTool))
				}
			}
			step["tools"] = tools
			steps[stepIndex] = step
		}
		next["steps"] = steps
		out = append(out, next)
	}
	return out
}

func agentInspectorCurrentHistoryToolFromItem(item M, stepIndex int, toolIndex int) M {
	steps := agentInspectorCloneSteps(item["steps"])
	if stepIndex < 0 || stepIndex >= len(steps) {
		return nil
	}
	step := steps[stepIndex]
	if strings.TrimSpace(aiGatewayString(step["type"])) != "tool" {
		return nil
	}
	rawTools := aiGatewaySlice(step["tools"])
	tools := make([]M, 0, len(rawTools))
	for _, rawTool := range rawTools {
		if tool := aiGatewayMap(rawTool); len(tool) > 0 {
			tools = append(tools, tool)
		}
	}
	if toolIndex < 0 || toolIndex >= len(tools) {
		return nil
	}
	return tools[toolIndex]
}

func agentHistoryLoadItemByID(agentID string, historyID int64) (M, bool, error) {
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return nil, false, err
	}
	var itemJSON string
	var q, a, thinking, model string
	if err := db.QueryRow(`SELECT item_json, q, a, thinking, model FROM history_turns WHERE agent_id=? AND id=?`, agentID, historyID).
		Scan(&itemJSON, &q, &a, &thinking, &model); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	item := M{}
	if strings.TrimSpace(itemJSON) != "" {
		_ = json.Unmarshal([]byte(itemJSON), &item)
	}
	record := aiGatewayMessageRecord{Q: q, A: a, Thinking: thinking, Model: model}
	item = agentHistoryNormalizePersistedItem(item, record)
	item["history_id"] = historyID
	return item, true, nil
}

func handleAgentCurrentHistoryToolDetailByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/current-history-tool/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	stepIndex, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("step_index")))
	if err != nil || stepIndex < 0 {
		httpErr(w, http.StatusBadRequest, "step_index required")
		return
	}
	toolIndex, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("tool_index")))
	if err != nil || toolIndex < 0 {
		httpErr(w, http.StatusBadRequest, "tool_index required")
		return
	}
	historyID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("history_id")), 10, 64)
	live := strings.TrimSpace(r.URL.Query().Get("live")) == "1"
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))

	var item M
	if historyID > 0 {
		_, loaded, ok, err := agentHistoryLoadCurrentItemByID(paneID, conversationID, historyID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			httpErr(w, http.StatusNotFound, "history item not found")
			return
		}
		item = loaded
	} else if live {
		current := agentInspectorLoadCurrent(paneID)
		liveTurns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
		if len(liveTurns) == 0 {
			httpErr(w, http.StatusNotFound, "live item not found")
			return
		}
		item = liveTurns[len(liveTurns)-1]
	} else {
		httpErr(w, http.StatusBadRequest, "history_id or live=1 required")
		return
	}
	tool := agentInspectorCurrentHistoryToolFromItem(item, stepIndex, toolIndex)
	if len(tool) == 0 {
		httpErr(w, http.StatusNotFound, "tool not found")
		return
	}
	J(w, M{"tool": tool})
}

func handleAgentHistoryIDsByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/history-ids/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	resolvedConversationID, maxID, err := agentHistoryCurrentMaxID(paneID, conversationID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{
		"id":              maxID,
		"conversation_id": resolvedConversationID,
	})
}

func handleAgentHistoryTurnByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/history-turn/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	historyID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("history_id")), 10, 64)
	if err != nil || historyID <= 0 {
		httpErr(w, http.StatusBadRequest, "history_id required")
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	resolvedConversationID, item, ok, err := agentHistoryLoadCurrentItemByID(paneID, conversationID, historyID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpErr(w, http.StatusNotFound, "history item not found")
		return
	}
	J(w, M{
		"pane_id":         paneID,
		"history_id":      historyID,
		"conversation_id": resolvedConversationID,
		"item":            item,
	})
}

func handleAgentCurrentHistoryByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/current-history/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	limit := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	before := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			before = parsed
		}
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	page, err := agentHistoryLoadCurrentItemsPage(paneID, conversationID, limit, before)
	if err == nil {
		J(w, M{
			"pane_id":         paneID,
			"conversation_id": aiGatewayFirstNonEmpty(page.ConversationID, conversationID),
			"items":           page.Items,
			"next_before":     page.NextBefore,
			"has_more":        page.HasMore,
		})
		return
	}
	J(w, M{
		"pane_id":         paneID,
		"conversation_id": conversationID,
		"items":           []M{},
		"next_before":     0,
		"has_more":        false,
	})
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
		"agent_id":        paneID,
		"conversation_id": conversationID,
		"cursor":          nextCursor,
		"base_complete":   baseComplete,
		"items":           nextItems,
		"reply": M{
			"turn_id":    strings.TrimSpace(reply.TurnID),
			"status":     strings.TrimSpace(reply.Status),
			"answer":     strings.TrimSpace(reply.Answer),
			"thinking":   strings.TrimSpace(reply.Thinking),
			"updated_at": strings.TrimSpace(reply.UpdatedAt),
			"model":      strings.TrimSpace(aiGatewayReplyPrimaryModel(reply)),
			"tool_calls": reply.ToolCalls,
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
		rawOutput = agentInspectorFormatToolOutput(name, rawOutput)
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

func agentInspectorFormatToolOutput(name string, rawOutput string) string {
	rawOutput = strings.TrimSpace(rawOutput)
	if rawOutput == "" {
		return ""
	}
	if strings.Contains(rawOutput, "\nOutput:\n") {
		parts := strings.SplitN(rawOutput, "\nOutput:\n", 2)
		suffix := strings.TrimSpace(parts[1])
		if suffix != "" {
			return suffix
		}
		if strings.Contains(rawOutput, "Process exited with code 0") {
			return ""
		}
	}
	switch strings.TrimSpace(name) {
	case "exec_command":
		if strings.Contains(rawOutput, "Process exited with code 0") {
			return ""
		}
	}
	return rawOutput
}

func agentInspectorBuildChatTurnsFromRecords(records []aiGatewayMessageRecord, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) []M {
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
			}{kind: "thinking", index: -1, text: thinking})
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
			case "thinking":
				steps = append(steps, M{"type": "thinking", "text": strings.TrimSpace(item.text)})
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

func agentInspectorBuildChatTurns(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) []M {
	records, _, _ := agentInspectorSnapshotHistoryData(agentID, current, reply, agentInspectorPaneRuntimeManaged(agentID))
	return agentInspectorBuildChatTurnsFromRecords(records, current, reply)
}

func handleAgentHistoryViewByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/history-view/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	offset, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("offset")))
	bundle := agentInspectorBuildBundle(paneID, query, limit, offset)
	J(w, M{
		"pane_id": paneID,
		"data":    bundle["history_view"],
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
	matched := make([]agentInspectorHistoryItem, 0, limit+1)
	seenQuestions := map[string]int{}
	activeQuestion := ""
	current := agentInspectorLoadCurrent(agentID)
	reply := agentInspectorLoadReply(agentID)
	if agentInspectorStatusIsActive(aiGatewayFirstNonEmpty(reply.Status, current.Status)) {
		activeQuestion = agentInspectorNormalizeQuestion(aiGatewayCurrentQuestion(current))
	}
	total := 0
	needCount := offset + limit + 1
	for idx := len(messages) - 1; idx >= 0; idx-- {
		record := messages[idx]
		question := agentInspectorCanonicalQuestion(record.Q)
		if activeQuestion != "" && (agentInspectorQuestionsOverlap(question, activeQuestion) || agentInspectorQuestionContained(question, activeQuestion)) {
			continue
		}
		matchField, snippet, ok := agentInspectorMatchHistory(record, query)
		if !ok {
			continue
		}
		item := agentInspectorHistoryItem{
			ID:         idx,
			Q:          question,
			A:          record.A,
			QTime:      record.QTime,
			ATime:      record.ATime,
			Model:      record.Model,
			Thinking:   record.Thinking,
			ToolNames:  agentInspectorToolNames(record.ToolCalls),
			MatchField: matchField,
			Snippet:    snippet,
			MergeCount: 1,
		}
		key := agentInspectorNormalizeQuestion(item.Q)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(item.Snippet))
		}
		if key != "" {
			if existingIdx, exists := seenQuestions[key]; exists {
				existing := &matched[existingIdx]
				existing.A = agentInspectorMergeStrings(existing.A, item.A)
				existing.ATime = agentInspectorMergeStrings(existing.ATime, item.ATime)
				existing.Model = agentInspectorMergeStrings(existing.Model, item.Model)
				existing.Thinking = agentInspectorMergeStrings(existing.Thinking, item.Thinking)
				existing.MatchField = agentInspectorMergeStrings(existing.MatchField, item.MatchField)
				existing.Snippet = agentInspectorMergeStrings(existing.Snippet, item.Snippet)
				existing.ToolNames = agentInspectorMergeHistoryItemToolNames(existing.ToolNames, item.ToolNames)
				existing.MergeCount++
				continue
			}
			seenQuestions[key] = len(matched)
		}
		matched = append(matched, item)
		total++
		if len(matched) >= needCount && query == "" {
			break
		}
	}
	if query != "" {
		total = len(matched)
	} else {
		total = len(matched)
	}
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

func agentInspectorRewriteRequestBody(provider string, agentID string, requestBody []byte, upstreamHost string) []byte {
	disableThinking := shouldDisableThinkingForHost(upstreamHost)
	trimmed := strings.TrimSpace(string(requestBody))
	if trimmed == "" {
		payload := map[string]interface{}{}
		payload = agentInspectorInjectPrompt(payload, provider, agentID)
		payload = agentInspectorOverrideModel(payload, agentID)
		if disableThinking {
			payload = agentInspectorDisableThinking(payload)
		}
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
	payload = agentInspectorOverrideModel(payload, agentID)
	if disableThinking {
		payload = agentInspectorDisableThinking(payload)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return requestBody
	}
	return body
}

// shouldDisableThinkingForHost returns true unless the upstream is the official
// OpenAI or Anthropic endpoint. Those providers validate thinking signatures
// strictly (Anthropic) or reject `reasoning_content`/`enable_thinking` outright
// (OpenAI), so we must not rewrite their request bodies. Everyone else —
// cicyAi, new-api, DeepSeek direct, Together, SiliconFlow, etc. — either
// silently strips unknown fields or actively requires the placeholders to
// satisfy their thinking-mode validators.
//
// Matches by exact hostname (case-insensitive, port stripped) — strings.Contains
// would let lookalikes like api.openai.com.evil.example bypass the rewrite,
// which is the opposite of what we want for downstreams that DO need it.
func shouldDisableThinkingForHost(upstreamHost string) bool {
	h := strings.ToLower(strings.TrimSpace(upstreamHost))
	if h == "" {
		return true
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	switch h {
	case "api.openai.com", "api.anthropic.com":
		return false
	}
	return true
}

// agentInspectorOverrideModel rewrites the request body's "model" field to the
// pane's configured default_model so users can hot-swap the model in the UI
// without restarting the CLI. Skipped when default_model is empty (lets the
// agent's own model selection pass through unchanged).
func agentInspectorOverrideModel(payload map[string]interface{}, agentID string) map[string]interface{} {
	if payload == nil {
		return payload
	}
	model := loadPaneDefaultModel(agentID)
	if model == "" {
		return payload
	}
	payload["model"] = model
	return payload
}

// agentInspectorDisableThinking neutralizes "extended thinking" / "reasoning"
// across the request body so downstream CLIs (opencode/codex) don't have to
// echo prior thinking content. DeepSeek (and proxies in front of it) reject
// follow-up requests with `content[].thinking in the thinking mode must be
// passed back to the API` when thinking mode is enabled server-side but the
// assistant's prior turn arrives without it.
//
// Belt + braces because cicyAi/new-api front-ends typically strip the
// `enable_thinking` flag on the way in:
//  1. Flip every per-request thinking switch off (Anthropic `thinking` config,
//     OpenAI `enable_thinking`, and the same nested under `extra_body`).
//  2. Inject empty placeholders into each assistant turn so upstream
//     validation passes even when the toggle didn't propagate:
//     - Anthropic content array: prepend `{"type":"thinking","thinking":"",
//       "signature":""}` when no thinking block is present.
//     - OpenAI assistant message: set `reasoning_content: ""` when missing.
func agentInspectorDisableThinking(payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return payload
	}
	delete(payload, "thinking")
	payload["enable_thinking"] = false
	if extra, ok := payload["extra_body"].(map[string]interface{}); ok {
		extra["enable_thinking"] = false
	}
	if msgs, ok := payload["messages"].([]interface{}); ok {
		for _, raw := range msgs {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			if role != "assistant" {
				continue
			}
			// OpenAI chat/completions shape: assistant message echoes need
			// reasoning_content. Empty string satisfies DeepSeek's validation.
			if _, exists := m["reasoning_content"]; !exists {
				m["reasoning_content"] = ""
			}
			// Anthropic Messages shape: content is either a string or an array
			// of blocks. Promote string to array, then ensure a thinking block.
			content := m["content"]
			if s, ok := content.(string); ok {
				m["content"] = []interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""},
					map[string]interface{}{"type": "text", "text": s},
				}
				continue
			}
			arr, ok := content.([]interface{})
			if !ok {
				continue
			}
			hasThinking := false
			for _, c := range arr {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "thinking" || t == "redacted_thinking" {
						hasThinking = true
						break
					}
				}
			}
			if !hasThinking {
				m["content"] = append([]interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""},
				}, arr...)
			}
		}
	}
	return payload
}

func agentInspectorProviderRequestToolItems(items []interface{}) []M {
	out := make([]M, 0, len(items))
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		name := strings.TrimSpace(aiGatewayFirstNonEmpty(
			aiGatewayString(item["name"]),
			aiGatewayString(aiGatewayMap(item["function"])["name"]),
		))
		if name == "" {
			continue
		}
		description := strings.TrimSpace(aiGatewayFirstNonEmpty(
			aiGatewayString(item["description"]),
			aiGatewayString(aiGatewayMap(item["function"])["description"]),
		))
		inputSchema := aiGatewayMap(aiGatewayFirstNonEmptyMap(item["input_schema"], aiGatewayMap(item["parameters"]), aiGatewayMap(item["function"])["parameters"]))
		propertyCount := 0
		required := []string{}
		properties := []M{}
		schemaType := ""
		additionalProperties := interface{}(nil)
		if len(inputSchema) > 0 {
			schemaType = strings.TrimSpace(aiGatewayString(inputSchema["type"]))
			if props := aiGatewayMap(inputSchema["properties"]); len(props) > 0 {
				propertyCount = len(props)
				keys := make([]string, 0, len(props))
				for key := range props {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					prop := aiGatewayMap(props[key])
					row := M{"name": key}
					if len(prop) > 0 {
						if value := strings.TrimSpace(aiGatewayString(prop["type"])); value != "" {
							row["type"] = value
						}
						if value := strings.TrimSpace(aiGatewayString(prop["description"])); value != "" {
							row["description"] = agentInspectorCompactText(value, 400)
						}
						if enumValues := aiGatewaySlice(prop["enum"]); len(enumValues) > 0 {
							row["enum"] = aiGatewayStringList(enumValues, 12)
						}
						if itemsSchema := aiGatewayMap(prop["items"]); len(itemsSchema) > 0 {
							itemInfo := M{}
							if value := strings.TrimSpace(aiGatewayString(itemsSchema["type"])); value != "" {
								itemInfo["type"] = value
							}
							if value := strings.TrimSpace(aiGatewayString(itemsSchema["description"])); value != "" {
								itemInfo["description"] = agentInspectorCompactText(value, 220)
							}
							if len(itemInfo) > 0 {
								row["items"] = itemInfo
							}
						}
						if value, ok := prop["additionalProperties"]; ok {
							row["additional_properties"] = value
						}
					}
					properties = append(properties, row)
				}
			}
			for _, rawRequired := range aiGatewaySlice(inputSchema["required"]) {
				if value := strings.TrimSpace(aiGatewayString(rawRequired)); value != "" {
					required = append(required, value)
				}
			}
			if value, ok := inputSchema["additionalProperties"]; ok {
				additionalProperties = value
			}
		}
		tool := M{
			"name": name,
		}
		if description != "" {
			tool["summary"] = agentInspectorCompactText(description, 240)
			tool["description"] = description
		}
		if schemaType != "" {
			tool["schema_type"] = schemaType
		}
		if propertyCount > 0 {
			tool["property_count"] = propertyCount
		}
		if len(required) > 0 {
			tool["required"] = required
		}
		if len(properties) > 0 {
			tool["properties"] = properties
		}
		if additionalProperties != nil {
			tool["additional_properties"] = additionalProperties
		}
		out = append(out, tool)
	}
	return out
}

func aiGatewayFirstNonEmptyMap(values ...interface{}) map[string]interface{} {
	for _, raw := range values {
		if item := aiGatewayMap(raw); len(item) > 0 {
			return item
		}
	}
	return nil
}

func agentInspectorProviderRequestMessageItems(items []interface{}) []M {
	out := make([]M, 0, len(items))
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			if text := strings.TrimSpace(aiGatewayJSONOrString(raw)); text != "" {
				out = append(out, M{"role": "input", "text": agentInspectorCompactText(text, 1200)})
			}
			continue
		}
		role := strings.TrimSpace(aiGatewayString(item["role"]))
		if role == "" {
			role = strings.TrimSpace(aiGatewayString(item["type"]))
		}
		text := strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"]))
		if text == "" {
			text = strings.TrimSpace(aiGatewayFlattenPromptValue(item["input"]))
		}
		if text == "" {
			text = strings.TrimSpace(aiGatewayJSONOrString(item))
		}
		if text == "" {
			continue
		}
		out = append(out, M{
			"role": aiGatewayFirstNonEmpty(role, "message"),
			"text": agentInspectorCompactText(text, 1600),
		})
	}
	return out
}

func agentInspectorProviderRequestMessageItemsByRole(items []interface{}, roleFilter string) []M {
	all := agentInspectorProviderRequestMessageItems(items)
	if strings.TrimSpace(roleFilter) == "" {
		return all
	}
	filter := strings.ToLower(strings.TrimSpace(roleFilter))
	out := make([]M, 0, len(all))
	for _, item := range all {
		role := strings.ToLower(strings.TrimSpace(aiGatewayString(item["role"])))
		if role == filter {
			out = append(out, item)
		}
	}
	return out
}

func agentInspectorProviderRequestPromptItems(items []interface{}) []M {
	out := make([]M, 0, len(items))
	for index, raw := range items {
		text := strings.TrimSpace(aiGatewayFlattenPromptValue(raw))
		if text == "" {
			text = strings.TrimSpace(aiGatewayJSONOrString(raw))
		}
		if text == "" {
			continue
		}
		item := aiGatewayMap(raw)
		entry := M{
			"index": index,
			"text":  text,
		}
		if len(item) > 0 {
			if value := strings.TrimSpace(aiGatewayString(item["type"])); value != "" {
				entry["part_type"] = value
			}
			if aiGatewayMap(item["cache_control"]) != nil {
				entry["cache_control"] = aiGatewayMap(item["cache_control"])
			}
		}
		out = append(out, entry)
	}
	return out
}

func agentInspectorProviderRequestView(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) M {
	body := aiGatewayMap(current.Body)
	if len(body) == 0 {
		return M{}
	}
	replyProvider := ""
	if len(reply.HTTPRequests) > 0 {
		replyProvider = reply.HTTPRequests[0].Provider
	}
	provider := strings.TrimSpace(aiGatewayFirstNonEmpty(current.Provider, replyProvider))
	model := strings.TrimSpace(aiGatewayFirstNonEmpty(current.Model, aiGatewayReplyPrimaryModel(reply)))
	requestKind := "generic"
	promptLabel := "Prompt"
	promptText := ""
	promptItems := []M{}
	developerItems := []M{}
	if systemItems := aiGatewaySlice(body["system"]); len(systemItems) > 0 {
		promptText = strings.TrimSpace(aiGatewayFlattenPromptValue(systemItems))
		promptItems = agentInspectorProviderRequestPromptItems(systemItems)
		requestKind = "anthropic_messages"
		promptLabel = "System"
	} else if systemText := strings.TrimSpace(aiGatewayString(body["system"])); systemText != "" {
		promptText = systemText
		requestKind = "anthropic_messages"
		promptLabel = "System"
	} else if instructions := strings.TrimSpace(aiGatewayString(body["instructions"])); instructions != "" {
		inputItems := aiGatewayExtractInputItems(body)
		promptText = instructions
		requestKind = "openai_responses"
		promptLabel = "Instructions"
		developerItems = agentInspectorProviderRequestMessageItemsByRole(inputItems, "developer")
	}
	toolItems := agentInspectorProviderRequestToolItems(aiGatewaySlice(body["tools"]))
	sections := []agentInspectorProviderRequestSection{}
	if strings.TrimSpace(promptText) != "" {
		section := agentInspectorProviderRequestSection{Type: "prompt", Label: promptLabel, Text: promptText}
		if len(promptItems) > 0 {
			section.Items = promptItems
		}
		sections = append(sections, section)
	}
	if len(developerItems) > 0 {
		sections = append(sections, agentInspectorProviderRequestSection{Type: "developer_messages", Label: "Developer Messages", Items: developerItems})
	}
	if len(toolItems) > 0 {
		sections = append(sections, agentInspectorProviderRequestSection{Type: "tools", Label: "Tools", Items: toolItems})
	}
	sections = append(sections, agentInspectorProviderRequestSection{Type: "meta", Label: "Meta", Items: []M{
		{"key": "provider", "value": provider},
		{"key": "model", "value": model},
		{"key": "method", "value": strings.TrimSpace(current.Method)},
		{"key": "url", "value": strings.TrimSpace(current.URL)},
		{"key": "status", "value": strings.TrimSpace(aiGatewayFirstNonEmpty(reply.Status, current.Status))},
	}})
	return M{
		"provider":     provider,
		"model":        model,
		"request_kind": requestKind,
		"request_id":   strings.TrimSpace(current.RequestID),
		"updated_at":   strings.TrimSpace(aiGatewayFirstNonEmpty(reply.UpdatedAt, current.UpdatedAt)),
		"tool_count":   len(toolItems),
		"sections":     sections,
	}
}

func agentInspectorLoadPaneDetail(agentID string) M {
	paneID := normPaneID(agentID)
	var title, workspace, initScript, agentType, agentDuty, config, commonPrompt, ttydPreview sql.NullString
	var port sql.NullInt64
	var active sql.NullInt64
	var allowAllActions sql.NullBool
	var replyInChinese sql.NullBool
	var tgEnable sql.NullBool
	var tgToken, tgChatID sql.NullString
	var groupID sql.NullInt64
	var role, defaultModel, trustLevel sql.NullString
	var machineID sql.NullInt64
	var machineLabel, machineURL, runtimeKind, capabilitiesJSON sql.NullString
	err := store.QueryRow(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script,
		t.tg_token, t.tg_chat_id, t.tg_enable, t.active, t.agent_type, t.agent_duty, t.config, t.common_prompt, t.ttyd_preview, gp.group_id, t.role, t.default_model, t.trust_level,
		COALESCE(t.allow_all_actions, 0),
		COALESCE(t.reply_in_chinese, 0),
		COALESCE(t.machine_id, 0), COALESCE(m.label, ''), COALESCE(m.url, ''), COALESCE(json_extract(m.capabilities_json, '$.runtime_kind'), ''), COALESCE(m.capabilities_json, '{}')
		FROM agent_config t
		LEFT JOIN group_windows gp ON t.pane_id=gp.win_id
		LEFT JOIN machines m ON t.machine_id=m.id
		WHERE t.pane_id=?`, paneID).Scan(
		&paneID, &title, &port, &workspace, &initScript,
		&tgToken, &tgChatID, &tgEnable, &active, &agentType, &agentDuty, &config, &commonPrompt, &ttydPreview, &groupID, &role, &defaultModel, &trustLevel, &allowAllActions,
		&replyInChinese,
		&machineID, &machineLabel, &machineURL, &runtimeKind, &capabilitiesJSON)
	if err != nil {
		return nil
	}
	resp := M{
		"pane_id": shortPaneID(paneID), "title": title.String, "ttyd_port": port.Int64,
		"workspace": workspace.String, "init_script": initScript.String,
		"tg_token": tgToken.String, "tg_chat_id": tgChatID.String, "tg_enable": tgEnable.Bool,
		"active": active.Int64, "agent_type": agentType.String, "agent_duty": agentDuty.String,
		"config": config.String, "common_prompt": commonPrompt.String, "ttyd_preview": ttydPreview.String,
		"allow_all_actions": allowAllActions.Bool,
		"reply_in_chinese":  replyInChinese.Bool,
		"role":              role.String, "default_model": defaultModel.String,
		"trust_level":   trustLevel.String,
		"machine_label": machineLabel.String,
		"machine_url":   machineURL.String,
		"runtime_kind":  runtimeKind.String,
	}
	if machineID.Valid && machineID.Int64 > 0 {
		resp["machine_id"] = machineID.Int64
	} else {
		resp["machine_id"] = nil
	}
	capabilities := M{}
	if strings.TrimSpace(capabilitiesJSON.String) != "" {
		_ = json.Unmarshal([]byte(capabilitiesJSON.String), &capabilities)
	}
	resp["capabilities"] = capabilities
	if groupID.Valid {
		resp["group_id"] = groupID.Int64
	} else {
		resp["group_id"] = nil
	}
	return resp
}

func agentInspectorBuildBundle(agentID string, query string, limit int, offset int) M {
	shortID := shortPaneID(normPaneID(agentID))
	promptRules := agentInspectorPromptRulesBundle(shortID)
	agentInspectorSyncPromptRuleFiles(shortID)
	current := agentInspectorLoadCurrent(shortID)
	reply := agentInspectorLoadReply(shortID)
	records, _, _ := agentInspectorSnapshotHistoryData(shortID, current, reply, agentInspectorPaneRuntimeManaged(shortID))
	return M{
		"pane_id":               shortID,
		"overview":              agentInspectorOverview(shortID),
		"history":               agentInspectorBuildHistory(shortID, query, limit, offset),
		"notes":                 agentInspectorLoadNotes(shortID),
		"runtime_memory":        agentInspectorRuntimeMemoryFromRule(promptRules.Agent),
		"prompt_rules":          promptRules,
		"pane":                  agentInspectorLoadPaneDetail(shortID),
		"history_view":          agentInspectorBuildChatTurnsFromRecords(records, current, reply),
		"provider_request_view": agentInspectorProviderRequestView(shortID, current, reply),
	}
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
		bundle := agentInspectorBuildBundle(shortID, "", 0, 0)
		J(w, M{
			"pane_id":               shortID,
			"pane":                  bundle["pane"],
			"runtime_memory":        bundle["runtime_memory"],
			"prompt_rules":          bundle["prompt_rules"],
			"provider_request_view": bundle["provider_request_view"],
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
