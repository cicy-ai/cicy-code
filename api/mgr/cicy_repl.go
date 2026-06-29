package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runCicyREPL is the terminal half of the dispatcher agent type: a tiny
// stdin → /api/dispatcher/chat → stdout loop that lives in the agent's tmux
// pane. It carries no LLM logic at all — the server-side dispatcher runtime
// (agent_dispatcher.go) owns the conversation, tools and gateway traffic.
// Because it reads plain stdin, every existing delivery channel (web chat
// send-keys, `cicy-agent msg`, Telegram bridge) reaches it unchanged.
//
// Usage: cicy-code dispatcher-repl [--agent <short-id>] [--server <base-url>]
// Defaults: agent = $X_AGENT_SHORT_ID, server = http://127.0.0.1:8008.
func runCicyREPL(args []string) int {
	agentID := strings.TrimSpace(os.Getenv("X_AGENT_SHORT_ID"))
	// Default to THIS instance's port. cicy-repl is a subcommand (separate
	// process) so it can't see the server's in-memory PORT; it runs inside an
	// agent pane, which exports CICY_API_PORT (falling back to PORT, then 8008).
	replPort := strings.TrimSpace(os.Getenv("CICY_API_PORT"))
	if replPort == "" {
		replPort = strings.TrimSpace(os.Getenv("PORT"))
	}
	if replPort == "" {
		replPort = "8008"
	}
	server := "http://127.0.0.1:" + replPort
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				i++
				agentID = strings.TrimSpace(args[i])
			}
		case "--server":
			if i+1 < len(args) {
				i++
				server = strings.TrimRight(strings.TrimSpace(args[i]), "/")
			}
		}
	}
	if agentID == "" {
		fmt.Fprintln(os.Stderr, "cicy-repl: agent id required (--agent or $X_AGENT_SHORT_ID)")
		return 1
	}

	const (
		cyan  = "\033[1;36m"
		dim   = "\033[2m"
		red   = "\033[1;31m"
		reset = "\033[0m"
	)
	fmt.Printf("%s● CiCy %s%s — 已就绪,直接输入需求。\n", cyan, agentID, reset)
	prompt := func() { fmt.Printf("%s>%s ", cyan, reset) }
	prompt()

	// Read stdin in a goroutine so input that arrives WHILE a reply is streaming
	// keeps buffering instead of blocking. After each turn we drain everything
	// that queued during it and send it as ONE merged turn — matching "回复没
	// 完成时的输入进队列,完成后一起发出去".
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	for {
		first, ok := <-lines
		if !ok {
			return 0
		}
		// Collect the first line plus everything already buffered (queued during
		// the previous turn or typed back-to-back). Stop at the first /quit|/exit.
		batch := []string{}
		quit := false
		add := func(raw string) {
			t := strings.TrimSpace(raw)
			if t == "/quit" || t == "/exit" {
				quit = true
				return
			}
			if t != "" {
				batch = append(batch, t)
			}
		}
		add(first)
	drain:
		for !quit {
			select {
			case l, ok := <-lines:
				if !ok {
					break drain // stdin closed; flush what we have, then exit after
				}
				add(l)
			default:
				break drain
			}
		}
		if len(batch) > 0 {
			merged := strings.Join(batch, "\n")
			if err := cicyREPLTurn(server, agentID, merged); err != nil {
				fmt.Printf("%s✗ %v%s\n", red, err, reset)
			}
		}
		if quit {
			return 0
		}
		prompt()
	}
}

// cicyREPLTurn posts one user line and prints the SSE event stream.
func cicyREPLTurn(server, agentID, text string) error {
	body, err := json.Marshal(map[string]string{"agent_id": agentID, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, server+"/api/cicy/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := json.Marshal(resp.Status)
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		if msg := strings.TrimSpace(buf.String()); msg != "" {
			return fmt.Errorf("server %d: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("server error: %s", raw)
	}

	const (
		dim    = "\033[2m"
		yellow = "\033[33m"
		red    = "\033[1;31m"
		reset  = "\033[0m"
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Name   string `json:"name"`
			Arg    string `json:"arg"`
			Result string `json:"result"`
			Error  string `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt) != nil {
			continue
		}
		switch evt.Type {
		case "text_delta":
			// Streamed tokens — print raw, no newline (os.Stdout is unbuffered,
			// so the terminal shows them as they arrive).
			fmt.Print(evt.Text)
		case "text_end":
			fmt.Println()
		case "text":
			// Non-stream fallback: whole block at once.
			fmt.Println(strings.TrimRight(evt.Text, "\n"))
		case "tool":
			fmt.Printf("%s⚙ %s%s %s%s\n", yellow, evt.Name, reset, dim, evt.Arg)
			if r := strings.TrimSpace(evt.Result); r != "" {
				for _, l := range strings.Split(r, "\n") {
					fmt.Printf("%s  %s%s\n", dim, l, reset)
				}
			} else {
				fmt.Print(reset)
			}
		case "error":
			fmt.Printf("%s✗ %s%s\n", red, evt.Error, reset)
		case "done":
			return nil
		}
	}
	return scanner.Err()
}
