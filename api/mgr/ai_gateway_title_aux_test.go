// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

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
