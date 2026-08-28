// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// SSH trust between the nodes of one CiCy Hub tenant. Each node reports its
// login user and public keys with the heartbeat; the hub hands back the union
// of every sibling's keys, which we keep in a marked block of
// ~/.ssh/authorized_keys. Any node can then `ssh -p <port> <user>@<hub>` into
// any other without touching keys by hand. The block is ours alone: keys the
// user added themselves are never touched, and turning the frp client off
// removes the block.

const (
	sshTrustBegin = "# >>> cicy-hub tenant keys (managed, do not edit) >>>"
	sshTrustEnd   = "# <<< cicy-hub tenant keys <<<"
)

var sshTrustMu sync.Mutex

func sshHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

func sshLoginUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 { // DOMAIN\user on Windows
			name = name[i+1:]
		}
		return name
	}
	return os.Getenv("USER")
}

// sshPublicKeys returns this node's public keys, generating an ed25519 pair
// first if the user has none, so every node can act as an SSH client.
func sshPublicKeys() []string {
	dir := filepath.Join(sshHomeDir(), ".ssh")
	names := []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"}
	var keys []string
	for _, n := range names {
		if raw, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			if k := strings.TrimSpace(string(raw)); k != "" {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) > 0 || runtime.GOOS == "windows" {
		return keys
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return nil
	}
	_ = os.MkdirAll(dir, 0o700)
	priv := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(priv); err != nil {
		host, _ := os.Hostname()
		cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", sshLoginUser()+"@"+host, "-f", priv)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[ssh-trust] keygen failed: %v %s", err, strings.TrimSpace(string(out)))
			return nil
		}
		log.Printf("[ssh-trust] generated %s", priv)
	}
	if raw, err := os.ReadFile(priv + ".pub"); err == nil {
		if k := strings.TrimSpace(string(raw)); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// syncTenantAuthorizedKeys fetches the tenant key set from the hub and writes
// it into the managed block of ~/.ssh/authorized_keys. Returns the number of
// keys installed.
func syncTenantAuthorizedKeys() (int, error) {
	cred, ok := loadCiCyCloudCredential()
	if !ok || cred.Mode != cicyCloudModeHub {
		return 0, fmt.Errorf("not signed in to CiCy Hub")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cred.Origin, "/")+"/api/ssh/authorized-keys", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("hub HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 0, 8192)
	for {
		chunk := make([]byte, 4096)
		n, err := resp.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil || len(buf) > 256<<10 {
			break
		}
	}
	mine := map[string]bool{}
	for _, k := range sshPublicKeys() {
		if f := strings.Fields(k); len(f) >= 2 {
			mine[f[0]+" "+f[1]] = true
		}
	}
	var keys []string
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(line, "#") || mine[f[0]+" "+f[1]] {
			continue // siblings only; our own key is pointless here
		}
		keys = append(keys, line)
	}
	sort.Strings(keys)
	return len(keys), writeTenantKeysBlock(keys)
}

// writeTenantKeysBlock replaces (or removes, when keys is empty) the managed
// block, leaving everything else in authorized_keys untouched.
func writeTenantKeysBlock(keys []string) error {
	sshTrustMu.Lock()
	defer sshTrustMu.Unlock()
	dir := filepath.Join(sshHomeDir(), ".ssh")
	path := filepath.Join(dir, "authorized_keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, _ := os.ReadFile(path)
	var kept []string
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.TrimSpace(line) == sshTrustBegin:
			inBlock = true
		case strings.TrimSpace(line) == sshTrustEnd:
			inBlock = false
		case !inBlock:
			kept = append(kept, line)
		}
	}
	// drop trailing blank lines so the file doesn't grow blank lines over time
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	out := strings.Join(kept, "\n")
	if len(keys) > 0 {
		if out != "" {
			out += "\n"
		}
		out += sshTrustBegin + "\n" + strings.Join(keys, "\n") + "\n" + sshTrustEnd + "\n"
	} else if out != "" {
		out += "\n"
	}
	if string(raw) == out {
		return nil
	}
	tmp := path + ".cicy-tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
