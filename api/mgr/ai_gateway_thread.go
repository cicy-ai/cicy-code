// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Thread-continue ("delta") requests.
//
// Newer Claude Code builds stop resending the whole conversation. A request
// carries `thread: {type: "continue", previous_message_id: "msg_…"}` and its
// `messages` array holds ONLY what is new since that response (one tool_result,
// or the next user message). Everything downstream of the gateway — current.json
// as "the full ordered conversation", positional history ids, the sidechain
// check, question extraction — assumes a full body, so an untreated delta
// collapsed an agent's history to a single message and lost the user's question.
//
// The fix is to put the full body back before anyone looks at it: every response
// is remembered under its message id together with the full message list that
// produced it, and a continue request is expanded to stored(previous) + delta.
// Chains are keyed by message id, so a subagent's thread never mixes with the
// main one.

// aiGatewayThreadKeepPerAgent bounds memory: a main chain plus a few parallel
// subagent chains. Entries share message maps with their predecessors, so an
// entry costs one slice of pointers, not a copy of the conversation.
const aiGatewayThreadKeepPerAgent = 8

type aiGatewayThreadEntry struct {
	messages []interface{} // full conversation up to and including this response
	seq      uint64
}

var aiGatewayThreadStore = struct {
	sync.Mutex
	seq     uint64
	byAgent map[string]map[string]*aiGatewayThreadEntry
}{byAgent: map[string]map[string]*aiGatewayThreadEntry{}}

func aiGatewayThreadPut(agentID, messageID string, messages []interface{}) {
	agentID, messageID = strings.TrimSpace(agentID), strings.TrimSpace(messageID)
	if agentID == "" || messageID == "" || len(messages) == 0 {
		return
	}
	st := &aiGatewayThreadStore
	st.Lock()
	defer st.Unlock()
	entries := st.byAgent[agentID]
	if entries == nil {
		entries = map[string]*aiGatewayThreadEntry{}
		st.byAgent[agentID] = entries
	}
	st.seq++
	entries[messageID] = &aiGatewayThreadEntry{messages: messages, seq: st.seq}
	if len(entries) <= aiGatewayThreadKeepPerAgent {
		return
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return entries[ids[i]].seq < entries[ids[j]].seq })
	for _, id := range ids[:len(ids)-aiGatewayThreadKeepPerAgent] {
		delete(entries, id)
	}
}

func aiGatewayThreadGet(agentID, messageID string) []interface{} {
	st := &aiGatewayThreadStore
	st.Lock()
	defer st.Unlock()
	if entry := st.byAgent[strings.TrimSpace(agentID)][strings.TrimSpace(messageID)]; entry != nil {
		return entry.messages
	}
	return nil
}

// aiGatewayThreadPreviousID returns the previous message id of a thread-continue
// request, or "" when the body is an ordinary full-conversation request.
func aiGatewayThreadPreviousID(body map[string]interface{}) string {
	thread := aiGatewayMap(body["thread"])
	if strings.TrimSpace(aiGatewayString(thread["type"])) != "continue" {
		return ""
	}
	return strings.TrimSpace(aiGatewayString(thread["previous_message_id"]))
}

// aiGatewayExpandThreadBody returns the body to treat as "the conversation" for
// this request, and whether a continue request could NOT be expanded (orphan).
//
//   - Ordinary request: the body itself, orphan=false.
//   - Continue request with a remembered predecessor: a shallow copy whose
//     messages are stored(previous) + delta.
//   - Continue request after a daemon restart (memory gone): rebuilt from the
//     snapshots on disk when they provably belong to the same chain.
//   - Otherwise: the body itself, orphan=true. The caller must keep it away
//     from current.json — a lone delta is not the conversation.
func aiGatewayExpandThreadBody(agentID, conversationID string, body map[string]interface{}) (map[string]interface{}, bool) {
	prevID := aiGatewayThreadPreviousID(body)
	if prevID == "" {
		return body, false
	}
	delta := aiGatewaySlice(body["messages"])
	prior := aiGatewayThreadGet(agentID, prevID)
	if len(prior) == 0 {
		prior = aiGatewayThreadPriorFromDisk(agentID, conversationID, delta)
	}
	if len(prior) == 0 {
		return body, true
	}
	full := make([]interface{}, 0, len(prior)+len(delta))
	full = append(full, prior...)
	full = append(full, delta...)
	next := aiGatewayCloneAnyMap(body)
	next["messages"] = full
	return next, false
}

// aiGatewayThreadPriorFromDisk rebuilds "conversation up to the previous
// response" from current.json (messages up to the previous request) plus
// reply.json (what the model answered since). Used only when the in-memory
// chain is gone. It refuses unless the delta provably continues that snapshot:
// its tool_results must answer tool calls recorded in reply.json.
func aiGatewayThreadPriorFromDisk(agentID, conversationID string, delta []interface{}) []interface{} {
	disk := agentInspectorLoadCurrent(agentID)
	if strings.TrimSpace(disk.ConversationID) == "" || strings.TrimSpace(disk.ConversationID) != strings.TrimSpace(conversationID) {
		return nil
	}
	diskMessages := aiGatewaySlice(aiGatewayMap(disk.Body)["messages"])
	if len(diskMessages) == 0 {
		return nil
	}
	reply := agentInspectorLoadReply(agentID)
	if conv := strings.TrimSpace(reply.ConversationID); conv != "" && conv != strings.TrimSpace(conversationID) {
		return nil
	}
	// Tool calls already present in the disk messages are not part of the
	// previous response; everything in reply.json after the last of them is.
	known := map[string]bool{}
	for _, raw := range diskMessages {
		for _, block := range aiGatewaySlice(aiGatewayMap(raw)["content"]) {
			b := aiGatewayMap(block)
			if aiGatewayString(b["type"]) == "tool_use" {
				known[aiGatewayString(b["id"])] = true
			}
		}
	}
	start := 0
	for i, item := range reply.Items {
		if aiGatewayString(item["type"]) == "tool_use" && known[aiGatewayString(item["tool_id"])] {
			start = i + 1
		}
	}
	blocks := []interface{}{}
	answered := map[string]bool{}
	for _, item := range reply.Items[start:] {
		switch aiGatewayString(item["type"]) {
		case "text":
			if text := aiGatewayThreadRaw(item["text"]); strings.TrimSpace(text) != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": text})
			}
		case "tool_use":
			id := aiGatewayString(item["tool_id"])
			input := item["input"]
			if text, ok := input.(string); ok {
				var parsed interface{}
				if json.Unmarshal([]byte(text), &parsed) == nil {
					input = parsed
				}
			}
			blocks = append(blocks, map[string]interface{}{"type": "tool_use", "id": id, "name": aiGatewayString(item["name"]), "input": input})
			answered[id] = true
		}
	}
	for _, raw := range delta {
		for _, block := range aiGatewaySlice(aiGatewayMap(raw)["content"]) {
			b := aiGatewayMap(block)
			if aiGatewayString(b["type"]) == "tool_result" && !answered[aiGatewayString(b["tool_use_id"])] {
				return nil // answers a tool call this snapshot never made: another chain
			}
		}
	}
	prior := make([]interface{}, 0, len(diskMessages)+1)
	for _, raw := range diskMessages {
		// Drop the positional id stamped at capture; the expanded body is
		// renumbered as a whole.
		if m := aiGatewayMap(raw); len(m) > 0 {
			clean := aiGatewayCloneAnyMap(m)
			delete(clean, "id")
			prior = append(prior, clean)
			continue
		}
		prior = append(prior, raw)
	}
	if len(blocks) > 0 {
		prior = append(prior, map[string]interface{}{"role": "assistant", "content": blocks})
	}
	return prior
}

// aiGatewayThreadAssistantFromResponse extracts the response's message id and
// its content as an assistant message, from an Anthropic SSE stream or a plain
// JSON response. Thinking blocks are left out: they are not part of the
// displayed history and can be large.
func aiGatewayThreadAssistantFromResponse(body []byte) (string, map[string]interface{}) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var message map[string]interface{}
		if json.Unmarshal([]byte(trimmed), &message) != nil {
			return "", nil
		}
		blocks := []interface{}{}
		for _, raw := range aiGatewaySlice(message["content"]) {
			block := aiGatewayMap(raw)
			if kind := aiGatewayString(block["type"]); kind == "text" || kind == "tool_use" {
				blocks = append(blocks, block)
			}
		}
		return aiGatewayThreadAssistant(aiGatewayString(message["id"]), blocks)
	}
	messageID := ""
	type openBlock struct {
		block map[string]interface{}
		text  strings.Builder
		json  strings.Builder
	}
	open := map[int]*openBlock{}
	order := []int{}
	for _, line := range strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n") {
		eventType, payload := aiGatewayParseSSELine(line)
		if eventType != "data" || payload == nil {
			continue
		}
		switch aiGatewayString(payload["type"]) {
		case "message_start":
			messageID = aiGatewayString(aiGatewayMap(payload["message"])["id"])
		case "content_block_start":
			index := aiGatewayInt(payload["index"])
			block := aiGatewayMap(payload["content_block"])
			if kind := aiGatewayString(block["type"]); kind != "text" && kind != "tool_use" {
				continue
			}
			entry := &openBlock{block: aiGatewayCloneAnyMap(block)}
			entry.text.WriteString(aiGatewayThreadRaw(block["text"]))
			open[index] = entry
			order = append(order, index)
		case "content_block_delta":
			entry := open[aiGatewayInt(payload["index"])]
			if entry == nil {
				continue
			}
			delta := aiGatewayMap(payload["delta"])
			switch aiGatewayString(delta["type"]) {
			case "text_delta":
				entry.text.WriteString(aiGatewayThreadRaw(delta["text"]))
			case "input_json_delta":
				entry.json.WriteString(aiGatewayThreadRaw(delta["partial_json"]))
			}
		}
	}
	blocks := []interface{}{}
	for _, index := range order {
		entry := open[index]
		switch aiGatewayString(entry.block["type"]) {
		case "text":
			if entry.text.Len() == 0 {
				continue
			}
			entry.block["text"] = entry.text.String()
		case "tool_use":
			if entry.json.Len() > 0 {
				var input interface{}
				if json.Unmarshal([]byte(entry.json.String()), &input) == nil {
					entry.block["input"] = input
				}
			}
			if entry.block["input"] == nil {
				entry.block["input"] = map[string]interface{}{}
			}
		}
		blocks = append(blocks, entry.block)
	}
	return aiGatewayThreadAssistant(messageID, blocks)
}

func aiGatewayThreadAssistant(messageID string, blocks []interface{}) (string, map[string]interface{}) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", nil
	}
	return messageID, map[string]interface{}{"role": "assistant", "content": blocks}
}

// aiGatewayThreadRemember records a finished response so the next continue
// request on this chain can be expanded. requestMessages is the FULL message
// list of the request that produced it.
func aiGatewayThreadRemember(agentID string, requestMessages []interface{}, responseBody []byte) {
	if len(requestMessages) == 0 {
		return
	}
	messageID, assistant := aiGatewayThreadAssistantFromResponse(responseBody)
	if messageID == "" {
		return
	}
	full := make([]interface{}, 0, len(requestMessages)+1)
	full = append(full, requestMessages...)
	if len(aiGatewaySlice(assistant["content"])) > 0 {
		full = append(full, assistant)
	}
	aiGatewayThreadPut(agentID, messageID, full)
}

// aiGatewayThreadSpanBody keeps the audit span honest: it records what actually
// went over the wire (the delta), not the expanded conversation.
func aiGatewayThreadSpanBody(wire map[string]interface{}, payloadValue interface{}) interface{} {
	if aiGatewayThreadPreviousID(wire) != "" {
		return wire
	}
	return payloadValue
}

// aiGatewayThreadRequestMessages is what to remember for the next delta: the
// request's full message list, or nil for a chain we could not place.
func aiGatewayThreadRequestMessages(body map[string]interface{}, orphan bool) []interface{} {
	if orphan {
		return nil
	}
	return aiGatewaySlice(body["messages"])
}

// aiGatewayThreadRaw reads a string WITHOUT trimming: stream deltas are
// fragments, and their leading/trailing spaces are part of the text.
func aiGatewayThreadRaw(value interface{}) string {
	text, _ := value.(string)
	return text
}
