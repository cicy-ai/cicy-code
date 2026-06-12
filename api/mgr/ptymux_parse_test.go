package main

import "testing"

// sendKeysBytes mimics ptmManager.Tmux's "send-keys" branch end-to-end: it takes
// the args AFTER the "send-keys" subcommand (exactly what runTmux passes), parses
// the flags, and returns the literal byte string that would be written into the
// pty. This is the native backend's entire /api/tmux/send fidelity surface.
func sendKeysBytes(argsAfterSendKeys ...string) string {
	f := ptmParseFlags(argsAfterSendKeys)
	return ptmTranslateKeys(f.positional, f.literal)
}

func TestPtmSendKeysVectors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		// sendPromptText literal paste: send-keys -t <pane> -l -- <text>
		{"literal-paste-simple", []string{"-t", "w-1:main.0", "-l", "--", "hello world"}, "hello world"},
		// text that begins with a dash MUST survive thanks to the -- guard.
		{"literal-paste-dash-text", []string{"-t", "w-1:main.0", "-l", "--", "-foo --bar"}, "-foo --bar"},
		// multi-line prompt text (one arg) is passed through verbatim.
		{"literal-paste-multiline", []string{"-t", "w-1:main.0", "-l", "--", "line1\nline2"}, "line1\nline2"},
		// sendPromptEnter: send-keys -t <pane> Enter  => CR
		{"enter", []string{"-t", "w-1:main.0", "Enter"}, "\r"},
		// clearPanePromptInput: C-u then Escape
		{"ctrl-u", []string{"-t", "w-1:main.0", "C-u"}, "\x15"},
		{"escape", []string{"-t", "w-1:main.0", "Escape"}, "\x1b"},
		// handleSend keys-mode passthrough of a named key.
		{"ctrl-c", []string{"-t", "w-1:main.0", "C-c"}, "\x03"},
		// arrow key.
		{"up", []string{"-t", "w-1:main.0", "Up"}, "\x1b[A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sendKeysBytes(c.args...); got != c.want {
				t.Fatalf("sendKeysBytes(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestPtmSendKeysNoSubmitNeedsDashDash pins the bug fixed alongside this test:
// the compose-without-submit path used `send-keys -t <pane> -l <text>` WITHOUT a
// `--` separator. When <text> began with "-<lowercase>", ptmParseFlags swallowed
// it as an unknown flag and the text was silently DROPPED. The fix adds `--`.
func TestPtmSendKeysNoSubmitNeedsDashDash(t *testing.T) {
	// Without -- a dash-leading text is lost (documents WHY the source needs --).
	if got := sendKeysBytes("-t", "w-1:main.0", "-l", "-foo"); got != "" {
		t.Fatalf("expected dash-leading text to be dropped without --, got %q", got)
	}
	// With -- (the fixed call shape) it survives.
	if got := sendKeysBytes("-t", "w-1:main.0", "-l", "--", "-foo"); got != "-foo" {
		t.Fatalf("with -- guard, want %q, got %q", "-foo", got)
	}
	// Plain (non-dash) text is unaffected either way.
	if got := sendKeysBytes("-t", "w-1:main.0", "-l", "ok"); got != "ok" {
		t.Fatalf("plain text want %q, got %q", "ok", got)
	}
}

func TestPtmSessionOf(t *testing.T) {
	if got := ptmSessionOf("w-1001:main.0"); got != "w-1001" {
		t.Fatalf("got %q", got)
	}
	if got := ptmSessionOf("w-1001"); got != "w-1001" {
		t.Fatalf("got %q", got)
	}
}

func TestPtmFromPosix(t *testing.T) {
	cases := map[string]string{
		"/c/Users/x":     `C:\Users\x`,
		"/d/work/repo":   `D:\work\repo`,
		"/c":             "C:",
		`C:\already\win`: `C:\already\win`,
	}
	for in, want := range cases {
		if got := ptmFromPosix(in); got != want {
			t.Fatalf("ptmFromPosix(%q) = %q, want %q", in, got, want)
		}
	}
}
