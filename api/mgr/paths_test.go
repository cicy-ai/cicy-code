// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapCicyPathsCreatesAndPreservesCrontab(t *testing.T) {
	root := t.TempDir()
	oldRoot, oldDB, oldProjects := cicyRootDir, cicyDBDir, cicyProjectsDir
	oldWorkers, oldSkills, oldLogs := cicyWorkersDir, cicySkillsDir, cicyLogsDir
	oldCrontab := cicyCrontabPath
	t.Cleanup(func() {
		cicyRootDir, cicyDBDir, cicyProjectsDir = oldRoot, oldDB, oldProjects
		cicyWorkersDir, cicySkillsDir, cicyLogsDir = oldWorkers, oldSkills, oldLogs
		cicyCrontabPath = oldCrontab
	})

	cicyRootDir = root
	cicyDBDir = filepath.Join(root, "db")
	cicyProjectsDir = filepath.Join(root, "projects")
	cicyWorkersDir = filepath.Join(root, "workers")
	cicySkillsDir = filepath.Join(root, "skills")
	cicyLogsDir = filepath.Join(root, "logs")
	cicyCrontabPath = filepath.Join(cicyDBDir, "crontab.txt")

	bootstrapCicyPaths()
	if _, err := os.Stat(cicyCrontabPath); err != nil {
		t.Fatalf("crontab file was not created: %v", err)
	}

	const schedule = "0 * * * * echo ok\n"
	if err := os.WriteFile(cicyCrontabPath, []byte(schedule), 0644); err != nil {
		t.Fatal(err)
	}
	bootstrapCicyPaths()
	got, err := os.ReadFile(cicyCrontabPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != schedule {
		t.Fatalf("bootstrap overwrote crontab: got %q", got)
	}
}
