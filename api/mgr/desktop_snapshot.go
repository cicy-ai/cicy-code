package main

// Desktop snapshots — periodically screenshot every connected cicy-desktop host
// (win/mac/linux), store a compressed JPEG to ~/cicy-ai/snapshots/<deviceKey>/,
// and expose them to the UI's "桌面" (Desktop) tab.
//
// Transport: the server drives a screenshot on the device through the existing
// `exec_shell` tool, routed over the chat-WS sync bridge (same mechanism the
// browser BrowserWindowsPanel uses). mac/linux run the OS grabber LIVE; Windows
// instead reads a base64 file that cicy-desktop's snapshot daemon keeps fresh
// (live PowerShell capture fails there under 360/AppLocker/RDP). Either way the
// tool returns base64 JPEG on stdout; the server decodes it and writes one file
// per capture, pruning old ones.
//
// Config lives in the global_settings blob (read/written via /api/settings/global):
//   desktop_snapshot_enabled       bool   (default true)
//   desktop_snapshot_interval_sec  number (default 300, floored at 30)
//   desktop_snapshot_keep          number (default 48, per device)

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	snapDefaultIntervalSec = 300
	snapMinIntervalSec     = 30
	snapDefaultKeep        = 1 // keep only the latest snapshot per device
	snapRPCTimeout         = 25 * time.Second
)

type snapConfig struct {
	enabled  bool
	interval time.Duration
	keep     int
}

func loadSnapConfig() snapConfig {
	blob := globalSettingsBlob()
	cfg := snapConfig{enabled: true, interval: snapDefaultIntervalSec * time.Second, keep: snapDefaultKeep}
	if v, ok := blob["desktop_snapshot_enabled"].(bool); ok {
		cfg.enabled = v
	}
	if v, ok := blob["desktop_snapshot_interval_sec"].(float64); ok && v > 0 {
		secs := int(v)
		if secs < snapMinIntervalSec {
			secs = snapMinIntervalSec
		}
		cfg.interval = time.Duration(secs) * time.Second
	}
	if v, ok := blob["desktop_snapshot_keep"].(float64); ok && v >= 1 {
		cfg.keep = int(v)
	}
	return cfg
}

// snapDeviceKey is the per-device storage folder name: the stable device_id when
// present, else the client_id. Sanitized to a filesystem-safe token.
var snapKeyUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func snapDeviceKey(deviceID, clientID string) string {
	k := strings.TrimSpace(deviceID)
	if k == "" {
		k = strings.TrimSpace(clientID)
	}
	k = snapKeyUnsafe.ReplaceAllString(k, "_")
	if k == "" {
		k = "unknown"
	}
	return k
}

func snapDeviceDir(key string) string { return filepath.Join(cicySnapshotsDir, key) }

// ── desktop RPC (server → device, synchronous) ────────────────────────────────
// Mirrors handleChatPush's wait_ack mode: inject a requestId, register a waiter,
// push the desktop_event to the client, block for its reply. Returns the tool's
// `result` value (decoded from the reply envelope) or an error.
func desktopRPC(clientID, tool string, args map[string]interface{}, timeout time.Duration) (interface{}, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("client_id required")
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	requestID := "snap-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	data := map[string]interface{}{"type": "rpc_call", "tool": tool, "args": args, "requestId": requestID}
	ch := hub.registerWaiter(requestID)
	if !hub.sendToClient(clientID, ChatEvent{Type: "desktop_event", Data: data}) {
		hub.cancelWaiter(requestID)
		return nil, errors.New("client not connected")
	}
	select {
	case evt, ok := <-ch:
		if !ok {
			return nil, errors.New("waiter canceled")
		}
		m, _ := evt.Data.(map[string]interface{})
		if m == nil {
			return nil, errors.New("empty reply")
		}
		if e, _ := m["error"].(string); strings.TrimSpace(e) != "" {
			return nil, errors.New(e)
		}
		return m["result"], nil
	case <-time.After(timeout):
		hub.cancelWaiter(requestID)
		return nil, errors.New("device timeout")
	}
}

// deviceShellOut runs exec_shell on a device and returns trimmed stdout.
// The desktop bridge's `result` is not uniform: small outputs arrive as a JSON
// object {stdout,stderr,exitCode}; large ones (our base64 screenshots) arrive as
// a JSON-encoded STRING of that same object. Handle all three shapes (object,
// JSON-string-of-object, raw string) so we always end up with just stdout.
func deviceShellOut(clientID, command string) (string, error) {
	res, err := desktopRPC(clientID, "exec_shell", map[string]interface{}{"command": command}, snapRPCTimeout)
	if err != nil {
		return "", err
	}
	stdoutFromMap := func(m map[string]interface{}) (string, error) {
		if e, _ := m["error"].(string); strings.TrimSpace(e) != "" {
			return "", errors.New(e)
		}
		s, _ := m["stdout"].(string)
		return strings.TrimSpace(s), nil
	}
	switch v := res.(type) {
	case map[string]interface{}:
		return stdoutFromMap(v)
	case string:
		t := strings.TrimSpace(v)
		if strings.HasPrefix(t, "{") {
			var m map[string]interface{}
			if json.Unmarshal([]byte(t), &m) == nil {
				return stdoutFromMap(m)
			}
		}
		return t, nil
	default:
		return "", errors.New("unexpected exec_shell result")
	}
}

// ── per-platform screenshot ───────────────────────────────────────────────────
// mac/linux capture LIVE via the OS's own grabber (screencapture / scrot) — no
// 360/PowerShell hurdles, works fine, unchanged. Windows is the exception: live
// PowerShell CopyFromScreen returns empty under 360/AppLocker or a display-less
// RDP session, so there cicy-desktop captures the desktop on a timer (its
// utils/desktop-snapshot.js → a --disable-gpu desktopCapturer child, ≤600px
// wide) and writes ~/cicy-files/desktop-snapshot/desktop.b64 — we just read it.
const snapTmpUnix = "/tmp/cicy_desktop_snap.jpg"

func snapShellCommand(platform string) (string, error) {
	switch platform {
	case "darwin":
		// screencapture → jpg, downscale to ≤600px long edge via sips, base64.
		// NOTE: exec_shell runs a bare /bin/sh whose PATH lacks /usr/sbin, where
		// `screencapture` lives — so normalize PATH and prefer the absolute path.
		return fmt.Sprintf(`export PATH="/usr/sbin:/usr/bin:/bin:$PATH"; f=%[1]s; rm -f "$f"; /usr/sbin/screencapture -x -t jpg "$f" 2>/dev/null || screencapture -x -t jpg "$f" 2>/dev/null; [ -s "$f" ] && { sips -Z 600 "$f" >/dev/null 2>&1 || true; base64 < "$f"; }; rm -f "$f"`, snapTmpUnix), nil
	case "linux":
		// Try common X11/Wayland grabbers in order; quality-compressed jpg.
		return fmt.Sprintf(`f=%[1]s; export PATH="/usr/bin:/usr/local/bin:/bin:$PATH"; export DISPLAY="${DISPLAY:-:0}"; rm -f "$f";
if command -v scrot >/dev/null 2>&1; then scrot -o -q 60 "$f" 2>/dev/null;
elif command -v import >/dev/null 2>&1; then import -window root -quality 60 "$f" 2>/dev/null;
elif command -v gnome-screenshot >/dev/null 2>&1; then gnome-screenshot -f "$f" 2>/dev/null;
elif command -v grim >/dev/null 2>&1; then grim -t jpeg "$f" 2>/dev/null;
elif command -v spectacle >/dev/null 2>&1; then spectacle -b -n -o "$f" 2>/dev/null;
fi
[ -s "$f" ] && base64 < "$f"; rm -f "$f"`, snapTmpUnix), nil
	case "win":
		// exec_shell on Windows runs through cmd.exe, so `type` reads the file the
		// cicy-desktop snapshot daemon writes (≤600px, refreshed on a timer).
		return `type "%USERPROFILE%\cicy-files\desktop-snapshot\desktop.b64" 2>nul`, nil
	default:
		return "", fmt.Errorf("unsupported platform %q", platform)
	}
}

// captureDevice screenshots one device and writes a new snapshot file. Returns
// the written file's base name.
func captureDevice(clientID, deviceID, platform string, keep int) (string, error) {
	cmd, err := snapShellCommand(platform)
	if err != nil {
		return "", err
	}
	out, err := deviceShellOut(clientID, cmd)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("empty screenshot (no grabber or no display)")
	}
	// base64 may arrive wrapped; strip whitespace before decode.
	clean := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(out)
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil || len(raw) < 512 {
		return "", fmt.Errorf("invalid screenshot data: %v (%d bytes)", err, len(raw))
	}
	key := snapDeviceKey(deviceID, clientID)
	dir := snapDeviceDir(key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d.jpg", time.Now().UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		return "", err
	}
	pruneSnapshots(dir, keep)
	return name, nil
}

func pruneSnapshots(dir string, keep int) {
	if keep < 1 {
		keep = 1
	}
	files := listSnapshotFiles(dir)
	for i := keep; i < len(files); i++ {
		_ = os.Remove(filepath.Join(dir, files[i]))
	}
}

var snapFileRe = regexp.MustCompile(`^\d+\.jpg$`)

// listSnapshotFiles returns snapshot file names newest-first.
func listSnapshotFiles(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() && snapFileRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] }) // ts desc
	return out
}

// ── scheduler ─────────────────────────────────────────────────────────────────
func startDesktopSnapshots() {
	// brief startup delay so we don't capture during boot churn
	time.Sleep(20 * time.Second)
	for {
		cfg := loadSnapConfig()
		if cfg.enabled {
			captureAllDesktops(cfg.keep)
		}
		time.Sleep(cfg.interval)
	}
}

// desktopClient is one connected cicy-desktop host eligible for snapshots.
type desktopClient struct {
	clientID string
	deviceID string
	platform string
}

func captureAllDesktops(keep int) {
	clients := hub.snapshotEligibleClients()
	for _, c := range clients {
		if _, err := captureDevice(c.clientID, c.deviceID, c.platform, keep); err != nil {
			log.Printf("[snapshot] capture failed device=%s platform=%s: %v", snapDeviceKey(c.deviceID, c.clientID), c.platform, err)
		}
	}
}

// snapshotEligibleClients lists connected Electron desktop hosts on a supported
// OS. Defined here (snapshot feature) but operates on hub state.
func (h *chatHub) snapshotEligibleClients() []desktopClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := map[string]bool{}
	out := []desktopClient{}
	for _, bucket := range h.clients {
		for clientID, c := range bucket {
			if c == nil || !c.electron {
				continue
			}
			plat := normalizeChatClientPlatform(c.platform)
			if plat != "win" && plat != "darwin" && plat != "linux" {
				continue
			}
			key := c.deviceId
			if key == "" {
				key = clientID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, desktopClient{clientID: clientID, deviceID: c.deviceId, platform: plat})
		}
	}
	return out
}

// snapshotClientInfo resolves a clientID to its (deviceID, platform). ok=false if
// the client is not currently connected.
func (h *chatHub) snapshotClientInfo(clientID string) (deviceID, platform string, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.lookupClientLocked(clientID)
	if c == nil {
		return "", "", false
	}
	return c.deviceId, normalizeChatClientPlatform(c.platform), true
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────
// resolveSnapKey picks the storage key for a request: client_id (resolved via
// hub to its device_id) falling back to an explicit device_id param or the raw
// client_id when the device is offline.
func resolveSnapKey(r *http.Request) string {
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if clientID != "" {
		if did, _, ok := hub.snapshotClientInfo(clientID); ok {
			return snapDeviceKey(did, clientID)
		}
	}
	if deviceID != "" {
		return snapDeviceKey(deviceID, clientID)
	}
	return snapDeviceKey("", clientID)
}

// GET /api/desktop/snapshots?client_id=... → { items:[{name, ts}], count }
func handleDesktopSnapshots(w http.ResponseWriter, r *http.Request) {
	key := resolveSnapKey(r)
	files := listSnapshotFiles(snapDeviceDir(key))
	type item struct {
		Name string `json:"name"`
		Ts   int64  `json:"ts"`
	}
	items := make([]item, 0, len(files))
	for _, f := range files {
		ms, _ := strconv.ParseInt(strings.TrimSuffix(f, ".jpg"), 10, 64)
		items = append(items, item{Name: f, Ts: ms})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"key": key, "count": len(items), "items": items})
}

// GET /api/desktop/snapshot-image?client_id=...&name=<ts>.jpg → the JPEG bytes.
func handleDesktopSnapshotImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if !snapFileRe.MatchString(name) {
		http.Error(w, "bad name", 400)
		return
	}
	key := resolveSnapKey(r)
	abs := filepath.Join(snapDeviceDir(key), name)
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// POST /api/desktop/snapshot-now { client_id } → capture immediately.
func handleDesktopSnapshotNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	deviceID, platform, ok := hub.snapshotClientInfo(clientID)
	if !ok {
		http.Error(w, "device not connected", 404)
		return
	}
	if platform != "win" && platform != "darwin" && platform != "linux" {
		http.Error(w, "unsupported platform", 400)
		return
	}
	cfg := loadSnapConfig()
	name, err := captureDevice(clientID, deviceID, platform, cfg.keep)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	ms, _ := strconv.ParseInt(strings.TrimSuffix(name, ".jpg"), 10, 64)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": name, "ts": ms})
}
