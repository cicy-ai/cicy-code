// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import type { HistoryTurn, RawHistoryItem, EnvironmentContextData } from '../types';
import { extractContentText } from './turns';

function serializeToolResult(value: unknown): string {
  if (typeof value === 'string') return value.trim();
  if (value == null) return '';
  try { return JSON.stringify(value).trim(); } catch { return String(value).trim(); }
}

// Harness-injected wrappers that ride along at the START of a role:user message
// (Claude puts system-reminders / slash-command echoes in the user turn). They
// are NOT real user input but the user still wants them available — so we peel
// the LEADING blocks off and render them in a small collapsed fold, leaving the
// real question as the bubble. The \1 backreference matches each block to its
// OWN closing tag, so a stray inner </tag> can't cut a block short.
// fork-inherited-context: a cicy fork's first user message carries the source
// agent's conversation summary in this wrapper — fold it like other harness
// blocks so the (potentially huge) inherited dump shows as a collapsed chip.
export const HARNESS_BLOCK_RE = /^\s*<(system-reminder|task-notification|local-command-caveat|local-command-stdout|command-name|command-message|command-args|fork-inherited-context)>([\s\S]*?)<\/\1>\s*/;
// Codex prepends its memory file to the FIRST user message as
// `# AGENTS.md instructions for <path>\n\n<INSTRUCTIONS>…</INSTRUCTIONS>` (a
// leading markdown `#` header; CLAUDE.md uses the same shape) and often follows
// it with an `<environment_context>…</environment_context>` block. Both are
// harness-injected guidance, not the real question — peel them into the fold.
export const AGENTS_PREFIX_RE = /^\s*#*\s*(?:AGENTS|CLAUDE)\.md instructions for [^\n]*\n+<INSTRUCTIONS>[\s\S]*?<\/INSTRUCTIONS>\s*/;
export const ENV_CONTEXT_RE = /^\s*<environment_context>[\s\S]*?<\/environment_context>\s*/;
// Claude Code's "recap on return" injects a fixed instruction as a user turn
// when the user comes back after stepping away ("The user stepped away and is
// coming back. Recap in under N words …"). It rides as plain text (no tag) at
// the START of the user message. Two shapes occur:
//   (a) bundled: {instruction}\n\n{generated recap}  {real user message}
//       — the harness joins the recap to the user's typed message with a
//         double space, so we peel through the instruction + recap up to that
//         "  " join, leaving the real question as the bubble.
//   (b) standalone: just the instruction (no bundled recap / message).
// The opening is distinctive enough that no genuine user message starts with it.
export const RECAP_BUNDLED_RE = /^\s*The user (?:stepped away and is coming back|is back)\.\s*Recap[\s\S]*?\n\s*\n[\s\S]*?\.\s{2,}(?=\S)/;
export const RECAP_PREFIX_RE = /^\s*The user (?:stepped away and is coming back|is back)\.\s*Recap[\s\S]*?(?:\n\s*\n|$)/;
// Post-/compact continuation banner, injected as a user turn at the start of a
// resumed session ("This session is being continued from a previous
// conversation … Summary: …"). It rides as its OWN text block (the real
// conversation resumes in separate messages — the banner even says "Continue …
// without asking the user"), so the ENTIRE block (banner + the long Summary) is
// harness context, not the user's question — fold all of it. Greedy to the end
// of the block; in current.json it sits in a self-contained content block right
// after the system-reminder, so this won't swallow a real question.
export const CONTINUATION_PREFIX_RE = /^\s*This session is being continued from a previous conversation[\s\S]*$/;
// cicy /compact appends its summary as a user message opening with this fixed
// banner (see cicyCompactSummaryPrefix, backend). The UI renders such a turn as
// the ✨已压缩 compaction MARKER (a foldable divider), never as a user bubble.
export const CICY_COMPACT_SUMMARY_RE = /^\s*\[(?:Compressed summary of the earlier conversation|以下是更早对话的压缩摘要)[^\]]*\]\s*/;
export function cicyCompactSummaryOf(text: string | undefined | null): string | null {
  const s = String(text || '');
  const m = s.match(CICY_COMPACT_SUMMARY_RE);
  if (!m) return null;
  return s.slice(m[0].length).trim();
}

export function isTechnicalTransportFailureText(value: unknown): boolean {
  const text = String(value || '').trim().toLowerCase();
  if (!text.includes('生成失败') && !text.includes('http 5')) return false;
  return [
    'broken pipe', 'closed network connection', 'connection reset by peer',
    'write tcp', 'read tcp', 'dial tcp', 'tls: bad record mac',
    'upstream tls handshake', '\neof',
  ].some((marker) => text.includes(marker));
}

export function splitLeadingHarnessBlocks(text: string): { blocks: string[]; remaining: string } {
  let remaining = String(text || '');
  const blocks: string[] = [];
  // Guard against pathological inputs with a hard cap.
  for (let i = 0; i < 50; i += 1) {
    const m = remaining.match(HARNESS_BLOCK_RE)
      || remaining.match(AGENTS_PREFIX_RE)
      || remaining.match(ENV_CONTEXT_RE)
      || remaining.match(RECAP_BUNDLED_RE)
      || remaining.match(RECAP_PREFIX_RE)
      || remaining.match(CONTINUATION_PREFIX_RE);
    if (!m) break;
    blocks.push(m[0].trim());
    remaining = remaining.slice(m[0].length);
  }
  return { blocks, remaining: remaining.trim() };
}

// cicy 失败/取消记录:后端通常已带 cicy_outcome + 干净 label。这里再兜底——任何带
// cicy_outcome 字段、或文本以 outcome 标记(新的干净前缀 / 旧的 ⟦…⟧ 形态)开头的项,
// 一律按 outcome 渲染(头像左对齐 + 重试),与 role 无关。标记文本本身已是干净中文短句
// (不含符号/JSON),不再解析 detail。
const CICY_OUTCOME_MARK = '(no reply this turn';
const CICY_OUTCOME_MARK_LEGACY_CN = '（本轮未生成回复';
const CICY_OUTCOME_LEGACY = '⟦cicy-turn-outcome⟧';
export function parseCicyOutcome(item: any, contentText: string): { kind: string; label: string; detail?: string } | null {
  const outcomeLabel = (k: string) => k === 'cancelled' ? '已停止生成' : k === 'blocked' ? '已拦截' : '生成失败';
  const detail = String(item?.cicy_outcome_detail || '').trim();
  const tagged = String(item?.cicy_outcome || '').trim();
  if (tagged) {
    return { kind: tagged, label: outcomeLabel(tagged), detail };
  }
  if (contentText.startsWith(CICY_OUTCOME_MARK) || contentText.startsWith(CICY_OUTCOME_MARK_LEGACY_CN) || contentText.startsWith(CICY_OUTCOME_LEGACY)) {
    const kind = (contentText.includes('已停止') || contentText.includes('cancelled')) ? 'cancelled'
      : (contentText.includes('已拦截') || contentText.includes('blocked')) ? 'blocked'
      : 'error';
    return { kind, label: outcomeLabel(kind), detail };
  }
  return null;
}

export function normalizeRawHistoryItem(raw: any, toolNameByCallId?: Map<string, string>, toolResultByCallId?: Map<string, string>): HistoryTurn | null {
  if (!raw || typeof raw !== 'object') return null;
  const item = raw as RawHistoryItem;
  const historyId = Number(item.history_id || item.id || 0);
  const conversationId = String(item.conversation_id || '').trim();
  const role = String(item.role || '').trim();
  const itemType = String(item.type || '').trim();
  const model = String(item.model || '').trim();
  const status = String(item.status || '').trim() || 'text';
  // Outcome record (cancel / post-retry failure) → a normal assistant output
  // (avatar, left-aligned) carrying an `outcome` tag so the render styles it
  // (failed/cancelled) + offers 重试. Detected by the cicy_outcome field OR the raw
  // marker, regardless of the role any serving/cache path gave it.
  {
    const outcomeText = extractContentText(item.content) || String(item.text || '').trim();
    const outcome = parseCicyOutcome(item, outcomeText);
    if (outcome) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'assistant',
        q: '',
        text: outcome.label,
        a: outcome.label,
        steps: [],
        status,
        model,
        outcome: outcome.kind,
        outcomeDetail: outcome.detail || undefined,
      };
    }
  }
  // System / developer items are harness-injected notices (system-reminders,
  // task notifications, date changes, and codex's `<permissions instructions>`
  // / sandbox preamble which rides in a `developer` role message). They are NOT
  // assistant output — without this branch they fall through to the assistant
  // path and render as AI bubbles. Fold the whole message into the collapsed
  // SystemNoticeCard. (codex uses `developer`; Anthropic/others use `system`.)
  if (role === 'system' || role === 'developer') {
    const sysText = extractContentText(item.content) || String(item.text || '').trim();
    if (!sysText) return null;
    return {
      history_id: historyId || undefined,
      conversation_id: conversationId,
      role: 'system',
      q: '',
      text: sysText,
      a: '',
      steps: [],
      status,
      model,
      outcome: String((item as any).cicy_outcome || '').trim() || undefined,
    };
  }
  if (role === 'user') {
    // Keep the FULL text (including any harness-injected <system-reminder> /
    // command echoes). CollapsibleQ separates those leading markers into a small
    // collapsed fold — they must stay visible-on-demand, not be stripped.
    const question = extractContentText(item.content) || String(item.text || item.q || '').trim();

    if (question) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'user',
        q: question,
        text: question,
        a: '',
        steps: [],
        status,
        model,
      };
    }
    const toolSteps: any[] = [];
    if (Array.isArray(item.content)) {
      for (const part of item.content as any[]) {
        const pt = String(part?.type || '').trim();
        if (pt === 'tool_result' || pt === 'function_call_output') {
          const callId = String(part?.tool_use_id || part?.tool_id || '').trim();
          let name = String(part?.name || part?.tool_name || '').trim();
          if (!name && callId && toolNameByCallId?.has(callId)) {
            name = toolNameByCallId.get(callId) || 'tool_result';
          }
          if (!name) name = 'tool_result';
          toolSteps.push({
            type: 'tool',
            tools: [{
              name,
              arg: '',
              result: typeof part.content === 'string' ? part.content.trim() : (part.content ? JSON.stringify(part.content).trim() : ''),
              isError: part?.is_error === true,
            }],
          });
        }
      }
    }
    if (toolSteps.length) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'assistant',
        q: '',
        text: '',
        a: '',
        steps: toolSteps,
        status,
        model,
      };
    }
    return null;
  }
  // OpenAI Chat tool-result message (role:tool / role:function). Its content is
  // the raw tool output — render it as a tool RESULT card, NOT as assistant text,
  // otherwise the output (e.g. "Chunk ID: … Process exited with code 0 …") leaks
  // in as a chat bubble. (Anthropic puts tool_result inside a role:user message,
  // handled above; this is the OpenAI Chat shape.)
  if (role === 'tool' || role === 'function') {
    const callId = String((item as any).tool_call_id || (item as any).tool_id || (item as any).call_id || '').trim();
    let name = String((item as any).name || '').trim();
    if (!name && callId && toolNameByCallId?.has(callId)) name = toolNameByCallId.get(callId) || '';
    if (!name) name = 'tool_result';
    const result = typeof item.content === 'string'
      ? item.content.trim()
      : (item.content ? JSON.stringify(item.content).trim() : '');
    if (!result) return null;
    return {
      history_id: historyId || undefined,
      conversation_id: conversationId,
      role: 'assistant',
      q: '',
      text: '',
      a: '',
      steps: [{ type: 'tool', tools: [{ name, arg: '', result }] }],
      status,
      model,
    };
  }
  const steps: NonNullable<HistoryTurn['steps']> = [];
  // Anthropic extended-thinking blocks lead the assistant message (before text /
  // tool_use). The committed history dropped them — only the live tail rendered
  // thinking — so multi-round reasoning vanished once a turn was committed. Push
  // them first to preserve the real order: thinking → text → tools.
  if (Array.isArray(item.content)) {
    const thinkingText = (item.content as any[])
      .filter((p) => p && typeof p === 'object' && String(p.type || '').trim() === 'thinking')
      .map((p) => String(p.thinking || '').trim())
      .filter(Boolean)
      .join('\n\n');
    if (thinkingText) steps.push({ type: 'thinking', text: thinkingText });
  }
  // OpenAI Chat / opencode: committed reasoning lives in a top-level
  // `reasoning_content` string (not a content block). Anthropic uses content[]
  // thinking blocks (above); without this the thinking shows in the live tail
  // but vanishes the moment the turn commits to current.json. Push before text
  // to keep the real order: thinking → text → tools.
  const reasoningText = String((item as any).reasoning_content || (item as any).reasoning || '').trim();
  if (reasoningText) steps.push({ type: 'thinking', text: reasoningText });
  const assistantText = extractContentText(item.content);
  if (assistantText && !isTechnicalTransportFailureText(assistantText)) {
    steps.push({ type: 'text', text: assistantText });
  }
  if (itemType === 'custom_tool_call') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || 'tool'),
        tool_id: String((item as any).call_id || (item as any).tool_id || item.id || '').trim(),
        arg: String(item.input || '').trim(),
        // Folded from the paired custom_tool_call_output (apply_patch result).
        result: String((item as any)._tool_output || '').trim(),
      }],
    });
  }
  if (itemType === 'custom_tool_call_output') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || item.tool_name || 'tool'),
        tool_id: String((item as any).call_id || (item as any).tool_id || item.id || '').trim(),
        arg: '',
        result: serializeToolResult(item.output ?? item.result),
      }],
    });
  }
  // OpenAI Responses: top-level function_call (e.g. exec_command). Unlike
  // Anthropic tool_use (a content block) or codex apply_patch (custom_tool_call),
  // its name + arguments sit at the item top level — without this it has no
  // matching case, produces no step, and the whole item is dropped.
  if (itemType === 'function_call') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || 'tool'),
        tool_id: String((item as any).call_id || (item as any).tool_id || item.id || '').trim(),
        arg: typeof (item as any).arguments === 'string'
          ? (item as any).arguments.trim()
          : ((item as any).arguments ? JSON.stringify((item as any).arguments).trim()
            : ((item as any).input ? JSON.stringify((item as any).input).trim() : '')),
        // Folded by buildTurnsFromRawItems from the paired function_call_output,
        // so the call + its result render as ONE tool card.
        result: String((item as any)._tool_output || '').trim(),
      }],
    });
  }
  // OpenAI Responses: top-level function_call_output (the tool result, name-less).
  if (itemType === 'function_call_output') {
    const callId = String((item as any).call_id || (item as any).tool_id || '').trim();
    let name = '';
    if (callId && toolNameByCallId?.has(callId)) name = toolNameByCallId.get(callId) || '';
    if (!name) name = 'tool';
    steps.push({
      type: 'tool',
      tools: [{
        name,
        tool_id: callId,
        arg: '',
        result: serializeToolResult((item as any).output ?? (item as any).result),
      }],
    });
  }
  // OpenAI Chat: assistant message carries tool_calls[] (name + arguments under
  // .function), separate from any text content.
  if (Array.isArray((item as any).tool_calls)) {
    for (const tc of (item as any).tool_calls as any[]) {
      const fn = tc?.function || {};
      const callId = String(tc?.id || '').trim();
      // Fold the matching role:tool result (collected by buildTurnsFromRawItems)
      // so the call + its result render as ONE tool card (gateway codex).
      const result = (callId && toolResultByCallId?.get(callId)) || '';
      steps.push({
        type: 'tool',
        tools: [{
          name: String(fn.name || tc?.name || 'tool'),
          tool_id: callId,
          arg: typeof fn.arguments === 'string'
            ? fn.arguments.trim()
            : (fn.arguments ? JSON.stringify(fn.arguments).trim() : ''),
          result: String(result).trim(),
        }],
      });
    }
  }
  if (itemType !== 'custom_tool_call' && itemType !== 'custom_tool_call_output' && Array.isArray(item.content)) {
    for (const part of item.content as any[]) {
      const pt = String(part?.type || '').trim();
      if (pt === 'tool_use') {
        const toolResult = String(part?._tool_result || '').trim();
        steps.push({
          type: 'tool',
          tools: [{
            name: String(part.name || 'tool'),
            tool_id: String(part?.id || part?.tool_id || part?.call_id || '').trim(),
            arg: typeof part.input === 'string' ? part.input.trim() : (part.input ? JSON.stringify(part.input).trim() : ''),
            result: toolResult,
            isError: part?._tool_is_error === true,
          }],
        });
      }
      if (pt === 'tool_result' || pt === 'function_call_output') {
        const callId = String(part?.tool_use_id || part?.tool_id || '').trim();
        let name = String(part?.name || part?.tool_name || '').trim();
        if (!name && callId && toolNameByCallId?.has(callId)) {
          name = toolNameByCallId.get(callId) || 'tool';
        }
        if (!name) name = 'tool_result';
        steps.push({
          type: 'tool',
          tools: [{
            name,
            tool_id: callId,
            arg: '',
            result: typeof part.content === 'string' ? part.content.trim() : (part.content ? JSON.stringify(part.content).trim() : ''),
            isError: part?.is_error === true,
          }],
        });
      }
    }
  }
  if (!steps.length) return null;
  const answer = steps
    .filter((step) => step.type === 'text')
    .map((step) => String((step as any).text || '').trim())
    .filter(Boolean)
    .join('\n\n');
  return {
    history_id: historyId || undefined,
    conversation_id: conversationId,
    role: 'assistant',
    q: '',
    text: '',
    a: answer,
    steps,
    status,
    model,
  };
}

export function parseEnvironmentContext(text: string): EnvironmentContextData | null {
  const trimmed = String(text || '').trim();
  if (!trimmed.startsWith('<environment_context>') || !trimmed.endsWith('</environment_context>')) {
    return null;
  }
  const read = (tag: keyof EnvironmentContextData) => {
    const match = trimmed.match(new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`));
    return match?.[1]?.trim() || '';
  };
  const context: EnvironmentContextData = {
    cwd: read('cwd'),
    shell: read('shell'),
    current_date: read('current_date'),
    timezone: read('timezone'),
  };
  if (!context.cwd && !context.shell && !context.current_date && !context.timezone) {
    return null;
  }
  return context;
}
