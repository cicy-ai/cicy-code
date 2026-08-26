// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"testing"
)

// Claude Code 2.1.x 的会话标题请求:system 是一个数组,标题指令前面垫着
// billing header 和 CLI 自述块,措辞也从 "You are a title generator..."
// 改成了 "Generate a concise, sentence-case title (3-7 words)..."。
// 识别失败的后果不是少个标签:标题请求会以主线身份抢先写入
// chat/<conv>/current.json,之后所有真正的主线请求因首条消息对不上而被
// 误判 sidechain → 永远不再写 current/reply.json(w-10185 实测)。
func titleRequestBodyCC21() map[string]interface{} {
	return map[string]interface{}{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 32000,
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.207.6f5; cc_entrypoint=cli; cch=f2670;"},
			map[string]interface{}{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
			map[string]interface{}{"type": "text", "text": "Generate a concise, sentence-case title (3-7 words) that captures the main topic or goal of this coding session. The title should be clear enough that the user can recognize the session later."},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "<session>\n你是 agent w-10144 的分身(fork)……\n</session>\n\nWrite the title in the preferred language."},
		},
	}
}

func TestAuxKindDetectsCC21TitleRequest(t *testing.T) {
	body := titleRequestBodyCC21()
	if got := aiGatewayAuxiliaryKind("", body); got != "title" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"title\" — CC 2.1.x 标题请求未被识别,会以主线身份污染 current.json", got)
	}
}

func TestAuxKindStillDetectsLegacyTitleRequest(t *testing.T) {
	body := map[string]interface{}{
		"model":  "claude-haiku-4-5-20251001",
		"system": "You are a title generator. Summarize the conversation in a few words.",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	if got := aiGatewayAuxiliaryKind("", body); got != "title" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"title\" (legacy prompt)", got)
	}
}

// 真正的主线请求(带 tools、system 里没有标题指令)绝不能被吸进 title 类,
// 否则主线永远不写快照。
func TestAuxKindDoesNotFlagMainlineAsTitle(t *testing.T) {
	body := map[string]interface{}{
		"model":      "claude-fable-5",
		"max_tokens": 32000,
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.207.6f5; cc_entrypoint=cli;"},
			map[string]interface{}{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.\nYou are an interactive agent that helps users with software engineering tasks."},
		},
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash"},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "查下 cicy-code 的内存泄漏"},
		},
	}
	if got := aiGatewayAuxiliaryKind("查下 cicy-code 的内存泄漏", body); got != "" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"\" — 主线请求被误判为辅助调用", got)
	}
}

// Claude Code 2.1.245 又换了措辞:"You are naming a coding session so the user
// can pick it out of a long list of sessions..."。w-1005 / w-1006 实测:这条
// 请求以主线身份写入 chat/<conv>/current.json,之后全部主线被判 sidechain,
// current/reply.json 冻结在标题请求上——分身继承到的"完整对话"只有标题指令,
// 状态点也永远停在最后一次标题请求的 completed。
func titleRequestBodyCC21245() map[string]interface{} {
	return map[string]interface{}{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 32000,
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.245.7b5; cc_entrypoint=cli; cch=3af38;"},
			map[string]interface{}{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
			map[string]interface{}{"type": "text", "text": "You are naming a coding session so the user can pick it out of a long list of sessions. The title is a name for what the session is about, not a sentence describing the task: a short noun phrase of two to five words."},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{
				map[string]interface{}{"type": "text", "text": "<session>\n看下cicy-code 和cicy-desktop 这两个项目\n</session>\n\nWrite the title in the predominant language of the session — a stray word or code token in another language doesn't change it, and neither does the English of these instructions."},
			}},
		},
	}
}

func TestAuxKindDetectsCC21245TitleRequest(t *testing.T) {
	if got := aiGatewayAuxiliaryKind("", titleRequestBodyCC21245()); got != "title" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"title\" — CC 2.1.245 标题请求未被识别", got)
	}
}

// 措辞完全未知时靠结构兜底:无 tools + 单条 <session> 包裹的 user 消息 + "Write the
// title … language" 尾缀。
func TestAuxKindDetectsUnknownWordingTitleRequestByShape(t *testing.T) {
	body := titleRequestBodyCC21245()
	body["system"] = []interface{}{
		map[string]interface{}{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
		map[string]interface{}{"type": "text", "text": "Some brand new wording nobody has seen yet."},
	}
	if got := aiGatewayAuxiliaryKind("", body); got != "title" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"title\" (shape fallback)", got)
	}
	// 同样的消息一旦带 tools(主线形态)就绝不能被兜底吸走。
	body["tools"] = []interface{}{map[string]interface{}{"name": "Bash"}}
	if got := aiGatewayAuxiliaryKind("", body); got != "" {
		t.Fatalf("aiGatewayAuxiliaryKind = %q, want \"\" — 带 tools 的主线被兜底误判", got)
	}
}

// 磁盘上的 current.json 已经被(旧版本放过的)标题请求污染时,后续主线不能再被
// 判成 sidechain,而要把快照抢回来——否则冻结永远不会自愈。
func TestMainlineReclaimsSnapshotPoisonedByTitleRequest(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19402"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	conv := "sess-poisoned-1"

	// 模拟旧版本:标题请求以主线身份落盘。
	poisoned := titleRequestBodyCC21245()
	poisoned["metadata"] = map[string]interface{}{"session_id": conv}
	current := aiGatewayCurrentSnapshot{AgentID: agent, ConversationID: conv, Body: poisoned, Status: "completed"}
	if err := aiGatewayWriteCurrentSnapshot(agent, current); err != nil {
		t.Fatalf("seed poisoned snapshot: %v", err)
	}

	main := []byte(`{"model":"m","metadata":{"session_id":"` + conv + `"},"tools":[{"name":"Bash"}],"messages":[{"role":"user","content":"看下cicy-code 和cicy-desktop 这两个项目"}]}`)
	s := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, main)
	if s.auxiliary {
		t.Fatalf("mainline after poisoned snapshot misclassified as auxiliary (%q) — snapshot never heals", s.auxKind)
	}
	if err := s.writeStartSnapshots(); err != nil {
		t.Fatalf("start: %v", err)
	}
	healed := agentInspectorLoadCurrent(agent)
	if aiGatewayAuxiliaryKind("", aiGatewayMap(healed.Body)) != "" {
		t.Fatalf("current.json still holds the title request after a mainline turn")
	}
}
