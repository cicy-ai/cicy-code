// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// 豆包语音 (volcengine speech) test client — the runtime behind the provider
// dashboard's 「语音测试」 panel for protocol:"voice" providers.
//
// Two vendor protocols, neither of which is chat completions:
//   TTS  POST {base}/api/v3/tts/unidirectional — JSON-lines stream, each line
//        {code,data:<base64 audio chunk>}; format "mp3" for whole-clip tests,
//        "pcm" (16LE mono 24k) for streaming playback. Streaming vs not is the
//        body's format field — the Resource-Id does NOT change.
//   ASR  wss {base}/api/v3/sauc/bigmodel_async — custom binary frames
//        (4-byte header + BE length + gzip payload). The browser cannot set
//        X-Api-* headers on a websocket, so handleVoiceASR bridges: browser WS
//        (binary PCM16@16k in, JSON transcripts out) ⇄ upstream volc WS.
//
// Auth per request: X-Api-Key = the provider's apiKey (a UUID from the
// volcengine 语音技术 console — NOT an ark-* key), X-Api-Resource-Id per
// capability. Protocol ported from cicy-pet's renderer-server.js/volc-asr.js.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	doubaoTTSPath = "/api/v3/tts/unidirectional"
	doubaoASRPath = "/api/v3/sauc/bigmodel_async"
	// ASR resource ids: 2.0 first, silent fallback to 1.0 on 403 (2.0 not
	// enabled on the account) — same behavior cicy-pet ships.
	doubaoASRResource         = "volc.seedasr.sauc.duration"
	doubaoASRResourceFallback = "volc.bigasr.sauc.duration"
)

var doubaoLegacySpeaker = regexp.MustCompile(`(_moon_bigtts$|^BV\d+(_24k)?_streaming$)`)

// resolveDoubaoTTSResourceID: seed-tts-1.0 for legacy moon/BV speakers,
// seed-tts-2.0 for everything else (all *_uranus_bigtts 2.0 音色).
func resolveDoubaoTTSResourceID(speaker string) string {
	if doubaoLegacySpeaker.MatchString(speaker) {
		return "seed-tts-1.0"
	}
	return "seed-tts-2.0"
}

// voiceProviderByKey loads a provider and insists it is a voice provider.
func voiceProviderByKey(key string) (*providerConfig, error) {
	pc, ok := loadProviderByKey(strings.TrimSpace(key))
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", key)
	}
	if !strings.EqualFold(strings.TrimSpace(pc.Protocol), "voice") {
		return nil, fmt.Errorf("provider %s is not a voice provider", key)
	}
	if strings.TrimSpace(pc.APIKey) == "" {
		return nil, fmt.Errorf("provider %s has no apiKey — 前往火山「语音技术」控制台申请(UUID 格式,非 ark- key)", key)
	}
	return pc, nil
}

// doubaoTTSProbe is the voice provider's connection test: synthesize ONE
// character with the given speaker and report success/latency, or the vendor's
// error verbatim ("resource not enabled", bad key, …). Called from
// handleProviderTest for protocol "voice"; model doubles as the speaker id.
func doubaoTTSProbe(baseURL, apiKey, speaker string) providerTestResult {
	endpoint := strings.TrimRight(baseURL, "/") + doubaoTTSPath
	if strings.TrimSpace(apiKey) == "" {
		return providerTestResult{OK: false, Endpoint: endpoint, Detail: "apiKey is empty — 火山「语音技术」控制台的 UUID key"}
	}
	speaker = strings.TrimSpace(speaker)
	if speaker == "" {
		speaker = "zh_female_shuangkuaisisi_uranus_bigtts"
	}
	payload, _ := json.Marshal(M{
		"user":       M{"uid": "cicy-code"},
		"req_params": M{"text": "测", "speaker": speaker, "audio_params": M{"format": "mp3", "sample_rate": 24000}},
	})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return providerTestResult{OK: false, Endpoint: endpoint, Detail: err.Error()}
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("X-Api-Resource-Id", resolveDoubaoTTSResourceID(speaker))
	req.Header.Set("X-Api-Request-Id", fmt.Sprintf("cicy_probe_%d", time.Now().UnixNano()))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return providerTestResult{OK: false, Endpoint: endpoint, Detail: err.Error(), DurationMS: time.Since(start).Milliseconds()}
	}
	defer resp.Body.Close()
	dur := time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return providerTestResult{OK: false, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Detail: strings.TrimSpace(string(body)), Model: speaker}
	}
	audioBytes := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "data:"))
		if line == "" || line == "[DONE]" || !strings.HasPrefix(line, "{") {
			continue
		}
		var d struct {
			Code       json.Number `json:"code"`
			StatusCode json.Number `json:"status_code"`
			Message    string      `json:"message"`
			StatusText string      `json:"status_text"`
			Data       string      `json:"data"`
		}
		if json.Unmarshal([]byte(line), &d) != nil {
			continue
		}
		code, _ := d.Code.Int64()
		if code == 0 {
			code, _ = d.StatusCode.Int64()
		}
		if code > 0 && code != 20000000 {
			msg := d.Message
			if msg == "" {
				msg = d.StatusText
			}
			return providerTestResult{OK: false, Status: int(code), Endpoint: endpoint, DurationMS: time.Since(start).Milliseconds(), Detail: msg, Model: speaker}
		}
		if d.Data != "" {
			if chunk, err := base64.StdEncoding.DecodeString(d.Data); err == nil {
				audioBytes += len(chunk)
			}
		}
	}
	dur = time.Since(start).Milliseconds()
	if audioBytes == 0 {
		return providerTestResult{OK: false, Endpoint: endpoint, DurationMS: dur, Detail: "no audio data returned", Model: speaker}
	}
	return providerTestResult{OK: true, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Detail: fmt.Sprintf("ok — %d bytes audio", audioBytes), Model: speaker}
}

type voiceTTSRequest struct {
	Key        string `json:"key"`
	Speaker    string `json:"speaker"`
	Text       string `json:"text"`
	Stream     bool   `json:"stream"`
	SpeechRate int    `json:"speech_rate"`
}

// handleVoiceTTS — POST /api/voice/tts
// stream=false → complete audio/mpeg clip (整段合成 test).
// stream=true  → chunked raw PCM16LE mono 24k (流式合成 test); the client
// measures time-to-first-byte for the first-chunk latency figure.
func handleVoiceTTS(w http.ResponseWriter, r *http.Request) {
	var req voiceTTSRequest
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		httpErr(w, 400, "text is required")
		return
	}
	pc, err := voiceProviderByKey(req.Key)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	speaker := strings.TrimSpace(req.Speaker)
	if speaker == "" {
		speaker = strings.TrimSpace(pc.DefaultModel)
	}
	format := "mp3"
	if req.Stream {
		format = "pcm"
	}
	audioParams := M{"format": format, "sample_rate": 24000}
	if req.SpeechRate != 0 {
		rate := req.SpeechRate
		if rate < -50 {
			rate = -50
		}
		if rate > 100 {
			rate = 100
		}
		audioParams["speech_rate"] = rate
	}
	payload, _ := json.Marshal(M{
		"user":       M{"uid": "cicy-code"},
		"req_params": M{"text": req.Text, "speaker": speaker, "audio_params": audioParams},
	})
	endpoint := strings.TrimRight(pc.URL, "/") + doubaoTTSPath
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	httpReq.Header.Set("X-Api-Key", pc.APIKey)
	httpReq.Header.Set("X-Api-Resource-Id", resolveDoubaoTTSResourceID(speaker))
	httpReq.Header.Set("X-Api-Request-Id", fmt.Sprintf("cicy_%d", time.Now().UnixNano()))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(httpReq)
	if err != nil {
		httpErr(w, 502, "doubao request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		// Pass the vendor error through verbatim — "resource not enabled" style
		// errors are exactly what the test panel exists to surface.
		httpErr(w, 502, fmt.Sprintf("doubao %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}

	started := false
	writeChunk := func(chunk []byte) error {
		if !started {
			if req.Stream {
				w.Header().Set("Content-Type", "audio/pcm")
				w.Header().Set("X-Sample-Rate", "24000")
				w.Header().Set("Cache-Control", "no-store")
			}
			started = true
		}
		if req.Stream {
			if _, err := w.Write(chunk); err != nil {
				return err
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		return nil
	}

	var full bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "data:"))
		if line == "" || line == "[DONE]" || !strings.HasPrefix(line, "{") {
			continue
		}
		var d struct {
			Code       json.Number `json:"code"`
			StatusCode json.Number `json:"status_code"`
			Message    string      `json:"message"`
			StatusText string      `json:"status_text"`
			Data       string      `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			continue
		}
		code, _ := d.Code.Int64()
		if code == 0 {
			code, _ = d.StatusCode.Int64()
		}
		if code > 0 && code != 20000000 {
			msg := d.Message
			if msg == "" {
				msg = d.StatusText
			}
			if !started {
				httpErr(w, 502, fmt.Sprintf("doubao %d: %s", code, msg))
			} else {
				log.Printf("[voice] doubao tts mid-stream error %d: %s", code, msg)
			}
			return
		}
		if d.Data == "" {
			continue
		}
		chunk, err := base64.StdEncoding.DecodeString(d.Data)
		if err != nil || len(chunk) == 0 {
			continue
		}
		if req.Stream {
			if writeChunk(chunk) != nil {
				return
			}
		} else {
			full.Write(chunk)
		}
	}
	if err := scanner.Err(); err != nil && !started {
		httpErr(w, 502, "doubao stream read failed: "+err.Error())
		return
	}
	if req.Stream {
		return
	}
	if full.Len() == 0 {
		httpErr(w, 502, "doubao: no audio data returned")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(full.Bytes())
}

// ── ASR binary frame protocol ───────────────────────────────────────────────

const (
	volcMsgFullRequest = 0x1
	volcMsgAudio       = 0x2
	volcMsgResponse    = 0x9
	volcMsgError       = 0xf
	volcFlagNone       = 0x0
	volcFlagLast       = 0x2
	volcSerNone        = 0x0
	volcSerJSON        = 0x1
	volcGzip           = 0x1
)

func volcGzipBytes(p []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(p)
	_ = zw.Close()
	return buf.Bytes()
}

func volcFrame(msgType, flags, ser byte, payload []byte) []byte {
	body := volcGzipBytes(payload)
	out := make([]byte, 0, 8+len(body))
	out = append(out, (0x1<<4)|0x1, (msgType<<4)|flags, (ser<<4)|volcGzip, 0x00)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(body)))
	out = append(out, size[:]...)
	return append(out, body...)
}

func volcFullRequest() []byte {
	payload, _ := json.Marshal(M{
		"user":  M{"uid": "cicy-code"},
		"audio": M{"format": "pcm", "codec": "raw", "rate": 16000, "bits": 16, "channel": 1},
		"request": M{
			"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
			"enable_ddc": false, "result_type": "full", "show_utterances": true,
		},
	})
	return volcFrame(volcMsgFullRequest, volcFlagNone, volcSerJSON, payload)
}

func volcAudioFrame(pcm []byte, last bool) []byte {
	flags := byte(volcFlagNone)
	if last {
		flags = volcFlagLast
	}
	return volcFrame(volcMsgAudio, flags, volcSerNone, pcm)
}

type volcUtterance struct {
	Text     string `json:"text"`
	Definite bool   `json:"definite"`
}

type volcResult struct {
	Text       string          `json:"text"`
	Utterances []volcUtterance `json:"utterances"`
}

// volcParseResponse decodes one upstream frame. Returns (utterances, errMsg,
// isLast, ok).
func volcParseResponse(data []byte) ([]volcUtterance, string, bool, bool) {
	if len(data) < 8 {
		return nil, "", false, false
	}
	headerSize := int(data[0]&0x0f) * 4
	msgType := (data[1] >> 4) & 0x0f
	flags := data[1] & 0x0f
	compression := data[2] & 0x0f
	offset := headerSize

	if msgType == volcMsgError {
		if len(data) < offset+8 {
			return nil, "volc ASR error frame", false, true
		}
		code := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		size := int(binary.BigEndian.Uint32(data[offset:]))
		offset += 4
		msg := ""
		if len(data) >= offset+size {
			msg = string(data[offset : offset+size])
		}
		return nil, fmt.Sprintf("volc ASR error %d: %s", code, msg), false, true
	}
	if msgType != volcMsgResponse {
		return nil, "", false, false
	}
	if flags == 0x1 || flags == 0x3 {
		offset += 4
	}
	if len(data) < offset+4 {
		return nil, "", false, false
	}
	size := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4
	if len(data) < offset+size {
		return nil, "", false, false
	}
	payload := data[offset : offset+size]
	if compression == volcGzip && len(payload) > 0 {
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, "", false, false
		}
		decoded, err := io.ReadAll(io.LimitReader(zr, 4<<20))
		_ = zr.Close()
		if err != nil {
			return nil, "", false, false
		}
		payload = decoded
	}
	if len(payload) == 0 {
		return nil, "", false, false
	}
	var body struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || len(body.Result) == 0 {
		return nil, "", false, false
	}
	var results []volcResult
	if err := json.Unmarshal(body.Result, &results); err != nil {
		var one volcResult
		if err := json.Unmarshal(body.Result, &one); err != nil {
			return nil, "", false, false
		}
		results = []volcResult{one}
	}
	isLast := flags == 0x3
	var utts []volcUtterance
	for _, res := range results {
		utts = append(utts, res.Utterances...)
	}
	if len(utts) == 0 {
		text := ""
		for _, res := range results {
			text += res.Text
		}
		if text != "" {
			utts = []volcUtterance{{Text: text, Definite: isLast}}
		}
	}
	return utts, "", isLast, true
}

// handleVoiceASR — GET /api/voice/asr?key=<provider>&token=<api token> (WS).
// Browser side: binary messages = PCM16LE mono 16k chunks; the text message
// "flush" finalizes (LAST frame upstream). Server side: JSON events
// {type:"transcript",text,definite,seg} / {type:"error",error} / {type:"done"}.
func handleVoiceASR(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("token")
	if t == "" || !verifyToken(t) {
		httpErr(w, 401, "unauthorized")
		return
	}
	pc, err := voiceProviderByKey(r.URL.Query().Get("key"))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sendJSON := func(v any) {
		_ = conn.WriteJSON(v)
	}

	base := strings.TrimRight(pc.URL, "/")
	wsURL := "wss://" + strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://") + doubaoASRPath
	requestID := fmt.Sprintf("cicy-%d", time.Now().UnixNano())

	dial := func(resourceID string) (*websocket.Conn, *http.Response, error) {
		hdr := http.Header{}
		hdr.Set("X-Api-Key", pc.APIKey)
		hdr.Set("X-Api-Resource-Id", resourceID)
		hdr.Set("X-Api-Request-Id", requestID)
		hdr.Set("X-Api-Connect-Id", requestID)
		hdr.Set("X-Api-Sequence", "-1")
		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		return dialer.Dial(wsURL, hdr)
	}
	up, resp, err := dial(doubaoASRResource)
	if err != nil {
		// 2.0 resource not enabled → silent fallback to 1.0 (cicy-pet's behavior).
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			up, _, err = dial(doubaoASRResourceFallback)
		}
		if err != nil {
			sendJSON(M{"type": "error", "error": "volc ASR connect failed: " + err.Error()})
			return
		}
	}
	defer up.Close()
	if err := up.WriteMessage(websocket.BinaryMessage, volcFullRequest()); err != nil {
		sendJSON(M{"type": "error", "error": "volc ASR handshake failed: " + err.Error()})
		return
	}

	// upstream → browser
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, data, err := up.ReadMessage()
			if err != nil {
				return
			}
			utts, errMsg, isLast, ok := volcParseResponse(data)
			if !ok {
				continue
			}
			if errMsg != "" {
				sendJSON(M{"type": "error", "error": errMsg})
				return
			}
			for i, u := range utts {
				if u.Text == "" {
					continue
				}
				sendJSON(M{"type": "transcript", "text": u.Text, "definite": u.Definite, "seg": fmt.Sprintf("v%d", i)})
			}
			if isLast {
				sendJSON(M{"type": "done"})
				return
			}
		}
	}()

	// browser → upstream
	for {
		select {
		case <-done:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			_ = up.WriteMessage(websocket.BinaryMessage, volcAudioFrame(nil, true))
			<-done
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			if err := up.WriteMessage(websocket.BinaryMessage, volcAudioFrame(data, false)); err != nil {
				sendJSON(M{"type": "error", "error": "volc ASR send failed: " + err.Error()})
				return
			}
		case websocket.TextMessage:
			if strings.TrimSpace(string(data)) == "flush" {
				_ = up.WriteMessage(websocket.BinaryMessage, volcAudioFrame(nil, true))
			}
		}
	}
}
