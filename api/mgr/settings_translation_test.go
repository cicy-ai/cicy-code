package main

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAIChatCompletionsURL(t *testing.T) {
	tests := map[string]string{
		"base URL":          "https://example.com/v1/chat/completions",
		"base URL slash":    "https://example.com/v1/chat/completions",
		"complete endpoint": "https://opencode.ai/zen/v1/chat/completions",
	}
	inputs := map[string]string{
		"base URL":          "https://example.com/v1",
		"base URL slash":    "https://example.com/v1/",
		"complete endpoint": "https://opencode.ai/zen/v1/chat/completions",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := openAIChatCompletionsURL(inputs[name]); got != want {
				t.Fatalf("openAIChatCompletionsURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestLiveTranslationProvider(t *testing.T) {
	if os.Getenv("CICY_TEST_LIVE_TRANSLATION") != "1" {
		t.Skip("set CICY_TEST_LIVE_TRANSLATION=1 to call the configured provider")
	}
	got, err := translateTextViaProvider(
		"This skill drives system-installed Google Chrome on a connected desktop host.",
		"zh-CN",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "技能") && !strings.Contains(got, "Chrome") {
		t.Fatalf("expected a Chinese translation, got %q", got)
	}
}
