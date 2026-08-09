// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxPublishedPorts = 64

type publishedPort struct {
	Port       int    `json:"port"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

var publishedPortsMu sync.RWMutex
var discoveredPortsCache struct {
	sync.Mutex
	at    time.Time
	ports []int
}

func publishedPortsPath() string { return filepath.Join(cicyDBDir, "ports.json") }

func validPublishedPort(port int) bool {
	if port < 1024 || port > 65535 {
		return false
	}
	managementPort, _ := strconv.Atoi(resolvePort())
	return port != managementPort
}

func normalizePortVisibility(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "private", "public", "closed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func loadPublishedPorts() []publishedPort {
	publishedPortsMu.RLock()
	defer publishedPortsMu.RUnlock()
	data, err := os.ReadFile(publishedPortsPath())
	if err != nil {
		return []publishedPort{}
	}
	var ports []publishedPort
	if json.Unmarshal(data, &ports) != nil {
		return []publishedPort{}
	}
	out := make([]publishedPort, 0, len(ports))
	seen := map[int]bool{}
	for _, item := range ports {
		item.Name = strings.TrimSpace(item.Name)
		if len(item.Name) > 80 {
			item.Name = item.Name[:80]
		}
		item.Visibility = normalizePortVisibility(item.Visibility)
		if !validPublishedPort(item.Port) || item.Visibility == "" || seen[item.Port] {
			continue
		}
		seen[item.Port] = true
		out = append(out, item)
		if len(out) == maxPublishedPorts {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func savePublishedPorts(ports []publishedPort) error {
	publishedPortsMu.Lock()
	defer publishedPortsMu.Unlock()
	if err := os.MkdirAll(cicyDBDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ports, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cicyDBDir, ".ports.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, publishedPortsPath())
}

func portOnline(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 250*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func portMaps(ports []publishedPort) []M {
	out := make([]M, 0, len(ports))
	for _, item := range ports {
		out = append(out, M{"port": item.Port, "name": item.Name, "visibility": item.Visibility, "online": portOnline(item.Port), "detected": false})
	}
	return out
}

func listeningPortCandidates() []int {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN")
	case "windows":
		command = exec.Command("netstat", "-ano", "-p", "tcp")
	default:
		command = exec.Command("ss", "-ltnH")
	}
	output, err := command.Output()
	if err != nil && runtime.GOOS != "windows" {
		output, _ = exec.Command("netstat", "-an", "-p", "tcp").Output()
	}
	re := regexp.MustCompile(`(?:\[[0-9a-fA-F:]*\]|[0-9a-fA-F.*]+):(\d{2,5})`)
	seen := map[int]bool{}
	ports := []int{}
	for _, match := range re.FindAllStringSubmatch(string(output), -1) {
		port, _ := strconv.Atoi(match[1])
		if validPublishedPort(port) && !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

func isLoopbackHTTP(port int) bool {
	dialer := &net.Dialer{Timeout: 300 * time.Millisecond}
	client := &http.Client{
		Timeout:       500 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: dialer.DialContext, DisableKeepAlives: true},
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil || len(strings.TrimSpace(string(body))) == 0 {
		return false
	}
	contentType := strings.ToLower(resp.Header.Get("content-type"))
	lowerBody := strings.ToLower(string(body))
	isHTML := strings.Contains(contentType, "text/html") || strings.Contains(lowerBody, "<!doctype html") || strings.Contains(lowerBody, "<html")
	if !isHTML {
		return false
	}
	// Chromium/Electron debugging endpoints are HTML too, but they are not
	// user web applications and must never pollute the automatic Ports list.
	for _, marker := range []string{"devtools://", "chrome-devtools", "content shell remote debugging", "inspectable webcontents"} {
		if strings.Contains(lowerBody, marker) {
			return false
		}
	}
	return true
}

func discoverHTTPPorts() []int {
	discoveredPortsCache.Lock()
	defer discoveredPortsCache.Unlock()
	if time.Since(discoveredPortsCache.at) < 5*time.Second {
		return append([]int(nil), discoveredPortsCache.ports...)
	}
	ports := []int{}
	for _, port := range listeningPortCandidates() {
		if isLoopbackHTTP(port) {
			ports = append(ports, port)
		}
	}
	discoveredPortsCache.at = time.Now()
	discoveredPortsCache.ports = ports
	return append([]int(nil), ports...)
}

func listPortsForUI() []M {
	configured := loadPublishedPorts()
	out := portMaps(configured)
	seen := map[int]bool{}
	for _, item := range configured {
		seen[item.Port] = true
	}
	for _, port := range discoverHTTPPorts() {
		if seen[port] {
			continue
		}
		out = append(out, M{"port": port, "name": "HTTP " + strconv.Itoa(port),
			"visibility": "closed", "online": true, "detected": true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["port"].(int) < out[j]["port"].(int) })
	return out
}

func handlePorts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		J(w, M{"ports": listPortsForUI()})
	case http.MethodPost, http.MethodPatch:
		var input publishedPort
		if readBody(r, &input) != nil || !validPublishedPort(input.Port) {
			httpErr(w, 400, "port must be 1024-65535 and cannot be the cicy-code management port")
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if len(input.Name) > 80 {
			httpErr(w, 400, "name too long")
			return
		}
		input.Visibility = normalizePortVisibility(input.Visibility)
		if input.Visibility == "" {
			httpErr(w, 400, "visibility must be private, public or closed")
			return
		}
		ports := loadPublishedPorts()
		found := false
		for i := range ports {
			if ports[i].Port == input.Port {
				ports[i] = input
				found = true
				break
			}
		}
		if !found {
			if len(ports) >= maxPublishedPorts {
				httpErr(w, 400, "too many ports")
				return
			}
			ports = append(ports, input)
		}
		sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
		if err := savePublishedPorts(ports); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "port": M{"port": input.Port, "name": input.Name, "visibility": input.Visibility, "online": portOnline(input.Port)}})
	case http.MethodDelete:
		port, _ := strconv.Atoi(r.URL.Query().Get("port"))
		if !validPublishedPort(port) {
			httpErr(w, 400, "invalid port")
			return
		}
		ports := loadPublishedPorts()
		out := ports[:0]
		for _, item := range ports {
			if item.Port != port {
				out = append(out, item)
			}
		}
		if err := savePublishedPorts(out); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func publishedPortByNumber(port int) (publishedPort, error) {
	for _, item := range loadPublishedPorts() {
		if item.Port == port && item.Visibility != "closed" {
			return item, nil
		}
	}
	return publishedPort{}, errors.New("port is not published")
}

func handlePublishedPortProxy(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-cicy-instance-proxy") != "1" {
		httpErr(w, 403, "instance proxy required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/_cicy/ports/")
	parts := strings.SplitN(path, "/", 2)
	port, _ := strconv.Atoi(parts[0])
	if !validPublishedPort(port) {
		httpErr(w, 403, "invalid published port")
		return
	}
	if _, err := publishedPortByNumber(port); err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.URL.Path = "/"
		if len(parts) == 2 && parts[1] != "" {
			req.URL.Path += parts[1]
		}
		req.Host = target.Host
		for _, header := range []string{"authorization", "cookie", "x-cicy-instance-proxy", "x-cicy-forward-port"} {
			req.Header.Del(header)
		}
		req.Header.Set("x-forwarded-host", r.Host)
		req.Header.Set("x-forwarded-proto", "https")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if location := resp.Header.Get("location"); location != "" {
			if parsed, err := url.Parse(location); err == nil && parsed.IsAbs() &&
				(parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost") && parsed.Port() == strconv.Itoa(port) {
				parsed.Scheme = "https"
				parsed.Host = r.Host
				resp.Header.Set("location", parsed.String())
			}
		}
		if cookies := resp.Header.Values("set-cookie"); len(cookies) > 0 {
			resp.Header.Del("set-cookie")
			for _, value := range cookies {
				parts := strings.Split(value, ";")
				out := parts[:0]
				for _, part := range parts {
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "domain=") {
						continue
					}
					out = append(out, part)
				}
				resp.Header.Add("set-cookie", strings.Join(out, ";"))
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		httpErr(w, 502, "published port is unavailable")
	}
	proxy.ServeHTTP(w, r)
}
