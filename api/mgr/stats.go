package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type httpLogEntry struct {
	Type   string  `json:"type"`
	Pane   string  `json:"pane"`
	Method string  `json:"method"`
	URL    string  `json:"url"`
	ReqKB  float64 `json:"req_kb"`
	ResKB  float64 `json:"res_kb"`
	Status int     `json:"status"`
	TS     int64   `json:"ts"`
}

type minuteStats struct {
	Minute string  `json:"minute"`
	ReqKB  float64 `json:"req_kb"`
	ResKB  float64 `json:"res_kb"`
	Count  int     `json:"count"`
}

type uploadedAssetFile struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	IsImage     bool   `json:"is_image"`
	URL         string `json:"url"`
	Path        string `json:"path"`
	FileRef     string `json:"file_ref"`
}

func redisKey(key string) string {
	if db := os.Getenv("REDIS_DB"); db != "" && db != "0" {
		return "db" + db + ":" + key
	}
	return key
}

func redisPublish(channel, message string) {
	if !useRedis {
		pubsub.Publish(redisKey(channel), message)
		return
	}
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
	req := fmt.Sprintf("*3\r\n$7\r\nPUBLISH\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(redisKey(channel)), redisKey(channel), len(message), message)
	conn.Write([]byte(req))
}

func redisLRange(key string) []string {
	if !useRedis {
		return kv.LRange(key, -5000, -1)
	}
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

	// Limit to last 5000 entries to prevent OOM
	req := fmt.Sprintf("*4\r\n$6\r\nLRANGE\r\n$%d\r\n%s\r\n$5\r\n-5000\r\n$2\r\n-1\r\n", len(key), key)
	conn.Write([]byte(req))

	buf := make([]byte, 1024*1024)
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

func handleStatsTraffic(w http.ResponseWriter, r *http.Request) {
	minutes := 60
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil {
			minutes = v
		}
	}
	interval := 1
	if m := r.URL.Query().Get("interval"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			interval = v
		}
	}
	paneFilter := r.URL.Query().Get("pane")

	items := redisLRange(redisKey("kiro_http_log"))
	if items == nil {
		J(w, M{"success": true, "data": []minuteStats{}})
		return
	}

	cutoff := time.Now().Unix() - int64(minutes*60)
	agg := map[string]*minuteStats{}

	for _, item := range items {
		var log httpLogEntry
		if err := json.Unmarshal([]byte(item), &log); err != nil {
			continue
		}
		if log.TS < cutoff {
			continue
		}
		if paneFilter != "" && log.Pane != paneFilter {
			continue
		}
		min := time.Unix(log.TS-log.TS%int64(interval*60), 0).Format("2006-01-02T15:04")
		if agg[min] == nil {
			agg[min] = &minuteStats{Minute: min}
		}
		agg[min].ReqKB += log.ReqKB
		agg[min].ResKB += log.ResKB
		agg[min].Count++
	}

	result := []minuteStats{}
	for _, v := range agg {
		result = append(result, *v)
	}

	J(w, M{"success": true, "data": result})
}

func handleStatsTrafficRaw(w http.ResponseWriter, r *http.Request) {
	paneFilter := r.URL.Query().Get("pane")
	items := redisLRange(redisKey("kiro_http_log"))
	if items == nil {
		J(w, M{"success": true, "data": []httpLogEntry{}})
		return
	}

	result := []httpLogEntry{}
	for _, item := range items {
		var log httpLogEntry
		if err := json.Unmarshal([]byte(item), &log); err != nil {
			continue
		}
		if paneFilter != "" && log.Pane != paneFilter {
			continue
		}
		result = append(result, log)
	}

	J(w, M{"success": true, "data": result})
}

func handleTrafficLive(w http.ResponseWriter, r *http.Request) {
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

	// SUBSCRIBE kiro_traffic_live
	ch := redisKey("kiro_traffic_live")
	conn.Write([]byte(fmt.Sprintf("SUBSCRIBE %s\r\n", ch)))

	ctx := r.Context()
	buf := make([]byte, 4096)
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
		raw := string(buf[:n])
		lines := strings.Split(raw, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "{") {
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		}
	}
}

func handleNotify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pane    string `json:"pane"`
		Action  string `json:"action"`
		Tab     string `json:"tab"`
		Message string `json:"message"`
		File    string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	// open_file: use code-server IPC to open file directly
	if body.Action == "open_file" && body.File != "" {
		go openInCodeServer(body.File)
	}
	data, _ := json.Marshal(body)
	redisPublish(redisKey("kiro_notify"), string(data))
	J(w, M{"success": true})
}

func findCodeServerRemoteCLI() string {
	if bin, err := exec.LookPath("code-server"); err == nil {
		paths := []string{bin}
		if resolved, resolveErr := filepath.EvalSymlinks(bin); resolveErr == nil && resolved != "" && resolved != bin {
			paths = append(paths, resolved)
		}
		for _, current := range paths {
			binDir := filepath.Dir(current)
			candidates := []string{
				filepath.Clean(filepath.Join(binDir, "..", "lib", "vscode", "bin", "remote-cli", "code-server")),
				filepath.Clean(filepath.Join(binDir, "..", "lib", "code-server", "lib", "vscode", "bin", "remote-cli", "code-server")),
				"/usr/lib/code-server/lib/vscode/bin/remote-cli/code-linux.sh",
			}
			for _, candidate := range candidates {
				if _, statErr := os.Stat(candidate); statErr == nil {
					return candidate
				}
			}
		}
	}
	return ""
}

func openInCodeServer(file string) {
	remoteCLI := findCodeServerRemoteCLI()
	if remoteCLI == "" {
		log.Printf("[code-server] remote CLI not found")
		return
	}
	out, err := exec.Command("bash", "-lc",
		`find /tmp -name "vscode-ipc-*.sock" -type s -printf "%T@ %p\n" 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2`).Output()
	if err != nil || len(out) == 0 {
		log.Printf("[code-server] no IPC socket found")
		return
	}
	sock := strings.TrimSpace(string(out))
	target := strings.TrimSpace(file)
	if target == "" {
		return
	}
	if strings.HasPrefix(target, "file://") {
		target = strings.TrimSpace(strings.TrimPrefix(target, "file://"))
		if target != "" && !strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "~/") && !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") {
			target = "/" + strings.TrimLeft(target, "/")
		}
	}
	if matches := regexp.MustCompile(`^(.*?):(\d+):(\d+)-(\d+):(\d+)$`).FindStringSubmatch(target); len(matches) > 0 {
		target = strings.TrimSpace(matches[1]) + ":" + strings.TrimSpace(matches[2]) + ":" + strings.TrimSpace(matches[3])
	} else if matches := regexp.MustCompile(`^(.*?):(\d+)(?::(\d+))?$`).FindStringSubmatch(target); len(matches) > 0 {
		line := strings.TrimSpace(matches[2])
		column := strings.TrimSpace(matches[3])
		if column == "" {
			column = "1"
		}
		target = matches[1] + ":" + line + ":" + column
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return
	}
	if !regexp.MustCompile(`:\d+:\d+$`).MatchString(target) {
		target += ":1:1"
	}
	cmd := exec.Command(remoteCLI, "--reuse-window", "--goto", target)
	cmd.Env = append(os.Environ(), "VSCODE_IPC_HOOK_CLI="+sock)
	if err := cmd.Run(); err != nil {
		log.Printf("[code-server] open file error: %v", err)
	}
}

func handleNotifyStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	filterPane := r.URL.Query().Get("pane")
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
	conn.Write([]byte("SUBSCRIBE kiro_notify\r\n"))

	ctx := r.Context()
	buf := make([]byte, 4096)
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
				// Filter by pane if specified
				if filterPane != "" {
					var msg struct {
						Pane string `json:"pane"`
					}
					json.Unmarshal([]byte(line), &msg)
					if msg.Pane != "" && msg.Pane != filterPane {
						continue
					}
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		}
	}
}

func paneWorkspace(pane string) string {
	pane = normPaneID(strings.TrimSpace(pane))
	if pane == "" {
		return ""
	}
	var ws string
	store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", pane).Scan(&ws)
	if ws == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return runtimePathToHostPath(os.ExpandEnv(strings.Replace(ws, "~", home, 1)))
}

func sanitizeAssetFileName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = path.Base(name)
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		allowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
		if allowed {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	cleaned := strings.Trim(b.String(), "._-")
	if cleaned == "" {
		return "file"
	}
	return cleaned
}

func detectAssetContentType(headerValue string, fileName string, head []byte) string {
	contentType := strings.TrimSpace(headerValue)
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	if contentType == "" || contentType == "application/octet-stream" {
		if extType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))); extType != "" {
			contentType = extType
		}
	}
	if (contentType == "" || contentType == "application/octet-stream") && len(head) > 0 {
		contentType = http.DetectContentType(head)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}

func randomAssetID(byteLen int) string {
	if byteLen <= 0 {
		byteLen = 8
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func buildAssetFileURL(r *http.Request, pane string, relPath string) string {
	parts := []string{url.PathEscape(shortPaneID(normPaneID(pane)))}
	for _, segment := range strings.Split(relPath, "/") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		parts = append(parts, url.PathEscape(segment))
	}
	assetURL := "/assets/files/" + strings.Join(parts, "/")
	token := strings.TrimSpace(getToken(r))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token != "" {
		assetURL += "?token=" + url.QueryEscape(token)
	}
	return assetURL
}

func resolveAssetDiskPath(root string, relPath string) (string, bool) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || strings.Contains(relPath, "\\") || strings.Contains(relPath, "\x00") {
		return "", false
	}
	cleanRel := strings.TrimPrefix(path.Clean("/"+relPath), "/")
	if cleanRel == "." || cleanRel == "" || cleanRel != relPath {
		return "", false
	}
	cleanRoot := filepath.Clean(root)
	fullPath := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(cleanRel)))
	if fullPath != cleanRoot && !strings.HasPrefix(fullPath, cleanRoot+string(os.PathSeparator)) {
		return "", false
	}
	return fullPath, true
}

func assetDownloadName(filePath string) string {
	name := filepath.Base(filePath)
	if parts := strings.SplitN(name, "__", 2); len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		return parts[1]
	}
	return name
}

func imageDimensionsFromFile(filePath string) (int, int, bool) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	cfg, _, err := image.DecodeConfig(file)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func decorateImageAssetFileName(fileName string, size int64, filePath string) string {
	width, height, ok := imageDimensionsFromFile(filePath)
	if !ok {
		return fileName
	}
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	if base == "" {
		base = "image"
	}
	return fmt.Sprintf("%s_%d_%d_%d%s", base, width, height, size, ext)
}

func handleAssetFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	pane := normPaneID(r.URL.Query().Get("pane"))
	if pane == "" {
		httpErr(w, 400, "pane required")
		return
	}
	workspace := paneWorkspace(pane)
	if workspace == "" {
		httpErr(w, 404, "pane not found")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httpErr(w, 400, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpErr(w, 400, "file required")
		return
	}
	defer file.Close()

	fileName := sanitizeAssetFileName(header.Filename)
	head := make([]byte, 512)
	headN, readErr := file.Read(head)
	if readErr != nil && readErr != io.EOF {
		httpErr(w, 400, "failed to read uploaded file")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		httpErr(w, 500, "failed to reset uploaded file")
		return
	}
	contentType := detectAssetContentType(header.Header.Get("Content-Type"), fileName, head[:headN])
	now := time.Now()
	relDir := path.Join(now.Format("2006"), now.Format("01"), now.Format("02"))
	relPath := path.Join(relDir, randomAssetID(8)+"__"+fileName)
	assetsRoot := workspaceAssetsFilesDir(workspace)
	fullPath, ok := resolveAssetDiskPath(assetsRoot, relPath)
	if !ok {
		httpErr(w, 400, "bad asset path")
		return
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		httpErr(w, 500, "failed to create asset directory")
		return
	}
	dst, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		httpErr(w, 500, "failed to create asset file")
		return
	}
	size, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(fullPath)
		httpErr(w, 500, "failed to save asset file")
		return
	}
	if closeErr != nil {
		_ = os.Remove(fullPath)
		httpErr(w, 500, "failed to finalize asset file")
		return
	}
	if strings.HasPrefix(contentType, "image/") {
		decoratedFileName := sanitizeAssetFileName(decorateImageAssetFileName(fileName, size, fullPath))
		if decoratedFileName != "" && decoratedFileName != fileName {
			if parts := strings.SplitN(filepath.Base(relPath), "__", 2); len(parts) == 2 {
				nextRelPath := path.Join(relDir, parts[0]+"__"+decoratedFileName)
				nextFullPath, nextOK := resolveAssetDiskPath(assetsRoot, nextRelPath)
				if nextOK {
					if err := os.Rename(fullPath, nextFullPath); err == nil {
						fileName = decoratedFileName
						relPath = nextRelPath
						fullPath = nextFullPath
					}
				}
			}
		}
	}
	asset := uploadedAssetFile{
		Name:        fileName,
		Size:        size,
		ContentType: contentType,
		IsImage:     strings.HasPrefix(contentType, "image/"),
		URL:         buildAssetFileURL(r, pane, relPath),
		Path:        relPath,
		FileRef:     hostPathToFileRef(fullPath),
	}
	J(w, M{"ok": true, "file": asset})
}

func handleAssetFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, 405, "method not allowed")
		return
	}
	rawPath := strings.TrimPrefix(r.URL.Path, "/assets/files/")
	parts := strings.SplitN(rawPath, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	pane := normPaneID(parts[0])
	workspace := paneWorkspace(pane)
	if workspace == "" {
		httpErr(w, 404, "pane not found")
		return
	}
	assetsRoot := workspaceAssetsFilesDir(workspace)
	fullPath, ok := resolveAssetDiskPath(assetsRoot, parts[1])
	if !ok {
		httpErr(w, 400, "bad asset path")
		return
	}
	if info, err := os.Stat(fullPath); err != nil || info.IsDir() {
		legacyRoot := workspaceLegacyAssetsFilesDir(workspace)
		legacyPath, legacyOK := resolveAssetDiskPath(legacyRoot, parts[1])
		if !legacyOK {
			http.NotFound(w, r)
			return
		}
		if legacyInfo, legacyErr := os.Stat(legacyPath); legacyErr != nil || legacyInfo.IsDir() {
			http.NotFound(w, r)
			return
		}
		fullPath = legacyPath
	}
	contentType := detectAssetContentType("", fullPath, nil)
	if contentType == "application/octet-stream" {
		if f, err := os.Open(fullPath); err == nil {
			defer f.Close()
			info, statErr := f.Stat()
			if statErr != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			head := make([]byte, 512)
			n, _ := f.Read(head)
			contentType = detectAssetContentType("", fullPath, head[:n])
		}
	} else if info, err := os.Stat(fullPath); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	disposition := "attachment"
	if strings.HasPrefix(contentType, "image/") {
		disposition = "inline"
	}
	headerValue := mime.FormatMediaType(disposition, map[string]string{"filename": assetDownloadName(fullPath)})
	if headerValue != "" {
		w.Header().Set("Content-Disposition", headerValue)
	}
	http.ServeFile(w, r, fullPath)
}

func handleCicyFiles(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	ws := paneWorkspace(pane)
	if ws == "" {
		J(w, M{"files": []string{}})
		return
	}
	dir := filepath.Join(ws, ".cicy")
	entries, err := os.ReadDir(dir)
	if err != nil {
		J(w, M{"files": []string{}})
		return
	}
	files := []M{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		files = append(files, M{"name": e.Name(), "size": info.Size(), "modified": info.ModTime()})
	}
	J(w, M{"files": files, "path": dir})
}

func handleCicyFile(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	name := r.URL.Query().Get("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "bad name", 400)
		return
	}
	ws := paneWorkspace(pane)
	if ws == "" {
		http.Error(w, "pane not found", 404)
		return
	}
	f, err := os.Open(filepath.Join(ws, ".cicy", name))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.Copy(w, f)
}

func handlePair(w http.ResponseWriter, r *http.Request) {
	pane := normPaneID(r.URL.Query().Get("pane"))
	if pane == "" {
		httpErr(w, 400, "pane required")
		return
	}
	var ws sql.NullString
	var myRole sql.NullString
	store.QueryRow("SELECT workspace, role FROM agent_config WHERE pane_id=?", pane).Scan(&ws, &myRole)
	if ws.String == "" {
		httpErr(w, 404, "pane not found")
		return
	}
	rows, err := store.Query(`SELECT pane_id, title, role, default_model FROM agent_config WHERE workspace=? AND active=1 AND role IS NOT NULL AND role!=''`, ws.String)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var master, worker M
	for rows.Next() {
		var pid, title, role, model sql.NullString
		rows.Scan(&pid, &title, &role, &model)
		info := M{"pane_id": shortPaneID(pid.String), "title": title.String, "role": role.String, "default_model": model.String}
		if role.String == "master" {
			master = info
		} else if role.String == "worker" {
			worker = info
		}
	}
	J(w, M{"master": master, "worker": worker})
}
