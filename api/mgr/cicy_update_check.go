// Copyright 2026 CiCy AI
//
// "Is a newer cicy-code published?" check behind GET /api/cicy-update, consumed
// by the version badge in the membership popover. The newest published version
// is resolved from the npm registry and cached, so the frontend's focus-polling
// never hammers npm.

package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const updCheckTTL = 30 * time.Minute

var (
	updCheckMu     sync.Mutex
	updCheckLatest string
	updCheckAt     time.Time
)

// latestCicyCodeVersion returns the newest published cicy-code version, cached
// for updCheckTTL. npmjs is authoritative for the `latest` dist-tag (npmmirror's
// tag lags minutes behind a release); fall back to the CN mirror if npmjs is
// unreachable. Returns "" when neither registry answers.
func latestCicyCodeVersion() string {
	updCheckMu.Lock()
	if updCheckLatest != "" && time.Since(updCheckAt) < updCheckTTL {
		v := updCheckLatest
		updCheckMu.Unlock()
		return v
	}
	updCheckMu.Unlock()

	v := ""
	for _, reg := range []string{npmRegistryOfficial, npmRegistryMirror} {
		if got := fetchNpmLatestVersion(reg, "cicy-code"); got != "" {
			v = got
			break
		}
	}
	if v != "" {
		updCheckMu.Lock()
		updCheckLatest, updCheckAt = v, time.Now()
		updCheckMu.Unlock()
	}
	return v
}

// fetchNpmLatestVersion GETs <registry>/<pkg>/latest and returns its .version.
func fetchNpmLatestVersion(registry, pkg string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registry+"/"+pkg+"/latest", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return ""
	}
	return strings.TrimSpace(body.Version)
}

// handleCicyUpdateStatus serves /api/cicy-update:
//
//	GET  → {current, latest, has_update}
//	POST → trigger an in-place update to the latest published version.
//
// has_update is true only when a strictly newer version is published; on a
// registry failure latest is "" and has_update false.
func handleCicyUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleCicyUpdateApply(w, r)
		return
	}
	latest := latestCicyCodeVersion()
	J(w, M{
		"current":    version,
		"latest":     latest,
		"has_update": latest != "" && versionGreater(latest, version),
	})
}

// handleCicyUpdateApply runs cicy-code-update.sh for the resolved latest version
// in a DETACHED process: the script installs the new version, repoints the
// ~/.local/bin/cicy-code symlink, and `supervisorctl restart cicy-code`s — which
// kills THIS server, so it must outlive us (setsid, not a child). We pin the
// npmjs-resolved version (not the "latest" tag) to sidestep npmmirror tag lag.
// The frontend polls health/version afterwards and reloads once we're back.
func handleCicyUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !isContainerRuntime() {
		J(w, M{"started": false, "error": "in-place update is only available in the container runtime"})
		return
	}
	if _, err := os.Stat(legacyUpdaterPath); err != nil {
		J(w, M{"started": false, "error": "updater not found: " + legacyUpdaterPath})
		return
	}
	target := latestCicyCodeVersion()
	if target == "" {
		J(w, M{"started": false, "error": "could not resolve the latest version from npm"})
		return
	}
	if !versionGreater(target, version) {
		J(w, M{"started": false, "current": version, "latest": target, "error": "already up to date"})
		return
	}

	// Detach fully: setsid + own process group so supervisor's restart of
	// cicy-code doesn't take the updater down with us mid-install.
	cmd := exec.Command("setsid", "bash", legacyUpdaterPath, target)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("[cicy-update] launch failed: %v", err)
		J(w, M{"started": false, "error": "failed to launch updater: " + err.Error()})
		return
	}
	_ = cmd.Process.Release()
	log.Printf("[cicy-update] launched update %s -> %s (detached); server will restart", version, target)
	J(w, M{"started": true, "current": version, "target": target})
}

// versionGreater reports whether dotted-numeric version a is newer than b
// ("2.3.193" > "2.3.192"). A leading "v" is tolerated; non-numeric parts read 0.
func versionGreater(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		ai, bi := 0, 0
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai != bi {
			return ai > bi
		}
	}
	return false
}
