package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// nodeExec 通过 xui HTTP API 在远程节点执行 shell 命令
func nodeExec(nodeURL, cmd string) (string, error) {
	u := strings.TrimRight(nodeURL, "/") + "/api/run_shell"
	payload, _ := json.Marshal(map[string]string{"cmd": cmd})
	req, _ := http.NewRequest("POST", u, strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var res struct {
		Success bool   `json:"success"`
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Code    int    `json:"code"`
	}
	json.Unmarshal(raw, &res)
	if !res.Success {
		return res.Stdout, fmt.Errorf("exit %d: %s", res.Code, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// nodeURL 从数据库获取 pane 对应的 node_url
func nodeURL(paneID string) string {
	var u string
	if err := db.QueryRow("SELECT COALESCE(node_url,'') FROM agent_config WHERE pane_id=?", paneID).Scan(&u); err != nil || u == "" {
		return ""
	}
	return u
}

// nodeTmux 在 pane 所属节点执行 tmux 命令
func nodeTmux(paneID string, args ...string) (string, error) {
	u := nodeURL(paneID)
	if u == "" {
		out, err := exec.Command("tmux", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	cmd := "tmux " + shellJoin(args)
	return nodeExec(u, cmd)
}

// nodeTmuxURL 在指定 node 执行 tmux 命令
func nodeTmuxURL(nodeURL string, args ...string) (string, error) {
	cmd := "tmux " + shellJoin(args)
	return nodeExec(nodeURL, cmd)
}

// nodePing 检查节点是否在线
func nodePing(nodeURL string) bool {
	u := strings.TrimRight(nodeURL, "/") + "/api/ping"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// shellJoin 简单拼接 shell 参数
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t'\"\\$#{}") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", "'\\''") + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// API: GET /api/nodes - 列出所有节点及状态
func handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT DISTINCT node_url FROM agent_config WHERE active=1")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type node struct {
		URL    string `json:"url"`
		Online bool   `json:"online"`
	}
	var nodes []node
	seen := map[string]bool{}
	for rows.Next() {
		var u string
		rows.Scan(&u)
		if seen[u] {
			continue
		}
		seen[u] = true
		nodes = append(nodes, node{URL: u, Online: nodePing(u)})
	}
	J(w, nodes)
}

// API: POST /api/nodes/exec - 在指定节点执行命令
func handleNodeExec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeURL string `json:"node_url"`
		Cmd     string `json:"cmd"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.NodeURL == "" || req.Cmd == "" {
		httpErr(w, 400, "node_url and cmd required")
		return
	}
	out, err := nodeExec(req.NodeURL, req.Cmd)
	if err != nil {
		J(w, M{"success": false, "error": err.Error(), "output": out})
		return
	}
	J(w, M{"success": true, "output": out})
}
