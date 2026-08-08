// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
)

const maxCrontabBytes = 1024 * 1024

func handleCrontab(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(cicyCrontabPath)
		if err != nil && !os.IsNotExist(err) {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"content": string(data), "path": cicyCrontabPath})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, maxCrontabBytes+1))
		if err := dec.Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid_json")
			return
		}
		if len(body.Content) > maxCrontabBytes || bytes.IndexByte([]byte(body.Content), 0) >= 0 {
			httpErr(w, http.StatusBadRequest, "invalid_crontab")
			return
		}
		if body.Content != "" && !bytes.HasSuffix([]byte(body.Content), []byte("\n")) {
			body.Content += "\n"
		}
		if err := os.WriteFile(cicyCrontabPath, []byte(body.Content), 0644); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if runtime.GOOS == "windows" {
			httpErr(w, http.StatusBadRequest, "crontab_unavailable_on_windows")
			return
		}
		if err := ensureCrontabCommand(); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := runCrontabInstall(cicyCrontabPath); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid crontab: "+err.Error())
			return
		}
		J(w, M{"ok": true, "content": body.Content})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

var runCrontabInstall = func(path string) error {
	cmd := exec.Command("crontab", path)
	return cmd.Run()
}

var lookPath = exec.LookPath
var runCronSetup = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func privilegedCommand(name string, args ...string) (string, []string, error) {
	if os.Geteuid() == 0 {
		return name, args, nil
	}
	if _, err := lookPath("sudo"); err != nil {
		return "", nil, fmt.Errorf("root privileges required and sudo is unavailable")
	}
	if err := runCronSetup("sudo", "-n", "true"); err != nil {
		return "", nil, fmt.Errorf("root privileges required; passwordless sudo is unavailable")
	}
	return "sudo", append([]string{"-n", name}, args...), nil
}

func runPrivileged(name string, args ...string) error {
	command, commandArgs, err := privilegedCommand(name, args...)
	if err != nil {
		return err
	}
	return runCronSetup(command, commandArgs...)
}

func installLinuxCrontab() error {
	switch {
	case commandExists("apt-get"):
		if err := runPrivileged("apt-get", "update"); err != nil {
			return err
		}
		return runPrivileged("apt-get", "install", "-y", "cron")
	case commandExists("dnf"):
		return runPrivileged("dnf", "install", "-y", "cronie")
	case commandExists("yum"):
		return runPrivileged("yum", "install", "-y", "cronie")
	case commandExists("apk"):
		return runPrivileged("apk", "add", "--no-cache", "dcron")
	case commandExists("pacman"):
		return runPrivileged("pacman", "-Sy", "--noconfirm", "cronie")
	default:
		return fmt.Errorf("unsupported Linux package manager")
	}
}

func commandExists(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func ensureCrontabCommand() error {
	if commandExists("crontab") {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		log.Printf("[cron] crontab command missing; installing for Linux")
		if err := installLinuxCrontab(); err != nil {
			return err
		}
	case "darwin":
		// macOS ships /usr/bin/crontab as an operating-system component. There is
		// no portable Homebrew replacement that should overwrite it.
		return fmt.Errorf("macOS system crontab is missing; restore /usr/bin/crontab with a macOS update/reinstall")
	default:
		return fmt.Errorf("crontab installation is unsupported on %s", runtime.GOOS)
	}
	if !commandExists("crontab") {
		return fmt.Errorf("package installation completed but crontab is still unavailable")
	}
	return nil
}

// installConfiguredCrontab installs the persistent schedule for the user that
// runs cicy-code. An empty file is intentionally ignored: starting cicy-code
// must not erase an existing crontab.
func installConfiguredCrontab() {
	if runtime.GOOS == "windows" {
		return
	}
	data, err := os.ReadFile(cicyCrontabPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[cron] read %s: %v", cicyCrontabPath, err)
		}
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	if err := ensureCrontabCommand(); err != nil {
		log.Printf("[cron] cannot install %s: %v", cicyCrontabPath, err)
		return
	}
	if err := runCrontabInstall(cicyCrontabPath); err != nil {
		log.Printf("[cron] install %s: %v", cicyCrontabPath, err)
		return
	}
	log.Printf("[cron] installed %s", cicyCrontabPath)
}
