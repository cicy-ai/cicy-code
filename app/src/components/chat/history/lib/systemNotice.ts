// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Classification of harness-injected "system" text (role:system history items
// and the leading <system-reminder> blocks peeled off user turns).
//
// Coding-agent harnesses (Claude Code, Codex) attach a lot of machinery to the
// wire that is NOT conversation: per-request token budgets, "only you see this
// output" reminders, nudges, CLAUDE.md context dumps. Rendering each of them as
// its own pill between every tool call is what made the history unreadable.
// This module decides, per block, what it is:
//   noise   — pure harness chatter, dropped entirely
//   steer   — a REAL user message delivered mid-turn ("The user sent a new
//             message while you were working: …") → shown as a user bubble
//   task    — background task / subagent completion notice → compact chip
//   context — injected context (CLAUDE.md, memory recall, date) → folded chip
//   notice  — anything else → folded chip
export type SystemNoticeKind = 'noise' | 'steer' | 'task' | 'context' | 'notice';

export type SystemNotice = {
  kind: SystemNoticeKind;
  // Display text: for `steer` the user's actual message, for `task` the
  // summary line, otherwise the cleaned block text.
  text: string;
  // Full original block (for the expanded view).
  raw: string;
  // Short label for task chips (e.g. the task id / summary).
  title?: string;
};

const NOISE_PARAGRAPH_RES: RegExp[] = [
  /^<total_tokens>[\s\S]*?<\/total_tokens>$/i,
  /^Only you see that command's output\b/i,
  /^If the user needs to read any of it, put it in your reply\.?$/i,
  /^The user hasn't heard from you in a while\b/i,
  /^First privately list what you need next\b/i,
  /^Shell cwd was reset to\b/i,
  /^This is how Claude Code surfaces messages the user sends mid-turn\b/i,
  /^Address the message above as you continue this turn\.?$/i,
  /^Before ending your turn\b/i,
  /^Session cwd remains\b/i,
  /^\(Bash completed with no output\)$/i,
];

const STEER_HEAD_RE = /^The user sent a new message while you were working:\s*/i;
const TASK_HEAD_RE = /\[SYSTEM NOTIFICATION - NOT USER INPUT\]|<task-notification>/i;
const CONTEXT_HEAD_RES: RegExp[] = [
  /^As you answer the user's questions, you can use the following context/i,
  /^# claudeMd\b/i,
  /^# currentDate\b/i,
  /^# userEmail\b/i,
  /^Codebase and user instructions are shown below/i,
  /^<memory-recall>/i,
  /^Contents of [^\n]*(CLAUDE|AGENTS)\.md/i,
  /^# AGENTS\.md instructions for/i,
  /^<environment_context>/i,
  /^<permissions instructions>/i,
];

const WRAPPER_TAG_RE = /^\s*<(system-reminder|task-notification-wrapper)>\s*|\s*<\/(system-reminder|task-notification-wrapper)>\s*$/g;

function stripWrapper(text: string): string {
  return String(text || '').replace(WRAPPER_TAG_RE, '').trim();
}

function splitParagraphs(text: string): string[] {
  return text.split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
}

export function isNoiseParagraph(paragraph: string): boolean {
  const p = paragraph.trim();
  if (!p) return true;
  // A block that is only budget lines (possibly several) is noise.
  if (/^(<total_tokens>[\s\S]*?<\/total_tokens>\s*)+$/i.test(p)) return true;
  return NOISE_PARAGRAPH_RES.some((re) => re.test(p));
}

function readTag(text: string, tag: string): string {
  const m = text.match(new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`, 'i'));
  return m ? m[1].trim() : '';
}

// Classify ONE system block. Returns null when the block is pure noise.
export function classifySystemNotice(input: string): SystemNotice | null {
  const raw = String(input || '').trim();
  const text = stripWrapper(raw);
  if (!text) return null;

  if (STEER_HEAD_RE.test(text)) {
    // Everything after the head line up to the first noise paragraph (the
    // "This is how Claude Code surfaces…" explainer) is the user's message.
    const body = text.replace(STEER_HEAD_RE, '');
    const paragraphs = splitParagraphs(body);
    const kept: string[] = [];
    for (const p of paragraphs) {
      if (isNoiseParagraph(p)) break;
      kept.push(p);
    }
    const message = kept.join('\n\n').trim();
    return message ? { kind: 'steer', text: message, raw } : null;
  }

  if (TASK_HEAD_RE.test(text)) {
    const summary = readTag(text, 'summary');
    const status = readTag(text, 'status');
    const taskId = readTag(text, 'task-id');
    const title = [status && status.toLowerCase(), taskId].filter(Boolean).join(' · ');
    return { kind: 'task', text: summary || text, raw, title: title || undefined };
  }

  const paragraphs = splitParagraphs(text).filter((p) => !isNoiseParagraph(p));
  if (!paragraphs.length) return null;
  const cleaned = paragraphs.join('\n\n');
  const kind: SystemNoticeKind = CONTEXT_HEAD_RES.some((re) => re.test(cleaned)) ? 'context' : 'notice';
  return { kind, text: cleaned, raw };
}

// One-line preview for a folded notice (first meaningful line, trimmed).
export function noticePreview(notice: SystemNotice, max = 80): string {
  const line = notice.text.split('\n').map((l) => l.trim()).find((l) => l && !/^[#<\[]/.test(l)) || notice.text.split('\n')[0] || '';
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}
