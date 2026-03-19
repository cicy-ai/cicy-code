package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var useRedis bool

func initRedis() {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		useRedis = true
	}
}

// redisDo executes a raw Redis command via TCP (no client library).
// Falls back to KV store if Redis unavailable.
func redisDo(cmds ...string) string {
	if !useRedis {
		return kvDo(cmds...)
	}
	return redisDoRaw(cmds...)
}

func kvDo(cmds ...string) string {
	if len(cmds) == 0 {
		return ""
	}
	cmd := strings.ToUpper(cmds[0])
	switch cmd {
	case "GET":
		if len(cmds) >= 2 {
			return kv.Get(cmds[1])
		}
	case "SET":
		if len(cmds) >= 3 {
			kv.Set(cmds[1], cmds[2])
			return "OK"
		}
	case "DEL":
		if len(cmds) >= 2 {
			kv.Del(cmds[1])
			return "OK"
		}
	case "LPUSH":
		if len(cmds) >= 3 {
			kv.LPush(cmds[1], cmds[2])
			return "OK"
		}
	case "RPOP":
		if len(cmds) >= 2 {
			return kv.RPop(cmds[1])
		}
	case "LRANGE":
		if len(cmds) >= 4 {
			start, _ := strconv.Atoi(cmds[2])
			stop, _ := strconv.Atoi(cmds[3])
			list := kv.LRange(cmds[1], start, stop)
			return strings.Join(list, "\n")
		}
	}
	return ""
}

func redisDoRaw(cmds ...string) string {
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
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := fmt.Sprintf("*%d\r\n", len(cmds))
	for _, c := range cmds {
		req += fmt.Sprintf("$%d\r\n%s\r\n", len(c), c)
	}
	conn.Write([]byte(req))

	var all []byte
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			all = append(all, buf[:n]...)
		}
		resp := string(all)
		if strings.HasPrefix(resp, "$") {
			idx := strings.Index(resp, "\r\n")
			if idx >= 0 {
				sz, _ := strconv.Atoi(resp[1:idx])
				if sz < 0 {
					return ""
				}
				need := idx + 2 + sz + 2
				if len(all) >= need {
					return resp[idx+2 : idx+2+sz]
				}
			}
		} else if strings.HasPrefix(resp, "+") || strings.HasPrefix(resp, "-") {
			if strings.Contains(resp, "\r\n") {
				idx := strings.Index(resp, "\r\n")
				return resp[1:idx]
			}
		}
		if err != nil {
			break
		}
	}
	return string(all)
}
