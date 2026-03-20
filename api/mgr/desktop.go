package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const desktopPort = 18101

// handleDesktopStatus returns the status of the electron-mcp RPC server
func handleDesktopStatus(w http.ResponseWriter, r *http.Request) {
	status := M{
		"desktop_mode": desktopMode,
		"port":         desktopPort,
		"running":      isPortListening(desktopPort),
	}

	if desktopCmd != nil && desktopCmd.Process != nil {
		status["pid"] = desktopCmd.Process.Pid
	}

	if isPortListening(desktopPort) {
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/rpc/tools", desktopPort))
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			status["tools_raw"] = string(body)
		}
	}

	J(w, status)
}

// handleDesktopProxy proxies /api/desktop/* → 127.0.0.1:18101/*
func handleDesktopProxy(w http.ResponseWriter, r *http.Request) {
	if !isPortListening(desktopPort) {
		httpErr(w, 503, "Desktop (electron-mcp) not running")
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", desktopPort))
	proxy := httputil.NewSingleHostReverseProxy(target)

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/desktop")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Host = target.Host

	proxy.ServeHTTP(w, r)
}

// ensureDesktop starts electron-mcp if --desktop mode is active
func ensureDesktop() {
	if !desktopMode {
		return
	}

	if isPortListening(desktopPort) {
		log.Printf("[desktop] electron-mcp already running on port %d", desktopPort)
		return
	}

	electronPath, err := exec.LookPath("electron")
	if err != nil {
		log.Printf("[desktop] electron not found in PATH, desktop mode disabled")
		return
	}

	// Find electron-mcp package via node resolve
	var mcpDir string
	out, err := exec.Command("node", "-e", "try{console.log(require.resolve('cicy/package.json'))}catch(e){}").Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		mcpDir = strings.TrimSuffix(strings.TrimSpace(string(out)), "/package.json")
	}

	if mcpDir == "" {
		log.Printf("[desktop] electron-mcp (cicy) not found, install: npm install -g cicy")
		return
	}

	token := getFirstToken()
	apiPort := os.Getenv("PORT")
	if apiPort == "" {
		apiPort = "18008"
	}
	startURL := fmt.Sprintf("http://127.0.0.1:%s/?token=%s", apiPort, token)

	desktopCmd = exec.Command(electronPath, mcpDir,
		fmt.Sprintf("--url=%s", startURL),
		fmt.Sprintf("--port=%d", desktopPort),
	)

	if err := desktopCmd.Start(); err != nil {
		log.Printf("[desktop] failed to start electron: %v", err)
		return
	}

	log.Printf("[desktop] electron-mcp started (PID: %d, RPC port: %d)", desktopCmd.Process.Pid, desktopPort)

	go func() {
		for i := 0; i < 30; i++ {
			if isPortListening(desktopPort) {
				log.Printf("[desktop] RPC/MCP ready on port %d", desktopPort)
				return
			}
			time.Sleep(time.Second)
		}
		log.Printf("[desktop] warning: RPC port %d not ready after 30s", desktopPort)
	}()

	go func() {
		if err := desktopCmd.Wait(); err != nil {
			log.Printf("[desktop] electron exited: %v", err)
		} else {
			log.Printf("[desktop] electron exited normally")
		}
		desktopCmd = nil
	}()
}
