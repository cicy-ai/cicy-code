// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Progress helper: every main agent can own ONE hidden kiro-cli pane (cheap
// model) that reads the main agent's conversation through the agent-progress
// skill and keeps the main agent's todo list current. The agent card's 进度
// tab shows the todo list on top and the helper's terminal below.
//
//	GET  /api/agents/progress/<main>          → {helper: "w-xxxx"|""}
//	POST /api/agents/progress/<main>          → create (if needed) + {helper}
//	POST /api/agents/progress/<main>/refresh  → create (if needed) + ask it to update
//
// The helper is an ordinary agent_config row tagged config.progress_of=<main>;
// it is hidden from /api/panes (isBuiltinAgent) and deleted with its main.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// progressHelperModel is the kiro-cli model the helper runs on — the cheap
// tier is plenty for turning a transcript into a short todo list.
const progressHelperModel = "claude-haiku-4.5"

const progressRefreshPrompt = "更新进度（使用 agent-progress skill：digest → 更新 todo → commit）"

const progressHelperSteering = `---
inclusion: always
---

# 你的角色：进度助手

你是主 Agent %s 的进度助手。你只做一件事：用 agent-progress skill 把主 Agent
的对话整理成它的 todo 列表（已完成 / 进行中 / 待处理）。收到「更新进度」时，
严格按 agent-progress 的 SKILL.md 执行：digest → 增改 todo → commit，最后回复一行结果。
不要修改任何项目文件，不要给其他 Agent 发消息。用户直接问你主 Agent 的进展时，
也基于 digest 的内容回答。
`

var progressCreateMu sync.Mutex

// progressHelperFor returns the helper pane id (short) of main, or "".
func progressHelperFor(main string) string {
	var id string
	_ = store.QueryRow(`SELECT pane_id FROM agent_config WHERE active=1 AND json_extract(config,'$.progress_of')=? ORDER BY created_at DESC LIMIT 1`, main).Scan(&id)
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

// progressHelpersOf lists every helper pane (full ids) of main, active or not.
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

// deleteProgressHelpers tears down main's helpers (called when main is deleted).
func deleteProgressHelpers(main string) {
	for _, id := range progressHelpersOf(main) {
		go func(id string) {
			defer func() { recover() }()
			stopAgentByPaneID(id)
		}(id)
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
	res, err := doCreatePane(title+" · 进度", "progress helper", progressHelperModel, "kiro-cli", "", true, true, false, false, nil, nil, "", "", false, "", "", "zh", "", token, nil)
	if err != nil {
		return "", false, err
	}
	helper, _ := res["pane_id"].(string)
	helper = shortPaneID(helper)
	if helper == "" {
		return "", false, fmt.Errorf("helper creation returned no pane id")
	}
	store.Exec(`UPDATE agent_config SET config=json_set(COALESCE(NULLIF(config,''),'{}'),'$.progress_of',?) WHERE pane_id=?`, main, normPaneID(helper))
	invalidateProgressHelperCache()
	if ws, _ := res["workspace"].(string); ws != "" {
		writeProgressHelperFiles(ws, main)
	}
	return helper, true, nil
}

// writeProgressHelperFiles links the helper workspace to its main agent: the
// skill reads .cicy/progress.json, kiro picks up the steering role file.
func writeProgressHelperFiles(workspace, main string) {
	link, _ := json.Marshal(map[string]string{"main": main})
	_ = os.MkdirAll(filepath.Join(workspace, ".cicy"), 0755)
	_ = os.WriteFile(filepath.Join(workspace, ".cicy", "progress.json"), link, 0644)
	steer := filepath.Join(workspace, ".kiro", "steering")
	_ = os.MkdirAll(steer, 0755)
	_ = os.WriteFile(filepath.Join(steer, "progress-helper.md"), []byte(fmt.Sprintf(progressHelperSteering, main)), 0644)
}

// progressHelperBusy: a one-shot run is still going in the helper pane.
// (pane_current_command keeps reporting the wrapping bash, so look for a kiro
// child of the pane's shell instead.)
func progressHelperBusy(helper string) bool {
	pid, err := runTmux("display-message", "-p", "-t", normPaneID(helper), "#{pane_pid}")
	if err != nil || strings.TrimSpace(pid) == "" {
		return false
	}
	return exec.Command("pgrep", "-P", strings.TrimSpace(pid), "-f", "kiro-cli").Run() == nil
}

// runProgressOnce replaces whatever runs in the helper pane with ONE
// non-interactive kiro turn; when it ends the pane drops to an idle shell, so
// no kiro process stays resident. The terminal (ttyd attach) keeps showing it.
func runProgressOnce(helper string) error {
	pane := normPaneID(helper)
	var ws string
	if err := store.QueryRow(`SELECT COALESCE(workspace,'') FROM agent_config WHERE pane_id=?`, pane).Scan(&ws); err != nil || ws == "" {
		return fmt.Errorf("helper %s has no workspace", helper)
	}
	short := shortPaneID(pane)
	script := fmt.Sprintf(`cd %[1]s && export WORKSPACE=%[1]s X_AGENT_SHORT_ID=%[2]s X_AGENT_ID=%[2]s:main.0; clear; `+
		`kiro-cli chat --no-interactive --model %[3]s --trust-all-tools %[4]s; `+
		`echo; echo "[cicy] 本次进度更新结束 $(date +%%H:%%M)"; exec bash`,
		shellQuote(ws), short, shellQuote(progressHelperModel), shellQuote(progressRefreshPrompt))
	_, err := runTmux("respawn-pane", "-k", "-t", pane, "-c", ws, "bash -lc "+shellQuote(script))
	return err
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
			if created || !progressHelperBusy(helper) {
				if err := runProgressOnce(helper); err != nil {
					J(w, M{"success": false, "helper": helper, "error": err.Error()})
					return
				}
				sent = true
			}
		}
		J(w, M{"success": true, "main": main, "helper": helper, "created": created, "sent": sent})
	default:
		httpErr(w, 405, "method not allowed")
	}
}
