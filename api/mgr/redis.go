package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// redisDo executes a raw Redis command via TCP (no client library).
func redisDo(cmds ...string) string {
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
