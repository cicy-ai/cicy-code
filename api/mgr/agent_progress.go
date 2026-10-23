// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Progress helper (副 agent): every main agent can own ONE hidden sub-agent that
// reads the main agent's conversation through the agent-progress skill and
// keeps the main agent's todo list current. The sub-agent is an instance of the
// CUSTOM CICY agent ~/cicy-ai/agents/progress-helper/AGENT.md (cheap gateway
// model, tools skill+shell). cicy runs in-process — no CLI, no tmux pane, no
// resident process — so a helper per card costs nothing while idle.
//
//	GET  /api/agents/progress/<main>          → {helper: "w-xxxx"|""}
//	POST /api/agents/progress/<main>          → create (if needed) + {helper}
//	POST /api/agents/progress/<main>/refresh  → create (if needed) + run one turn
//
// The helper is an ordinary agent_config row tagged config.progress_of=<main>;
// it is hidden from /api/panes (isBuiltinAgent) and deleted with its main.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// progressAgentSlug is the custom cicy agent every helper is an instance of.
// Its AGENT.md (persona, tools, model) is seeded once and then user-editable.
const progressAgentSlug = "progress-helper"

// progressHelperModel: the cheap gateway model — plenty for a todo list.
const progressHelperModel = "deepseek-v4-flash"

const progressAgentDoc = `---
name: 进度助手
tools: [skill, shell]
model: ` + progressHelperModel + `
---
你是某个主 Agent 的进度助手。你只做一件事：用 agent-progress skill 把主 Agent 的对话
整理成它的 todo 列表（进行中 / 待确认），让用户一眼看到它做了什么、做到哪了。

收到「更新进度」时，严格执行消息里给出的命令：digest → 按 digest 末尾的要求用
add / set / edit 增改 todo → commit，最后回复一行结果（例如「已更新：新增 2，完成 1」）。
需要细节时用 skill 工具读取 agent-progress 的 SKILL.md。标题和说明一律用中文。

不要修改任何项目文件，不要给其他 Agent 发消息，不要把任务标成 done（done 只能由用户确认）。
用户直接问你主 Agent 的进展时，先 digest 再基于内容回答（问答时不要 commit）。
`

// ensureProgressAgentDoc seeds the custom agent definition when missing.
func ensureProgressAgentDoc() {
	path := customAgentPath(progressAgentSlug)
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(progressAgentDoc), 0644)
}

// progressRefreshPrompt carries the helper's own id: a cicy agent's shell tool
// runs with the server's cwd/env, so the skill is told where its state lives.
func progressRefreshPrompt(helper string) string {
	return fmt.Sprintf("更新进度。依次执行：\n"+
		"1. agent-progress digest --helper %[1]s\n"+
		"2. 按输出末尾的要求，用 agent-progress add / set / edit（都带 --helper %[1]s）记录任务\n"+
		"3. agent-progress commit --helper %[1]s\n"+
		"最后回复一行结果。", helper)
}

var progressCreateMu sync.Mutex

// progressHelperFor returns the (cicy) helper of main, or "". Legacy kiro-cli
// helpers don't count: ensureProgressHelper replaces them.
func progressHelperFor(main string) string {
	var id string
	_ = store.QueryRow(`SELECT pane_id FROM agent_config WHERE active=1 AND agent_type='cicy' AND json_extract(config,'$.progress_of')=? ORDER BY created_at DESC LIMIT 1`, main).Scan(&id)
	return shortPaneID(id)
}

// progress helper id set, cached briefly: isBuiltinAgent asks per listed row.
var progressHelperCache = struct {
	sync.Mutex
	at  time.Time
	ids map[string]bool
}{}

func isProgressHelperPane(paneID string) bool {
	short := shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	if short == "" || store == nil {
		return false
	}
	progressHelperCache.Lock()
	defer progressHelperCache.Unlock()
	if progressHelperCache.ids == nil || time.Since(progressHelperCache.at) > 5*time.Second {
		ids := map[string]bool{}
		rows, err := store.Query(`SELECT pane_id FROM agent_config WHERE json_extract(config,'$.progress_of') IS NOT NULL`)
		if err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids[shortPaneID(id)] = true
				}
			}
			rows.Close()
		}
		progressHelperCache.ids, progressHelperCache.at = ids, time.Now()
	}
	return progressHelperCache.ids[short]
}

func invalidateProgressHelperCache() {
	progressHelperCache.Lock()
	progressHelperCache.ids = nil
	progressHelperCache.Unlock()
}

// progressHelpersOf lists every helper pane (full ids) of main, any type.
func progressHelpersOf(main string) []string {
	out := []string{}
	rows, err := store.Query(`SELECT pane_id FROM agent_config WHERE json_extract(config,'$.progress_of')=?`, main)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// deleteProgressHelpers tears down main's helpers (main deleted, or migration).
func deleteProgressHelpers(main string) {
	for _, id := range progressHelpersOf(main) {
		go func(id string) {
			defer func() { recover() }()
			stopAgentByPaneID(id) // legacy kiro helpers had a tmux session
		}(id)
		removeCicySession(shortPaneID(id))
		store.Exec("DELETE FROM group_windows WHERE win_id=?", id)
		store.Exec("DELETE FROM agent_config WHERE pane_id=?", id)
	}
	invalidateProgressHelperCache()
}

func ensureProgressHelper(main, token string) (string, bool, error) {
	progressCreateMu.Lock()
	defer progressCreateMu.Unlock()
	if id := progressHelperFor(main); id != "" {
		return id, false, nil
	}
	var title string
	if err := store.QueryRow(`SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=? AND active=1`, normPaneID(main)).Scan(&title); err != nil {
		return "", false, fmt.Errorf("main agent %s not found", main)
	}
	if strings.TrimSpace(title) == "" {
		title = main
	}
	// Legacy helpers (kiro-cli panes) are replaced. Their cursor and ids carry
	// over so the new helper neither re-adds nor loses the todos they wrote.
	previous := []string{}
	var cursor []byte
	for _, id := range progressHelpersOf(main) {
		short := shortPaneID(id)
		previous = append(previous, short)
		ws := paneWorkspace(short)
		if ws == "" {
			ws = builtinWorkerWorkspace(short)
		}
		if b, err := os.ReadFile(filepath.Join(ws, ".cicy", "progress-cursor.json")); err == nil {
			cursor = b
		}
		if b, err := os.ReadFile(filepath.Join(ws, ".cicy", "progress.json")); err == nil {
			var link struct {
				Previous []string `json:"previous_helpers"`
			}
			if json.Unmarshal(b, &link) == nil {
				previous = append(previous, link.Previous...)
			}
		}
	}
	if len(previous) > 0 {
		deleteProgressHelpers(main)
	}
	ensureProgressAgentDoc()
	// Route through cicy's configured provider: an empty api style means
	// "openai", which picks the first openai provider — often a keyless
	// placeholder (→ 401 on every turn).
	provider := progressHelperProvider()
	apiStyle := ""
	var runtimeAI *runtimeAIOverride
	if provider != nil {
		apiStyle = normalizeCicyAPIStyle(provider.Protocol)
		runtimeAI = &runtimeAIOverride{ProviderName: provider.Key}
	}
	res, err := doCreatePane(title+" · 进度", "progress helper", progressHelperModel, "custom:"+progressAgentSlug, "",
		true, true, true, false, nil, nil, "", "", false, "", "", "zh", apiStyle, token, runtimeAI,
		func(o *paneCreateOpts) { o.configOnly = true }) // in-process cicy: no tmux pane
	if err != nil {
		return "", false, err
	}
	helper := shortPaneID(fmt.Sprint(res["pane_id"]))
	if helper == "" {
		return "", false, fmt.Errorf("helper creation returned no pane id")
	}
	store.Exec(`UPDATE agent_config SET config=json_set(COALESCE(NULLIF(config,''),'{}'),'$.progress_of',?) WHERE pane_id=?`, main, normPaneID(helper))
	invalidateProgressHelperCache()
	ws := fmt.Sprint(res["workspace"])
	writeProgressHelperFiles(ws, main, previous)
	if cursor != nil {
		_ = os.WriteFile(filepath.Join(ws, ".cicy", "progress-cursor.json"), cursor, 0644)
	}
	return helper, true, nil
}

// progressHelperProvider picks the provider the helper routes through: the one
// configured for lightweight utility work (the "translate" slot) when it serves
// the helper's cheap model, else cicy's default. Pinned per helper (runtime_ai)
// so a change of the cicy default doesn't silently move it.
func progressHelperProvider() *providerConfig {
	for _, slot := range []string{"translate", "cicy"} {
		if p, ok := loadProviderForAgentType(slot); ok && p != nil && strings.TrimSpace(p.Key) != "" &&
			(slot == "cicy" || providerServesModel(p, progressHelperModel)) {
			return p
		}
	}
	return nil
}

// writeProgressHelperFiles links the helper workspace to its main agent (read
// by the agent-progress skill). previous lists earlier helper ids whose todos
// count as this helper's own.
func writeProgressHelperFiles(workspace, main string, previous []string) {
	if previous == nil {
		previous = []string{}
	}
	link, _ := json.Marshal(map[string]any{"main": main, "previous_helpers": previous})
	_ = os.MkdirAll(filepath.Join(workspace, ".cicy"), 0755)
	_ = os.WriteFile(filepath.Join(workspace, ".cicy", "progress.json"), link, 0644)
}

// startProgressTurn runs one helper turn in the background. A refresh while a
// turn is in flight is dropped: the running turn reads the same new history.
func startProgressTurn(helper string) bool {
	ws := paneWorkspace(helper)
	if ws == "" {
		return false
	}
	session := getCicySession(helper, ws)
	if session.isBusy() || session.enqueueIfBusy(progressRefreshPrompt(helper)) {
		return false
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				session.forceRelease()
			}
		}()
		runCicyOwnedTurns(session, helper, ws, progressRefreshPrompt(helper), func(M) {})
	}()
	return true
}

func handleAgentProgress(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agents/progress/"), "/")
	parts := strings.Split(rest, "/")
	main := shortPaneID(normPaneID(parts[0]))
	if main == "" || isProgressHelperPane(main) {
		httpErr(w, 400, "invalid main agent")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case r.Method == "GET" && action == "":
		J(w, M{"success": true, "main": main, "helper": progressHelperFor(main)})
	case r.Method == "POST" && (action == "" || action == "refresh"):
		helper, created, err := ensureProgressHelper(main, getToken(r))
		if err != nil {
			J(w, M{"success": false, "error": err.Error()})
			return
		}
		sent := false
		if action == "refresh" {
			sent = startProgressTurn(helper)
		}
		J(w, M{"success": true, "main": main, "helper": helper, "created": created, "sent": sent})
	default:
		httpErr(w, 405, "method not allowed")
	}
}
