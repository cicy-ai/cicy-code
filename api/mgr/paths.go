package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	cicyRootSpec       = "~/cicy-ai"
	defaultWorkerIndex = 20000
)

var (
	cicyRootDir            = resolveCicyPathSpec(cicyRootSpec)
	cicyDBDir              = filepath.Join(cicyRootDir, "db")
	cicyProjectsDir        = filepath.Join(cicyRootDir, "projects")
	cicyWorkersDir         = filepath.Join(cicyRootDir, "workers")
	cicySkillsDir          = filepath.Join(cicyRootDir, "skills")
	cicyGlobalJSONPath     = filepath.Join(cicyRootDir, "global.json")
	cicyStateDir           = filepath.Join(cicyRootDir, ".cicy")
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
		cicyStateDir,
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
