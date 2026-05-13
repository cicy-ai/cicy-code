package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cicy-ai/cicy-skills/internal/voice"
	"github.com/cicy-ai/cicy-skills/internal/voicecmd"
)

type ttsClient interface {
	TTSHealth(context.Context) (voice.HealthStatus, error)
	GetVoicePreference() (voice.VoicePreference, error)
	SetVoicePreference(voice.VoicePreference) error
	ListVoices(context.Context, string) ([]voice.TTSVoice, error)
	TTS(context.Context, voice.TTSRequest) ([]byte, error)
}

var newTTSClient = func(timeout time.Duration) (ttsClient, error) {
	return voice.NewClient(timeout)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if err := execute(args, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func execute(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "set-voice":
			return executeSetVoice(args[1:], stdout, stderr)
		case "list-voices":
			return executeListVoices(args[1:], stdout, stderr)
		}
	}

	fs := flag.NewFlagSet("tts", flag.ContinueOnError)
	fs.SetOutput(stderr)

	language := fs.String("language", envOrDefault("CICY_TTS_LANGUAGE", ""), "Voice language code, for example en-US")
	voiceName := fs.String("voice", "", "Google voice name, for example en-US-Standard-C")
	output := fs.String("output", "", "Output path. Use - for stdout")
	fs.StringVar(output, "o", "", "Output path. Use - for stdout")
	textFile := fs.String("text-file", "", "Read synthesis text from a file")
	audioEncoding := fs.String("audio-encoding", "MP3", "Audio encoding, for example MP3 or LINEAR16")
	speakingRate := fs.Float64("speaking-rate", 0, "Speaking rate")
	pitch := fs.Float64("pitch", 0, "Pitch")
	ssml := fs.Bool("ssml", false, "Treat input text as SSML")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")

	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, ttsUsage())
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	client, err := newTTSClient(*timeout)
	if err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 1 && rest[0] == "health" {
		status, err := client.TTSHealth(context.Background())
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
	if len(rest) == 1 && rest[0] == "get-voice" {
		pref, err := client.GetVoicePreference()
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(pref, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(data))
		return nil
	}
	if len(rest) == 2 && rest[0] == "set-voice" {
		pref := voice.VoicePreference{
			LanguageCode: strings.TrimSpace(*language),
			VoiceName:    strings.TrimSpace(rest[1]),
		}
		if err := client.SetVoicePreference(pref); err != nil {
			return err
		}
		saved, err := client.GetVoicePreference()
		if err != nil {
			return err
		}
		data, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, voicecmd.PrettyJSON(data))
		return nil
	}
	if len(rest) == 1 && rest[0] == "list-voices" {
		voices, err := client.ListVoices(context.Background(), strings.TrimSpace(*language))
		if err != nil {
			return err
		}
		for _, item := range voices {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%d\n", item.Name, strings.Join(item.LanguageCodes, ","), item.SsmlGender, item.NaturalSampleHtz)
		}
		return nil
	}

	text, err := synthText(*textFile, rest)
	if err != nil {
		fs.Usage()
		return err
	}

	pref, err := client.GetVoicePreference()
	if err != nil {
		return err
	}
	lang := strings.TrimSpace(*language)
	if lang == "" {
		lang = strings.TrimSpace(pref.LanguageCode)
	}
	voiceChoice := strings.TrimSpace(*voiceName)
	if voiceChoice == "" {
		voiceChoice = strings.TrimSpace(pref.VoiceName)
	}
	if lang == "" && voiceChoice != "" {
		lang = voice.DeriveLanguageCode(voiceChoice)
	}

	req := voice.TTSRequest{
		LanguageCode:  lang,
		VoiceName:     voiceChoice,
		AudioEncoding: strings.TrimSpace(*audioEncoding),
		SpeakingRate:  *speakingRate,
		Pitch:         *pitch,
	}
	if *ssml {
		req.SSML = text
	} else {
		req.Text = text
	}

	audio, err := client.TTS(context.Background(), req)
	if err != nil {
		return err
	}

	target := resolveOutput(*output)
	if target == "-" {
		_, _ = stdout.Write(audio)
		return nil
	}
	if err := os.WriteFile(target, audio, 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, target)
	return nil
}

func synthText(textFile string, args []string) (string, error) {
	if strings.TrimSpace(textFile) != "" {
		if len(args) > 0 {
			return "", fmt.Errorf("text-file cannot be combined with inline text")
		}
		data, err := os.ReadFile(textFile)
		if err != nil {
			return "", err
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("text file is empty")
		}
		return text, nil
	}
	if len(args) == 0 {
		return "", fmt.Errorf("text is required")
	}
	return strings.TrimSpace(strings.Join(args, " ")), nil
}

func resolveOutput(output string) string {
	output = strings.TrimSpace(output)
	if output != "" {
		return output
	}
	info, err := os.Stdout.Stat()
	if err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		return "-"
	}
	return "tts-output.mp3"
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func executeSetVoice(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tts set-voice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", envOrDefault("CICY_TTS_LANGUAGE", ""), "Voice language code")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage:\n  tts set-voice [--language LANG] <voice-name>")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("usage: tts set-voice [--language LANG] <voice-name>")
	}
	client, err := newTTSClient(*timeout)
	if err != nil {
		return err
	}
	pref := voice.VoicePreference{
		LanguageCode: strings.TrimSpace(*language),
		VoiceName:    strings.TrimSpace(rest[0]),
	}
	if err := client.SetVoicePreference(pref); err != nil {
		return err
	}
	saved, err := client.GetVoicePreference()
	if err != nil {
		return err
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, voicecmd.PrettyJSON(data))
	return nil
}

func executeListVoices(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tts list-voices", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", envOrDefault("CICY_TTS_LANGUAGE", ""), "Voice language code")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage:\n  tts list-voices [--language LANG]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	client, err := newTTSClient(*timeout)
	if err != nil {
		return err
	}
	voices, err := client.ListVoices(context.Background(), strings.TrimSpace(*language))
	if err != nil {
		return err
	}
	for _, item := range voices {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%d\n", item.Name, strings.Join(item.LanguageCodes, ","), item.SsmlGender, item.NaturalSampleHtz)
	}
	return nil
}

func ttsUsage() string {
	return `tts

Usage:
  tts health
  tts get-voice
  tts set-voice [--language LANG] <voice-name>
  tts list-voices [--language LANG]
  tts [flags] <text>
  tts [flags] --text-file input.txt

Flags:
  --language        Voice language code, for example en-US
  --voice           Google voice name, for example en-US-Standard-C
  -o, --output      Output path. Use - for stdout. Default: stdout when piped, otherwise tts-output.mp3
  --text-file       Read synthesis text from a file
  --audio-encoding  Audio encoding, for example MP3 or LINEAR16
  --speaking-rate   Speaking rate
  --pitch           Pitch
  --ssml            Treat input text as SSML
  --timeout         HTTP timeout (default 30s)

Google Auth:
  Reads GOOGLE_CLOUD_REFRESH_TOKEN if present, otherwise falls back to GMAIL_REFRESH_TOKEN.
  The token must include https://www.googleapis.com/auth/cloud-platform.

Examples:
  tts "Hello world"
  tts --language en-US --voice en-US-Standard-C "Hello world"
  tts set-voice --language en-US en-US-Standard-C
  tts list-voices --language en-US
  tts health`
}
