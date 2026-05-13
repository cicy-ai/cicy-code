package hosttools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultAPIBase = "http://127.0.0.1:8008"
	defaultRepo    = "cicy-dev/Private"
)

type Env struct {
	Global map[string]any
	TM     map[string]any
	Token  string
	API    string
	WS     string
	HTTP   *http.Client
	Stdout io.Writer
	Stderr io.Writer
}

func Run(invoked string, args []string, stdout, stderr io.Writer) int {
	cmd := filepath.Base(strings.TrimSpace(invoked))
	if cmd == "cicy-hosttools" {
		if len(args) == 0 {
			printAvailable(stderr)
			return 1
		}
		cmd = args[0]
		args = args[1:]
	}

	if cmd == "frp-server" {
		if err := newFRPTool(frpServerKind, stdout, stderr).run(args); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if cmd == "frp-client" {
		if err := newFRPTool(frpClientKind, stdout, stderr).run(args); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	env, err := newEnv(stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch cmd {
	case "gpt":
		err = env.runSimpleAI(args, "/api/ai/chat", "Usage: gpt <question>")
	case "eng":
		err = env.runSimpleAI(args, "/api/ai/correct", "Usage: eng <text>")
	case "gpt-chat":
		err = env.runGPTChat(args)
	case "tg":
		err = env.runTG(args)
	case "cicy-agent":
		err = env.runTM(args)
	case "agent-webpage":
		err = env.runAgentWebpage(args)
	case "agent-code-server":
		err = env.runAgentCodeServer(args)
	case "gemini-ask":
		err = env.runGeminiAsk(args)
	case "gemini-vision":
		err = env.runGeminiVision(args)
	case "mysql-exec":
		err = env.runMySQLExec(args)
	case "todo":
		err = env.runTodo(args)
	case "cf-tunnel", "cf-tunnel-py", "cf-tunnel.py":
		err = env.runCFTunnel(args)
	case "cping":
		err = env.runCPing(args)
	case "globalApiToken", "global-api-token", "global-api-token.py":
		err = env.runGlobalAPIToken(args)
	case "frp-server":
		err = env.runFRPServer(args)
	case "frp-client":
		err = env.runFRPClient(args)
	case "cicy-mihomo":
		err = env.runCicyMihomo(args)
	default:
		fmt.Fprintf(stderr, "unsupported host tool: %s\n", cmd)
		printAvailable(stderr)
		return 1
	}

	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newEnv(stdout, stderr io.Writer) (*Env, error) {
	global, err := loadGlobalJSON()
	if err != nil {
		return nil, err
	}
	tm, err := loadTMJSON()
	if err != nil {
		return nil, err
	}
	api := strings.TrimSpace(os.Getenv("API_BASE"))
	if api == "" {
		port := strings.TrimSpace(os.Getenv("API_PORT"))
		if port == "" {
			port = "8008"
		}
		api = "http://127.0.0.1:" + port
	}
	ws := "ws" + strings.TrimPrefix(api, "http")
	return &Env{
		Global: global,
		TM:     tm,
		Token:  strings.TrimSpace(anyString(global["api_token"])),
		API:    strings.TrimRight(api, "/"),
		WS:     strings.TrimRight(ws, "/"),
		HTTP:   &http.Client{Timeout: 60 * time.Second},
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

func loadGlobalJSON() (map[string]any, error) {
	path := globalJSONPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func globalJSONPath() string {
	return filepath.Join(userHomeDir(), "cicy-ai", "global.json")
}

func tmJSONPath() string {
	return filepath.Join(userHomeDir(), "cicy-ai", "db", "cicy-agent.json")
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~"
	}
	return home
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func loadTMJSON() (map[string]any, error) {
	data, err := os.ReadFile(tmJSONPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

type tmOptions struct {
	Node string
}

type tmConfig struct {
	API   string
	Token string
	Node  string
}

func printAvailable(w io.Writer) {
	_, _ = fmt.Fprintln(w, "available commands: gpt, gpt-chat, eng, tg, cicy-agent, agent-webpage, agent-code-server, gemini-ask, gemini-vision, mysql-exec, todo, cf-tunnel, cping, globalApiToken, frp-server, frp-client, cicy-mihomo")
}

func (e *Env) apiRequest(ctx context.Context, method, path string, payload any) ([]byte, error) {
	return e.apiRequestTo(ctx, e.API, e.Token, method, path, payload)
}

func (e *Env) apiRequestTo(ctx context.Context, apiBase, token, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(apiBase, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("api error: %s", strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (e *Env) wsConnect(agentID string) (*websocket.Conn, error) {
	u := fmt.Sprintf("%s/api/chat/ws?agent_id=%s&token=%s", e.WS, url.QueryEscape(agentID), url.QueryEscape(e.Token))
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	return conn, err
}

func currentAgentID() string {
	re := regexp.MustCompile(`(w-\d+)`)
	if match := re.FindStringSubmatch(mustGetwd()); len(match) == 2 {
		return match[1]
	}
	return "w-10001"
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func (e *Env) waitForMessage(conn *websocket.Conn, timeout time.Duration, match func(map[string]any) bool) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				break
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, fmt.Errorf("timeout waiting for response")
			}
			return nil, err
		}
		if match(msg) {
			return msg, nil
		}
	}
	return nil, fmt.Errorf("timeout waiting for response")
}

func randomID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()/1e6)
}

func newGlobalAPIToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("cicy_%x", buf), nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func (e *Env) runSimpleAI(args []string, endpoint, usage string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	payload := map[string]any{"text": strings.Join(args, " ")}
	data, err := e.apiRequest(context.Background(), http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err == nil {
		if result := strings.TrimSpace(anyString(out["result"])); result != "" {
			_, _ = fmt.Fprintln(e.Stdout, result)
			return nil
		}
	}
	_, _ = e.Stdout.Write(data)
	_, _ = fmt.Fprintln(e.Stdout)
	return nil
}

func (e *Env) runGPTChat(args []string) error {
	hist := filepath.Join(userHomeDir(), "Private", "data", "gpt-chat-history.json")
	sysPath := filepath.Join(userHomeDir(), "Private", "data", "gpt-chat-system.txt")
	if len(args) == 0 {
		return errors.New("Usage: gpt-chat <message>")
	}
	switch args[0] {
	case "--clear":
		_ = os.Remove(hist)
		_, _ = fmt.Fprintln(e.Stdout, "History cleared.")
		return nil
	case "--system":
		if len(args) < 2 {
			return errors.New("Usage: gpt-chat --system <text>")
		}
		if err := os.MkdirAll(filepath.Dir(sysPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(sysPath, []byte(strings.Join(args[1:], " ")+"\n"), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(e.Stdout, "System prompt set.")
		return nil
	case "--show-system":
		data, err := os.ReadFile(sysPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				_, _ = fmt.Fprintln(e.Stdout, "(none)")
				return nil
			}
			return err
		}
		_, _ = e.Stdout.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			_, _ = fmt.Fprintln(e.Stdout)
		}
		return nil
	}

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []message
	if data, err := os.ReadFile(sysPath); err == nil {
		text := strings.TrimSpace(string(data))
		if text != "" {
			msgs = append(msgs, message{Role: "system", Content: text})
		}
	}
	if data, err := os.ReadFile(hist); err == nil {
		var histMsgs []message
		if json.Unmarshal(data, &histMsgs) == nil {
			msgs = append(msgs, histMsgs...)
		}
	}
	userText := strings.Join(args, " ")
	msgs = append(msgs, message{Role: "user", Content: userText})

	data, err := e.apiRequest(context.Background(), http.MethodPost, "/api/ai/chat", map[string]any{"messages": msgs})
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	result := strings.TrimSpace(anyString(out["result"]))
	_, _ = fmt.Fprintln(e.Stdout, result)

	msgs = append(msgs, message{Role: "assistant", Content: result})
	persist := msgs
	if len(persist) > 0 && persist[0].Role == "system" {
		persist = persist[1:]
	}
	if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(persist, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	return os.WriteFile(hist, buf, 0o644)
}

func (e *Env) runTG(args []string) error {
	if len(args) == 0 {
		return errors.New("Usage: tg <send|photo> [args]")
	}
	switch args[0] {
	case "send":
		if len(args) < 2 {
			return errors.New("Usage: tg send <message>")
		}
		data, err := e.apiRequest(context.Background(), http.MethodPost, "/api/tg/send", map[string]any{"text": strings.Join(args[1:], " ")})
		if err != nil {
			return err
		}
		return printTelegramResult(e.Stdout, data)
	case "photo":
		if len(args) < 2 {
			return errors.New("Usage: tg photo <url> [caption]")
		}
		caption := ""
		if len(args) > 2 {
			caption = args[2]
		}
		data, err := e.apiRequest(context.Background(), http.MethodPost, "/api/tg/photo", map[string]any{"photo": args[1], "caption": caption})
		if err != nil {
			return err
		}
		return printTelegramResult(e.Stdout, data)
	default:
		return errors.New("Usage: tg <send|photo> [args]")
	}
}

func (e *Env) runGlobalAPIToken(args []string) error {
	cmd := "show"
	if len(args) > 0 {
		cmd = strings.TrimSpace(args[0])
	}

	switch cmd {
	case "show":
		token := strings.TrimSpace(anyString(e.Global["api_token"]))
		if token == "" {
			return fmt.Errorf("api_token is empty in %s", globalJSONPath())
		}
		_, _ = fmt.Fprintln(e.Stdout, token)
		return nil
	case "refresh":
		global, err := loadGlobalJSON()
		if err != nil {
			return err
		}
		token, err := newGlobalAPIToken()
		if err != nil {
			return err
		}
		global["api_token"] = token
		data, err := json.MarshalIndent(global, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := os.WriteFile(globalJSONPath(), data, 0o644); err != nil {
			return err
		}
		e.Global = global
		e.Token = token
		_, _ = fmt.Fprintln(e.Stdout, token)
		return nil
	default:
		return errors.New("Usage: globalApiToken <show|refresh>")
	}
}

func printTelegramResult(w io.Writer, data []byte) error {
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if ok, _ := out["ok"].(bool); ok {
		_, _ = fmt.Fprintln(w, "✓ Sent.")
		return nil
	}
	_, _ = fmt.Fprintf(w, "✗ %v\n", out["description"])
	return nil
}

func (e *Env) runTM(args []string) error {
	opts, rest, err := parseTMArgs(args)
	if err != nil {
		return err
	}
	cmd := "help"
	if len(rest) > 0 {
		cmd = rest[0]
	}
	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(e.Stdout, tmUsage())
		return nil
	}
	cfg, err := e.resolveTMConfig(opts.Node)
	if err != nil {
		return err
	}
	switch cmd {
	case "ls":
		data, err := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodGet, "/api/tmux/panes", nil)
		if err != nil {
			return err
		}
		var out struct {
			Panes []map[string]any `json:"panes"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return err
		}
		for _, pane := range out.Panes {
			_, _ = fmt.Fprintf(e.Stdout, "%v\t%v\t%v\n", pane["pane_id"], pane["role"], pane["title"])
		}
	case "tree":
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodGet, "/api/tmux/tree", nil)
	case "windows":
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodGet, "/api/tmux/windows", nil)
	case "capture":
		if len(rest) < 2 {
			return errors.New("Usage: cicy-agent capture <pane>")
		}
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": rest[1]})
	case "msg":
		if len(rest) < 3 {
			return errors.New("Usage: cicy-agent msg <pane> <text>")
		}
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send", map[string]any{"pane_id": rest[1], "text": strings.Join(rest[2:], " ")})
	case "msg_wait":
		if len(rest) < 3 {
			return errors.New("Usage: cicy-agent msg_wait <pane> <text> [timeout]")
		}
		timeout := 60
		if len(rest) > 3 {
			timeout, _ = strconv.Atoi(rest[3])
		}
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send_wait", map[string]any{"target": rest[1], "text": rest[2], "timeout": timeout})
	case "send-keys":
		if len(rest) < 3 {
			return errors.New("Usage: cicy-agent send-keys <pane> <keys>")
		}
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{"pane_id": rest[1], "keys": strings.Join(rest[2:], " ")})
	case "create":
		return e.runTMCreate(cfg, rest[1:])
	case "upgrade":
		return e.runTMUpgrade(cfg, rest[1:])
	case "restart":
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/restart_all", map[string]any{})
	case "clear":
		if len(rest) < 2 {
			return errors.New("Usage: cicy-agent clear <pane>")
		}
		return e.copyAPITo(cfg.API, cfg.Token, http.MethodPost, "/api/tmux/clear", map[string]any{"pane": rest[1]})
	default:
		_, _ = fmt.Fprintln(e.Stdout, tmUsage())
	}
	return nil
}

func (e *Env) runTMCreate(cfg tmConfig, args []string) error {
	title := "全栈软件工程师"
	agentType := "claude"
	allowAllActions := true
	replyInChinese := true
	master := strings.TrimSpace(os.Getenv("X_AGENT_SHORT_ID"))
	if master == "" {
		master = "w-10001"
	}
	forkFrom := ""

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--title" || arg == "-t":
			if i+1 < len(args) {
				title = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--title="):
			title = strings.TrimSpace(strings.TrimPrefix(arg, "--title="))
		case arg == "--type" || arg == "--agent-type":
			if i+1 < len(args) {
				agentType = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--type="):
			agentType = strings.TrimSpace(strings.TrimPrefix(arg, "--type="))
		case strings.HasPrefix(arg, "--agent-type="):
			agentType = strings.TrimSpace(strings.TrimPrefix(arg, "--agent-type="))
		case arg == "--master" || arg == "-m":
			if i+1 < len(args) {
				master = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--master="):
			master = strings.TrimSpace(strings.TrimPrefix(arg, "--master="))
		case arg == "--fork" || arg == "-f":
			if i+1 < len(args) {
				forkFrom = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--fork="):
			forkFrom = strings.TrimSpace(strings.TrimPrefix(arg, "--fork="))
		case arg == "--no-allow-all":
			allowAllActions = false
		case arg == "--allow-all":
			allowAllActions = true
		case arg == "--no-chinese":
			replyInChinese = false
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprintln(e.Stdout, tmCreateUsage())
			return nil
		default:
			if !strings.HasPrefix(arg, "-") && title == "全栈软件工程师" {
				title = arg
			}
		}
	}

	_, _ = fmt.Fprintf(e.Stdout, "创建新员工...\n")
	_, _ = fmt.Fprintf(e.Stdout, "  title: %s\n", title)
	_, _ = fmt.Fprintf(e.Stdout, "  agent_type: %s\n", agentType)
	_, _ = fmt.Fprintf(e.Stdout, "  allow_all_actions: %v\n", allowAllActions)
	_, _ = fmt.Fprintf(e.Stdout, "  master: %s\n", master)
	if forkFrom != "" {
		_, _ = fmt.Fprintf(e.Stdout, "  fork_from: %s\n", forkFrom)
	}
	_, _ = fmt.Fprintln(e.Stdout)

	data, err := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/create", map[string]any{
		"title":             title,
		"agent_type":        agentType,
		"allow_all_actions": allowAllActions,
		"reply_in_chinese":  replyInChinese,
	})
	if err != nil {
		return err
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}

	if success, _ := result["success"].(bool); !success {
		errMsg := anyString(result["error"])
		if errMsg == "" {
			errMsg = "创建失败"
		}
		return errors.New(errMsg)
	}

	paneID := anyString(result["pane_id"])
	session := anyString(result["session"])
	port := result["ttyd_port"]

	_, _ = fmt.Fprintf(e.Stdout, "✅ 创建成功: %s (session=%s, port=%v)\n", paneID, session, port)

	if master != "" && master != "-" {
		_, _ = fmt.Fprintf(e.Stdout, "\n绑定到 %s...\n", master)
		bindData, bindErr := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/agents/bind", map[string]any{
			"pane_id":    master,
			"agent_name": paneID,
		})
		if bindErr != nil {
			_, _ = fmt.Fprintf(e.Stdout, "⚠️  绑定失败: %v\n", bindErr)
		} else {
			var bindResult map[string]any
			if json.Unmarshal(bindData, &bindResult) == nil {
				if bindSuccess, _ := bindResult["success"].(bool); bindSuccess {
					if alreadyBound, _ := bindResult["already_bound"].(bool); alreadyBound {
						_, _ = fmt.Fprintf(e.Stdout, "✅ 已绑定到 %s (已存在)\n", master)
					} else {
						_, _ = fmt.Fprintf(e.Stdout, "✅ 已绑定到 %s\n", master)
					}
				} else {
					_, _ = fmt.Fprintf(e.Stdout, "⚠️  绑定失败: %v\n", bindResult["error"])
				}
			}
		}
	}

	// Fork: 获取源 agent 的 summary 文件，等待新 agent 就绪后发送
	if forkFrom != "" {
		if err := e.forkFromAgent(cfg, paneID, forkFrom); err != nil {
			_, _ = fmt.Fprintf(e.Stdout, "⚠️  Fork 失败: %v\n", err)
		}
	}

	return nil
}

func tmCreateUsage() string {
	return `Usage: cicy-agent create [title] [options]

Options:
  --title, -t <title>       员工名称 (默认: 全栈软件工程师)
  --type <type>             agent 类型 (默认: claude)
  --master, -m <pane_id>    绑定到的 master (默认: $X_AGENT_SHORT_ID 或 w-10001, 用 - 跳过绑定)
  --fork, -f <agent_id>     从指定 agent fork，读取其 summary 并启动
  --allow-all               允许所有操作 (默认)
  --no-allow-all            不允许所有操作
  --no-chinese              不使用中文回复

Examples:
  cicy-agent create                           # 使用默认值创建
  cicy-agent create 前端工程师                 # 指定名称
  cicy-agent create -t "后端工程师" -m w-10002 # 指定名称和 master
  cicy-agent create --type codex              # 使用 codex 类型
  cicy-agent create -m -                      # 创建但不绑定
  cicy-agent create --fork w-10001            # 从 w-10001 fork`
}

func (e *Env) runTMUpgrade(cfg tmConfig, args []string) error {
	if len(args) == 0 {
		return errors.New(tmUpgradeUsage())
	}

	paneID := args[0]
	if paneID == "-h" || paneID == "--help" {
		_, _ = fmt.Fprintln(e.Stdout, tmUpgradeUsage())
		return nil
	}

	// 获取 agent 信息
	data, err := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodGet, "/api/tmux/panes", nil)
	if err != nil {
		return err
	}
	var panesResp struct {
		Panes []map[string]any `json:"panes"`
	}
	if err := json.Unmarshal(data, &panesResp); err != nil {
		return err
	}

	var agentType string
	for _, pane := range panesResp.Panes {
		pid := anyString(pane["pane_id"])
		if strings.HasPrefix(pid, paneID+":") || pid == paneID {
			agentType = anyString(pane["agent_type"])
			break
		}
	}

	if agentType == "" {
		agentType = "claude" // 默认
	}

	// 根据 agent_type 确定升级命令和清理命令
	var upgradeCmd, cleanupCmd string
	switch strings.ToLower(agentType) {
	case "claude", "claude-code":
		cleanupCmd = "rm -rf ~/.npm-global/lib/node_modules/@anthropic-ai/claude-code"
		upgradeCmd = "npm i -g @anthropic-ai/claude-code@latest"
	case "codex":
		cleanupCmd = "rm -rf ~/.npm-global/lib/node_modules/@openai/codex"
		upgradeCmd = "npm i -g @openai/codex@latest"
	case "opencode":
		cleanupCmd = "rm -rf ~/.npm-global/lib/node_modules/opencode-ai"
		upgradeCmd = "npm i -g opencode-ai@latest"
	case "openclaw":
		cleanupCmd = "rm -rf ~/.npm-global/lib/node_modules/openclaw"
		upgradeCmd = "npm i -g openclaw@latest"
	case "cicy-claude":
		cleanupCmd = "rm -rf ~/.npm-global/lib/node_modules/cicy-claude"
		upgradeCmd = "npm i -g cicy-claude@latest"
	default:
		return fmt.Errorf("不支持升级 agent_type: %s", agentType)
	}

	_, _ = fmt.Fprintf(e.Stdout, "升级 %s (%s)...\n", paneID, agentType)

	// Step 1: 发送 /exit 退出当前进程
	_, _ = fmt.Fprintf(e.Stdout, "  [1/5] 退出当前进程...\n")
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "/exit",
	})
	time.Sleep(200 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "Enter",
	})
	time.Sleep(2 * time.Second)

	// Step 2: 等待 shell prompt
	_, _ = fmt.Fprintf(e.Stdout, "  [2/5] 等待 shell 就绪...\n")
	shellReady := false
	for i := 0; i < 10; i++ {
		capData, capErr := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": paneID})
		if capErr == nil {
			output := string(capData)
			// 检查是否有 shell prompt ($ 或 ❯ 在最后几行)
			lines := strings.Split(output, "\n")
			for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
				line := strings.TrimSpace(lines[i])
				if strings.HasSuffix(line, "$") || strings.HasSuffix(line, "❯") || strings.Contains(line, "$ ") {
					shellReady = true
					break
				}
			}
			if shellReady {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !shellReady {
		_, _ = fmt.Fprintf(e.Stdout, "  ⚠️  未检测到 shell prompt，继续尝试...\n")
	}

	// Step 3: 先清理旧目录，再运行升级命令
	_, _ = fmt.Fprintf(e.Stdout, "  [3/5] 清理旧目录并升级...\n")
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    cleanupCmd + " && " + upgradeCmd,
	})
	time.Sleep(200 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "Enter",
	})

	// 等待升级完成 (最多 120 秒)
	_, _ = fmt.Fprintf(e.Stdout, "  等待升级完成...\n")
	upgradeComplete := false
	for i := 0; i < 120; i++ {
		time.Sleep(1 * time.Second)
		capData, capErr := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": paneID})
		if capErr == nil {
			output := string(capData)
			lines := strings.Split(output, "\n")
			for j := len(lines) - 1; j >= 0 && j >= len(lines)-5; j-- {
				line := strings.TrimSpace(lines[j])
				if strings.HasSuffix(line, "$") || strings.Contains(line, "$ ") {
					upgradeComplete = true
					break
				}
			}
			if upgradeComplete {
				break
			}
		}
		if i%10 == 9 {
			_, _ = fmt.Fprintf(e.Stdout, "    ... %ds\n", i+1)
		}
	}
	if !upgradeComplete {
		_, _ = fmt.Fprintf(e.Stdout, "  ⚠️  升级可能未完成，继续尝试重启...\n")
	}

	// Step 4: 重启 boot.sh
	_, _ = fmt.Fprintf(e.Stdout, "  [4/5] 重启 agent...\n")
	time.Sleep(500 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "bash ./.cicy/boot.sh",
	})
	time.Sleep(200 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "Enter",
	})

	// 等待 agent 启动
	agentReady := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		capData, capErr := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": paneID})
		if capErr == nil {
			output := string(capData)
			if strings.Contains(output, "❯") || strings.Contains(output, "> ") || strings.Contains(output, "Enter a prompt") {
				agentReady = true
				break
			}
		}
	}
	if !agentReady {
		_, _ = fmt.Fprintf(e.Stdout, "  ⚠️  agent 可能未就绪\n")
	}

	// Step 5: 发送 /resume 并选择会话
	_, _ = fmt.Fprintf(e.Stdout, "  [5/5] 恢复会话...\n")
	time.Sleep(500 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "/resume",
	})
	time.Sleep(200 * time.Millisecond)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "Enter",
	})

	// 等待会话列表出现，然后选择第一个
	time.Sleep(2 * time.Second)
	_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": paneID,
		"keys":    "1",
	})
	time.Sleep(200 * time.Millisecond)
	// 检查是否需要按 Enter
	capData, _ := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": paneID})
	if capData != nil && strings.Contains(string(capData), "1") {
		_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
			"pane_id": paneID,
			"keys":    "Enter",
		})
	}

	_, _ = fmt.Fprintf(e.Stdout, "✅ 升级完成: %s\n", paneID)
	return nil
}

func tmUpgradeUsage() string {
	return `Usage: cicy-agent upgrade <pane_id>

升级指定 agent 的 CLI 工具并恢复会话。

流程:
  1. 退出当前进程 (Ctrl+C)
  2. 运行 npm 升级命令
  3. 重启 boot.sh
  4. 发送 /resume 恢复会话
  5. 自动选择最近的会话

Examples:
  cicy-agent upgrade w-10001
  cicy-agent upgrade w-10026`
}

func (e *Env) forkFromAgent(cfg tmConfig, newPaneID, sourceAgentID string) error {
	_, _ = fmt.Fprintf(e.Stdout, "\n从 %s fork...\n", sourceAgentID)

	// 获取 summary 文件路径
	summaryPath := e.findSummaryFile(sourceAgentID)
	if summaryPath == "" {
		return fmt.Errorf("找不到 %s 的 summary 文件", sourceAgentID)
	}
	_, _ = fmt.Fprintf(e.Stdout, "  summary: %s\n", summaryPath)

	// 等待新 agent 就绪
	_, _ = fmt.Fprintf(e.Stdout, "  等待 %s 就绪...\n", newPaneID)
	ready := false
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)
		data, err := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": newPaneID})
		if err != nil {
			continue
		}
		output := string(data)
		// 检查是否可以输入 prompt (claude 显示 ❯, codex 显示 >)
		if strings.Contains(output, "❯") || strings.Contains(output, "> ") || strings.Contains(output, "Enter a prompt") {
			ready = true
			break
		}
	}
	if !ready {
		return errors.New("等待超时，agent 未就绪")
	}

	// 发送 read summary 命令
	prompt := fmt.Sprintf("file://%s read", summaryPath)
	_, _ = fmt.Fprintf(e.Stdout, "  发送: %s\n", prompt)

	// 先发送文本，再发送 Enter
	_, err := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": newPaneID,
		"keys":    prompt,
	})
	if err != nil {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	_, err = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
		"pane_id": newPaneID,
		"keys":    "Enter",
	})
	if err != nil {
		return err
	}

	// 检查是否发送成功，如果 prompt 还在输入框里说明 Enter 没生效，补发
	time.Sleep(500 * time.Millisecond)
	capData, capErr := e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/capture_pane", map[string]any{"pane_id": newPaneID})
	if capErr == nil {
		output := string(capData)
		// 如果 capture 里还能看到完整的 prompt 文本在最后一行，说明 Enter 没发出去
		if strings.Contains(output, "file://") && strings.Contains(output, ".summary.md read") {
			_, _ = fmt.Fprintf(e.Stdout, "  检测到 Enter 未生效，补发...\n")
			_, _ = e.apiRequestTo(context.Background(), cfg.API, cfg.Token, http.MethodPost, "/api/tmux/send-keys", map[string]any{
				"pane_id": newPaneID,
				"keys":    "Enter",
			})
			time.Sleep(300 * time.Millisecond)
		}
	}

	_, _ = fmt.Fprintf(e.Stdout, "✅ Fork 完成，%s 已启动\n", newPaneID)
	return nil
}

func (e *Env) findSummaryFile(agentID string) string {
	// 尝试多个可能的路径
	bases := []string{
		filepath.Join(userHomeDir(), "cicy-ai", "workers", agentID, ".cicy", "history", "summary"),
		filepath.Join(userHomeDir(), "workers", agentID, "history", "summary"),
	}

	for _, base := range bases {
		// 先尝试 current.summary.md 软链接
		currentSummary := filepath.Join(base, "current.summary.md")
		if target, err := os.Readlink(currentSummary); err == nil {
			if filepath.IsAbs(target) {
				return target
			}
			return filepath.Join(base, target)
		}

		// 找最新的 .summary.md 文件
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		var latest string
		var latestTime time.Time
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".summary.md") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latest = filepath.Join(base, entry.Name())
			}
		}
		if latest != "" {
			return latest
		}
	}
	return ""
}

func (e *Env) copyAPI(method, path string, payload any) error {
	return e.copyAPITo(e.API, e.Token, method, path, payload)
}

func (e *Env) copyAPITo(apiBase, token, method, path string, payload any) error {
	data, err := e.apiRequestTo(context.Background(), apiBase, token, method, path, payload)
	if err != nil {
		return err
	}
	_, _ = e.Stdout.Write(data)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, _ = fmt.Fprintln(e.Stdout)
	}
	return nil
}

func parseTMArgs(args []string) (tmOptions, []string, error) {
	opts := tmOptions{}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "-n" || arg == "--node":
			if i+1 >= len(args) {
				return tmOptions{}, nil, errors.New("Usage: cicy-agent [--node NAME] <command> [args]")
			}
			opts.Node = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--node="):
			opts.Node = strings.TrimSpace(strings.TrimPrefix(arg, "--node="))
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func (e *Env) resolveTMConfig(nodeOverride string) (tmConfig, error) {
	apiBase := firstNonEmpty(
		strings.TrimSpace(os.Getenv("TM_API_BASE")),
		strings.TrimSpace(os.Getenv("API_BASE")),
	)
	nodeName := strings.TrimSpace(nodeOverride)
	if nodeName == "" {
		nodeName = strings.TrimSpace(os.Getenv("TM_NODE"))
	}
	tmRoot := e.effectiveTMConfig()
	selected := tmRoot
	defaultNodeName := strings.TrimSpace(anyString(tmRoot["default"]))
	if nodeName == "" {
		nodeName = defaultNodeName
	}
	if nodeName != "" {
		nodes, _ := tmRoot["nodes"].(map[string]any)
		nodeCfg, _ := nodes[nodeName].(map[string]any)
		if len(nodeCfg) == 0 {
			return tmConfig{}, fmt.Errorf("cicy-agent node %q not found in %s", nodeName, tmJSONPath())
		}
		selected = nodeCfg
	}
	if apiBase == "" {
		apiBase = firstNonEmpty(
			strings.TrimSpace(anyString(selected["api"])),
			strings.TrimSpace(anyString(selected["api_base"])),
			strings.TrimSpace(anyString(selected["url"])),
			strings.TrimSpace(anyString(tmRoot["api"])),
			strings.TrimSpace(anyString(tmRoot["api_base"])),
			strings.TrimSpace(anyString(tmRoot["url"])),
		)
	}
	if apiBase == "" {
		port := firstNonEmpty(
			strings.TrimSpace(os.Getenv("TM_API_PORT")),
			strings.TrimSpace(os.Getenv("API_PORT")),
			strings.TrimSpace(anyString(selected["port"])),
			strings.TrimSpace(anyString(tmRoot["port"])),
			"8008",
		)
		apiBase = "http://127.0.0.1:" + port
	}
	token := firstNonEmpty(
		strings.TrimSpace(os.Getenv("TM_TOKEN")),
		strings.TrimSpace(anyString(selected["api_token"])),
		strings.TrimSpace(anyString(selected["token"])),
	)
	if token == "" {
		nodeLabel := nodeName
		if nodeLabel == "" {
			nodeLabel = defaultNodeName
		}
		if nodeLabel == "" {
			nodeLabel = "default"
		}
		return tmConfig{}, fmt.Errorf("cicy-agent node %q is missing api_token in %s", nodeLabel, tmJSONPath())
	}
	return tmConfig{
		API:   strings.TrimRight(apiBase, "/"),
		Token: token,
		Node:  nodeName,
	}, nil
}

func (e *Env) effectiveTMConfig() map[string]any {
	root := e.TM
	if len(root) == 0 {
		root = map[string]any{}
	}
	hadConfiguredNodes := false
	if nodes, _ := root["nodes"].(map[string]any); len(nodes) > 0 {
		hadConfiguredNodes = true
	}
	out := map[string]any{}
	for _, key := range []string{"default", "api", "api_base", "url", "port"} {
		if value, ok := root[key]; ok {
			out[key] = value
		}
	}
	nodes, _ := root["nodes"].(map[string]any)
	outNodes := map[string]any{}
	for name, rawNode := range nodes {
		node, ok := rawNode.(map[string]any)
		if !ok || len(node) == 0 {
			continue
		}
		copyNode := map[string]any{}
		for _, key := range []string{"api", "api_base", "url", "port", "api_token", "token"} {
			if value, ok := node[key]; ok {
				copyNode[key] = value
			}
		}
		outNodes[name] = copyNode
	}
	defaultNodeName := strings.TrimSpace(anyString(out["default"]))
	if defaultNodeName == "" {
		defaultNodeName = "default"
		out["default"] = defaultNodeName
	}
	defaultNode, _ := outNodes[defaultNodeName].(map[string]any)
	if len(defaultNode) == 0 {
		defaultNode = map[string]any{}
		outNodes[defaultNodeName] = defaultNode
	}
	if firstNonEmpty(
		strings.TrimSpace(anyString(defaultNode["api"])),
		strings.TrimSpace(anyString(defaultNode["api_base"])),
		strings.TrimSpace(anyString(defaultNode["url"])),
	) == "" {
		defaultNode["api"] = defaultAPIBase
	}
	if firstNonEmpty(
		strings.TrimSpace(anyString(defaultNode["api_token"])),
		strings.TrimSpace(anyString(defaultNode["token"])),
	) == "" {
		if !hadConfiguredNodes {
			if token := strings.TrimSpace(anyString(e.Global["api_token"])); token != "" {
				defaultNode["api_token"] = token
			}
		}
	}
	out["nodes"] = outNodes
	return out
}

func tmUsage() string {
	return `Usage: cicy-agent [--node NAME] <command> [args]

Commands:
  help                                  Show cicy-agent help and config rules
  ls                                    List panes
  tree                                  Tmux tree
  windows                               Window list
  capture <pane>                        Capture pane output
  msg <pane> <text>                     Send message with Enter
  msg_wait <pane> <text> [timeout]
  send-keys <pane> <keys>               Send raw keys
  create <name>                         Create pane
  restart                               Restart all panes
  clear <pane>                          Clear pane

Multi-node selection:
  cicy-agent ls                         Use the configured default target
  cicy-agent --node dev ls              Use nodes.dev
  TM_NODE=dev cicy-agent ls             Same as --node dev
  TM_API_BASE=http://127.0.0.1:8021 cicy-agent ls
                                         Bypass node lookup and hit this API directly

How to use configured nodes:
  1. Put node definitions in ~/cicy-ai/db/cicy-agent.json
  2. Pick the default node with the top-level "default" field
  3. Use cicy-agent directly for the default node
  4. Use cicy-agent --node <name> ... for a specific node

Config resolution order:
  1. TM_API_BASE or API_BASE
  2. TM_NODE / --node, then ~/cicy-ai/db/cicy-agent.json nodes[<name>]
  3. ~/cicy-ai/db/cicy-agent.json default -> nodes[<default>]
  4. ~/cicy-ai/db/cicy-agent.json api / api_base / url
  5. http://127.0.0.1:${TM_API_PORT|API_PORT|8008}

Token order:
  1. TM_TOKEN
  2. selected cicy-agent node api_token

Default fallback:
  - cicy-agent never writes config files
  - if ~/cicy-ai/db/cicy-agent.json is missing or incomplete, cicy-agent uses an in-memory default:
      default = "default"
      nodes.default.api = "http://127.0.0.1:8008"
      nodes.default.api_token = ~/cicy-ai/global.json api_token

Example ~/cicy-ai/db/cicy-agent.json:
  {
    "default": "default",
    "nodes": {
      "default": {
        "api": "http://127.0.0.1:8008",
        "api_token": "<copy from ~/cicy-ai/global.json api_token>"
      },
      "dev": {
        "api": "http://127.0.0.1:8021",
        "api_token": "<copy from ~/cicy-ai/global.json api_token>"
      }
    }
  }

Supported cicy-agent.json keys:
  default                               Default node name
  api | api_base | url                  Default API base when no node is selected
  port                                  Default local port fallback
  nodes.<name>.api                      Node API base
  nodes.<name>.api_base                 Node API base alias
  nodes.<name>.url                      Node API base alias
  nodes.<name>.api_token                Node token
  nodes.<name>.token                    Legacy node token alias
  nodes.<name>.port                     Node-local port fallback

Notes:
  - --node wins over default
  - TM_API_BASE wins over all cicy-agent config
  - TM_TOKEN wins over node api_token
  - cicy-agent reads config from ~/cicy-ai/db/cicy-agent.json
  - cicy-agent never writes ~/cicy-ai/global.json or ~/cicy-ai/db/cicy-agent.json
  - if the selected node is missing, cicy-agent returns an explicit config error`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (e *Env) chatClients() (map[string]map[string]map[string]any, error) {
	data, err := e.apiRequest(context.Background(), http.MethodGet, "/api/chat/clients", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]map[string]any{}
	if err := json.Unmarshal(data, &out); err == nil {
		return out, nil
	}
	var flat []map[string]any
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, err
	}
	for _, item := range flat {
		agentID := anyString(item["master_agent_id"])
		clientID := anyString(item["client_id"])
		if agentID == "" || clientID == "" {
			continue
		}
		if out[agentID] == nil {
			out[agentID] = map[string]map[string]any{}
		}
		out[agentID][clientID] = item
	}
	return out, nil
}

func (e *Env) resolveWebTarget(clientID string) (string, string, error) {
	clients, err := e.chatClients()
	if err != nil {
		return "", "", err
	}
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		var matchedAgentID string
		for agentID, clientMap := range clients {
			if _, ok := clientMap[clientID]; !ok {
				continue
			}
			if matchedAgentID != "" {
				return "", "", fmt.Errorf("client_id %s matched multiple agents; pass a unique client_id", clientID)
			}
			matchedAgentID = agentID
		}
		if matchedAgentID == "" {
			return "", "", fmt.Errorf("client_id %s not found", clientID)
		}
		return matchedAgentID, clientID, nil
	}

	agentID := currentAgentID()
	clientMap := clients[agentID]
	switch len(clientMap) {
	case 0:
		return "", "", fmt.Errorf("current agent %s has no connected webpage client", agentID)
	case 1:
		for resolvedClientID := range clientMap {
			return agentID, resolvedClientID, nil
		}
	}
	return "", "", fmt.Errorf("current agent %s has multiple clients; pass client_id explicitly", agentID)
}

func (e *Env) printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, _ = e.Stdout.Write(data)
	_, _ = fmt.Fprintln(e.Stdout)
	return nil
}

func (e *Env) runDesktopPing(kind, expectType, successText string, args []string) error {
	agentID := currentAgentID()
	if len(args) > 0 {
		agentID = args[0]
	}
	rid := randomID(kind)
	conn, err := e.wsConnect(agentID)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
		"agent_id": agentID,
		"type":     "desktop_event",
		"data":     map[string]any{"type": kind, "requestId": rid},
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 %s (%s)\n", kind, rid)
	_, err = e.waitForMessage(conn, 15*time.Second, func(m map[string]any) bool {
		return anyString(m["type"]) == expectType && anyString(asMap(m["data"])["requestId"]) == rid
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(e.Stdout, successText)
	return nil
}

func (e *Env) runIPCPing(args []string) error {
	clientID := ""
	if len(args) > 0 {
		clientID = args[0]
	}
	agentID, clientID, err := e.resolveWebTarget(clientID)
	if err != nil {
		return err
	}
	rid := randomID("ipc-ping")
	conn, err := e.wsConnect(agentID)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
		"agent_id":  agentID,
		"client_id": clientID,
		"type":      "desktop_event",
		"data":      map[string]any{"type": "ipc_ping", "requestId": rid},
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 ipc_ping (%s) → client_id=%s agent_id=%s\n", rid, clientID, agentID)
	msg, err := e.waitForMessage(conn, 15*time.Second, func(m map[string]any) bool {
		return anyString(m["type"]) == "ipc_pong" && anyString(asMap(m["data"])["requestId"]) == rid
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(e.Stdout, "✅ 收到 ipc_pong！Electron 连通成功")
	if result := strings.TrimSpace(anyString(asMap(msg["data"])["result"])); result != "" {
		_, _ = fmt.Fprintf(e.Stdout, "   Electron 返回: %s\n", result)
	}
	return nil
}

func expandHostPath(input string) string {
	value := strings.TrimSpace(input)
	if value == "" {
		return ""
	}
	home := userHomeDir()
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	wd := mustGetwd()
	if wd == "" {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(wd, value))
}

var codeServerLineSuffixRe = regexp.MustCompile(`^(.*?)(:\d+(?::\d+)?(?:-\d+:\d+)?)$`)

func normalizeCodeServerOpenPath(input string) string {
	value := strings.TrimSpace(input)
	if value == "" {
		return ""
	}
	suffix := ""
	if match := codeServerLineSuffixRe.FindStringSubmatch(value); len(match) == 3 {
		value = strings.TrimSpace(match[1])
		suffix = strings.TrimSpace(match[2])
	}
	if strings.HasPrefix(value, "file://") {
		pathValue := strings.TrimSpace(strings.TrimPrefix(value, "file://"))
		if pathValue != "" && !strings.HasPrefix(pathValue, "/") && !strings.HasPrefix(pathValue, "~/") && !strings.HasPrefix(pathValue, "./") && !strings.HasPrefix(pathValue, "../") && !regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(pathValue) {
			pathValue = "/" + strings.TrimLeft(pathValue, "/")
		}
		return pathValue + suffix
	}
	return expandHostPath(value) + suffix
}

type codeServerTarget struct {
	agentID            string
	pageClientID       string
	codeServerClientID string
}

func (e *Env) resolveCodeServerTarget(pageClientID string) (codeServerTarget, error) {
	clients, err := e.chatClients()
	if err != nil {
		return codeServerTarget{}, err
	}
	pageClientID = strings.TrimSpace(pageClientID)
	if pageClientID != "" {
		for agentID, clientMap := range clients {
			if _, ok := clientMap[pageClientID]; !ok {
				continue
			}
			return codeServerTarget{agentID: agentID, pageClientID: pageClientID, codeServerClientID: pageClientID + ":code-ext"}, nil
		}
		return codeServerTarget{}, fmt.Errorf("page_client_id %s not found", pageClientID)
	}
	agentID := currentAgentID()
	clientMap := clients[agentID]
	var pageClients []string
	for clientID := range clientMap {
		if strings.HasSuffix(clientID, ":code-ext") {
			continue
		}
		pageClients = append(pageClients, clientID)
	}
	switch len(pageClients) {
	case 0:
		return codeServerTarget{}, fmt.Errorf("current agent %s has no connected page client", agentID)
	case 1:
		return codeServerTarget{agentID: agentID, pageClientID: pageClients[0], codeServerClientID: pageClients[0] + ":code-ext"}, nil
	default:
		return codeServerTarget{}, fmt.Errorf("current agent %s has multiple page clients; pass page_client_id explicitly", agentID)
	}
}

func (e *Env) listCodeServerTargets() ([]map[string]any, error) {
	clients, err := e.chatClients()
	if err != nil {
		return nil, err
	}
	agentID := currentAgentID()
	clientMap := clients[agentID]
	out := []map[string]any{}
	for clientID := range clientMap {
		if strings.HasSuffix(clientID, ":code-ext") {
			continue
		}
		codeClientID := clientID + ":code-ext"
		_, codeReady := clientMap[codeClientID]
		out = append(out, map[string]any{
			"agent_id":              agentID,
			"page_client_id":        clientID,
			"code_server_client_id": codeClientID,
			"code_server_connected": codeReady,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return anyString(out[i]["page_client_id"]) < anyString(out[j]["page_client_id"])
	})
	return out, nil
}

func (e *Env) runAgentCodeServer(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(e.Stdout, "agent-code-server - CiCy code-server open-file tool\n\nCommands:\n  help\n  tools\n  ping [page_client_id]\n  list\n  clients\n  open <path> [page_client_id]\n\nNotes:\n  - target by page_client_id, not agent_id\n  - if omitted, current agent must have exactly one connected page client\n  - ping sends host.ping to the matching :code-ext client and waits for code.pong\n  - open accepts plain paths, file:// paths, and optional :line[:column] or :line:column-endLine:endColumn suffixes\n  - open first tells the page client to show the files drawer, then waits for code.opened from :code-ext")
		return nil
	case "tools":
		_, _ = fmt.Fprintln(e.Stdout, "# agent-code-server tools\n\n- ping [page_client_id] -> sends host.ping to the matching :code-ext client and waits for code.pong\n- list -> lists current page_client_id values and code-server connectivity\n- clients -> legacy alias of list\n- open <path> [page_client_id] -> asks the page client to open the files drawer and forwards the same requestId to :code-ext; supports file:// and line/column suffixes")
		return nil
	case "ping":
		pageClientID := ""
		if len(args) > 0 {
			pageClientID = args[0]
		}
		target, err := e.resolveCodeServerTarget(pageClientID)
		if err != nil {
			return err
		}
		conn, err := e.wsConnect(target.agentID)
		if err != nil {
			return err
		}
		defer conn.Close()
		rid := randomID("code-ping")
		_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id":  target.agentID,
			"client_id": target.codeServerClientID,
			"type":      "host.ping",
			"data": map[string]any{
				"requestId":      rid,
				"page_client_id": target.pageClientID,
				"code_client_id": target.codeServerClientID,
			},
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 host.ping → page_client_id=%s code_server_client_id=%s agent_id=%s\n", target.pageClientID, target.codeServerClientID, target.agentID)
		msg, err := e.waitForMessage(conn, 15*time.Second, func(m map[string]any) bool {
			data := asMap(m["data"])
			return anyString(m["type"]) == "code.pong" &&
				anyString(data["requestId"]) == rid &&
				anyString(data["code_client_id"]) == target.codeServerClientID
		})
		if err != nil {
			return err
		}
		data := asMap(msg["data"])
		version := strings.TrimSpace(anyString(data["version"]))
		if version == "" {
			version = "unknown"
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 收到 code.pong！code-server 在线 (v%s)\n", version)
		return nil
	case "list", "clients":
		out, err := e.listCodeServerTargets()
		if err != nil {
			return err
		}
		return e.printJSON(out)
	case "open":
		if len(args) < 1 {
			return errors.New("Usage: agent-code-server open <path> [page_client_id]")
		}
		pageClientID := ""
		if len(args) > 1 {
			pageClientID = args[1]
		}
		target, err := e.resolveCodeServerTarget(pageClientID)
		if err != nil {
			return err
		}
		conn, err := e.wsConnect(target.agentID)
		if err != nil {
			return err
		}
		defer conn.Close()
		rid := randomID("code-open")
		payload := map[string]any{
			"path":      normalizeCodeServerOpenPath(args[0]),
			"requestId": rid,
		}
		payload["page_client_id"] = target.pageClientID
		payload["code_client_id"] = target.codeServerClientID
		_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{"agent_id": target.agentID, "client_id": target.pageClientID, "type": "code.open_file", "data": payload})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 code.open_file → page_client_id=%s code_server_client_id=%s agent_id=%s\n", target.pageClientID, target.codeServerClientID, target.agentID)
		msg, err := e.waitForMessage(conn, 15*time.Second, func(m map[string]any) bool {
			data := asMap(m["data"])
			if anyString(data["requestId"]) != rid || anyString(data["code_client_id"]) != target.codeServerClientID {
				return false
			}
			typ := anyString(m["type"])
			return typ == "code.opened" || typ == "code.open_file_error"
		})
		if err != nil {
			return err
		}
		if anyString(msg["type"]) == "code.open_file_error" {
			return fmt.Errorf("code-server open failed: %s", strings.TrimSpace(anyString(asMap(msg["data"])["error"])))
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 收到 code.opened → %s\n", anyString(asMap(msg["data"])["path"]))
		return nil
	default:
		return e.runAgentCodeServer([]string{"help"})
	}
}

func (e *Env) runAgentWebpage(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(e.Stdout, "agent-webpage - CiCy live webpage client tool\n\nCommands:\n  help\n  tools\n  ping [client_id]\n  ipc-ping [client_id]\n  exec-js '<js>' [client_id]\n  current-active-agent-id [client_id]\n  current-master-agent-id [client_id]\n  send <type> <data_json> [client_id] [expect_type]\n  clients\n\nNotes:\n  - target by client_id, not agent_id\n  - if omitted, current agent must have exactly one connected client\n  - response-oriented commands wait for and print the real webpage response")
		return nil
	case "tools":
		_, _ = fmt.Fprintln(e.Stdout, "# agent-webpage tools\n\n- ping [client_id] -> direct push to client_id, waits for webpage_pong\n- ipc-ping [client_id] -> direct push to client_id, waits for ipc_pong\n- exec-js '<js>' [client_id] -> direct push to client_id, waits for exec_js_result\n- current-active-agent-id [client_id] -> prints devStore Workspace.activeCliPaneId from the live webpage\n- current-master-agent-id [client_id] -> prints devStore Workspace.masterAgentId from the live webpage\n- send <type> <data_json> [client_id] [expect_type] -> direct push to client_id, waits for matching response when requestId / expect_type is available\n- clients -> /api/chat/clients")
		return nil
	case "ping":
		clientID := ""
		if len(args) > 0 {
			clientID = args[0]
		}
		agentID, clientID, err := e.resolveWebTarget(clientID)
		if err != nil {
			return err
		}
		rid := randomID("webpage-ping")
		conn, err := e.wsConnect(agentID)
		if err != nil {
			return err
		}
		defer conn.Close()
		_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id":  agentID,
			"client_id": clientID,
			"type":      "webpage_ping",
			"data":      map[string]any{"requestId": rid},
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 webpage_ping → client_id=%s agent_id=%s\n", clientID, agentID)
		msg, err := e.waitForMessage(conn, 15*time.Second, func(m map[string]any) bool {
			return anyString(m["type"]) == "webpage_pong" && anyString(asMap(m["data"])["requestId"]) == rid
		})
		if err != nil {
			return err
		}
		version := anyString(asMap(msg["data"])["version"])
		if version == "" {
			version = "unknown"
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 网页客户端在线 (v%s)\n", version)
		return nil
	case "ipc-ping", "ipc_ping":
		return e.runIPCPing(args)
	case "exec-js", "exec_js":
		if len(args) < 1 {
			return errors.New("Usage: agent-webpage exec-js '<js代码>' [client_id]")
		}
		clientID := ""
		if len(args) > 1 {
			clientID = args[1]
		}
		agentID, clientID, err := e.resolveWebTarget(clientID)
		if err != nil {
			return err
		}
		rid := randomID("exec")
		conn, err := e.wsConnect(agentID)
		if err != nil {
			return err
		}
		defer conn.Close()
		_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id":  agentID,
			"client_id": clientID,
			"type":      "exec_js",
			"data":      map[string]any{"code": args[0], "requestId": rid},
		})
		if err != nil {
			return err
		}
		msg, err := e.waitForMessage(conn, 20*time.Second, func(m map[string]any) bool {
			return anyString(m["type"]) == "exec_js_result" && anyString(asMap(m["data"])["requestId"]) == rid
		})
		if err != nil {
			return err
		}
		data := asMap(msg["data"])
		if errText := anyString(data["error"]); errText != "" {
			return errors.New(errText)
		}
		_, _ = fmt.Fprintln(e.Stdout, data["result"])
		return nil
	case "current-active-agent-id", "current_active_agent_id":
		clientID := ""
		if len(args) > 0 {
			clientID = args[0]
		}
		return e.runAgentWebpageReadWorkspaceField(clientID, "activeCliPaneId")
	case "current-master-agent-id", "current_master_agent_id":
		clientID := ""
		if len(args) > 0 {
			clientID = args[0]
		}
		return e.runAgentWebpageReadWorkspaceField(clientID, "masterAgentId")
	case "send":
		if len(args) < 2 {
			return errors.New("Usage: agent-webpage send <type> <data_json> [client_id] [expect_type]")
		}
		var payload any
		if err := json.Unmarshal([]byte(args[1]), &payload); err != nil {
			return err
		}
		clientID := ""
		if len(args) > 2 {
			clientID = args[2]
		}
		expectType := ""
		if len(args) > 3 {
			expectType = strings.TrimSpace(args[3])
		}
		agentID, clientID, err := e.resolveWebTarget(clientID)
		if err != nil {
			return err
		}
		requestID := ""
		if payloadMap, ok := payload.(map[string]any); ok {
			requestID = strings.TrimSpace(anyString(payloadMap["requestId"]))
			if requestID == "" {
				requestID = randomID(args[0])
				payloadMap["requestId"] = requestID
			}
			payload = payloadMap
		}
		conn, err := e.wsConnect(agentID)
		if err != nil {
			return err
		}
		defer conn.Close()
		_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id":  agentID,
			"client_id": clientID,
			"type":      args[0],
			"data":      payload,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "✅ 发送 %s → client_id=%s agent_id=%s\n", args[0], clientID, agentID)
		if requestID == "" && expectType == "" {
			return nil
		}
		msg, err := e.waitForMessage(conn, 20*time.Second, func(m map[string]any) bool {
			if expectType != "" && anyString(m["type"]) != expectType {
				return false
			}
			if requestID == "" {
				return true
			}
			return anyString(asMap(m["data"])["requestId"]) == requestID
		})
		if err != nil {
			return err
		}
		return e.printJSON(msg)
	case "clients":
		return e.copyAPI(http.MethodGet, "/api/chat/clients", nil)
	default:
		return e.runAgentWebpage([]string{"help"})
	}
}

func (e *Env) runAgentWebpageReadWorkspaceField(clientID string, field string) error {
	agentID, clientID, err := e.resolveWebTarget(clientID)
	if err != nil {
		return err
	}
	rid := randomID("exec")
	conn, err := e.wsConnect(agentID)
	if err != nil {
		return err
	}
	defer conn.Close()
	code := fmt.Sprintf(`(() => {
  const snapshot = globalThis.devStore && typeof globalThis.devStore.getSnapshot === "function"
    ? globalThis.devStore.getSnapshot()
    : null;
  const workspace = snapshot && snapshot.Workspace && snapshot.Workspace.state ? snapshot.Workspace.state : null;
  const value = workspace ? workspace[%q] : null;
  return value == null ? "" : String(value);
})()`, field)
	_, err = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
		"agent_id":  agentID,
		"client_id": clientID,
		"type":      "exec_js",
		"data":      map[string]any{"code": code, "requestId": rid},
	})
	if err != nil {
		return err
	}
	msg, err := e.waitForMessage(conn, 20*time.Second, func(m map[string]any) bool {
		return anyString(m["type"]) == "exec_js_result" && anyString(asMap(m["data"])["requestId"]) == rid
	})
	if err != nil {
		return err
	}
	data := asMap(msg["data"])
	if errText := anyString(data["error"]); errText != "" {
		return errors.New(errText)
	}
	_, _ = fmt.Fprintln(e.Stdout, anyString(data["result"]))
	return nil
}

func (e *Env) runGeminiAsk(args []string) error {
	if len(args) < 1 {
		return errors.New("Usage: gemini-ask <prompt> [win_id]")
	}
	winID := 4
	if len(args) > 1 {
		winID, _ = strconv.Atoi(args[1])
	}
	agentID := currentAgentID()
	rid := randomID("gemini")
	conn, err := e.wsConnect(agentID)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id": agentID,
			"type":     "desktop_event",
			"data":     map[string]any{"type": "gemini_ask", "prompt": args[0], "win_id": winID, "requestId": rid},
		})
	}()
	msg, err := e.waitForMessage(conn, 60*time.Second, func(m map[string]any) bool {
		return anyString(m["type"]) == "gemini_ask_result" && anyString(asMap(m["data"])["requestId"]) == rid
	})
	if err != nil {
		return err
	}
	data := asMap(msg["data"])
	if errText := anyString(data["error"]); errText != "" {
		return errors.New(errText)
	}
	_, _ = fmt.Fprintln(e.Stdout, data["result"])
	return nil
}

func (e *Env) runGeminiVision(args []string) error {
	prompt := "描述这个截图的内容"
	if len(args) > 0 {
		prompt = args[0]
	}
	winID := 4
	srcWinID := 1
	if len(args) > 1 {
		winID, _ = strconv.Atoi(args[1])
	}
	if len(args) > 2 {
		srcWinID, _ = strconv.Atoi(args[2])
	}
	agentID := currentAgentID()
	rid := randomID("vision")
	conn, err := e.wsConnect(agentID)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = e.apiRequest(context.Background(), http.MethodPost, "/api/chat/push", map[string]any{
			"agent_id": agentID,
			"type":     "desktop_event",
			"data":     map[string]any{"type": "gemini_vision_request", "prompt": prompt, "win_id": winID, "src_win_id": srcWinID, "requestId": rid},
		})
	}()
	msg, err := e.waitForMessage(conn, 60*time.Second, func(m map[string]any) bool {
		return anyString(m["type"]) == "gemini_vision_result" && anyString(asMap(m["data"])["requestId"]) == rid
	})
	if err != nil {
		return err
	}
	data := asMap(msg["data"])
	if errText := anyString(data["error"]); errText != "" {
		return errors.New(errText)
	}
	_, _ = fmt.Fprintln(e.Stdout, data["result"])
	return nil
}

func (e *Env) runMySQLExec(args []string) error {
	if len(args) < 1 {
		return errors.New("Usage: mysql-exec \"SQL\" [database]")
	}
	db := "cicy_code"
	if len(args) > 1 {
		db = args[1]
	}
	pass := readEnvValue(filepath.Join(userHomeDir(), "projects", "cicy-code", ".env"), "MYSQL_ROOT_PASSWORD")
	cmd := exec.Command("docker", "exec", "-i", "cicy-mysql", "sh", "-c", fmt.Sprintf("exec mysql -u root -p'%s' %s -e %q", pass, db, args[0]))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	lines := filterLines(string(out), func(line string) bool {
		return !strings.Contains(line, "Warning")
	})
	_, _ = fmt.Fprint(e.Stdout, lines)
	return nil
}

func readEnvValue(path, key string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func filterLines(s string, keep func(string) bool) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if keep(line) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func (e *Env) runTodo(args []string) error {
	if len(args) == 0 {
		return e.runExternal("gh", "issue", "list", "--repo", defaultRepo)
	}
	switch args[0] {
	case "feat", "fix", "todo", "add":
		label := args[0]
		if label == "add" {
			label = "todo"
		}
		title, assign := parseTodoArgs(args[1:])
		if title == "" {
			return errors.New("todo title is required")
		}
		out, err := exec.Command("gh", "issue", "create", "--repo", defaultRepo, "--title", title, "--label", label, "--body", bodyOrAssign(assign)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		url := strings.TrimSpace(string(out))
		_, _ = fmt.Fprintln(e.Stdout, url)
		_, _ = exec.Command("gh", "project", "item-add", "4", "--owner", "cicy-dev", "--url", url).CombinedOutput()
		return nil
	case "done":
		if len(args) < 2 {
			return errors.New("Usage: todo done <number>")
		}
		return e.runExternal("gh", "issue", "close", args[1], "--repo", defaultRepo)
	case "view":
		if len(args) < 2 {
			return errors.New("Usage: todo view <number>")
		}
		return e.runExternal("gh", "issue", "view", args[1], "--repo", defaultRepo)
	case "url":
		_, _ = fmt.Fprintln(e.Stdout, "https://github.com/users/cicy-dev/projects/4")
		return nil
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(e.Stdout, "Usage: todo [command] [args]\n\nCommands:\n  (none)                        List all open todos\n  feat <title> [--assign <w>]   Add a feature todo\n  fix  <title> [--assign <w>]   Add a fix todo\n  add  <title> [--assign <w>]   Add a general todo\n  done <number>                 Close/complete a todo\n  view <number>                 View a todo\n  url                           Show project board URL")
		return nil
	default:
		return e.runExternal("gh", "issue", "list", "--repo", defaultRepo)
	}
}

func parseTodoArgs(args []string) (title, assign string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--assign" && i+1 < len(args) {
			assign = args[i+1]
			i++
			continue
		}
		if title == "" {
			title = args[i]
		} else {
			title += " " + args[i]
		}
	}
	return strings.TrimSpace(title), strings.TrimSpace(assign)
}

func bodyOrAssign(assign string) string {
	if assign == "" {
		return ""
	}
	return "assigned to: " + assign
}

func (e *Env) runExternal(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		_, _ = e.Stdout.Write(out)
		if out[len(out)-1] != '\n' {
			_, _ = fmt.Fprintln(e.Stdout)
		}
	}
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *Env) runCFTunnel(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(e.Stdout, cfTunnelUsage())
		return nil
	}
	cfEnv := envOr("CF_ENV", "prod")
	cfRoot, _ := e.Global["cf"].(map[string]any)
	cfg, _ := cfRoot[cfEnv].(map[string]any)
	if len(cfg) == 0 {
		return fmt.Errorf("missing cf.%s config in ~/cicy-ai/global.json", cfEnv)
	}
	token := strings.TrimSpace(anyString(cfg["api_token"]))
	accountID := strings.TrimSpace(anyString(cfg["account_id"]))
	tunnelID := strings.TrimSpace(anyString(cfg["tunnel_id"]))
	domain := strings.TrimSpace(anyString(cfg["domain"]))
	zoneID := strings.TrimSpace(anyString(cfg["zone_id"]))
	if token == "" || accountID == "" || tunnelID == "" || domain == "" || zoneID == "" {
		return errors.New("incomplete Cloudflare tunnel config")
	}

	cf := &cfTunnel{
		token:       token,
		accountID:   accountID,
		tunnelID:    tunnelID,
		tunnelCNAME: tunnelID + ".cfargotunnel.com",
		domain:      domain,
		zoneID:      zoneID,
		http:        e.HTTP,
		stdout:      e.Stdout,
	}
	switch args[0] {
	case "list":
		return cf.list()
	case "add":
		if len(args) < 2 {
			return errors.New("Usage: cf-tunnel add <port> [port2 ...]")
		}
		return cf.add(parsePorts(args[1:]))
	case "del":
		if len(args) < 2 {
			return errors.New("Usage: cf-tunnel del <port> [port2 ...]")
		}
		return cf.del(parsePorts(args[1:]))
	default:
		return fmt.Errorf("unknown cf-tunnel command: %s", args[0])
	}
}

func cfTunnelUsage() string {
	return `Usage: cf-tunnel <list|add|del> [ports...]

Commands:
  list                      List current tunnel routes and local port status
  add <port> [port2 ...]    Add one or more routes and DNS records
  del <port> [port2 ...]    Delete one or more routes and DNS records

Environment:
  CF_ENV=prod|dev           Choose the Cloudflare config from ~/cicy-ai/global.json`
}

func parsePorts(args []string) []int {
	var ports []int
	for _, arg := range args {
		if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil {
			ports = append(ports, n)
		}
	}
	return ports
}

type cfTunnel struct {
	token       string
	accountID   string
	tunnelID    string
	tunnelCNAME string
	domain      string
	zoneID      string
	http        *http.Client
	stdout      io.Writer
}

func (c *cfTunnel) hostnameFor(port int) string { return fmt.Sprintf("g-%d.%s", port, c.domain) }

func (c *cfTunnel) api(method, path string, payload any) (map[string]any, error) {
	endpoint := "https://api.cloudflare.com/client/v4/" + path
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cfTunnel) getConfig() (map[string]any, error) {
	out, err := c.api(http.MethodGet, fmt.Sprintf("accounts/%s/cfd_tunnel/%s/configurations", c.accountID, c.tunnelID), nil)
	if err != nil {
		return nil, err
	}
	if success, _ := out["success"].(bool); !success {
		return nil, fmt.Errorf("获取配置失败: %v", out["errors"])
	}
	result := asMap(out["result"])
	return asMap(result["config"]), nil
}

func (c *cfTunnel) putConfig(config map[string]any) error {
	out, err := c.api(http.MethodPut, fmt.Sprintf("accounts/%s/cfd_tunnel/%s/configurations", c.accountID, c.tunnelID), map[string]any{"config": config})
	if err != nil {
		return err
	}
	if success, _ := out["success"].(bool); !success {
		return fmt.Errorf("更新配置失败: %v", out["errors"])
	}
	return nil
}

func (c *cfTunnel) dnsList() ([]map[string]any, error) {
	var all []map[string]any
	for page := 1; ; page++ {
		out, err := c.api(http.MethodGet, fmt.Sprintf("zones/%s/dns_records?type=CNAME&per_page=100&page=%d", c.zoneID, page), nil)
		if err != nil {
			return nil, err
		}
		if success, _ := out["success"].(bool); !success {
			return nil, fmt.Errorf("dns list failed: %v", out["errors"])
		}
		result, _ := out["result"].([]any)
		if len(result) == 0 {
			break
		}
		for _, item := range result {
			all = append(all, asMap(item))
		}
		if len(result) < 100 {
			break
		}
	}
	return all, nil
}

func (c *cfTunnel) dnsAdd(hostname string) bool {
	out, err := c.api(http.MethodPost, fmt.Sprintf("zones/%s/dns_records", c.zoneID), map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": c.tunnelCNAME,
		"proxied": true,
		"ttl":     1,
	})
	if err != nil {
		return false
	}
	if success, _ := out["success"].(bool); success {
		return true
	}
	errorsList, _ := out["errors"].([]any)
	for _, item := range errorsList {
		if strings.Contains(strings.ToLower(fmt.Sprint(item)), "already") {
			return true
		}
	}
	return false
}

func (c *cfTunnel) dnsDel(hostname string) bool {
	records, err := c.dnsList()
	if err != nil {
		return false
	}
	for _, rec := range records {
		if anyString(rec["name"]) == hostname {
			out, err := c.api(http.MethodDelete, fmt.Sprintf("zones/%s/dns_records/%s", c.zoneID, anyString(rec["id"])), nil)
			if err != nil {
				return false
			}
			success, _ := out["success"].(bool)
			return success
		}
	}
	return false
}

func (c *cfTunnel) list() error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	count := 0
	for _, item := range ingress {
		if _, ok := asMap(item)["hostname"]; ok {
			count++
		}
	}
	_, _ = fmt.Fprintf(c.stdout, "📡 Tunnel 路由 (%d 条):\n\n", count)
	for _, item := range ingress {
		rule := asMap(item)
		hostname := anyString(rule["hostname"])
		if hostname == "" {
			hostname = "(catch-all)"
		}
		service := anyString(rule["service"])
		status := ""
		if strings.Contains(service, "localhost:") {
			parts := strings.Split(service, ":")
			port, _ := strconv.Atoi(parts[len(parts)-1])
			if portListening(port) {
				status = " ✅"
			} else {
				status = " ❌"
			}
		}
		_, _ = fmt.Fprintf(c.stdout, "  %s → %s%s\n", hostname, service, status)
	}
	return nil
}

func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *cfTunnel) add(ports []int) error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	catchAll := map[string]any{"service": "http_status:404"}
	var rules []map[string]any
	for _, item := range ingress {
		rule := asMap(item)
		if anyString(rule["hostname"]) == "" {
			catchAll = rule
			continue
		}
		rules = append(rules, rule)
	}
	existing := map[string]bool{}
	for _, rule := range rules {
		existing[anyString(rule["hostname"])] = true
	}
	type addItem struct {
		port     int
		hostname string
	}
	var added []addItem
	for _, port := range ports {
		hostname := c.hostnameFor(port)
		if existing[hostname] {
			_, _ = fmt.Fprintf(c.stdout, "  ⏭️  %s 已存在 %s\n", hostname, ternary(portListening(port), "✅", "❌ 端口未监听"))
			continue
		}
		if !portListening(port) {
			_, _ = fmt.Fprintf(c.stdout, "  ⚠️  localhost:%d 未监听，仍然添加路由\n", port)
		}
		rules = append(rules, map[string]any{"hostname": hostname, "service": fmt.Sprintf("http://localhost:%d", port)})
		added = append(added, addItem{port: port, hostname: hostname})
	}
	if len(added) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "\n没有新增路由")
		return nil
	}
	config["ingress"] = append(anySliceFromMap(rules), catchAll)
	if err := c.putConfig(config); err != nil {
		return err
	}
	for _, item := range added {
		ok := c.dnsAdd(item.hostname)
		_, _ = fmt.Fprintf(c.stdout, "  ✅ %s → localhost:%d  DNS:%s\n", item.hostname, item.port, ternary(ok, "✅", "❌"))
	}
	_, _ = fmt.Fprintf(c.stdout, "\n🎉 成功添加 %d 条路由\n", len(added))
	return nil
}

func (c *cfTunnel) del(ports []int) error {
	config, err := c.getConfig()
	if err != nil {
		return err
	}
	ingress, _ := config["ingress"].([]any)
	catchAll := map[string]any{"service": "http_status:404"}
	var rules []map[string]any
	for _, item := range ingress {
		rule := asMap(item)
		if anyString(rule["hostname"]) == "" {
			catchAll = rule
			continue
		}
		rules = append(rules, rule)
	}
	toDelete := map[string]bool{}
	for _, port := range ports {
		toDelete[c.hostnameFor(port)] = true
	}
	var kept []map[string]any
	var removed []string
	for _, rule := range rules {
		if toDelete[anyString(rule["hostname"])] {
			removed = append(removed, anyString(rule["hostname"]))
		} else {
			kept = append(kept, rule)
		}
	}
	if len(removed) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "没有匹配的路由")
		return nil
	}
	config["ingress"] = append(anySliceFromMap(kept), catchAll)
	if err := c.putConfig(config); err != nil {
		return err
	}
	for _, hostname := range removed {
		ok := c.dnsDel(hostname)
		_, _ = fmt.Fprintf(c.stdout, "  🗑️  %s  DNS:%s\n", hostname, ternary(ok, "✅", "⚠️ 未找到"))
	}
	_, _ = fmt.Fprintf(c.stdout, "\n🎉 成功删除 %d 条路由\n", len(removed))
	return nil
}

func anySliceFromMap(items []map[string]any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (e *Env) runCPing(args []string) error {
	if len(args) < 1 {
		return errors.New("Usage: cping <domain_or_ip>")
	}
	target := args[0]
	ip := target
	if addrs, err := net.LookupHost(target); err == nil && len(addrs) > 0 {
		ip = addrs[0]
	}
	_, _ = fmt.Fprintf(e.Stdout, "\n🏓 cping - %s (%s)\n", target, ip)
	_, _ = fmt.Fprintln(e.Stdout, strings.Repeat("━", 50))
	resp, err := e.HTTP.Get("https://www.itdog.cn/ping/" + target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("无法获取数据")
	}
	html := string(body)
	rowRe := regexp.MustCompile(`(?s)<tr class="node_tr"[^>]*>(.*?)</tr>`)
	ispRe := regexp.MustCompile(`(?s)<span class="badge[^"]*">\s*(.*?)\s*</span>\s*(.*?)\s*</td>`)
	avgRe := regexp.MustCompile(`id="avg_ping_\d+"[^>]*>([^<]+)`)
	lossRe := regexp.MustCompile(`id="loss_\d+"[^>]*>([^<]+)`)
	type result struct {
		isp      string
		location string
		avg      string
		loss     string
	}
	var results []result
	for _, row := range rowRe.FindAllStringSubmatch(html, -1) {
		m := ispRe.FindStringSubmatch(row[1])
		if len(m) != 3 {
			continue
		}
		avg := "--"
		if v := avgRe.FindStringSubmatch(row[1]); len(v) == 2 {
			avg = strings.TrimSpace(v[1])
		}
		loss := ""
		if v := lossRe.FindStringSubmatch(row[1]); len(v) == 2 {
			loss = strings.TrimSpace(v[1])
		}
		results = append(results, result{isp: strings.TrimSpace(m[1]), location: strings.TrimSpace(m[2]), avg: avg, loss: loss})
	}
	if len(results) == 0 {
		return errors.New("无法获取数据")
	}
	groups := map[string][]result{}
	for _, item := range results {
		groups[item.isp] = append(groups[item.isp], item)
	}
	for _, isp := range []string{"电信", "联通", "移动", "海外"} {
		nodes := groups[isp]
		if len(nodes) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(e.Stdout, "\n  📡 %s:\n", isp)
		for _, node := range nodes {
			_, _ = fmt.Fprintf(e.Stdout, "    %-12s avg=%-8s loss=%s\n", node.location, node.avg, node.loss)
		}
	}
	_, _ = fmt.Fprintln(e.Stdout)
	return nil
}
