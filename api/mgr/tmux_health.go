package main

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// startTmuxHealth periodically ensures tmux sessions and ttyd instances are alive.
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

		// ttyd is served on demand inline; no per-pane instance to revive.
	}
}
