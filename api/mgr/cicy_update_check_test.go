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
	"sync/atomic"
	"testing"
	"time"
)

func configureCicyUpdateHandlerTest(t *testing.T) {
	t.Helper()
	origCoordinator := cicyUpdateLocalBinCoordinator
	cicyUpdateLocalBinCoordinator = &localBinUpdateCoordinator{}
	origContainerRuntime := cicyUpdateIsContainerRuntime
	origGOOS := cicyUpdateGOOS
	origGOARCH := cicyUpdateGOARCH
	origHomeDir := cicyUpdateUserHomeDir
	origLatest := cicyUpdateLatestVersion
	origInstalled := cicyUpdateInstalledLocalBinVersion
	origInstall := cicyUpdateInstallLocalBin
	origProcessArgs := cicyUpdateProcessArgs
	origScheduleRestart := cicyUpdateScheduleLocalBinRestart
	t.Cleanup(func() {
		cicyUpdateLocalBinCoordinator = origCoordinator
		cicyUpdateIsContainerRuntime = origContainerRuntime
		cicyUpdateGOOS = origGOOS
		cicyUpdateGOARCH = origGOARCH
		cicyUpdateUserHomeDir = origHomeDir
		cicyUpdateLatestVersion = origLatest
		cicyUpdateInstalledLocalBinVersion = origInstalled
		cicyUpdateInstallLocalBin = origInstall
		cicyUpdateProcessArgs = origProcessArgs
		cicyUpdateScheduleLocalBinRestart = origScheduleRestart
	})
}

func TestHandleCicyUpdateStatusKeepsUpdateAvailableUntilStagedMacVersionIsRunning(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	cicyUpdateInstalledLocalBinVersion = func() string { return "99.0.0" }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/cicy-update", nil)
	handleCicyUpdateStatus(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["has_update"] != true || body["restart_required"] != true {
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

func TestHandleCicyUpdateApplyStagesMacLocalBinForRestart(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	homeDir := t.TempDir()
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateGOARCH = "amd64"
	cicyUpdateUserHomeDir = func() (string, error) { return homeDir, nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	cicyUpdateProcessArgs = func() []string { return []string{"/old/cicy-code", "--port", "8008"} }
	installCalls := 0
	restartExecutable := ""
	var restartArgs []string
	cicyUpdateScheduleLocalBinRestart = func(executable string, args []string) {
		restartExecutable = executable
		restartArgs = append([]string(nil), args...)
	}
	cicyUpdateInstallLocalBin = func(_ context.Context, opts localBinUpdateOptions) error {
		installCalls++
		if opts.Version != "99.0.0" || opts.GOOS != "darwin" || opts.GOARCH != "amd64" {
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
	if body["started"] != true || body["restarting"] != true {
		t.Fatalf("unexpected macOS update response: %#v", body)
	}
	if _, ok := body["restart_required"]; ok {
		t.Fatalf("macOS update must restart automatically: %#v", body)
	}
	if body["target"] != "99.0.0" || body["current"] != version {
		t.Fatalf("unexpected versions in response: %#v", body)
	}
	wantExecutable := filepath.Join(homeDir, ".local", "bin", "cicy-code")
	if restartExecutable != wantExecutable {
		t.Fatalf("restart executable = %q, want %q", restartExecutable, wantExecutable)
	}
	if len(restartArgs) != 3 || restartArgs[0] != wantExecutable || restartArgs[1] != "--port" || restartArgs[2] != "8008" {
		t.Fatalf("restart args = %#v", restartArgs)
	}
}

func TestHandleCicyUpdateApplyReturnsMacInstallerError(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "darwin"
	cicyUpdateGOARCH = "arm64"
	cicyUpdateUserHomeDir = func() (string, error) { return t.TempDir(), nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	restartScheduled := false
	cicyUpdateScheduleLocalBinRestart = func(string, []string) { restartScheduled = true }
	cicyUpdateInstallLocalBin = func(context.Context, localBinUpdateOptions) error {
		return errors.New("checksum failed")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil)
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["started"] != false || body["error"] != "failed to install update: checksum failed" {
		t.Fatalf("unexpected installer failure response: %#v", body)
	}
	if restartScheduled {
		t.Fatal("restart must not be scheduled after an installer failure")
	}
}

func TestHandleCicyUpdateApplyAllowsRetryAfterLinuxInstallerFailure(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "linux"
	cicyUpdateGOARCH = "amd64"
	cicyUpdateUserHomeDir = func() (string, error) { return t.TempDir(), nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	installCalls := 0
	cicyUpdateInstallLocalBin = func(context.Context, localBinUpdateOptions) error {
		installCalls++
		if installCalls == 1 {
			return errors.New("temporary download failure")
		}
		return nil
	}
	restartCalls := 0
	cicyUpdateScheduleLocalBinRestart = func(string, []string) { restartCalls++ }

	firstRecorder := httptest.NewRecorder()
	handleCicyUpdateApply(firstRecorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
	firstBody := decodeCicyUpdateResponse(t, firstRecorder)
	if firstBody["started"] != false {
		t.Fatalf("first update unexpectedly started: %#v", firstBody)
	}

	secondRecorder := httptest.NewRecorder()
	handleCicyUpdateApply(secondRecorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
	secondBody := decodeCicyUpdateResponse(t, secondRecorder)
	if secondBody["started"] != true || secondBody["restarting"] != true {
		t.Fatalf("retry did not start after installer failure: %#v", secondBody)
	}
	if installCalls != 2 || restartCalls != 1 {
		t.Fatalf("install calls = %d, restart calls = %d; want 2 and 1", installCalls, restartCalls)
	}
}

func TestHandleCicyUpdateApplyStagesLinuxLocalBinForRestart(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	homeDir := t.TempDir()
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "linux"
	cicyUpdateGOARCH = "arm64"
	cicyUpdateUserHomeDir = func() (string, error) { return homeDir, nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }
	cicyUpdateProcessArgs = func() []string { return []string{"/old/cicy-code", "--email", "user@example.com"} }
	installCalls := 0
	restartExecutable := ""
	var restartArgs []string
	cicyUpdateScheduleLocalBinRestart = func(executable string, args []string) {
		restartExecutable = executable
		restartArgs = append([]string(nil), args...)
	}
	cicyUpdateInstallLocalBin = func(_ context.Context, opts localBinUpdateOptions) error {
		installCalls++
		if opts.Version != "99.0.0" || opts.GOOS != "linux" || opts.GOARCH != "arm64" {
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
		t.Fatalf("installer calls = %d, want 1; response = %#v", installCalls, body)
	}
	if body["started"] != true || body["restarting"] != true {
		t.Fatalf("unexpected Linux update response: %#v", body)
	}
	if _, ok := body["restart_required"]; ok {
		t.Fatalf("Linux update must restart automatically: %#v", body)
	}
	if body["target"] != "99.0.0" || body["current"] != version {
		t.Fatalf("unexpected versions in response: %#v", body)
	}
	wantExecutable := filepath.Join(homeDir, ".local", "bin", "cicy-code")
	if restartExecutable != wantExecutable {
		t.Fatalf("restart executable = %q, want %q", restartExecutable, wantExecutable)
	}
	if len(restartArgs) != 3 || restartArgs[0] != wantExecutable || restartArgs[1] != "--email" || restartArgs[2] != "user@example.com" {
		t.Fatalf("restart args = %#v", restartArgs)
	}
}

func TestHandleCicyUpdateApplyCoalescesConcurrentLinuxUpdateForSameTarget(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	homeDir := t.TempDir()
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "linux"
	cicyUpdateGOARCH = "amd64"
	cicyUpdateUserHomeDir = func() (string, error) { return homeDir, nil }
	cicyUpdateLatestVersion = func() string { return "99.0.0" }

	installEntered := make(chan struct{}, 1)
	releaseInstall := make(chan struct{})
	var installCalls atomic.Int32
	cicyUpdateInstallLocalBin = func(context.Context, localBinUpdateOptions) error {
		if installCalls.Add(1) == 1 {
			installEntered <- struct{}{}
		}
		<-releaseInstall
		return nil
	}
	var restartCalls atomic.Int32
	cicyUpdateScheduleLocalBinRestart = func(string, []string) {
		restartCalls.Add(1)
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handleCicyUpdateApply(recorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
		firstDone <- recorder
	}()
	select {
	case <-installEntered:
	case <-time.After(time.Second):
		t.Fatal("first update never entered the installer")
	}

	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handleCicyUpdateApply(recorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
		secondDone <- recorder
	}()

	var secondRecorder *httptest.ResponseRecorder
	select {
	case secondRecorder = <-secondDone:
		// A duplicate request must join the in-progress update without waiting
		// for the installer owned by the first request.
	case <-time.After(250 * time.Millisecond):
		close(releaseInstall)
		<-firstDone
		<-secondDone
		t.Fatal("same-target update request blocked behind a duplicate installation")
	}
	close(releaseInstall)
	firstBody := decodeCicyUpdateResponse(t, <-firstDone)
	secondBody := decodeCicyUpdateResponse(t, secondRecorder)

	if secondBody["started"] != true || secondBody["restarting"] != true || secondBody["target"] != "99.0.0" {
		t.Fatalf("unexpected coalesced response: %#v", secondBody)
	}
	if firstBody["started"] != true || firstBody["target"] != "99.0.0" {
		t.Fatalf("unexpected owner response: %#v", firstBody)
	}
	if got := installCalls.Load(); got != 1 {
		t.Fatalf("installer calls = %d, want 1", got)
	}
	if got := restartCalls.Load(); got != 1 {
		t.Fatalf("restart schedules = %d, want 1", got)
	}
}

func TestHandleCicyUpdateApplyRejectsConcurrentLinuxUpdateForDifferentTarget(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	homeDir := t.TempDir()
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "linux"
	cicyUpdateGOARCH = "amd64"
	cicyUpdateUserHomeDir = func() (string, error) { return homeDir, nil }
	var latestCalls atomic.Int32
	cicyUpdateLatestVersion = func() string {
		if latestCalls.Add(1) == 1 {
			return "99.0.0"
		}
		return "100.0.0"
	}

	installEntered := make(chan struct{}, 1)
	releaseInstall := make(chan struct{})
	var installCalls atomic.Int32
	cicyUpdateInstallLocalBin = func(context.Context, localBinUpdateOptions) error {
		if installCalls.Add(1) == 1 {
			installEntered <- struct{}{}
			<-releaseInstall
		}
		return nil
	}
	var restartCalls atomic.Int32
	cicyUpdateScheduleLocalBinRestart = func(string, []string) {
		restartCalls.Add(1)
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handleCicyUpdateApply(recorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
		firstDone <- recorder
	}()
	select {
	case <-installEntered:
	case <-time.After(time.Second):
		t.Fatal("first update never entered the installer")
	}

	secondRecorder := httptest.NewRecorder()
	handleCicyUpdateApply(secondRecorder, httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil))
	secondBody := decodeCicyUpdateResponse(t, secondRecorder)
	close(releaseInstall)
	<-firstDone

	if secondBody["started"] != false {
		t.Fatalf("conflicting target unexpectedly started: %#v", secondBody)
	}
	if secondBody["active_target"] != "99.0.0" || secondBody["target"] != "100.0.0" {
		t.Fatalf("conflicting targets missing from response: %#v", secondBody)
	}
	if got := installCalls.Load(); got != 1 {
		t.Fatalf("installer calls = %d, want 1", got)
	}
	if got := restartCalls.Load(); got != 1 {
		t.Fatalf("restart schedules = %d, want 1", got)
	}
}

func TestHandleCicyUpdateApplyRejectsUnsupportedHost(t *testing.T) {
	configureCicyUpdateHandlerTest(t)
	cicyUpdateIsContainerRuntime = func() bool { return false }
	cicyUpdateGOOS = "windows"
	cicyUpdateLatestVersion = func() string { return "99.0.0" }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/cicy-update", nil)
	handleCicyUpdateApply(recorder, request)
	body := decodeCicyUpdateResponse(t, recorder)

	if body["started"] != false || body["error"] != "in-place update is only available in the container runtime or macOS/Linux local-bin installation" {
		t.Fatalf("unexpected unsupported-host response: %#v", body)
	}
}
