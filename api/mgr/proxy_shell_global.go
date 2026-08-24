package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	proxyBashrcStartMarker = "# >>> cicy-code mihomo proxy >>>"
	proxyBashrcEndMarker   = "# <<< cicy-code mihomo proxy <<<"
)

var (
	proxyShellProxyEnvKeys   = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}
	proxyShellNoProxyEnvKeys = []string{"NO_PROXY", "no_proxy"}
	proxyShellEnvKeys        = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"}
)

func handleProxyShellGlobal(w http.ResponseWriter, r *http.Request) {
	path, err := proxyBashrcPath()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	proxyURL := proxyShellURL()

	switch r.Method {
	case http.MethodGet:
		enabled, err := proxyBashrcEnabled(path)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "enabled": enabled, "path": path, "proxy_url": proxyURL})
	case http.MethodPatch:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
			return
		}
		if body.Enabled == nil {
			httpErr(w, http.StatusBadRequest, "enabled required")
			return
		}
		changed, err := setProxyBashrc(path, proxyURL, *body.Enabled)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := applyProxyShellRuntime(*body.Enabled, proxyURL); err != nil {
			httpErr(w, http.StatusInternalServerError, "apply proxy runtime: "+err.Error())
			return
		}
		J(w, M{
			"success":   true,
			"enabled":   *body.Enabled,
			"changed":   changed,
			"immediate": true,
			"path":      path,
			"proxy_url": proxyURL,
		})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func proxyBashrcPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		if err == nil {
			err = errors.New("home directory is empty")
		}
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".bashrc"), nil
}

func proxyShellURL() string {
	port := defaultMihomoMixedPort
	if value := strings.TrimSpace(os.Getenv("CICY_MIHOMO_PORT")); value != "" {
		port = value
	}
	return "http://127.0.0.1:" + port
}

func proxyBashrcEnabled(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text := string(data)
	start := strings.Contains(text, proxyBashrcStartMarker)
	end := strings.Contains(text, proxyBashrcEndMarker)
	if start != end {
		return false, errors.New("incomplete cicy-code proxy block in .bashrc")
	}
	return start, nil
}

func setProxyBashrc(path, proxyURL string, enabled bool) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("bashrc path is empty")
	}
	if enabled && strings.TrimSpace(proxyURL) == "" {
		return false, errors.New("proxy URL is empty")
	}

	original, err := os.ReadFile(path)
	mode := os.FileMode(0o644)
	if err == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	} else {
		original = nil
	}

	clean, err := removeProxyBashrcBlock(string(original))
	if err != nil {
		return false, err
	}
	next := strings.TrimRight(clean, "\r\n")
	if enabled {
		if next != "" {
			next += "\n\n"
		}
		next += proxyBashrcBlock(proxyURL)
	}
	if next != "" {
		next += "\n"
	}
	if string(original) == next {
		return false, nil
	}
	if err := writeProxyBashrcAtomic(path, []byte(next), mode); err != nil {
		return false, err
	}
	return true, nil
}

func removeProxyBashrcBlock(text string) (string, error) {
	for {
		start := strings.Index(text, proxyBashrcStartMarker)
		if start < 0 {
			if strings.Contains(text, proxyBashrcEndMarker) {
				return "", errors.New("incomplete cicy-code proxy block in .bashrc")
			}
			return text, nil
		}
		rest := text[start+len(proxyBashrcStartMarker):]
		endOffset := strings.Index(rest, proxyBashrcEndMarker)
		if endOffset < 0 {
			return "", errors.New("incomplete cicy-code proxy block in .bashrc")
		}
		end := start + len(proxyBashrcStartMarker) + endOffset + len(proxyBashrcEndMarker)
		if end < len(text) && text[end] == '\r' {
			end++
		}
		if end < len(text) && text[end] == '\n' {
			end++
		}
		text = text[:start] + text[end:]
	}
}

func proxyBashrcBlock(proxyURL string) string {
	quotedURL := fmt.Sprintf("%q", proxyURL)
	lines := []string{proxyBashrcStartMarker}
	for _, key := range proxyShellProxyEnvKeys {
		lines = append(lines, fmt.Sprintf("export %s=%s", key, quotedURL))
	}
	lines = append(lines,
		`_cicy_no_proxy="${NO_PROXY:-${no_proxy:-}}"`,
		`for _cicy_loopback in localhost 127.0.0.1 ::1; do`,
		`  case ",${_cicy_no_proxy}," in`,
		`    *,"${_cicy_loopback}",*) ;;`,
		`    *) _cicy_no_proxy="${_cicy_no_proxy:+${_cicy_no_proxy},}${_cicy_loopback}" ;;`,
		`  esac`,
		`done`,
		`export NO_PROXY="${_cicy_no_proxy}"`,
		`export no_proxy="${_cicy_no_proxy}"`,
		`unset _cicy_no_proxy _cicy_loopback`,
		proxyBashrcEndMarker,
	)
	return strings.Join(lines, "\n")
}

func writeProxyBashrcAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create bashrc directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".bashrc.cicy-*")
	if err != nil {
		return fmt.Errorf("stage bashrc: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set bashrc permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write bashrc: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync bashrc: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close bashrc: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace bashrc: %w", err)
	}
	return nil
}

func mergeProxyLoopbacks(value string) string {
	items := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	for _, item := range []string{"localhost", "127.0.0.1", "::1"} {
		if !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	return strings.Join(items, ",")
}

func applyProxyShellRuntime(enabled bool, proxyURL string) error {
	if enabled {
		for _, key := range proxyShellProxyEnvKeys {
			if err := os.Setenv(key, proxyURL); err != nil {
				return err
			}
			_, _ = runTmux("set-environment", "-g", key, proxyURL)
		}
		for _, key := range proxyShellNoProxyEnvKeys {
			value := mergeProxyLoopbacks(os.Getenv(key))
			if err := os.Setenv(key, value); err != nil {
				return err
			}
			_, _ = runTmux("set-environment", "-g", key, value)
		}
	} else {
		for _, key := range proxyShellEnvKeys {
			if err := os.Unsetenv(key); err != nil {
				return err
			}
			_, _ = runTmux("set-environment", "-gu", key)
		}
	}

	paneList, err := runTmux("list-panes", "-a", "-F", "#{pane_id}\t#{pane_current_command}")
	if err != nil {
		return nil
	}
	command := "source ~/.bashrc"
	if !enabled {
		command = "unset " + strings.Join(proxyShellEnvKeys, " ") + "; source ~/.bashrc"
	}
	for _, line := range strings.Split(paneList, "\n") {
		paneID, currentCommand, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimPrefix(filepath.Base(strings.TrimSpace(currentCommand)), "-") != "bash" {
			continue
		}
		paneID = strings.TrimSpace(paneID)
		if paneID == "" {
			continue
		}
		capture, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-8")
		if err != nil || !isShellPromptVisible(capture) {
			continue
		}
		if _, err := runTmux("send-keys", "-t", paneID, "-l", "--", command); err != nil {
			continue
		}
		_, _ = runTmux("send-keys", "-t", paneID, "Enter")
	}
	return nil
}
