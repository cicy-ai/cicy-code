// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func configureCicyUpdateHandlerTest(t *testing.T) {
	t.Helper()
	origContainerRuntime := cicyUpdateIsContainerRuntime
	origGOOS := cicyUpdateGOOS
	origGOARCH := cicyUpdateGOARCH
	origHomeDir := cicyUpdateUserHomeDir
	origLatest := cicyUpdateLatestVersion
	origInstalled := cicyUpdateInstalledMacVersion
	origInstall := cicyUpdateInstallMacLocalBin
	t.Cleanup(func() {
		cicyUpdateIsContainerRuntime = origContainerRuntime
		cicyUpdateGOOS = origGOOS
		cicyUpdateGOARCH = origGOARCH
		cicyUpdateUserHomeDir = origHomeDir
		cicyUpdateLatestVersion = origLatest
		cicyUpdateInstalledMacVersion = origInstalled
		cicyUpdateInstallMacLocalBin = origInstall
	})
}

func TestHandleCicyUpdateStatusUsesStagedMacVersion(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	cicyUpdateInstalledMacVersion = func() string { return "99.0.0" }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/cicy-update", nil)
	handleCicyUpdateStatus(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["has_update"] != false || body["restart_required"] != true {
		t.Fatalf("unexpected staged update status: %#v", body)
	}
	if body["installed"] != "99.0.0" || body["current"] != version {
		t.Fatalf("unexpected staged versions: %#v", body)
	}
}

func decodeCicyUpdateResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return body
}

func TestHandleCicyUpdateApplyStagesMacLocalBinWithoutRestart(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	homeDir := t.TempDir()
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateGOARCH = "amd64"
	cicyUpdateUserHomeDir = func() (string, error) { return homeDir, nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	installCalls := 0
	cicyUpdateInstallMacLocalBin = func(_ context.Context, opts macLocalBinUpdateOptions) error {
		installCalls++
		if opts.Version != "99.0.0" || opts.GOARCH != "amd64" {
			t.Fatalf("unexpected installer options: %#v", opts)
		}
		if opts.BinDir != filepath.Join(homeDir, ".local", "bin") {
			t.Fatalf("bin dir = %q", opts.BinDir)
		}
		return nil
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil)
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if installCalls != 1 {
		t.Fatalf("installer calls = %d, want 1", installCalls)
	}
	if body["started"] != true || body["completed"] != true || body["restart_required"] != true {
		t.Fatalf("unexpected macOS update response: %#v", body)
	}
	if body["target"] != "99.0.0" || body["current"] != version {
		t.Fatalf("unexpected versions in response: %#v", body)
	}
}

func TestHandleCicyUpdateApplyReturnsMacInstallerError(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateGOARCH = "arm64"
	cicyUpdateUserHomeDir = func() (string, error) { return t.TempDir(), nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	cicyUpdateInstallMacLocalBin = func(context.Context, macLocalBinUpdateOptions) error {
		return errors.New("checksum failed")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil)
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["started"] != false || body["error"] != "failed to install update: checksum failed" {
		t.Fatalf("unexpected installer failure response: %#v", body)
	}
}

func TestHandleCicyUpdateApplyRejectsUnsupportedHost(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "linux"
	cicyUpdateLatestVersion = func() string { return "99.0.0" }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil)
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["started"] != false || body["error"] != "in-place update is only available in the container runtime or macOS local-bin installation" {
		t.Fatalf("unexpected unsupported-host response: %#v", body)
	}
}
