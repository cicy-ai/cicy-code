package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

//go:embed monitor
var monitorFS embed.FS

var (
	auditMode    bool
	mitmCmd      *exec.Cmd
	mitmMu       sync.Mutex
	monitorDir   string
)

// initAudit checks mitmproxy installation, writes default addons, starts mitmproxy
func initAudit() {
	home, _ := os.UserHomeDir()
	monitorDir = filepath.Join(home, ".cicy", "monitor")

	// 1. Check mitmproxy installed
	if _, err := exec.LookPath("mitmdump"); err != nil {
		log.Println("[audit] mitmdump not found, installing mitmproxy...")
		cmd := exec.Command("pip3", "install", "mitmproxy")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("[audit] failed to install mitmproxy: %v", err)
		}
	}
	log.Println("[audit] ✅ mitmdump found")

	// 2. Ensure ~/.cicy/monitor/ exists, write embedded *.monitor.py
	os.MkdirAll(monitorDir, 0755)
	entries, _ := monitorFS.ReadDir("monitor")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(monitorDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // already exists, don't overwrite
		}
		data, _ := monitorFS.ReadFile("monitor/" + e.Name())
		os.WriteFile(dst, data, 0644)
		log.Printf("[audit] wrote %s", dst)
	}

	// 3. Install python deps (redis)
	exec.Command("pip3", "install", "-q", "redis").Run()

	// 4. Start mitmproxy
	startMitmproxy()
}

func mitmproxyPort() string {
	p := os.Getenv("MITMPROXY_PORT")
	if p == "" {
		p = "8003"
	}
	return p
}

func startMitmproxy() {
	mitmMu.Lock()
	defer mitmMu.Unlock()

	if mitmCmd != nil && mitmCmd.Process != nil {
		// already running
		if mitmCmd.ProcessState == nil {
			log.Println("[audit] mitmproxy already running")
			return
		}
	}

	// Build addon args: load all *.monitor.py
	addons := []string{}
	entries, _ := os.ReadDir(monitorDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".monitor.py") {
			addons = append(addons, filepath.Join(monitorDir, e.Name()))
		}
	}

	port := mitmproxyPort()
	args := []string{
		"-p", port,
		"--ssl-insecure",
		"--set", "block_global=false",
		"--set", "proxyauth=any",
	}
	for _, a := range addons {
		args = append(args, "-s", a)
	}

	log.Printf("[audit] starting mitmdump on :%s with %d addons", port, len(addons))
	mitmCmd = exec.Command("mitmdump", args...)
	mitmCmd.Stdout = os.Stdout
	mitmCmd.Stderr = os.Stderr
	mitmCmd.Env = append(os.Environ(),
		"REDIS_PORT="+os.Getenv("REDIS_PORT"),
		"API_PORT="+os.Getenv("PORT"),
	)
	if err := mitmCmd.Start(); err != nil {
		log.Printf("[audit] failed to start mitmdump: %v", err)
		return
	}
	log.Printf("[audit] ✅ mitmdump started (pid %d)", mitmCmd.Process.Pid)

	// Wait in background to detect exit
	go func() {
		mitmCmd.Wait()
		log.Println("[audit] mitmdump exited")
	}()
}

func stopMitmproxy() {
	mitmMu.Lock()
	defer mitmMu.Unlock()
	if mitmCmd != nil && mitmCmd.Process != nil {
		mitmCmd.Process.Signal(syscall.SIGTERM)
		mitmCmd.Wait()
		mitmCmd = nil
		log.Println("[audit] mitmdump stopped")
	}
}

func restartMitmproxy() {
	stopMitmproxy()
	startMitmproxy()
}

func mitmproxyStatus() M {
	mitmMu.Lock()
	defer mitmMu.Unlock()
	running := mitmCmd != nil && mitmCmd.Process != nil && mitmCmd.ProcessState == nil
	pid := 0
	if running {
		pid = mitmCmd.Process.Pid
	}
	// List addons
	addons := []string{}
	entries, _ := os.ReadDir(monitorDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".monitor.py") {
			addons = append(addons, e.Name())
		}
	}
	return M{
		"running": running,
		"pid":     pid,
		"port":    mitmproxyPort(),
		"addons":  addons,
		"dir":     monitorDir,
	}
}

// ── API Handlers ──

func handleAuditStatus(w http.ResponseWriter, r *http.Request) {
	J(w, M{"success": true, "data": mitmproxyStatus()})
}

func handleAuditRestart(w http.ResponseWriter, r *http.Request) {
	restartMitmproxy()
	J(w, M{"success": true, "message": "mitmproxy restarted"})
}

func handleAuditStop(w http.ResponseWriter, r *http.Request) {
	stopMitmproxy()
	J(w, M{"success": true, "message": "mitmproxy stopped"})
}

func handleAuditStart(w http.ResponseWriter, r *http.Request) {
	startMitmproxy()
	J(w, M{"success": true, "message": "mitmproxy started"})
}

// GET /api/audit/addons — list addons
// POST /api/audit/addons — upload/update addon {name, code}
// DELETE /api/audit/addons?name=xxx — delete addon
func handleAuditAddons(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		entries, _ := os.ReadDir(monitorDir)
		addons := []M{}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".monitor.py") {
				continue
			}
			info, _ := e.Info()
			code, _ := os.ReadFile(filepath.Join(monitorDir, e.Name()))
			addons = append(addons, M{
				"name":     e.Name(),
				"size":     info.Size(),
				"modified": info.ModTime(),
				"code":     string(code),
			})
		}
		J(w, M{"success": true, "data": addons})

	case "POST":
		var req struct {
			Name string `json:"name"`
			Code string `json:"code"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "bad json")
			return
		}
		if req.Name == "" || req.Code == "" {
			httpErr(w, 400, "name and code required")
			return
		}
		if !strings.HasSuffix(req.Name, ".monitor.py") {
			req.Name += ".monitor.py"
		}
		dst := filepath.Join(monitorDir, req.Name)
		if err := os.WriteFile(dst, []byte(req.Code), 0644); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "message": fmt.Sprintf("addon %s saved, restart mitmproxy to apply", req.Name)})

	case "DELETE":
		name := r.URL.Query().Get("name")
		if name == "" {
			httpErr(w, 400, "name required")
			return
		}
		if !strings.HasSuffix(name, ".monitor.py") {
			name += ".monitor.py"
		}
		dst := filepath.Join(monitorDir, name)
		if err := os.Remove(dst); err != nil {
			httpErr(w, 404, "addon not found")
			return
		}
		J(w, M{"success": true, "message": fmt.Sprintf("addon %s deleted", name)})
	}
}

// GET/POST /api/audit/rules — manage monitor/audit rules via rules.json
func handleAuditRules(w http.ResponseWriter, r *http.Request) {
	rulesFile := filepath.Join(monitorDir, "rules.json")
	switch r.Method {
	case "GET":
		data, err := os.ReadFile(rulesFile)
		if err != nil {
			J(w, M{"success": true, "data": M{
				"monitor_domains": []string{},
				"blocked_patterns": []string{},
			}})
			return
		}
		var rules interface{}
		json.Unmarshal(data, &rules)
		J(w, M{"success": true, "data": rules})

	case "POST":
		var rules interface{}
		if err := readBody(r, &rules); err != nil {
			httpErr(w, 400, "bad json")
			return
		}
		data, _ := json.MarshalIndent(rules, "", "  ")
		os.WriteFile(rulesFile, data, 0644)
		J(w, M{"success": true, "message": "rules saved"})
	}
}
