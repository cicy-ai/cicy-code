package main

// Universal IM voice-to-text helper.
// Accepts audio bytes from any IM transport, converts to a format the STT
// backend understands, and returns transcribed text.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	voiceMaxBytes  = 100 << 20 // 100 MB
	silkSampleRate = 24000
)

// imTranscribeVoice converts audio to a format the STT backend understands
// and calls the local /api/stt endpoint. Returns the transcribed text.
func imTranscribeVoice(audio []byte, format, filename string) (string, error) {
	if len(audio) == 0 {
		return "", fmt.Errorf("empty audio")
	}
	if len(audio) > voiceMaxBytes {
		return "", fmt.Errorf("audio too large: %d bytes", len(audio))
	}

	converted, err := imConvertToSTTFormat(audio, format, filename)
	if err != nil {
		return "", fmt.Errorf("convert format: %w", err)
	}

	text, err := imCallSTT(converted, safeVoiceFilename(filename))
	if err != nil {
		return "", fmt.Errorf("stt: %w", err)
	}
	return strings.TrimSpace(text), nil
}

// imConvertToSTTFormat converts audio to a format the STT backend understands.
func imConvertToSTTFormat(audio []byte, format, filename string) ([]byte, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	ext := strings.ToLower(filepath.Ext(filename))

	switch format {
	case "silk":
		return silkToWAV(audio)
	case "":
		switch ext {
		case ".silk":
			return silkToWAV(audio)
		case ".amr":
			return amrToWAV(audio)
		default:
			return audio, nil
		}
	default:
		return audio, nil
	}
}

// silkToWAV converts SILK-encoded audio to WAV using ffmpeg.
func silkToWAV(silk []byte) ([]byte, error) {
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.Command(ffmpegPath,
			"-f", "silk", "-i", "pipe:0",
			"-f", "wav", "-ar", fmt.Sprintf("%d", silkSampleRate),
			"-ac", "1", "-acodec", "pcm_s16le",
			"pipe:1",
		)
		cmd.Stdin = bytes.NewReader(silk)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			log.Printf("[im-voice] ffmpeg silk→wav: %d→%d bytes", len(silk), out.Len())
			return out.Bytes(), nil
		} else {
			log.Printf("[im-voice] ffmpeg silk→wav failed: %v, stderr=%s", err, stderr.String())
		}
	}
	// Fallback: wrap raw SILK bytes as WAV (probably SILK-encoded PCM)
	log.Printf("[im-voice] ffmpeg not available, wrapping raw bytes (%d) as WAV", len(silk))
	return pcmToWAV(silk, silkSampleRate), nil
}

// amrToWAV converts AMR audio to WAV using ffmpeg.
func amrToWAV(amr []byte) ([]byte, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found for AMR conversion")
	}
	cmd := exec.Command(ffmpegPath,
		"-f", "amr", "-i", "pipe:0",
		"-f", "wav", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(amr)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg amr→wav: %w", err)
	}
	return out.Bytes(), nil
}

// pcmToWAV wraps raw PCM s16le mono bytes in a WAV container.
func pcmToWAV(pcm []byte, sampleRate int) []byte {
	dataLen := len(pcm)
	total := 44 + dataLen
	buf := make([]byte, total)

	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(total-8))
	copy(buf[8:12], "WAVE")

	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)

	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	copy(buf[44:], pcm)

	return buf
}

// imCallSTT sends audio to the local /api/stt endpoint and returns text.
func imCallSTT(audio []byte, filename string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	url := fmt.Sprintf("http://127.0.0.1:%s/api/stt", runtimeAPIBasePort())

	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// /api/stt 端点经过 authM 包装，需要带 cicy global token。
	if token := strings.TrimSpace(getFirstToken()); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("stt %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Try {"text":"..."} format (OpenAI/Cloudflare Whisper via our wrapper)
	var simple struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &simple); err == nil && simple.Text != "" {
		return simple.Text, nil
	}

	// Try Google-style {"results":[{"alternatives":[{"transcript":"..."}]}]}
	var google struct {
		Results []struct {
			Alternatives []struct {
				Transcript string `json:"transcript"`
			} `json:"alternatives"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &google); err == nil {
		var lines []string
		for _, r := range google.Results {
			if len(r.Alternatives) > 0 && r.Alternatives[0].Transcript != "" {
				lines = append(lines, r.Alternatives[0].Transcript)
			}
		}
		if len(lines) > 0 {
			return strings.Join(lines, "\n"), nil
		}
	}

	if len(respBody) > 0 {
		return "", fmt.Errorf("unexpected STT response: %s", string(respBody[:min(len(respBody), 200)]))
	}
	return "", fmt.Errorf("STT returned empty response")
}

func safeVoiceFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "voice.wav"
	}
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "voice.wav"
	}
	return s
}
