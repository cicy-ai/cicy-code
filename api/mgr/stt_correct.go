// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Speech-to-text post-processing — two layers, both driven by what the agent
// is currently working on (its title, projects, and the user's recent prompts):
//
//  1. a Whisper `prompt` that carries that vocabulary, so proper nouns, agent /
//     machine names and mixed Chinese-English come out right in the first place;
//  2. an LLM pass that fixes homophone and proper-noun mistakes the recogniser
//     still made, with strict guards: only recognition errors and punctuation,
//     no rewording, no answering, no translating, and the result is rejected
//     when its length drifts too far from the raw transcript (the model went
//     off-script) — the raw text is then returned unchanged.
//
// Provider: providers.default["stt_correct"], else providers.default["translate"],
// else the first openai-protocol provider with a key. Failures never fail the
// request: the raw transcript is returned with corrected=false.

const (
	sttCorrectMinRunes    = 4   // shorter than this → nothing to fix
	sttCorrectMaxRunes    = 800 // longer → skip (cost / latency; dictation, not commands)
	sttContextPromptCount = 6
	sttContextPromptRunes = 120
	sttWhisperPromptRunes = 600 // Whisper only honours the tail of a long prompt
)

type sttAgentContext struct {
	Title    string
	Projects []string
	Prompts  []string
}

func loadSTTAgentContext(agentID string) sttAgentContext {
	ctx := sttAgentContext{}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || store == nil {
		return ctx
	}
	pane := normPaneID(agentID)
	_ = store.QueryRow("SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=?", pane).Scan(&ctx.Title)
	if rows, err := store.Query("SELECT g.name FROM group_windows gw JOIN agent_groups g ON g.id = gw.group_id WHERE gw.win_id=? ORDER BY gw.id", pane); err == nil {
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && strings.TrimSpace(name) != "" {
				ctx.Projects = append(ctx.Projects, strings.TrimSpace(name))
			}
		}
		rows.Close()
	}
	short := shortPaneID(agentID)
	if snapshot, err := aiGatewayReadCurrentSnapshotCached(short); err == nil {
		conv, _ := agentHistoryCurrentMaxIDFrom(snapshot, "")
		prompts := aiGatewayBuildCurrentPrompts(short, conv, snapshot.Body, snapshot.Timestamp)
		if n := len(prompts); n > sttContextPromptCount {
			prompts = prompts[n-sttContextPromptCount:]
		}
		for _, p := range prompts {
			if t := strings.TrimSpace(p.Content); t != "" {
				ctx.Prompts = append(ctx.Prompts, truncateRunes(strings.Join(strings.Fields(t), " "), sttContextPromptRunes))
			}
		}
	}
	return ctx
}

func (c sttAgentContext) empty() bool {
	return c.Title == "" && len(c.Projects) == 0 && len(c.Prompts) == 0
}

// sttWhisperPrompt is the recogniser-side hint: a short, natural passage in the
// user's mixed register that names the vocabulary the speech is likely to
// contain. Whisper biases towards words that appear in it.
func sttWhisperPrompt(c sttAgentContext) string {
	if c.empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("CiCy、cicy-code、cicy-mobile、Agent、hub、frp、OTA、API、token。")
	if c.Title != "" {
		b.WriteString("Agent：" + c.Title + "。")
	}
	if len(c.Projects) > 0 {
		b.WriteString("项目：" + strings.Join(c.Projects, "、") + "。")
	}
	for _, p := range c.Prompts {
		b.WriteString(p + " ")
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > sttWhisperPromptRunes {
		r := []rune(out)
		out = string(r[len(r)-sttWhisperPromptRunes:])
	}
	return out
}

func sttCorrectionProvider() (*providerConfig, string, bool) {
	cfg := loadProvidersConfig()
	if cfg == nil {
		return nil, "", false
	}
	for _, key := range []string{cfg.Default["stt_correct"], cfg.Default["translate"]} {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if p, ok := loadProviderByKey(key); ok && strings.EqualFold(p.Protocol, "openai") && strings.TrimSpace(p.APIKey) != "" && strings.TrimSpace(p.URL) != "" {
			return p, key, true
		}
	}
	for i := range cfg.Items {
		p := &cfg.Items[i]
		if strings.EqualFold(p.Protocol, "openai") && strings.TrimSpace(p.APIKey) != "" && strings.TrimSpace(p.URL) != "" && !strings.Contains(strings.ToLower(p.DefaultModel), "whisper") {
			return p, p.Key, true
		}
	}
	return nil, "", false
}

const sttCorrectSystemPrompt = `你是语音转写纠错器。用户给你的是语音识别(ASR)直接输出的文字，可能有同音字/近音字错误、专有名词识别错误、中英文混排被音译、缺少标点和分句。
你的任务只有一个：在不改变原意的前提下修正识别错误，并补上合适的标点和分句。
规则：
- 只改识别错误；不改写措辞，不增删内容，不总结，不回答问题，不翻译。
- 对照"上下文"里出现的产品名、项目名、Agent 名、机器名、命令、英文术语来修正同音错误；不确定的保持原样。
- 保持原有的中英文混排；英文术语保留英文。
- 只输出修正后的文本，不要任何解释、引号或前缀。`

// sttCorrectTranscript runs the LLM pass. Returns (text, corrected, err):
// corrected=false with err=nil means "kept the raw text on purpose".
func sttCorrectTranscript(raw string, c sttAgentContext) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	n := utf8.RuneCountInString(raw)
	if n < sttCorrectMinRunes || n > sttCorrectMaxRunes {
		return raw, false, nil
	}
	provider, _, ok := sttCorrectionProvider()
	if !ok {
		return raw, false, fmt.Errorf("no correction provider configured")
	}
	model := strings.TrimSpace(provider.DefaultModel)
	if model == "" || strings.Contains(strings.ToLower(model), "whisper") {
		model = "deepseek-v4-flash"
	}
	var user strings.Builder
	if !c.empty() {
		user.WriteString("上下文：\n")
		if c.Title != "" {
			user.WriteString("- Agent：" + c.Title + "\n")
		}
		if len(c.Projects) > 0 {
			user.WriteString("- 项目：" + strings.Join(c.Projects, "、") + "\n")
		}
		for _, p := range c.Prompts {
			user.WriteString("- 最近对话：" + p + "\n")
		}
		user.WriteString("\n")
	}
	user.WriteString("ASR 原文：\n" + raw)
	payload := M{
		"model": model,
		"messages": []M{
			{"role": "system", "content": sttCorrectSystemPrompt},
			{"role": "user", "content": user.String()},
		},
		"temperature": 0,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return raw, false, err
	}
	req, err := http.NewRequest(http.MethodPost, openAIChatCompletionsURL(strings.TrimRight(strings.TrimSpace(provider.URL), "/")), bytes.NewReader(body))
	if err != nil {
		return raw, false, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(provider.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return raw, false, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return raw, false, err
	}
	if resp.StatusCode >= 400 {
		return raw, false, fmt.Errorf("correction provider %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(respBody)), 200))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || len(result.Choices) == 0 {
		return raw, false, fmt.Errorf("correction empty")
	}
	out := sttCleanCorrection(result.Choices[0].Message.Content)
	if !sttCorrectionPlausible(raw, out) {
		return raw, false, nil
	}
	return out, out != raw, nil
}

// sttCleanCorrection strips the wrappers a model tends to add despite being
// told not to (quotes, code fences, a "修正后：" label).
func sttCleanCorrection(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	for _, label := range []string{"修正后：", "修正后:", "修正：", "修正:", "Corrected:", "corrected:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, label))
	}
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
		if strings.HasPrefix(s, "“") && strings.HasSuffix(s, "”") {
			s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "“"), "”"))
		}
	}
	return s
}

// sttCorrectionPlausible rejects a "correction" that is not a light edit of the
// raw transcript — a paraphrase, an answer, a translation — by length: the
// letter count must stay within 60–150 % of the original.
func sttCorrectionPlausible(raw, out string) bool {
	if out == "" {
		return false
	}
	countLetters := func(s string) int {
		n := 0
		for _, r := range s {
			if r > ' ' && !strings.ContainsRune("，。！？、；：,.!?;:\"'“”‘’()（）[]【】-—…", r) {
				n++
			}
		}
		return n
	}
	a, b := countLetters(raw), countLetters(out)
	if a == 0 {
		return false
	}
	ratio := float64(b) / float64(a)
	return ratio >= 0.6 && ratio <= 1.5
}
