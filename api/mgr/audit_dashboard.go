package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── Token Management ──

// POST /api/audit/register — register audit user, get proxy credential
// Body: {"user_id": "xxx", "plan": "free"}
func handleAuditRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
		Plan   string `json:"plan"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	if req.UserID == "" {
		httpErr(w, 400, "user_id required")
		return
	}
	if req.Plan == "" {
		req.Plan = "free"
	}

	// Generate a proxy auth token
	token := fmt.Sprintf("cicy_%s_%d", req.UserID, time.Now().UnixNano()%100000)

	userJSON, _ := json.Marshal(M{
		"user_id":    req.UserID,
		"plan":       req.Plan,
		"created_at": time.Now().Unix(),
	})

	redisHSet("audit:tokens", token, string(userJSON))

	J(w, M{
		"success": true,
		"data": M{
			"token":     token,
			"proxy_url": fmt.Sprintf("https://%s:x@audit.cicy-ai.com:8003", token),
			"plan":      req.Plan,
		},
	})
}

// GET /api/audit/dashboard?user={id} — per-user audit dashboard
func handleAuditDashboard(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	if userID == "" {
		httpErr(w, 400, "user parameter required")
		return
	}

	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 90 {
			days = v
		}
	}

	// Gather daily stats
	dailyStats := []M{}
	totalCost := 0.0
	totalCalls := 0
	totalInput := 0
	totalOutput := 0
	modelBreakdown := map[string]M{}

	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		statsKey := fmt.Sprintf("audit:user:%s:stats:%s", userID, date)
		fields := redisHGetAll(statsKey)

		dayCost := 0.0
		dayCalls := 0
		dayInput := 0
		dayOutput := 0

		for model, raw := range fields {
			var stat struct {
				Calls        int     `json:"calls"`
				InputTokens  int     `json:"input_tokens"`
				OutputTokens int     `json:"output_tokens"`
				Cost         float64 `json:"cost"`
			}
			json.Unmarshal([]byte(raw), &stat)

			dayCost += stat.Cost
			dayCalls += stat.Calls
			dayInput += stat.InputTokens
			dayOutput += stat.OutputTokens

			if _, ok := modelBreakdown[model]; !ok {
				modelBreakdown[model] = M{
					"calls": 0, "input_tokens": 0, "output_tokens": 0, "cost": 0.0,
				}
			}
			mb := modelBreakdown[model]
			mb["calls"] = mb["calls"].(int) + stat.Calls
			mb["input_tokens"] = mb["input_tokens"].(int) + stat.InputTokens
			mb["output_tokens"] = mb["output_tokens"].(int) + stat.OutputTokens
			mb["cost"] = mb["cost"].(float64) + stat.Cost
		}

		totalCost += dayCost
		totalCalls += dayCalls
		totalInput += dayInput
		totalOutput += dayOutput

		dailyStats = append(dailyStats, M{
			"date":          date,
			"calls":         dayCalls,
			"input_tokens":  dayInput,
			"output_tokens": dayOutput,
			"cost_usd":      dayCost,
		})
	}

	// Get quota info
	monthKey := time.Now().Format("2006-01")
	monthlyCount := redisHGet(fmt.Sprintf("audit:user:%s:monthly", userID), monthKey)
	monthCalls, _ := strconv.Atoi(monthlyCount)

	J(w, M{
		"success": true,
		"data": M{
			"user_id":         userID,
			"period_days":     days,
			"total_cost_usd":  totalCost,
			"total_calls":     totalCalls,
			"total_input":     totalInput,
			"total_output":    totalOutput,
			"monthly_calls":   monthCalls,
			"daily":           dailyStats,
			"model_breakdown": modelBreakdown,
		},
	})
}

// GET /api/audit/usage?user={id}&limit=100 — per-user traffic log
func handleAuditUsage(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	if userID == "" {
		httpErr(w, 400, "user parameter required")
		return
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}

	key := fmt.Sprintf("audit:user:%s:log", userID)
	items := redisLRangeN(key, limit)

	entries := []M{}
	for _, item := range items {
		var entry M
		if err := json.Unmarshal([]byte(item), &entry); err == nil {
			entries = append(entries, entry)
		}
	}

	J(w, M{"success": true, "data": entries, "count": len(entries)})
}

// GET /api/audit/admin/overview — admin global stats
func handleAuditAdminOverview(w http.ResponseWriter, r *http.Request) {
	items := redisLRangeN("audit:usage", 5000)

	// Aggregate by user + provider
	userStats := map[string]M{}
	providerStats := map[string]int{}
	totalCost := 0.0
	cutoff24h := time.Now().Unix() - 86400

	for _, item := range items {
		var entry struct {
			UserID string  `json:"user_id"`
			TS     int64   `json:"ts"`
			Usage  *struct {
				Provider string  `json:"provider"`
				Cost     float64 `json:"cost_usd"`
			} `json:"ai_usage"`
		}
		if err := json.Unmarshal([]byte(item), &entry); err != nil {
			continue
		}
		if entry.TS < cutoff24h {
			continue
		}

		if _, ok := userStats[entry.UserID]; !ok {
			userStats[entry.UserID] = M{"calls": 0, "cost": 0.0}
		}
		us := userStats[entry.UserID]
		us["calls"] = us["calls"].(int) + 1

		if entry.Usage != nil {
			us["cost"] = us["cost"].(float64) + entry.Usage.Cost
			totalCost += entry.Usage.Cost
			providerStats[entry.Usage.Provider]++
		}
	}

	// Sort users by cost descending
	type userCost struct {
		UserID string  `json:"user_id"`
		Calls  int     `json:"calls"`
		Cost   float64 `json:"cost_usd"`
	}
	topUsers := []userCost{}
	for uid, s := range userStats {
		topUsers = append(topUsers, userCost{
			UserID: uid,
			Calls:  s["calls"].(int),
			Cost:   s["cost"].(float64),
		})
	}
	sort.Slice(topUsers, func(i, j int) bool { return topUsers[i].Cost > topUsers[j].Cost })
	if len(topUsers) > 20 {
		topUsers = topUsers[:20]
	}

	J(w, M{
		"success": true,
		"data": M{
			"period":          "24h",
			"total_calls":     len(items),
			"total_cost_usd":  totalCost,
			"active_users":    len(userStats),
			"top_users":       topUsers,
			"provider_stats":  providerStats,
		},
	})
}

// GET /api/audit/live — SSE stream of all audit events
func handleAuditLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		http.Error(w, "redis error", 500)
		return
	}
	defer conn.Close()
	conn.Write([]byte("SUBSCRIBE audit:live\r\n"))

	ctx := r.Context()
	buf := make([]byte, 8192)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				fmt.Fprintf(w, ": keepalive\n\n")
				flusher.Flush()
				continue
			}
			return
		}
		lines := strings.Split(string(buf[:n]), "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "{") {
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		}
	}
}

// ── Redis Helpers ──

func redisHSet(key, field, value string) {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	req := fmt.Sprintf("*4\r\n$4\r\nHSET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n",
		len(key), key, len(field), field, len(value), value)
	conn.Write([]byte(req))
}

func redisHGet(key, field string) string {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	req := fmt.Sprintf("*3\r\n$4\r\nHGET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n",
		len(key), key, len(field), field)
	conn.Write([]byte(req))
	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])
	lines := strings.Split(resp, "\r\n")
	if len(lines) >= 2 && strings.HasPrefix(lines[0], "$") {
		return lines[1]
	}
	return ""
}

func redisHGetAll(key string) map[string]string {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := fmt.Sprintf("*2\r\n$7\r\nHGETALL\r\n$%d\r\n%s\r\n", len(key), key)
	conn.Write([]byte(req))

	buf := make([]byte, 64 * 1024)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "*") {
		return nil
	}
	lines := strings.Split(resp, "\r\n")
	result := map[string]string{}
	i := 1
	for i+3 < len(lines) {
		if strings.HasPrefix(lines[i], "$") && strings.HasPrefix(lines[i+2], "$") {
			result[lines[i+1]] = lines[i+3]
			i += 4
		} else {
			i++
		}
	}
	return result
}

func redisLRangeN(key string, limit int) []string {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	limitStr := strconv.Itoa(limit - 1)
	req := fmt.Sprintf("*4\r\n$6\r\nLRANGE\r\n$%d\r\n%s\r\n$1\r\n0\r\n$%d\r\n%s\r\n",
		len(key), key, len(limitStr), limitStr)
	conn.Write([]byte(req))

	buf := make([]byte, 1024 * 1024)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "*") {
		return nil
	}
	lines := strings.Split(resp, "\r\n")
	count, _ := strconv.Atoi(lines[0][1:])
	result := []string{}
	i := 1
	for len(result) < count && i < len(lines)-1 {
		if strings.HasPrefix(lines[i], "$") {
			size, _ := strconv.Atoi(lines[i][1:])
			if size >= 0 && i+1 < len(lines) {
				result = append(result, lines[i+1])
			}
			i += 2
		} else {
			i++
		}
	}
	return result
}
