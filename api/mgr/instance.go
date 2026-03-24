package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"ttyd-go/backend/localcommand"
	"ttyd-go/server"
)

type Instance struct {
	PaneID string
	Port   int
	Cancel context.CancelFunc
}

var (
	instances   = make(map[string]*Instance)
	instancesMu sync.RWMutex
	portPool    = initPortPool()
)

type PortPool struct {
	start, end int
	used       map[int]bool
	mu         sync.Mutex
}

func initPortPool() *PortPool {
	start := 20000
	if s := os.Getenv("TTYD_PORT_START"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			start = v
		}
	}
	return &PortPool{start: start, end: 65535, used: make(map[int]bool)}
}

func (p *PortPool) Allocate() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := p.start; i <= p.end; i++ {
		if !p.used[i] {
			p.used[i] = true
			return i
		}
	}
	return 0
}

func (p *PortPool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, port)
}

func startInstance(paneID string, port int, token string) error {
	instancesMu.Lock()
	defer instancesMu.Unlock()
	if _, ok := instances[paneID]; ok {
		return nil
	}
	opts := &server.Options{
		Address: "127.0.0.1", Port: fmt.Sprintf("%d", port),
		PermitWrite: true,
		TitleFormat: "{{ .command }}@{{ .hostname }}",
		EnableReconnect: true, ReconnectTime: 30,
		Term: "xterm", WSOrigin: ".*", PermitArguments: true,
		Preferences: &server.HtermPrefernces{
			ForegroundColor:           "#c0c0c0",
			FontSize:                  10,
			CopyOnSelect:              true,
			CtrlCCopy:                 true,
			CtrlVPaste:                true,
			UseDefaultWindowCopy:      true,
			ClearSelectionAfterCopy:   false,
		},
	}
	factory, err := localcommand.NewFactory("tmux", []string{"attach", "-t", paneID}, &localcommand.Options{CloseSignal: 1, CloseTimeout: -1})
	if err != nil {
		return err
	}
	srv, err := server.New(factory, opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	instances[paneID] = &Instance{PaneID: paneID, Port: port, Cancel: cancel}
	//log.Printf("[instance] started %s on :%d", paneID, port)
	go func() {
		if err := srv.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("[instance] %s error: %v", paneID, err)
			// Clean up so watcher can retry
			instancesMu.Lock()
			delete(instances, paneID)
			instancesMu.Unlock()
		}
	}()
	return nil
}

func stopInstance(paneID string) {
	instancesMu.Lock()
	defer instancesMu.Unlock()
	if inst, ok := instances[paneID]; ok {
		inst.Cancel()
		portPool.Release(inst.Port)
		delete(instances, paneID)
	}
}

func getInstance(paneID string) *Instance {
	instancesMu.RLock()
	defer instancesMu.RUnlock()
	return instances[paneID]
}

func waitPort(port int, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func isPortListening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil { return false }
	c.Close()
	return true
}
func runTmux(args ...string) (string, error) {
	//log.Printf("[tmux] args=%v", args)
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// extractPaneID 从 tmux 命令参数中提取 pane_id（-t 后面的值）
func extractPaneID(args []string) string {
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
