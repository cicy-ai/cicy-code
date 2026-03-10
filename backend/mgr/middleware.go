package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type M = map[string]interface{}

func J(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(M{"detail": msg})
}

func corsM(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next(w, r)
	}
}

func authM(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || !verifyToken(strings.TrimPrefix(auth, "Bearer ")) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		next(w, r)
	}
}

func getToken(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if strings.HasPrefix(a, "Bearer ") {
		return a[7:]
	}
	return ""
}

func loadAPIToken() string {
	home, _ := os.UserHomeDir()
	data, _ := os.ReadFile(filepath.Join(home, "global.json"))
	var cfg M
	json.Unmarshal(data, &cfg)
	if t, ok := cfg["api_token"].(string); ok {
		return t
	}
	return ""
}

func normPaneID(id string) string {
	if id != "" && !strings.Contains(id, ":") {
		return id + ":main.0"
	}
	return id
}

func shortPaneID(id string) string {
	return strings.Replace(id, ":main.0", "", 1)
}

// redisGetJSON reads a key from Redis and returns parsed JSON map
func redisGetJSON(key string) map[string]interface{} {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2e9)
	if err != nil {
		return nil
	}
	defer conn.Close()
	fmt.Fprintf(conn, "*2\r\n$3\r\nGET\r\n$%d\r\n%s\r\n", len(key), key)
	reader := bufio.NewReader(conn)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "$-1" || !strings.HasPrefix(line, "$") {
		return nil
	}
	size, _ := strconv.Atoi(line[1:])
	buf := make([]byte, size+2)
	n := 0
	for n < len(buf) {
		nn, err := reader.Read(buf[n:])
		if err != nil {
			return nil
		}
		n += nn
	}
	var result map[string]interface{}
	json.Unmarshal(buf[:size], &result)
	return result
}
