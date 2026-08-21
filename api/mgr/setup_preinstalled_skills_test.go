// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestPreinstalledSkillsIncludeGitHubAndCloudflare(t *testing.T) {
	want := []string{"github", "cf", "cf-tunnel"}
	installed := make(map[string]bool, len(preinstalledSkills))
	for _, name := range preinstalledSkills {
		installed[name] = true
	}
	for _, name := range want {
		if !installed[name] {
			t.Errorf("preinstalledSkills does not include %q", name)
		}
	}
}
