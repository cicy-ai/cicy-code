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
	"regexp"
	"runtime"
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

	// Indirections keep the update handler testable without writing to the
	// user's real ~/.local/bin or pretending the whole test process is another OS.
	cicyUpdateIsContainerRuntime       = isContainerRuntime
	cicyUpdateGOOS                     = runtime.GOOS
	cicyUpdateGOARCH                   = runtime.GOARCH
	cicyUpdateUserHomeDir              = os.UserHomeDir
	cicyUpdateLatestVersion            = latestCicyCodeVersion
	cicyUpdateInstalledLocalBinVersion = installedLocalBinVersion
	cicyUpdateInstallLocalBin          = installLocalBinUpdate
	cicyUpdateProcessArgs              = func() []string { return append([]string(nil), os.Args...) }
	cicyUpdateScheduleLocalBinRestart  = scheduleLocalBinRestart
	cicyUpdateLocalBinCoordinator      = &localBinUpdateCoordinator{}
)

type localBinUpdateCoordinator struct {
	mu     sync.Mutex
	target string
}

// begin elects one request as the installer for target. A duplicate request
// for the same target joins the already-running update; a different target is
// rejected so two published versions can never race the stable symlink.
func (c *localBinUpdateCoordinator) begin(target string) (owner bool, activeTarget string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.target == "" {
		c.target = target
		return true, target
	}
	return false, c.target
}

func (c *localBinUpdateCoordinator) cancel(target string) {
	c.mu.Lock()
	if c.target == target {
		c.target = ""
	}
	c.mu.Unlock()
}

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
		"has_update":       latest != "" && versionGreater(latest, version),
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
//     binary, flush a success response, then replace this process with the new
//     binary while preserving its command-line arguments and environment.
func handleCicyUpdateApply(w http.ResponseWriter, r *http.Request) {
	if cicyUpdateIsContainerRuntime() {
		handleContainerCicyUpdateApply(w, r)
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

// cicyUpdateVersionRe: what a caller may pin as `target` — a plain semver.
var cicyUpdateVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Seams for handleContainerCicyUpdateApply (tests swap them).
var cicyUpdateContainerUpdaterPath = legacyUpdaterPath
var cicyUpdateCurrentVersion = func() string { return version }

// Single-flight for the container updater: the web button and cicy-desktop
// may both POST, and a user who sees no feedback clicks again. A second
// `npm install` into the same version dir races the first one; report the
// running update instead of launching another.
var (
	cicyUpdateContainerMu       sync.Mutex
	cicyUpdateContainerTarget   string
	cicyUpdateContainerStarted  time.Time
	cicyUpdateContainerInFlight = 10 * time.Minute
)

func cicyUpdateContainerInProgress(now time.Time) (string, bool) {
	cicyUpdateContainerMu.Lock()
	defer cicyUpdateContainerMu.Unlock()
	if cicyUpdateContainerTarget == "" || now.Sub(cicyUpdateContainerStarted) > cicyUpdateContainerInFlight {
		return "", false
	}
	return cicyUpdateContainerTarget, true
}

func cicyUpdateContainerMarkStarted(target string, now time.Time) {
	cicyUpdateContainerMu.Lock()
	cicyUpdateContainerTarget = target
	cicyUpdateContainerStarted = now
	cicyUpdateContainerMu.Unlock()
}

var cicyUpdateLaunchContainerUpdater = func(target, registry string) error {
	// Detach fully: `setsid` runs the updater in a NEW session, so supervisor's
	// restart of cicy-code (which kills our process group) doesn't take the
	// updater down mid-install. (setsid alone suffices — no Windows-incompatible
	// SysProcAttr needed; this path is container/Linux-only anyway.)
	cmd := exec.Command("setsid", "bash", cicyUpdateContainerUpdaterPath, target)
	cmd.Env = os.Environ()
	if registry != "" {
		cmd.Env = append(cmd.Env, "NPM_REGISTRY="+registry)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// POST /api/cicy-update in the container runtime. The body may carry
//
//	{"target": "2.3.573", "registry": "https://registry.npmmirror.com"}
//
// — cicy-desktop resolves the version on the HOST (fast, host network) and
// pins it here, so the container never runs its own `npm view` (which is what
// used to stall the update for minutes). Without a target the latest version
// is resolved here as before. This is the ONLY supported update entry point
// for a running container: it runs inside, needs no docker exec and no script
// push, and survives the supervisor restart it triggers.
func handleContainerCicyUpdateApply(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(cicyUpdateContainerUpdaterPath); err != nil {
		J(w, M{"started": false, "error": "updater not found: " + cicyUpdateContainerUpdaterPath})
		return
	}
	var body struct {
		Target   string `json:"target"`
		Registry string `json:"registry"`
	}
	if r != nil && r.Body != nil {
		_ = readBody(r, &body)
	}
	target := strings.TrimSpace(body.Target)
	if target != "" && !cicyUpdateVersionRe.MatchString(target) {
		J(w, M{"started": false, "error": "invalid target version: " + target})
		return
	}
	if target == "" {
		target = cicyUpdateLatestVersion()
	}
	if target == "" {
		J(w, M{"started": false, "error": "could not resolve the latest version from npm"})
		return
	}
	if !versionGreater(target, cicyUpdateCurrentVersion()) {
		J(w, M{"started": false, "current": cicyUpdateCurrentVersion(), "latest": target, "error": "already up to date"})
		return
	}
	registry := strings.TrimSpace(body.Registry)
	if registry != "" && !strings.HasPrefix(registry, "https://") {
		registry = ""
	}
	if running, ok := cicyUpdateContainerInProgress(time.Now()); ok {
		J(w, M{"started": true, "in_progress": true, "current": cicyUpdateCurrentVersion(), "target": running})
		return
	}
	if err := cicyUpdateLaunchContainerUpdater(target, registry); err != nil {
		log.Printf("[cicy-update] launch failed: %v", err)
		J(w, M{"started": false, "error": "failed to launch updater: " + err.Error()})
		return
	}
	cicyUpdateContainerMarkStarted(target, time.Now())
	log.Printf("[cicy-update] launched update %s -> %s (detached, registry=%q); server will restart", cicyUpdateCurrentVersion(), target, registry)
	J(w, M{"started": true, "current": cicyUpdateCurrentVersion(), "target": target})
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
	owner, activeTarget := cicyUpdateLocalBinCoordinator.begin(target)
	if !owner {
		if activeTarget != target {
			J(w, M{
				"started":       false,
				"current":       version,
				"target":        target,
				"active_target": activeTarget,
				"error":         "another update is already in progress",
			})
			return
		}
		writeLocalBinRestartingResponse(w, target)
		return
	}
	if err := cicyUpdateInstallLocalBin(r.Context(), opts); err != nil {
		cicyUpdateLocalBinCoordinator.cancel(target)
		log.Printf("[cicy-update] %s local-bin install failed: %v", cicyUpdateGOOS, err)
		J(w, M{"started": false, "error": "failed to install update: " + err.Error()})
		return
	}
	restartExecutable := filepath.Join(opts.BinDir, "cicy-code")
	processArgs := cicyUpdateProcessArgs()
	restartArgs := []string{restartExecutable}
	if len(processArgs) > 1 {
		restartArgs = append(restartArgs, processArgs[1:]...)
	}
	log.Printf("[cicy-update] staged %s local-bin update %s -> %s; restarting with original args", cicyUpdateGOOS, version, target)
	writeLocalBinRestartingResponse(w, target)
	cicyUpdateScheduleLocalBinRestart(restartExecutable, restartArgs)
}

func writeLocalBinRestartingResponse(w http.ResponseWriter, target string) {
	J(w, M{
		"started":    true,
		"restarting": true,
		"current":    version,
		"target":     target,
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// scheduleLocalBinRestart gives net/http enough time to deliver the successful
// update response, then atomically replaces the current process image. Exec
// preserves the PID and parent-process supervision while the rebuilt argv keeps
// the same port, email and other startup flags the user originally supplied.
func scheduleLocalBinRestart(executable string, args []string) {
	executable = strings.TrimSpace(executable)
	argv := append([]string(nil), args...)
	env := append([]string(nil), os.Environ()...)
	time.AfterFunc(500*time.Millisecond, func() {
		if executable == "" {
			log.Printf("[cicy-update] restart failed: executable is empty")
			return
		}
		if len(argv) == 0 {
			argv = []string{executable}
		}
		if err := syscall.Exec(executable, argv, env); err != nil {
			log.Printf("[cicy-update] restart exec failed: %v", err)
		}
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
