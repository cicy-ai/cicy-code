// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Durable per-agent prompt log: <history>/prompts.jsonl, one human prompt per
// line. current.json only holds the prompts still in the model's context, so a
// compaction or /clear drops the older ones from the prompts-only view. This
// log keeps every prompt that was actually delivered, with its time, so the
// cross-agent "my prompts" list can show the whole history.
//
// Three sources feed it, one per agent kind:
//   - transcript: Claude Code's own session JSONL (typed / queued rows and
//     mid-turn queued_command attachments). Authoritative and append-only, so it
//     also back-fills everything sent before this log existed.
//   - native: the CLI's native session log (kiro etc.), via nativeSessionView.
//   - snapshot: the gateway's current.json prompt list, for agents that have
//     neither of the above. Ingested on every snapshot write, before a later
//     compaction can drop the prompt.

type agentPromptLogEntry struct {
	TS             string `json:"ts"`
	Content        string `json:"content"`
	ConversationID string `json:"conversation_id,omitempty"`
	Source         string `json:"source,omitempty"`
}

const (
	agentPromptLogSourceTranscript = "transcript"
	agentPromptLogSourceNative     = "native"
	agentPromptLogSourceSnapshot   = "snapshot"

	// The same message can reach the log twice: once per row kind inside one
	// source (a queued prompt is both an attachment and a user row), or from two
	// sources whose clocks differ (a snapshot is stamped when the turn starts).
	agentPromptLogSameSourceWindow  = 15 * time.Second
	agentPromptLogCrossSourceWindow = 10 * time.Minute
)

var agentPromptLogLocks sync.Map // agent id → *sync.Mutex

func agentPromptLogLock(agentID string) *sync.Mutex {
	mu, _ := agentPromptLogLocks.LoadOrStore(agentID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func agentPromptLogPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "prompts.jsonl")
}

func agentPromptLogStatePath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "prompts.state.json")
}

// agentPromptLogState remembers how far each transcript has been scanned, so a
// multi-MB session file is only ever read once.
type agentPromptLogState struct {
	Transcripts map[string]int64 `json:"transcripts"`
}

func agentPromptLogNormTS(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return ts
}

// agentPromptLogIsHandoff reports a fork handoff: the harness types it into the
// new agent's pane, so every source labels it as a human prompt.
func agentPromptLogIsHandoff(content string) bool {
	head := content
	if len(head) > 400 {
		head = head[:400]
	}
	return strings.Contains(head, "的分身(fork)")
}

func agentPromptLogReadLocked(agentID string) []agentPromptLogEntry {
	f, err := os.Open(agentPromptLogPath(agentID))
	if err != nil {
		return nil
	}
	defer f.Close()
	out := []agentPromptLogEntry{}
	r := bufio.NewReaderSize(f, 256*1024)
	for {
		line, readErr := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var e agentPromptLogEntry
			if json.Unmarshal(trimmed, &e) == nil && strings.TrimSpace(e.Content) != "" {
				out = append(out, e)
			}
		}
		if readErr != nil {
			break
		}
	}
	return out
}

func agentPromptLogDuplicate(existing []agentPromptLogEntry, norms []string, candidate agentPromptLogEntry, norm string) bool {
	when, whenErr := time.Parse(time.RFC3339, candidate.TS)
	for i, e := range existing {
		if norms[i] != norm {
			continue
		}
		if e.TS == candidate.TS {
			return true
		}
		other, err := time.Parse(time.RFC3339, e.TS)
		if err != nil || whenErr != nil {
			continue
		}
		window := agentPromptLogCrossSourceWindow
		if e.Source == candidate.Source {
			window = agentPromptLogSameSourceWindow
		}
		diff := when.Sub(other)
		if diff < 0 {
			diff = -diff
		}
		if diff <= window {
			return true
		}
	}
	return false
}

// agentPromptLogIngest appends the entries not already in the log. Best-effort:
// a write failure only means the prompt is missing from the list.
func agentPromptLogIngest(agentID string, entries []agentPromptLogEntry) {
	if len(entries) == 0 {
		return
	}
	mu := agentPromptLogLock(agentID)
	mu.Lock()
	defer mu.Unlock()
	agentPromptLogIngestLocked(agentID, entries)
}

func agentPromptLogIngestLocked(agentID string, entries []agentPromptLogEntry) {
	existing := agentPromptLogReadLocked(agentID)
	norms := make([]string, len(existing))
	for i, e := range existing {
		norms[i] = aiGatewayNormPrompt(e.Content)
	}
	var buf bytes.Buffer
	for _, e := range entries {
		e.Content = strings.TrimSpace(e.Content)
		e.TS = agentPromptLogNormTS(e.TS)
		norm := aiGatewayNormPrompt(e.Content)
		if norm == "" || e.TS == "" || agentPromptLogIsHandoff(e.Content) {
			continue
		}
		if agentPromptLogDuplicate(existing, norms, e, norm) {
			continue
		}
		raw, err := json.Marshal(e)
		if err != nil {
			continue
		}
		buf.Write(raw)
		buf.WriteByte('\n')
		existing = append(existing, e)
		norms = append(norms, norm)
	}
	if buf.Len() == 0 {
		return
	}
	path := agentPromptLogPath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(buf.Bytes())
}

var agentPromptLogTranscriptPaths sync.Map // conversation id → transcript path

// agentPromptLogTranscriptPath locates Claude Code's session file for a
// conversation, or "" when the agent is not a Claude Code session. Project
// dirs hold thousands of files, so each one is probed with a single stat
// instead of a glob (which lists the whole directory), and a hit is remembered.
func agentPromptLogTranscriptPath(conversationID string) string {
	cid := strings.TrimSpace(conversationID)
	if cid == "" || sanitizeConvID(cid) != cid {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "projects")
	if cached, ok := agentPromptLogTranscriptPaths.Load(cid); ok {
		path := cached.(string)
		if strings.HasPrefix(path, root+string(filepath.Separator)) {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		agentPromptLogTranscriptPaths.Delete(cid)
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, d := range dirs {
		path := filepath.Join(root, d.Name(), cid+".jsonl")
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			agentPromptLogTranscriptPaths.Store(cid, path)
			return path
		}
	}
	return ""
}

type agentPromptLogTranscriptRow struct {
	Type         string `json:"type"`
	PromptSource string `json:"promptSource"`
	Timestamp    string `json:"timestamp"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment struct {
		Type        string `json:"type"`
		Prompt      string `json:"prompt"`
		CommandMode string `json:"commandMode"`
		Origin      struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	} `json:"attachment"`
}

var (
	agentPromptLogMarkPromptSource = []byte(`"promptSource"`)
	agentPromptLogMarkQueued       = []byte(`"queued_command"`)
)

// agentPromptLogScanTranscript reads the complete lines after offset and returns
// the human prompts in them plus the new offset. Transcript lines can be whole
// request bodies, so a line is only decoded when it carries a prompt marker.
func agentPromptLogScanTranscript(path, conversationID string, offset int64) ([]agentPromptLogEntry, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, offset
	}
	if fi.Size() < offset {
		offset = 0 // rotated or rewritten: start over, the log de-duplicates
	}
	if fi.Size() == offset {
		return nil, offset
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, offset
		}
	}
	out := []agentPromptLogEntry{}
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			offset += int64(len(line))
			if bytes.Contains(line, agentPromptLogMarkPromptSource) || bytes.Contains(line, agentPromptLogMarkQueued) {
				if text, ts := agentPromptLogTranscriptPrompt(line); text != "" {
					out = append(out, agentPromptLogEntry{TS: ts, Content: text, ConversationID: conversationID, Source: agentPromptLogSourceTranscript})
				}
			}
		}
		if readErr != nil {
			break // a trailing partial line is re-read next time
		}
	}
	return out, offset
}

func agentPromptLogTranscriptPrompt(line []byte) (string, string) {
	var row agentPromptLogTranscriptRow
	if json.Unmarshal(bytes.TrimRight(line, "\n"), &row) != nil {
		return "", ""
	}
	text := ""
	switch {
	case row.Type == "user" && (row.PromptSource == "typed" || row.PromptSource == "queued"):
		var content interface{}
		if len(row.Message.Content) > 0 {
			_ = json.Unmarshal(row.Message.Content, &content)
		}
		text = aiGatewayPromptTextFromUserMessage(content)
	case row.Type == "attachment" && row.Attachment.Type == "queued_command" && row.Attachment.Origin.Kind == "human" &&
		(row.Attachment.CommandMode == "" || row.Attachment.CommandMode == "prompt"):
		// A message sent while the agent was working, absorbed into the turn.
		text = row.Attachment.Prompt
	default:
		return "", ""
	}
	clean := strings.TrimSpace(aiGatewaySanitizeUserQuestion(text))
	if clean == "" || recapScaffoldRe.MatchString(clean) || skillInjectionRe.MatchString(clean) || harnessMarkerRe.MatchString(clean) {
		return "", ""
	}
	return clean, row.Timestamp
}

// agentPromptLogConversationIDs lists every conversation the agent has had:
// the per-conversation snapshot dirs plus the current one.
func agentPromptLogConversationIDs(agentID, currentID string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || id == "_nil" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(currentID)
	if dirs, err := os.ReadDir(filepath.Join(aiGatewayHistoryDir(agentID), "chat")); err == nil {
		for _, d := range dirs {
			if d.IsDir() {
				add(d.Name())
			}
		}
	}
	return out
}

// agentPromptLogSyncTranscripts ingests whatever the agent's Claude Code
// transcripts gained since the last call. Returns false when it has none.
func agentPromptLogSyncTranscripts(agentID, currentID string) bool {
	mu := agentPromptLogLock(agentID)
	mu.Lock()
	defer mu.Unlock()
	state := agentPromptLogState{}
	if raw, err := os.ReadFile(agentPromptLogStatePath(agentID)); err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	if state.Transcripts == nil {
		state.Transcripts = map[string]int64{}
	}
	found := false
	changed := false
	for _, cid := range agentPromptLogConversationIDs(agentID, currentID) {
		path := agentPromptLogTranscriptPath(cid)
		if path == "" {
			continue
		}
		found = true
		entries, offset := agentPromptLogScanTranscript(path, cid, state.Transcripts[cid])
		if offset == state.Transcripts[cid] {
			continue
		}
		agentPromptLogIngestLocked(agentID, entries)
		state.Transcripts[cid] = offset
		changed = true
	}
	if changed {
		_ = aiGatewayWriteJSONAtomic(agentPromptLogStatePath(agentID), state)
	}
	return found
}

func agentPromptLogFromPrompts(prompts []aiGatewayUserPrompt, conversationID, source string) []agentPromptLogEntry {
	out := make([]agentPromptLogEntry, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, agentPromptLogEntry{TS: p.TS, Content: p.Content, ConversationID: conversationID, Source: source})
	}
	return out
}

// agentPromptLogIngestSnapshot is the per-turn hook for gateway agents without a
// transcript: record the snapshot's prompts while they are still in context.
func agentPromptLogIngestSnapshot(agentID, conversationID string, prompts []aiGatewayUserPrompt) {
	if len(prompts) == 0 || agentPromptLogTranscriptPath(conversationID) != "" {
		return
	}
	agentPromptLogIngest(agentID, agentPromptLogFromPrompts(prompts, conversationID, agentPromptLogSourceSnapshot))
}

// agentPromptLogCollect brings the agent's log up to date from its source and
// returns it oldest-first.
func agentPromptLogCollect(paneID string) []agentPromptLogEntry {
	agentID := shortPaneID(paneID)
	if view, ok := loadNativeSessionView(agentID); ok {
		agentPromptLogIngest(agentID, agentPromptLogFromPrompts(view.prompts(), view.SessionID, agentPromptLogSourceNative))
	} else {
		snapshot, snapErr := aiGatewayReadCurrentSnapshot(agentID)
		currentID := ""
		if snapErr == nil {
			currentID = strings.TrimSpace(snapshot.ConversationID)
		}
		if !agentPromptLogSyncTranscripts(agentID, currentID) && snapErr == nil {
			prompts := aiGatewayBuildCurrentPrompts(agentID, currentID, snapshot.Body, snapshot.Timestamp)
			agentPromptLogIngest(agentID, agentPromptLogFromPrompts(prompts, currentID, agentPromptLogSourceSnapshot))
		}
	}
	mu := agentPromptLogLock(agentID)
	mu.Lock()
	entries := agentPromptLogReadLocked(agentID)
	mu.Unlock()
	if entries == nil {
		entries = []agentPromptLogEntry{}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].TS < entries[j].TS })
	return entries
}

// handleAgentPromptLog serves GET /api/agents/prompt-log?panes=a,b,c →
// {"agents": {"a": [{ts, content, conversation_id}], ...}}.
func handleAgentPromptLog(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	ids := []string{}
	for _, raw := range strings.Split(r.URL.Query().Get("panes"), ",") {
		id := shortPaneID(strings.TrimSpace(raw))
		if id == "" || seen[id] || sanitizeConvID(id) != id {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "panes required")
		return
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	results := make([][]agentPromptLogEntry, len(ids))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = agentPromptLogCollect(id)
		}(i, id)
	}
	wg.Wait()
	agents := M{}
	for i, id := range ids {
		agents[id] = results[i]
	}
	J(w, M{"agents": agents})
}
