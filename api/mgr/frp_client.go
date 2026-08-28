// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Built-in frp client for CiCy Hub. Instead of `curl …/frpc.sh | bash`, the
// server itself asks the hub for this instance's frpc config (fixed remote
// ports for HTTP and SSH), fetches the frpc binary from the hub's mirror and
// supervises it as a child process — no systemd, no shell, works the same on
// every host. Enabled per credential (`frp: true` in cloud-device.json) so it
// comes back after restarts; toggled from Settings → CiCy 账号.

const (
	frpClientVersion  = "0.70.1"
	frpClientRestart  = 5 * time.Second
	frpClientLogLimit = 2 << 20
)

type frpClient struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	running  bool
	lastErr  string
	startAt  time.Time
	ports    map[string]int
	host     string
	slug     string
	restarts int
}

var frpClientMgr = &frpClient{}

func frpClientDir() string  { return filepath.Join(cicyRootDir, "runtime", "frp") }
func frpClientConf() string { return filepath.Join(cicyDBDir, "frpc.toml") }
func frpClientBin() string {
	name := "frpc"
	if runtime.GOOS == "windows" {
		name = "frpc.exe"
	}
	return filepath.Join(frpClientDir(), name)
}

// frpClientWanted reports whether the hub credential asks for frp.
func frpClientWanted() bool {
	cred, ok := loadCiCyCloudCredential()
	return ok && cred.Mode == cicyCloudModeHub && cred.Frp
}

func (c *frpClient) status() M {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := M{"supported": runtime.GOOS != "windows", "enabled": frpClientWanted(), "running": c.running,
		"error": c.lastErr, "restarts": c.restarts, "host": c.host, "slug": c.slug, "ports": c.ports}
	if c.running {
		out["uptime_sec"] = int(time.Since(c.startAt).Seconds())
	}
	return out
}

func (c *frpClient) setErr(err error) {
	c.mu.Lock()
	if err != nil {
		c.lastErr = err.Error()
	} else {
		c.lastErr = ""
	}
	c.mu.Unlock()
}

// enable persists the choice and (re)starts the client.
func (c *frpClient) enable(on bool) error {
	cred, ok := loadCiCyCloudCredential()
	if !ok || cred.Mode != cicyCloudModeHub {
		return fmt.Errorf("sign in to CiCy Hub first")
	}
	if on && runtime.GOOS == "windows" {
		return fmt.Errorf("frp client is not supported on Windows yet")
	}
	cred.Frp = on
	if err := saveCiCyCloudCredential(cred); err != nil {
		return err
	}
	if on {
		return c.start()
	}
	c.stop()
	return nil
}

// ensure is called at startup and after a hub login: start when wanted.
func (c *frpClient) ensure() {
	if !frpClientWanted() || runtime.GOOS == "windows" {
		return
	}
	if err := c.start(); err != nil {
		log.Printf("[frp] start failed: %v", err)
	}
}

func (c *frpClient) start() error {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil // supervisor already running; it re-fetches config on each restart
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.mu.Unlock()
	go c.supervise(ctx)
	return nil
}

func (c *frpClient) stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// fetchConfig asks the hub for this instance's frpc config and writes it.
func (c *frpClient) fetchConfig() error {
	cred, ok := loadCiCyCloudCredential()
	if !ok || cred.Mode != cicyCloudModeHub {
		return fmt.Errorf("not signed in to CiCy Hub")
	}
	var out struct {
		ServerAddr string         `json:"serverAddr"`
		Slug       string         `json:"slug"`
		Proxies    map[string]int `json:"proxies"`
		TOML       string         `json:"toml"`
	}
	route := "/api/frp/config?local_http=" + resolvePort() + "&local_ssh=22"
	if err := cloudJSONAt(strings.TrimRight(cred.Origin, "/"), http.MethodGet, route, cred.Token, nil, &out); err != nil {
		return fmt.Errorf("hub frp config: %w", err)
	}
	if strings.TrimSpace(out.TOML) == "" {
		return fmt.Errorf("hub returned an empty frp config")
	}
	if err := os.MkdirAll(cicyDBDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(frpClientConf(), []byte(out.TOML), 0o600); err != nil {
		return err
	}
	c.mu.Lock()
	c.host, c.slug, c.ports = out.ServerAddr, out.Slug, out.Proxies
	c.mu.Unlock()
	return nil
}

// ensureBinary downloads frpc (hub mirror first, GitHub as fallback).
func (c *frpClient) ensureBinary() error {
	bin := frpClientBin()
	if out, err := exec.Command(bin, "--version").Output(); err == nil && strings.Contains(string(out), frpClientVersion) {
		return nil
	}
	cred, _ := loadCiCyCloudCredential()
	arch := runtime.GOARCH
	pkg := fmt.Sprintf("frp_%s_%s_%s.tar.gz", frpClientVersion, runtime.GOOS, arch)
	urls := []string{
		strings.TrimRight(cred.Origin, "/") + "/dl/" + pkg,
		"https://github.com/fatedier/frp/releases/download/v" + frpClientVersion + "/" + pkg,
	}
	if err := os.MkdirAll(frpClientDir(), 0o755); err != nil {
		return err
	}
	var lastErr error
	for _, u := range urls {
		if err := downloadFrpc(u, bin, "frp_"+frpClientVersion+"_"+runtime.GOOS+"_"+arch+"/frpc"); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("download frpc: %w", lastErr)
}

func downloadFrpc(url, dest, member string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		resp, err = client.Get(url)
		if err == nil && resp.StatusCode == 200 {
			break
		}
		if resp != nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not in archive", member)
		}
		if err != nil {
			return err
		}
		if h.Name != member || h.Typeflag != tar.TypeReg {
			continue
		}
		tmp := dest + ".new"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
		return os.Rename(tmp, dest)
	}
}

func (c *frpClient) supervise(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.fetchConfig(); err != nil {
			c.setErr(err)
			log.Printf("[frp] %v", err)
		} else if err := c.ensureBinary(); err != nil {
			c.setErr(err)
			log.Printf("[frp] %v", err)
		} else if err := c.runOnce(ctx); err != nil {
			c.setErr(err)
			log.Printf("[frp] frpc exited: %v", err)
		} else {
			c.setErr(nil)
		}
		c.mu.Lock()
		c.running = false
		c.restarts++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(frpClientRestart):
		}
	}
}

func (c *frpClient) runOnce(ctx context.Context) error {
	// A shell-installed frpc (frpc.sh) for the same config would fight over
	// the same proxy names; take over from it.
	killStrayFrpc()
	logPath := filepath.Join(cicyRootDir, "logs", "frpc.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	if st, err := os.Stat(logPath); err == nil && st.Size() > frpClientLogLimit {
		_ = os.Remove(logPath)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.CommandContext(ctx, frpClientBin(), "-c", frpClientConf())
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), "HTTP_PROXY=", "HTTPS_PROXY=", "http_proxy=", "https_proxy=", "ALL_PROXY=", "all_proxy=")
	if err := cmd.Start(); err != nil {
		return err
	}
	c.mu.Lock()
	c.running, c.startAt, c.lastErr = true, time.Now(), ""
	c.mu.Unlock()
	log.Printf("[frp] frpc started pid=%d", cmd.Process.Pid)
	return cmd.Wait()
}

func killStrayFrpc() {
	if runtime.GOOS == "windows" {
		return
	}
	self := frpClientBin() + " -c " + frpClientConf()
	out, err := exec.Command("pgrep", "-f", "--", self).Output()
	if err != nil {
		return
	}
	for _, pid := range strings.Fields(string(out)) {
		if pid == fmt.Sprint(os.Getpid()) {
			continue
		}
		_ = exec.Command("kill", pid).Run()
	}
}
