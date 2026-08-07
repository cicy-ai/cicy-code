// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
)

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
