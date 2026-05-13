package voice

import "testing"

func TestDetectEncoding(t *testing.T) {
	cases := map[string]string{
		"voice.wav":  "LINEAR16",
		"voice.flac": "FLAC",
		"voice.ogg":  "OGG_OPUS",
		"voice.opus": "OGG_OPUS",
		"voice.mp3":  "MP3",
		"voice.webm": "WEBM_OPUS",
		"voice.bin":  "",
	}
	for filename, want := range cases {
		if got := DetectEncoding(filename); got != want {
			t.Fatalf("DetectEncoding(%q) = %q, want %q", filename, got, want)
		}
	}
}

func TestDeriveLanguageCode(t *testing.T) {
	cases := map[string]string{
		"en-US-Standard-A": "en-US",
		"cmn-CN-Wavenet-A": "cmn-CN",
		"badvoice":         "",
	}
	for input, want := range cases {
		if got := DeriveLanguageCode(input); got != want {
			t.Fatalf("DeriveLanguageCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHasScope(t *testing.T) {
	scopes := splitScopes("https://mail.google.com/ https://www.googleapis.com/auth/cloud-platform")
	if !hasScope(scopes, cloudPlatformScope) {
		t.Fatal("expected cloud platform scope")
	}
	if hasScope(scopes, "missing") {
		t.Fatal("unexpected missing scope")
	}
}
