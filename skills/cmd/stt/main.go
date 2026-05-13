package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cicy-ai/cicy-skills/internal/voice"
	"github.com/cicy-ai/cicy-skills/internal/voicecmd"
)

type sttClient interface {
	STTHealth(context.Context) (voice.HealthStatus, error)
	STT(context.Context, voice.STTRequest) (voice.STTResponse, error)
}

var newSTTClient = func(timeout time.Duration) (sttClient, error) {
	return voice.NewClient(timeout)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if err := execute(args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func execute(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stt", flag.ContinueOnError)
	fs.SetOutput(stderr)

	language := fs.String("language", envOrDefault("CICY_STT_LANGUAGE", "en-US"), "Recognition language, for example en-US")
	encoding := fs.String("encoding", "", "Audio encoding, for example LINEAR16, FLAC, OGG_OPUS, MP3")
	sampleRate := fs.Int("sample-rate", 0, "Sample rate in Hertz")
	model := fs.String("model", "", "Google Speech model, for example latest_long")
	maxAlternatives := fs.Int("max-alternatives", 1, "Maximum alternative transcripts")
	autoPunctuation := fs.Bool("auto-punctuation", true, "Enable automatic punctuation")
	jsonOutput := fs.Bool("json", false, "Print the full JSON response")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	filename := fs.String("filename", "audio.bin", "Filename to use when reading audio from stdin")

	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, sttUsage())
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	client, err := newSTTClient(*timeout)
	if err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 1 && rest[0] == "health" {
		status, err := client.STTHealth(context.Background())
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(data))
		return nil
	}

	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("usage: stt [flags] <audio-file>|-")
	}

	audio, name, err := readAudio(rest[0], *filename, stdin)
	if err != nil {
		return err
	}

	req := voice.STTRequest{
		Filename:                   name,
		Audio:                      audio,
		LanguageCode:               *language,
		Encoding:                   strings.TrimSpace(*encoding),
		SampleRateHertz:            *sampleRate,
		Model:                      strings.TrimSpace(*model),
		MaxAlternatives:            *maxAlternatives,
		EnableAutomaticPunctuation: *autoPunctuation,
	}
	if req.Encoding == "" {
		req.Encoding = voice.DetectEncoding(name)
	}
	if req.SampleRateHertz == 0 {
		req.SampleRateHertz = voice.GuessSampleRate(name, audio, req.Encoding)
	}

	resp, err := client.STT(context.Background(), req)
	if err != nil {
		return err
	}

	if *jsonOutput {
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(data))
		return nil
	}

	_, _ = fmt.Fprintln(stdout, strings.TrimSpace(voice.TranscriptText(resp)))
	return nil
}

func readAudio(path, stdinFilename string, stdin io.Reader) ([]byte, string, error) {
	if strings.TrimSpace(path) == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, "", err
		}
		return data, strings.TrimSpace(stdinFilename), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.Base(path), nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func sttUsage() string {
	return `stt

Usage:
  stt health
  stt [flags] <audio-file>
  cat voice.ogg | stt [flags] -

Flags:
  --language          Recognition language, default en-US or $CICY_STT_LANGUAGE
  --encoding          Audio encoding, for example LINEAR16, FLAC, OGG_OPUS, MP3
  --sample-rate       Sample rate in Hertz
  --model             Google Speech model, for example latest_long
  --max-alternatives  Maximum alternative transcripts
  --auto-punctuation  Enable automatic punctuation (default true)
  --json              Print the full JSON response
  --timeout           HTTP timeout (default 30s)
  --filename          Filename to use when reading audio from stdin

Google Auth:
  Reads GOOGLE_CLOUD_REFRESH_TOKEN if present, otherwise falls back to GMAIL_REFRESH_TOKEN.
  The token must include https://www.googleapis.com/auth/cloud-platform.

Examples:
  stt voice.wav
  stt --language cmn-Hans-CN voice.ogg
  stt --encoding OGG_OPUS --sample-rate 48000 voice.ogg
  cat voice.wav | stt --filename voice.wav -
  stt health`
}

func prettyJSON(data []byte) string {
	return voicecmd.PrettyJSON(data)
}
