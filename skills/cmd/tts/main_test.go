package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicy-ai/cicy-skills/internal/voice"
)

type fakeTTSClient struct {
	healthResp    voice.HealthStatus
	healthErr     error
	pref          voice.VoicePreference
	getVoiceErr   error
	setVoiceErr   error
	listVoices    []voice.TTSVoice
	listVoicesErr error
	audio         []byte
	ttsErr        error
	lastRequest   voice.TTSRequest
}

func (f *fakeTTSClient) TTSHealth(context.Context) (voice.HealthStatus, error) {
	return f.healthResp, f.healthErr
}

func (f *fakeTTSClient) GetVoicePreference() (voice.VoicePreference, error) {
	return f.pref, f.getVoiceErr
}

func (f *fakeTTSClient) SetVoicePreference(pref voice.VoicePreference) error {
	if f.setVoiceErr != nil {
		return f.setVoiceErr
	}
	if pref.LanguageCode == "" {
		pref.LanguageCode = voice.DeriveLanguageCode(pref.VoiceName)
	}
	f.pref = pref
	return nil
}

func (f *fakeTTSClient) ListVoices(context.Context, string) ([]voice.TTSVoice, error) {
	return f.listVoices, f.listVoicesErr
}

func (f *fakeTTSClient) TTS(_ context.Context, req voice.TTSRequest) ([]byte, error) {
	f.lastRequest = req
	return f.audio, f.ttsErr
}

func TestRunHealth(t *testing.T) {
	fake := &fakeTTSClient{
		healthResp: voice.HealthStatus{OK: true, Provider: "google-text-to-speech"},
	}
	old := newTTSClient
	newTTSClient = func(time.Duration) (ttsClient, error) { return fake, nil }
	defer func() { newTTSClient = old }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{"health"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"provider": "google-text-to-speech"`) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunGetAndSetVoice(t *testing.T) {
	fake := &fakeTTSClient{
		pref: voice.VoicePreference{LanguageCode: "en-US", VoiceName: "en-US-Standard-C"},
	}
	old := newTTSClient
	newTTSClient = func(time.Duration) (ttsClient, error) { return fake, nil }
	defer func() { newTTSClient = old }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := run([]string{"get-voice"}, stdout, stderr); code != 0 {
		t.Fatalf("get-voice exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"voice_name": "en-US-Standard-C"`) {
		t.Fatalf("unexpected get-voice stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--language", "en-US", "set-voice", "en-US-Standard-D"}, stdout, stderr); code != 0 {
		t.Fatalf("set-voice exit code = %d, stderr = %s", code, stderr.String())
	}
	if fake.pref.VoiceName != "en-US-Standard-D" {
		t.Fatalf("unexpected saved voice: %q", fake.pref.VoiceName)
	}
}

func TestRunListVoices(t *testing.T) {
	fake := &fakeTTSClient{
		listVoices: []voice.TTSVoice{{
			Name:             "en-US-Standard-C",
			LanguageCodes:    []string{"en-US"},
			SsmlGender:       "FEMALE",
			NaturalSampleHtz: 24000,
		}},
	}
	old := newTTSClient
	newTTSClient = func(time.Duration) (ttsClient, error) { return fake, nil }
	defer func() { newTTSClient = old }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := run([]string{"list-voices", "--language", "en-US"}, stdout, stderr); code != 0 {
		t.Fatalf("list-voices exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "en-US-Standard-C") {
		t.Fatalf("unexpected list-voices stdout: %q", stdout.String())
	}
}

func TestRunSynthesizeToFile(t *testing.T) {
	fake := &fakeTTSClient{
		pref:  voice.VoicePreference{LanguageCode: "en-US", VoiceName: "en-US-Standard-C"},
		audio: []byte("fake-mp3"),
	}
	old := newTTSClient
	newTTSClient = func(time.Duration) (ttsClient, error) { return fake, nil }
	defer func() { newTTSClient = old }()

	dir := t.TempDir()
	output := filepath.Join(dir, "voice.mp3")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{"-o", output, "hello", "world"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "fake-mp3" {
		t.Fatalf("unexpected output file body: %q", string(data))
	}
	if fake.lastRequest.VoiceName != "en-US-Standard-C" {
		t.Fatalf("unexpected voice in request: %q", fake.lastRequest.VoiceName)
	}
}

func TestRunSynthesizeFromTextFile(t *testing.T) {
	fake := &fakeTTSClient{
		audio: []byte("fake-mp3"),
	}
	old := newTTSClient
	newTTSClient = func(time.Duration) (ttsClient, error) { return fake, nil }
	defer func() { newTTSClient = old }()

	dir := t.TempDir()
	textFile := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(textFile, []byte("hello from file\n"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}
	output := filepath.Join(dir, "voice.mp3")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{"--language", "en-US", "--text-file", textFile, "-o", output}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "fake-mp3" {
		t.Fatalf("unexpected output file body: %q", string(data))
	}
	if fake.lastRequest.LanguageCode != "en-US" {
		t.Fatalf("unexpected language: %q", fake.lastRequest.LanguageCode)
	}
}
