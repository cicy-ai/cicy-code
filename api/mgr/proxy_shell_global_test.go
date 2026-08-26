package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleProxyShellGlobalSetsReportsAndCancelsManagedProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range proxyShellEnvKeys {
		t.Setenv(key, "")
	}

	call := func(method, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "/api/proxy/shell-global", strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		handleProxyShellGlobal(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", method, rr.Code, rr.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return response
	}

	initial := call(http.MethodGet, "")
	if initial["enabled"] != false || initial["path"] != filepath.Join(home, ".bashrc") {
		t.Fatalf("initial response=%v", initial)
	}
	enabled := call(http.MethodPatch, `{"enabled":true}`)
	if enabled["enabled"] != true || enabled["changed"] != true || enabled["proxy_url"] != "http://127.0.0.1:9001" {
		t.Fatalf("enable response=%v", enabled)
	}
	if status := call(http.MethodGet, ""); status["enabled"] != true {
		t.Fatalf("status after enable=%v", status)
	}
	disabled := call(http.MethodPatch, `{"enabled":false}`)
	if disabled["enabled"] != false || disabled["changed"] != true {
		t.Fatalf("disable response=%v", disabled)
	}
}

func TestSetProxyBashrcIsIdempotentAndPreservesExistingShellConfig(t *testing.T) {
	home := t.TempDir()
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("export KEEP_ME=yes\nexport NO_PROXY=example.com\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	const proxyURL = "http://127.0.0.1:9001"
	for i := 0; i < 2; i++ {
		changed, err := setProxyBashrc(bashrc, proxyURL, true)
		if err != nil {
			t.Fatalf("setProxyBashrc(enable) attempt %d: %v", i+1, err)
		}
		if changed != (i == 0) {
			t.Fatalf("attempt %d changed=%t, want %t", i+1, changed, i == 0)
		}
	}

	data, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), proxyBashrcStartMarker); got != 1 {
		t.Fatalf("managed block count=%d, want 1\n%s", got, data)
	}
	if info, err := os.Stat(bashrc); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("bashrc mode=%v err=%v, want 0640", info.Mode().Perm(), err)
	}

	values := sourceBashrcEnv(t, bashrc, nil)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if values[key] != proxyURL {
			t.Errorf("%s=%q, want %q", key, values[key], proxyURL)
		}
	}
	if values["KEEP_ME"] != "yes" {
		t.Errorf("KEEP_ME=%q, want yes", values["KEEP_ME"])
	}
	for _, want := range []string{"example.com", "localhost", "127.0.0.1", "::1"} {
		if !commaListContains(values["NO_PROXY"], want) || !commaListContains(values["no_proxy"], want) {
			t.Errorf("loopback exclusion %q missing: NO_PROXY=%q no_proxy=%q", want, values["NO_PROXY"], values["no_proxy"])
		}
	}
}

func TestSetProxyBashrcDisableRemovesOnlyManagedBlock(t *testing.T) {
	home := t.TempDir()
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("export KEEP_ME=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setProxyBashrc(bashrc, "http://127.0.0.1:9001", true); err != nil {
		t.Fatal(err)
	}
	changed, err := setProxyBashrc(bashrc, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("disable reported no change")
	}
	data, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), proxyBashrcStartMarker) || strings.Contains(string(data), "HTTP_PROXY") {
		t.Fatalf("managed proxy block still present:\n%s", data)
	}
	if !strings.Contains(string(data), "export KEEP_ME=yes") {
		t.Fatalf("user config was removed:\n%s", data)
	}
	if changed, err := setProxyBashrc(bashrc, "", false); err != nil || changed {
		t.Fatalf("second disable changed=%t err=%v, want false nil", changed, err)
	}
}

func TestApplyProxyShellRuntimeUpdatesProcessTmuxAndOnlyIdleBashPanes(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	tmuxPath := filepath.Join(binDir, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CICY_TEST_TMUX_LOG"
if [ "$1" = "list-panes" ]; then
  printf '%%1\tbash\n%%2\tcodex\n%%3\tzsh\n%%4\tbash\n'
fi
if [ "$1" = "capture-pane" ]; then
  if [ "$3" = "%1" ]; then
    printf 'w-1001 (main) $ '
  else
    printf 'long-running bash script\n'
  fi
fi
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CICY_TEST_TMUX_LOG", logPath)
	for _, key := range proxyShellEnvKeys {
		t.Setenv(key, "before")
	}

	const proxyURL = "http://127.0.0.1:9001"
	if err := applyProxyShellRuntime(true, proxyURL); err != nil {
		t.Fatalf("enable runtime proxy: %v", err)
	}
	for _, key := range proxyShellProxyEnvKeys {
		if got := os.Getenv(key); got != proxyURL {
			t.Errorf("%s=%q, want %q", key, got, proxyURL)
		}
	}
	for _, key := range proxyShellNoProxyEnvKeys {
		if got := os.Getenv(key); !commaListContains(got, "localhost") || !commaListContains(got, "127.0.0.1") || !commaListContains(got, "::1") {
			t.Errorf("%s=%q missing loopback exclusions", key, got)
		}
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "set-environment -g HTTP_PROXY "+proxyURL) {
		t.Fatalf("tmux global proxy was not updated:\n%s", logText)
	}
	if !strings.Contains(logText, "send-keys -t %1 -l -- source ~/.bashrc") || !strings.Contains(logText, "send-keys -t %1 Enter") {
		t.Fatalf("idle Bash pane was not sourced:\n%s", logText)
	}
	if strings.Contains(logText, "send-keys -t %2") || strings.Contains(logText, "send-keys -t %3") || strings.Contains(logText, "send-keys -t %4") {
		t.Fatalf("non-idle panes received injected input:\n%s", logText)
	}

	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyProxyShellRuntime(false, ""); err != nil {
		t.Fatalf("disable runtime proxy: %v", err)
	}
	for _, key := range proxyShellEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("%s still set after disable", key)
		}
	}
	logData, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText = string(logData)
	if !strings.Contains(logText, "set-environment -gu HTTP_PROXY") {
		t.Fatalf("tmux global proxy was not removed:\n%s", logText)
	}
	if !strings.Contains(logText, "send-keys -t %1 -l -- unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy; source ~/.bashrc") {
		t.Fatalf("idle Bash pane did not clear and reload proxy state:\n%s", logText)
	}
}

func sourceBashrcEnv(t *testing.T, bashrc string, extraEnv []string) map[string]string {
	t.Helper()
	cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1"; env`, "bash", bashrc)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, extraEnv...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("source bashrc: %v", err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func commaListContains(value, want string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}
