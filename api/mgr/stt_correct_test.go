package main

import "testing"

func TestSTTCleanCorrectionStripsWrappers(t *testing.T) {
	for in, want := range map[string]string{
		"\"你好，世界。\"":                   "你好，世界。",
		"修正后：现在很多平台都是这样做的。":            "现在很多平台都是这样做的。",
		"```\n请把 cicy-mobile 发版。\n```": "请把 cicy-mobile 发版。",
		"“加载更早”":                       "加载更早",
	} {
		if got := sttCleanCorrection(in); got != want {
			t.Fatalf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestSTTCorrectionPlausibleRejectsRewrites(t *testing.T) {
	raw := "现在很多平台都是这样做的首先把要说的话先识别成文字然后再把文字发给大模型让大模型进行决错"
	if !sttCorrectionPlausible(raw, "现在很多平台都是这样做的：首先把要说的话识别成文字，然后再把文字发给大模型，让大模型进行纠错。") {
		t.Fatal("a punctuated, lightly corrected transcript must pass")
	}
	if sttCorrectionPlausible(raw, "好的。") {
		t.Fatal("an answer instead of a correction must be rejected")
	}
	if sttCorrectionPlausible(raw, raw+raw+raw) {
		t.Fatal("a tripled text must be rejected")
	}
	if sttCorrectionPlausible(raw, "") {
		t.Fatal("empty must be rejected")
	}
}

func TestSTTWhisperPromptKeepsTail(t *testing.T) {
	c := sttAgentContext{Title: "Claude", Projects: []string{"cicy-mobile"}, Prompts: []string{"把 groq 的 key 加到配置里", "mobile 的 stt 有问题"}}
	p := sttWhisperPrompt(c)
	if p == "" || !contains(p, "cicy-mobile") || !contains(p, "groq") {
		t.Fatalf("prompt missing vocabulary: %q", p)
	}
	if sttWhisperPrompt(sttAgentContext{}) != "" {
		t.Fatal("empty context must give no prompt")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
