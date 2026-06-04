package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestTtydInlineSmoke drives serveTTY end-to-end against a throwaway tmux
// session running `cat` (which echoes input). It exercises the full refactored
// path: WS upgrade → gotty init handshake swallow → in-memory localTTY pipe →
// webtty → `tmux attach` slave → echoed output → back to client. No DB, no
// per-pane port, no live cicy-code.
func TestTtydInlineSmoke(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	sess := "smoke-ttyd-inline-test"
	exec.Command("tmux", "kill-session", "-t", sess).Run()
	if err := exec.Command("tmux", "new-session", "-d", "-s", sess, "cat").Run(); err != nil {
		t.Fatalf("create tmux session: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sess).Run()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveTTY(w, r, sess, "smoke", sess)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	dialer := websocket.Dialer{Subprotocols: []string{"webtty"}}
	c, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// gotty init handshake — serveTTY swallows this first message.
	if err := c.WriteMessage(websocket.TextMessage, []byte(`{"AuthToken":"","Arguments":""}`)); err != nil {
		t.Fatalf("send init: %v", err)
	}

	// webtty.Run sends initialize messages (SetWindowTitle '3', SetPreferences
	// '4', maybe SetReconnect '5') right after start — proves the pipe is live.
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotInit := false
	for i := 0; i < 8; i++ {
		_, msg, err := c.ReadMessage()
		if err != nil {
			break
		}
		if len(msg) > 0 && (msg[0] == '3' || msg[0] == '4' || msg[0] == '5' || msg[0] == '1') {
			gotInit = true
			break
		}
	}
	if !gotInit {
		t.Fatal("did not receive webtty initialize/output messages over the pipe")
	}

	// Send input (webtty Input type '1'). The tmux pane's PTY echoes it; tmux
	// renders that to the attach client, which webtty relays back as Output
	// ('1') messages. tmux interleaves cursor/SGR escapes between characters, so
	// accumulate all output and strip ESC sequences before searching.
	if err := c.WriteMessage(websocket.TextMessage, []byte("1hello-smoke\r")); err != nil {
		t.Fatalf("send input: %v", err)
	}
	var acc []byte
	// One deadline for the whole window: a gorilla read-deadline timeout puts the
	// conn into a permanent failed state, so we must not poll with short ones.
	c.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			break
		}
		if len(msg) > 1 && msg[0] == '1' {
			// webtty Output payloads are base64-encoded after the type byte.
			dec, err := base64.StdEncoding.DecodeString(string(msg[1:]))
			if err != nil {
				continue
			}
			acc = append(acc, dec...)
			if strings.Contains(string(stripANSI(acc)), "hello-smoke") {
				return // success
			}
		}
	}
	t.Fatalf("did not see echoed input in terminal output; stripped output=%q", stripANSI(acc))
}

// TestTtydInlineStatic checks the non-WS branches of serveTtydHTTP: the index
// page, the shared static bundle (assetfs path mapping), and the auth shim.
func TestTtydInlineStatic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror handleTtydProxy's path parse, minus the DB/token gate.
		sub := strings.TrimPrefix(r.URL.Path, "/x")
		if sub == "" {
			sub = "/"
		}
		serveTtydHTTP(w, r, "x", sub, "w-test", "x")
	}))
	defer srv.Close()

	cases := []struct {
		path, wantSub string
	}{
		{"/x/", "gotty-bundle.js"},      // index.html references the bundle
		{"/x/js/gotty-bundle.js", ""},   // static bundle served (non-empty 200)
		{"/x/auth_token.js", "gotty_auth_token"},
		{"/x/config.js", "gotty_term"},
	}
	for _, tc := range cases {
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("GET %s: status %d", tc.path, resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			t.Errorf("GET %s: empty body", tc.path)
			continue
		}
		if tc.wantSub != "" && !strings.Contains(string(body), tc.wantSub) {
			t.Errorf("GET %s: body missing %q", tc.path, tc.wantSub)
		}
	}
}

// stripANSI removes CSI/OSC escape sequences so rendered terminal text can be
// matched as plain content.
func stripANSI(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == 0x1b && i+1 < len(b) {
			switch b[i+1] {
			case '[': // CSI: ESC [ ... <final 0x40-0x7e>
				i += 2
				for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
			case ']': // OSC: ESC ] ... BEL or ST
				i += 2
				for i < len(b) && b[i] != 0x07 {
					if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
						i++
						break
					}
					i++
				}
			default:
				i++ // ESC + single char
			}
			continue
		}
		if b[i] >= 0x20 || b[i] == '\n' || b[i] == '\r' || b[i] == '\t' {
			out = append(out, b[i])
		}
	}
	return out
}
