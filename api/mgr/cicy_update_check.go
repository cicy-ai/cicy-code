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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updCheckTTL = 30 * time.Minute

var (
	updCheckMu     sync.Mutex
	updCheckLatest string
	updCheckAt     time.Time

	// Indirections keep the update handler testable without writing to the
	// user's real ~/.local/bin or pretending the whole test process is another OS.
	cicyUpdateIsContainerRuntime       = isContainerRuntime
	cicyUpdateGOOS                     = runtime.GOOS
	cicyUpdateGOARCH                   = runtime.GOARCH
	cicyUpdateUserHomeDir              = os.UserHomeDir
	cicyUpdateLatestVersion            = latestCicyCodeVersion
	cicyUpdateInstalledLocalBinVersion = installedLocalBinVersion
	cicyUpdateInstallLocalBin          = installLocalBinUpdate
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
	latest := cicyUpdateLatestVersion()
	installed := version
	if !cicyUpdateIsContainerRuntime() && supportsLocalBinUpdate(cicyUpdateGOOS) {
		if staged := cicyUpdateInstalledLocalBinVersion(); versionGreater(staged, installed) {
			installed = staged
		}
	}
	J(w, M{
		"current":          version,
		"installed":        installed,
		"latest":           latest,
		"has_update":       latest != "" && versionGreater(latest, installed),
		"restart_required": versionGreater(installed, version),
	})
}

func installedLocalBinVersion() string {
	homeDir, err := cicyUpdateUserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(homeDir, ".local", "bin", ".cicy-localbin.json"))
	if err != nil {
		return ""
	}
	var manifest map[string]any
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	value, _ := manifest["cicy-code"].(string)
	return strings.TrimSpace(value)
}

// handleCicyUpdateApply updates either supported runtime:
//   - container: launch the existing detached updater, which restarts the
//     supervised server; the frontend polls until the new process is ready.
//   - macOS/Linux local-bin: download and atomically stage the published platform
//     binary, but deliberately leave restart timing to the user.
func handleCicyUpdateApply(w http.ResponseWriter, r *http.Request) {
	if cicyUpdateIsContainerRuntime() {
		handleContainerCicyUpdateApply(w)
		return
	}
	if supportsLocalBinUpdate(cicyUpdateGOOS) {
		handleLocalBinCicyUpdateApply(w, r)
		return
	}
	J(w, M{"started": false, "error": "in-place update is only available in the container runtime or macOS/Linux local-bin installation"})
}

func supportsLocalBinUpdate(goos string) bool {
	return goos == "darwin" || goos == "linux"
}

func handleContainerCicyUpdateApply(w http.ResponseWriter) {
	if _, err := os.Stat(legacyUpdaterPath); err != nil {
		J(w, M{"started": false, "error": "updater not found: " + legacyUpdaterPath})
		return
	}
	target := cicyUpdateLatestVersion()
	if target == "" {
		J(w, M{"started": false, "error": "could not resolve the latest version from npm"})
		return
	}
	if !versionGreater(target, version) {
		J(w, M{"started": false, "current": version, "latest": target, "error": "already up to date"})
		return
	}

	// Detach fully: `setsid` runs the updater in a NEW session, so supervisor's
	// restart of cicy-code (which kills our process group) doesn't take the
	// updater down mid-install. (setsid alone suffices — no Windows-incompatible
	// SysProcAttr needed; this path is container/Linux-only anyway.)
	cmd := exec.Command("setsid", "bash", legacyUpdaterPath, target)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		log.Printf("[cicy-update] launch failed: %v", err)
		J(w, M{"started": false, "error": "failed to launch updater: " + err.Error()})
		return
	}
	_ = cmd.Process.Release()
	log.Printf("[cicy-update] launched update %s -> %s (detached); server will restart", version, target)
	J(w, M{"started": true, "current": version, "target": target})
}

func handleLocalBinCicyUpdateApply(w http.ResponseWriter, r *http.Request) {
	target := cicyUpdateLatestVersion()
	if target == "" {
		J(w, M{"started": false, "error": "could not resolve the latest version from npm"})
		return
	}
	if !versionGreater(target, version) {
		J(w, M{"started": false, "current": version, "latest": target, "error": "already up to date"})
		return
	}
	homeDir, err := cicyUpdateUserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		if err == nil {
			err = os.ErrNotExist
		}
		J(w, M{"started": false, "error": "could not resolve home directory: " + err.Error()})
		return
	}
	opts := localBinUpdateOptions{
		Version:  target,
		GOOS:     cicyUpdateGOOS,
		GOARCH:   cicyUpdateGOARCH,
		BinDir:   filepath.Join(homeDir, ".local", "bin"),
		Registry: npmRegistryOfficial,
		Client:   http.DefaultClient,
	}
	if err := cicyUpdateInstallLocalBin(r.Context(), opts); err != nil {
		log.Printf("[cicy-update] %s local-bin install failed: %v", cicyUpdateGOOS, err)
		J(w, M{"started": false, "error": "failed to install update: " + err.Error()})
		return
	}
	log.Printf("[cicy-update] staged %s local-bin update %s -> %s; restart required", cicyUpdateGOOS, version, target)
	J(w, M{
		"started":          true,
		"completed":        true,
		"restart_required": true,
		"current":          version,
		"target":           target,
	})
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
