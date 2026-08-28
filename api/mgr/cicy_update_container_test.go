package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configureContainerCicyUpdateTest(t *testing.T) (calls *[]string) {
	t.Helper()
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return true }
	origPath := cicyUpdateContainerUpdaterPath
	origLaunch := cicyUpdateLaunchContainerUpdater
	origVersion := cicyUpdateCurrentVersion
	cicyUpdateContainerMarkStarted("", time.Time{})
	t.Cleanup(func() {
		cicyUpdateContainerUpdaterPath = origPath
		cicyUpdateLaunchContainerUpdater = origLaunch
		cicyUpdateCurrentVersion = origVersion
		cicyUpdateContainerMarkStarted("", time.Time{})
	})
	updater := filepath.Join(t.TempDir(), "cicy-code-update.sh")
	if err := os.WriteFile(updater, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cicyUpdateContainerUpdaterPath = updater
	cicyUpdateCurrentVersion = func() string { return "2.3.571" }
	recorded := []string{}
	cicyUpdateLaunchContainerUpdater = func(target, registry string) error {
		recorded = append(recorded, target+"|"+registry)
		return nil
	}
	return &recorded
}

// cicy-desktop 在宿主机解析好版本后 POST {target, registry}:容器里绝不能再跑
// npm view(那就是以前"点更新卡 2 分钟"的根因),直接用 pinned 版本拉起更新脚本。
func TestContainerCicyUpdateApplyUsesPinnedTargetWithoutResolving(t *testing.T) {
	calls := configureContainerCicyUpdateTest(t)
	cicyUpdateLatestVersion = func() string { t.Fatal("latest must not be resolved when target is pinned"); return "" }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", strings.NewReader(`{"target":"2.3.573","registry":"https://registry.npmmirror.com"}`))
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)
	if body["started"] != true || body["target"] != "2.3.573" {
		t.Fatalf("unexpected response: %#v", body)
	}
	if len(*calls) != 1 || (*calls)[0] != "2.3.573|https://registry.npmmirror.com" {
		t.Fatalf("updater launch = %v", *calls)
	}
}

func TestContainerCicyUpdateApplyRejectsBadTargetAndNoOpsWhenCurrent(t *testing.T) {
	calls := configureContainerCicyUpdateTest(t)
	cicyUpdateLatestVersion = func() string { return "2.3.571" }

	rec := httptest.NewRecorder()
	handleCicyUpdateApply(rec, httptest.NewRequest(http.MethodPost, "/api/cicy-update", strings.NewReader(`{"target":"latest; rm -rf /"}`)))
	if body := decodeCicyUpdateResponse(t, rec); body["started"] != false || !strings.HasPrefix(body["error"].(string), "invalid target version") {
		t.Fatalf("bad target accepted: %#v", body)
	}
	rec = httptest.NewRecorder()
	handleCicyUpdateApply(rec, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
	if body := decodeCicyUpdateResponse(t, rec); body["started"] != false || body["error"] != "already up to date" {
		t.Fatalf("expected already up to date: %#v", body)
	}
	if len(*calls) != 0 {
		t.Fatalf("updater must not launch: %v", *calls)
	}
}

// 网页按钮 / desktop 可能重复 POST(用户没看到反馈就再点):第二次不能再拉起一个
// npm install 和第一个抢同一个目录,而是回报"正在进行中"。
func TestContainerCicyUpdateApplyIsSingleFlight(t *testing.T) {
	calls := configureContainerCicyUpdateTest(t)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		handleCicyUpdateApply(rec, httptest.NewRequest(http.MethodPost, "/api/cicy-update", strings.NewReader(`{"target":"2.3.580"}`)))
		body := decodeCicyUpdateResponse(t, rec)
		if body["started"] != true || body["target"] != "2.3.580" {
			t.Fatalf("click %d: %#v", i, body)
		}
		if i > 0 && body["in_progress"] != true {
			t.Fatalf("click %d should report in_progress: %#v", i, body)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("updater launched %d times, want 1", len(*calls))
	}
}
