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

type fakeSTTClient struct {
	healthResp  voice.HealthStatus
	healthErr   error
	sttResp     voice.STTResponse
	sttErr      error
	lastRequest voice.STTRequest
}

func (f *fakeSTTClient) STTHealth(context.Context) (voice.HealthStatus, error) {
	return f.healthResp, f.healthErr
}

func (f *fakeSTTClient) STT(_ context.Context, req voice.STTRequest) (voice.STTResponse, error) {
	f.lastRequest = req
	return f.sttResp, f.sttErr
}

func TestRunHealth(t *testing.T) {
	fake := &fakeSTTClient{
		healthResp: voice.HealthStatus{
			OK:                    true,
			Provider:              "google-speech-to-text",
			HasCloudPlatformScope: true,
			Scopes:                []string{"https://www.googleapis.com/auth/cloud-platform"},
		},
	}
	old := newSTTClient
	newSTTClient = func(time.Duration) (sttClient, error) { return fake, nil }
	defer func() { newSTTClient = old }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{"health"}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"provider": "google-speech-to-text"`) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunRecognizeFile(t *testing.T) {
	fake := &fakeSTTClient{
		sttResp: voice.STTResponse{
			Results: []voice.STTResult{{
				Alternatives: []voice.STTAlternative{{Transcript: "recognized from google"}},
			}},
		},
	}
	old := newSTTClient
	newSTTClient = func(time.Duration) (sttClient, error) { return fake, nil }
	defer func() { newSTTClient = old }()

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "voice.wav")
	audio := make([]byte, 44)
	copy(audio[:4], []byte("RIFF"))
	copy(audio[8:12], []byte("WAVE"))
	audio[24] = 0x80
	audio[25] = 0x3e
	if err := os.WriteFile(audioPath, audio, 0o644); err != nil {
		t.Fatalf("write temp audio: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{audioPath}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "recognized from google" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if fake.lastRequest.Encoding != "LINEAR16" {
		t.Fatalf("unexpected encoding: %q", fake.lastRequest.Encoding)
	}
}

func TestRunRecognizeStdinJSON(t *testing.T) {
	fake := &fakeSTTClient{
		sttResp: voice.STTResponse{
			Results: []voice.STTResult{{
				Alternatives: []voice.STTAlternative{{Transcript: "recognized from stdin"}},
			}},
		},
	}
	old := newSTTClient
	newSTTClient = func(time.Duration) (sttClient, error) { return fake, nil }
	defer func() { newSTTClient = old }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run([]string{"--json", "--filename", "voice.ogg", "--language", "cmn-Hans-CN", "-"}, strings.NewReader("stdin-audio"), stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"transcript": "recognized from stdin"`) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if fake.lastRequest.Encoding != "OGG_OPUS" {
		t.Fatalf("unexpected encoding: %q", fake.lastRequest.Encoding)
	}
	if fake.lastRequest.SampleRateHertz != 48000 {
		t.Fatalf("unexpected sample rate: %d", fake.lastRequest.SampleRateHertz)
	}
}
