package main

import "testing"

func TestIsShellPromptVisible(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{name: "mac zsh percent prompt", out: "w-10009 %", want: true},
		{name: "bash dollar prompt", out: "w-10009 $", want: true},
		{name: "cicy prompt", out: "cicy tmux inited!!\nw-10009 $", want: true},
		{name: "install progress", out: "[cicy] [##########          ] 正在安装 Codex...  10s", want: false},
		{name: "plain output", out: "starting zsh...", want: false},
	}

	for _, tc := range cases {
		if got := isShellPromptVisible(tc.out); got != tc.want {
			t.Fatalf("%s: isShellPromptVisible() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsDarwinShellDollarPrompt(t *testing.T) {
	if !isDarwinShellDollarPrompt("cicy tmux inited!!\nw-10009 $") {
		t.Fatal("expected darwin dollar prompt to be detected")
	}
	if isDarwinShellDollarPrompt("w-10009 %") {
		t.Fatal("did not expect percent prompt to pass darwin dollar prompt check")
	}
}

func TestShellPromptTimeoutForRuntime(t *testing.T) {
	timeout := shellPromptTimeoutForRuntime()
	if timeout <= 0 {
		t.Fatalf("timeout must be positive, got %s", timeout)
	}
	if timeout != shellPromptTimeout && timeout != shellPromptTimeoutDarwin {
		t.Fatalf("unexpected timeout %s", timeout)
	}
}
