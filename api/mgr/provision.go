package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type provStep struct {
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

var (
	provMu    sync.Mutex
	provChans = map[string][]chan provStep{}
)

func provSend(uid string, s provStep) {
	provMu.Lock()
	defer provMu.Unlock()
	for _, ch := range provChans[uid] {
		select {
		case ch <- s:
		default:
		}
	}
}

func provSubscribe(uid string) chan provStep {
	ch := make(chan provStep, 20)
	provMu.Lock()
	provChans[uid] = append(provChans[uid], ch)
	provMu.Unlock()
	return ch
}

func provUnsubscribe(uid string, ch chan provStep) {
	provMu.Lock()
	defer provMu.Unlock()
	chs := provChans[uid]
	for i, c := range chs {
		if c == ch {
			provChans[uid] = append(chs[:i], chs[i+1:]...)
			break
		}
	}
	if len(provChans[uid]) == 0 {
		delete(provChans, uid)
	}
	close(ch)
}

// GET /api/provision/stream?token=JWT
func handleProvisionStream(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		httpErr(w, 401, "no token")
		return
	}
	userID, _, err := parseJWT(token)
	if err != nil {
		httpErr(w, 401, "invalid token")
		return
	}

	// CORS for SSE
	if o := r.Header.Get("Origin"); o != "" {
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming not supported")
		return
	}

	// Send initial comment to flush headers
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Already provisioned?
	var backendURL string
	db.QueryRow("SELECT COALESCE(backend_url,'') FROM saas_users WHERE id=?", userID).Scan(&backendURL)
	if backendURL != "" {
		data, _ := json.Marshal(provStep{5, 5, "done", backendURL})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	// Not provisioned yet — trigger provision
	var email string
	db.QueryRow("SELECT email FROM saas_users WHERE id=?", userID).Scan(&email)
	go provisionBackend(userID, email)

	ch := provSubscribe(userID)
	defer provUnsubscribe(userID, ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case step, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(step)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			if step.Status == "done" || step.Status == "error" {
				return
			}
		}
	}
}

func provisionBackend(userID, email string) {
	vmName := "u-" + userID[:8]
	home, _ := os.UserHomeDir()
	script := filepath.Join(home, "projects/cicy-code/provision.sh")

	// Lookup plan
	var plan string
	db.QueryRow("SELECT COALESCE(plan,'free') FROM saas_users WHERE id=?", userID).Scan(&plan)

	log.Printf("[provision] starting %s → %s (plan=%s)", userID, vmName, plan)
	provSend(userID, provStep{1, 5, "running", "Creating Cloudflare Tunnel..."})

	cmd := exec.Command("bash", script, vmName, "asia-east1-b")
	cmd.Dir = filepath.Join(home, "projects/cicy-code")

	// Strip proxy
	env := os.Environ()
	cleaned := make([]string, 0, len(env))
	for _, e := range env {
		k := strings.SplitN(e, "=", 2)[0]
		switch strings.ToLower(k) {
		case "https_proxy", "http_proxy", "all_proxy":
			continue
		}
		cleaned = append(cleaned, e)
	}
	// Free users get smaller VM
	if plan != "pro" {
		cleaned = append(cleaned, "MACHINE_TYPE=e2-micro", "DISK_SIZE=10GB")
	}
	cmd.Env = cleaned

	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		log.Printf("[provision] start failed: %v", err)
		provSend(userID, provStep{0, 5, "error", "Failed to start"})
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("[provision:%s] %s", vmName, line)
		if strings.Contains(line, "[1/") {
			provSend(userID, provStep{1, 5, "running", "Creating Cloudflare Tunnel..."})
		} else if strings.Contains(line, "[2/") {
			provSend(userID, provStep{2, 5, "running", "Creating server..."})
		} else if strings.Contains(line, "[3/") {
			provSend(userID, provStep{3, 5, "running", "Uploading deploy script..."})
		} else if strings.Contains(line, "[4/") {
			provSend(userID, provStep{4, 5, "running", "Deploying services..."})
		} else if strings.Contains(line, "[5/") {
			provSend(userID, provStep{5, 5, "running", "Verifying..."})
		}
	}

	if err := cmd.Wait(); err != nil {
		log.Printf("[provision] failed for %s: %v", userID, err)
		provSend(userID, provStep{0, 5, "error", "Provision failed"})
		return
	}

	infoFile := filepath.Join(home, "cicy/vms", vmName+".json")
	data, err := os.ReadFile(infoFile)
	if err != nil {
		log.Printf("[provision] info file not found: %s", infoFile)
		provSend(userID, provStep{0, 5, "error", "Result not found"})
		return
	}

	var info struct {
		APIURL string `json:"api_url"`
	}
	if json.Unmarshal(data, &info) != nil || info.APIURL == "" {
		provSend(userID, provStep{0, 5, "error", "Bad result"})
		return
	}

	db.Exec("UPDATE saas_users SET backend_url=? WHERE id=?", info.APIURL, userID)
	log.Printf("[provision] user %s → %s", userID, info.APIURL)
	provSend(userID, provStep{5, 5, "done", info.APIURL})
}
