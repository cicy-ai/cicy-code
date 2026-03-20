package main

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// startTmuxHealth periodically ensures tmux sessions, ttyd instances, and pipe-pane are alive.
func startTmuxHealth() {
	interval := 30 * time.Second
	log.Printf("[tmux-health] started | interval=%s", interval)
	time.Sleep(2 * time.Second) // wait for watcher to populate cfgCache
	for {
		healthCheck()
		time.Sleep(interval)
	}
}

func healthCheck() {
	watcherMu.Lock()
	cache := cfgCache
	watcherMu.Unlock()

	token := getFirstToken()
	for pid, cfg := range cache {
		sess := strings.Split(pid, ":")[0]

		// 1. session missing → create
		if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
			ws := cfg["workspace"]
			if ws == "" {
				ws = os.Getenv("HOME")
			}
			ws = strings.Replace(ws, "~", os.Getenv("HOME"), 1)
			exec.Command("tmux", "new-session", "-d", "-s", sess, "-n", "main", "-c", ws).Run()
			log.Printf("[tmux-health] created session %s", sess)
		}

		// 2. ttyd not listening → start
		if port, _ := strconv.Atoi(cfg["ttyd_port"]); port > 0 && !isPortListening(port) {
			log.Printf("[tmux-health] ttyd port %d down for %s, starting", port, pid)
			startInstance(pid, port, token)
		}

		// 3. pipe-pane restore
		ensurePipe(pid)
	}
}
