// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The install overlay runs spec.customCmd in a fresh bash, so the kiro command
// must define every helper it calls (it used to be a bare, undefined function
// name → "command not found").
func TestKiroCliInstallCmdIsSelfContained(t *testing.T) {
	cmd := kiroCliInstallCmd()
	if !strings.Contains(cmd, "__cicy_local_install_kiro() {") {
		t.Fatalf("install cmd must define __cicy_local_install_kiro, got:\n%s", cmd)
	}
	if !strings.HasSuffix(strings.TrimSpace(cmd), "__cicy_local_install_kiro") {
		t.Fatalf("install cmd must end by invoking the installer")
	}
	if strings.Contains(cmd, "__cicy_install_kiro_cli_with_progress") {
		t.Fatal("install cmd still references the undefined legacy function")
	}
	// Syntax-check in bash without executing.
	out, err := exec.Command("bash", "-n", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
}
