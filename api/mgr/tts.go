package main

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const ttsDir = "/tmp/tts"

func init() {
	os.MkdirAll(ttsDir, 0755)
}

func handleTTS(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" {
		httpErr(w, 400, "missing text")
		return
	}
	if len(text) > 2000 {
		text = text[:2000]
	}
	voice := r.URL.Query().Get("voice")
	if voice == "" {
		voice = "zh-CN-XiaoyiNeural"
	}

	hash := fmt.Sprintf("%x", md5.Sum([]byte(voice+":"+text)))
	mp3 := filepath.Join(ttsDir, hash+".mp3")

	// 有缓存直接返回
	if _, err := os.Stat(mp3); err == nil {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, mp3)
		return
	}

	// 找 edge-tts
	bin := "edge-tts"
	for _, p := range []string{
		filepath.Join(os.Getenv("HOME"), ".local/bin/edge-tts"),
		"/usr/local/bin/edge-tts",
	} {
		if _, err := os.Stat(p); err == nil {
			bin = p
			break
		}
	}

	cmd := exec.Command(bin, "--voice", voice, "--text", text, "--write-media", mp3)
	if out, err := cmd.CombinedOutput(); err != nil {
		httpErr(w, 500, "tts failed: "+strings.TrimSpace(string(out)))
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, mp3)
}
