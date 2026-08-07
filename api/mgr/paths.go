// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	cicyRootSpec = "~/cicy-ai"
	// defaultWorkerIndex is the worker_index floor: user-created agents count UP
	// from here (next id = defaultWorkerIndex+1 = w-1002). The official role
	// roster occupies w-1001 (PM master) down to w-996, all below this floor, so
	// user agents never collide with the reserved band.
	defaultWorkerIndex = 1001
)

var (
	cicyRootDir            = resolveCicyPathSpec(cicyRootSpec)
	cicyDBDir              = filepath.Join(cicyRootDir, "db")
	cicyProjectsDir        = filepath.Join(cicyRootDir, "projects")
	cicyWorkersDir         = filepath.Join(cicyRootDir, "workers")
	cicySkillsDir          = filepath.Join(cicyRootDir, "skills")
	cicyGlobalJSONPath     = filepath.Join(cicyRootDir, "global.json")
	cicyCrontabPath        = filepath.Join(cicyDBDir, "crontab.txt")
	cicyStateDir           = filepath.Join(cicyRootDir, ".cicy")
	cicySnapshotsDir       = filepath.Join(cicyRootDir, "snapshots")
	cicyLogsDir            = resolveCicyPathSpec("~/logs")
	cicyMachinesConfigPath = filepath.Join(cicyRootDir, "cicy-node.json")
	cicySharedWorkspaceDir = filepath.Join(cicyRootDir, "shared-workspace")
)

func resolveCicyPathSpec(spec string) string {
	path := strings.TrimSpace(spec)
	if path == "" {
		log.Fatal("[startup] cicy root path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(homeDir) == "" {
			log.Fatalf("[startup] failed to resolve home directory for cicy root %q: %v", spec, err)
		}
		if path == "~" {
			return homeDir
		}
		return filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func builtinWorkerWorkspace(session string) string {
	return filepath.Join(cicyWorkersDir, session)
}

func workspaceRuntimeDir(workspace string) string {
	return filepath.Join(workspace, ".cicy")
}

func workspaceAssetsFilesDir(workspace string) string {
	return filepath.Join(runtimePathToHostPath(workspace), "assets")
}

// sharedAssetsFilesDir 是所有聊天附件上传的**统一存储目录**(~/cicy-ai/assets),不再按
// 每个 agent 的工作区分散。上传/取文件都走这里;URL 用 /assets/files/<rel>(无 pane 段)。
func sharedAssetsFilesDir() string {
	return filepath.Join(cicyRootDir, "assets")
}

func workspaceLegacyAssetsFilesDir(workspace string) string {
	return filepath.Join(workspaceRuntimeDir(workspace), "assets", "files")
}

func runtimePathToHostPath(path string) string {
	value := strings.TrimSpace(path)
	if value == "" {
		return ""
	}
	return value
}

func hostPathToFileRef(path string) string {
	value := strings.TrimSpace(runtimePathToHostPath(path))
	if value == "" {
		return ""
	}
	return "file://" + strings.TrimLeft(filepath.ToSlash(value), "/")
}

func builtinWorkerRuntimeDir(session string) string {
	return workspaceRuntimeDir(builtinWorkerWorkspace(session))
}

func bootstrapCicyPaths() {
	paths := []string{
		cicyRootDir,
		cicyDBDir,
		cicyProjectsDir,
		cicyWorkersDir,
		cicySkillsDir,
		cicyLogsDir,
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			log.Fatalf("[startup] failed to create %s: %v", path, err)
		}
	}
	// Keep scheduled-task configuration in the persistent cicy-ai data tree.
	// Create it only when missing so upgrades never overwrite user entries.
	file, err := os.OpenFile(cicyCrontabPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("[startup] failed to create %s: %v", cicyCrontabPath, err)
	}
	if err := file.Close(); err != nil {
		log.Fatalf("[startup] failed to close %s: %v", cicyCrontabPath, err)
	}
}

func ensureRuntimeUnprivileged() {
	if isContainerRuntime() {
		return
	}
	if os.Geteuid() == 0 {
		log.Fatalf("[startup] refusing to continue as root after %s bootstrap", cicyRootDir)
	}
}

func ensureRuntimeDir(path string, mode os.FileMode) error {
	if path == "" {
		return nil
	}
	return os.MkdirAll(path, mode)
}

func ensureRuntimeFile(path string, mode os.FileMode) error {
	if path == "" {
		return nil
	}
	return os.Chmod(path, mode)
}
