package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type systemResourceSnapshot struct {
	CPUUsagePct    float64 `json:"cpu_usage_pct"`
	CPUCores       int     `json:"cpu_cores"`
	MemUsagePct    float64 `json:"mem_usage_pct"`
	MemTotalBytes  uint64  `json:"mem_total_bytes"`
	MemUsedBytes   uint64  `json:"mem_used_bytes"`
	DiskUsagePct   float64 `json:"disk_usage_pct"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	DiskUsedBytes  uint64  `json:"disk_used_bytes"`
	Load1          float64 `json:"load_1"`
	Load5          float64 `json:"load_5"`
	Load15         float64 `json:"load_15"`
	UpdatedAt      string  `json:"updated_at"`
}

type cpuCounters struct {
	idle  uint64
	total uint64
}

type systemResourceClient struct {
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
}

type systemResourceHub struct {
	mu      sync.RWMutex
	clients map[*systemResourceClient]struct{}
	latest  atomic.Value
}

var systemResources = &systemResourceHub{clients: make(map[*systemResourceClient]struct{})}

func startSystemResourceMonitor() {
	go systemResources.loop()
}

func (h *systemResourceHub) loop() {
	prevCPU, _ := readCPUCounters()
	if snap, curr, err := sampleSystemResources(prevCPU); err == nil {
		prevCPU = curr
		h.setLatest(snap)
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		snap, curr, err := sampleSystemResources(prevCPU)
		if err != nil {
			continue
		}
		prevCPU = curr
		h.setLatest(snap)
		h.broadcast(snap)
		hub.broadcastAll(ChatEvent{Type: "system_resources", Data: snap})
	}
}

func (h *systemResourceHub) setLatest(snapshot systemResourceSnapshot) {
	h.latest.Store(snapshot)
}

func (h *systemResourceHub) getLatest() systemResourceSnapshot {
	if v := h.latest.Load(); v != nil {
		if snap, ok := v.(systemResourceSnapshot); ok {
			return snap
		}
	}
	return systemResourceSnapshot{
		CPUCores:  runtime.NumCPU(),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func (h *systemResourceHub) register(c *systemResourceClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	if snap := h.getLatest(); snap.UpdatedAt != "" {
		c.enqueue(snap)
	}
}

func (h *systemResourceHub) unregister(c *systemResourceClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

func (h *systemResourceHub) broadcast(snapshot systemResourceSnapshot) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.enqueue(snapshot)
	}
}

func (c *systemResourceClient) enqueue(snapshot systemResourceSnapshot) {
	data, _ := json.Marshal(snapshot)
	select {
	case c.send <- data:
	default:
		select {
		case <-c.send:
		default:
		}
		select {
		case c.send <- data:
		default:
		}
	}
}

func (c *systemResourceClient) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

func (c *systemResourceClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *systemResourceClient) readPump() {
	defer systemResources.unregister(c)
	c.conn.SetReadLimit(4 * 1024)
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func handleSystemResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, 405, "method not allowed")
		return
	}
	J(w, systemResources.getLatest())
}

func handleSystemResourcesWS(w http.ResponseWriter, r *http.Request) {
	token := getToken(r)
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" || !verifyToken(token) {
		httpErr(w, 401, "Not authenticated")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &systemResourceClient{
		conn: conn,
		send: make(chan []byte, 1),
	}
	systemResources.register(client)
	go client.writePump()
	client.readPump()
}

func sampleSystemResources(prev cpuCounters) (systemResourceSnapshot, cpuCounters, error) {
	currentCPU := prev
	cpuUsagePct := 0.0
	var err error
	if runtime.GOOS == "darwin" {
		value, err := readDarwinCPUUsage()
		if err != nil {
			return systemResourceSnapshot{}, prev, err
		}
		cpuUsagePct = value
	} else {
		currentCPU, err = readCPUCounters()
		if err != nil {
			return systemResourceSnapshot{}, prev, err
		}
		if prev.total > 0 && currentCPU.total > prev.total && currentCPU.idle >= prev.idle {
			deltaTotal := currentCPU.total - prev.total
			deltaIdle := currentCPU.idle - prev.idle
			if deltaTotal > 0 && deltaIdle <= deltaTotal {
				cpuUsagePct = float64(deltaTotal-deltaIdle) * 100 / float64(deltaTotal)
			}
		}
	}
	memTotal, memUsed, memUsagePct, err := readMemorySnapshot()
	if err != nil {
		return systemResourceSnapshot{}, prev, err
	}
	diskTotal, diskUsed, diskUsagePct, err := readDiskSnapshot("/")
	if err != nil {
		return systemResourceSnapshot{}, prev, err
	}
	load1, load5, load15, err := readLoadAverage()
	if err != nil {
		return systemResourceSnapshot{}, prev, err
	}
	return systemResourceSnapshot{
		CPUUsagePct:    cpuUsagePct,
		CPUCores:       runtime.NumCPU(),
		MemUsagePct:    memUsagePct,
		MemTotalBytes:  memTotal,
		MemUsedBytes:   memUsed,
		DiskUsagePct:   diskUsagePct,
		DiskTotalBytes: diskTotal,
		DiskUsedBytes:  diskUsed,
		Load1:          load1,
		Load5:          load5,
		Load15:         load15,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}, currentCPU, nil
}

func readCPUCounters() (cpuCounters, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuCounters{}, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return cpuCounters{}, err
		}
		return cpuCounters{}, os.ErrInvalid
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, os.ErrInvalid
	}
	var total uint64
	for _, raw := range fields[1:] {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return cpuCounters{}, err
		}
		total += value
	}
	idle, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return cpuCounters{}, err
	}
	if len(fields) > 5 {
		iowait, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return cpuCounters{}, err
		}
		idle += iowait
	}
	return cpuCounters{idle: idle, total: total}, nil
}

func readMemorySnapshot() (uint64, uint64, float64, error) {
	if runtime.GOOS == "darwin" {
		return readDarwinMemorySnapshot()
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	values := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[key] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}
	total := values["MemTotal"]
	if total == 0 {
		return 0, 0, 0, os.ErrInvalid
	}
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if available > total {
		available = total
	}
	used := total - available
	return total, used, float64(used) * 100 / float64(total), nil
}

func readLoadAverage() (float64, float64, float64, error) {
	if runtime.GOOS == "darwin" {
		return readDarwinLoadAverage()
	}
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, os.ErrInvalid
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	load5, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	load15, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	return load1, load5, load15, nil
}

func readDarwinCPUUsage() (float64, error) {
	out, err := exec.Command("iostat", "-C", "1", "2").Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) < 6 {
			continue
		}
		idle, err := strconv.ParseFloat(fields[len(fields)-4], 64)
		if err != nil {
			continue
		}
		if idle < 0 {
			idle = 0
		}
		if idle > 100 {
			idle = 100
		}
		return 100 - idle, nil
	}
	return 0, os.ErrInvalid
}

func readDarwinMemorySnapshot() (uint64, uint64, float64, error) {
	memOut, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, 0, 0, err
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(memOut)), 10, 64)
	if err != nil {
		return 0, 0, 0, err
	}

	pageOut, err := exec.Command("sysctl", "-n", "hw.pagesize").Output()
	if err != nil {
		return 0, 0, 0, err
	}
	pageSize, err := strconv.ParseUint(strings.TrimSpace(string(pageOut)), 10, 64)
	if err != nil {
		return 0, 0, 0, err
	}

	vmOut, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, 0, 0, err
	}
	var activePages, wiredPages, compressedPages uint64
	scanner := bufio.NewScanner(strings.NewReader(string(vmOut)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "Pages active:"):
			activePages = parseVMStatPages(line)
		case strings.HasPrefix(line, "Pages wired down:"):
			wiredPages = parseVMStatPages(line)
		case strings.HasPrefix(line, "Pages occupied by compressor:"):
			compressedPages = parseVMStatPages(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}

	used := (activePages + wiredPages + compressedPages) * pageSize
	if used > total {
		used = total
	}
	return total, used, float64(used) * 100 / float64(total), nil
}

func parseVMStatPages(line string) uint64 {
	fields := strings.Fields(strings.ReplaceAll(line, ".", ""))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func readDarwinLoadAverage() (float64, float64, float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, 0, 0, err
	}
	raw := strings.TrimSpace(string(out))
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return 0, 0, 0, os.ErrInvalid
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	load5, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	load15, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	return load1, load5, load15, nil
}
