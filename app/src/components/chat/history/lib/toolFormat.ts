// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import i18n from '../../../../i18n';

export function isPatchText(text: string) {
  const value = String(text || '');
  return value.includes('*** Begin Patch');
}

export function shortenToolPath(text: string) {
  return String(text || '').replace(/^\/home\/cicy\/cicy-ai\/workers\//, '~/cicy-ai/workers/');
}

export function tryParseJSONObject(text: string) {
  const value = String(text || '').trim();
  if (!value.startsWith('{') && !value.startsWith('[')) return null;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function decodeJSStringLiteral(literal: string): string {
  const value = String(literal || '').trim();
  if (value.startsWith('"')) {
    try { return JSON.parse(value); } catch { return value.slice(1, -1); }
  }
  return value.slice(1, -1)
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\r')
    .replace(/\\t/g, '\t')
    .replace(/\\([\\'"`])/g, '$1');
}

function codexWrappedCommands(source: string): string[] {
  const commands: string[] = [];
  const pattern = /tools\.exec_command\s*\(\s*\{[\s\S]*?\bcmd\s*:\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`)/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(source))) {
    const command = decodeJSStringLiteral(match[1]).trim();
    if (command) commands.push(command);
  }
  return commands;
}

// Codex exposes orchestration calls as a generic `exec` whose input is
// JavaScript (`tools.exec_command(...)`). The user cares about the shell command,
// not that transport wrapper. Pure wait/empty-poll calls carry no user-facing
// action and are omitted entirely.
export function normalizeToolForDisplay(tool: any): any | null {
  const name = String(tool?.name || '').trim().toLowerCase();
  const raw = String(tool?.arg || '').trim();
  const parsed = tryParseJSONObject(raw) as Record<string, any> | null;
  if (name === 'wait') return null;
  if (name === 'write_stdin' && (!parsed || String(parsed.chars || '') === '')) return null;
  if (name !== 'exec') return tool;
  if (/tools\.wait\s*\(/.test(raw)) return null;
  if (/tools\.write_stdin\s*\(/.test(raw)) {
    const chars = raw.match(/\bchars\s*:\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`)/);
    if (!chars || decodeJSStringLiteral(chars[1]) === '') return null;
  }
  const commands = codexWrappedCommands(raw);
  if (!commands.length) return tool;
  return { ...tool, name: 'exec_command', arg: JSON.stringify({ command: commands.join('\n\n') }) };
}

export function humanizeToolPayload(value: any, depth = 0): string {
  if (value == null) return '';
  if (typeof value === 'string') return shortenToolPath(value);
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    const parts = value.map((item) => humanizeToolPayload(item, depth + 1)).filter(Boolean);
    return parts.join('\n').trim();
  }
  if (typeof value !== 'object') return String(value || '').trim();

  const preferredKeys = ['file_path', 'command', 'subject', 'description', 'text', 'content', 'input', 'output', 'result', 'old_string', 'new_string', 'question', 'label', 'name'];
  const lines: string[] = [];
  for (const key of preferredKeys) {
    if (!(key in value)) continue;
    const formatted = humanizeToolPayload(value[key], depth + 1);
    if (!formatted) continue;
    if (depth === 0 && (key === 'command' || key === 'file_path' || key === 'subject' || key === 'text')) {
      lines.push(formatted);
    } else {
      lines.push(`${key.replace(/_/g, ' ')}: ${formatted}`);
    }
  }
  if (lines.length) return lines.join('\n').trim();

  for (const [key, raw] of Object.entries(value)) {
    const formatted = humanizeToolPayload(raw, depth + 1);
    if (!formatted) continue;
    lines.push(`${key.replace(/_/g, ' ')}: ${formatted}`);
  }
  return lines.join('\n').trim();
}

export function formatToolArg(tool: any) {
  const raw = String(tool?.arg || '').trim();
  if (!raw) return '';
  const parsed = tryParseJSONObject(raw);
  if (parsed != null) {
    const pretty = humanizeToolPayload(parsed);
    if (pretty) return pretty;
  }
  return shortenToolPath(raw);
}

// Parse a tool's input back into its object form (arg is JSON-stringified input).
export function parseToolInput(tool: any): Record<string, any> | null {
  const raw = String(tool?.arg || '').trim();
  if (!raw.startsWith('{') && !raw.startsWith('[')) return null;
  try {
    const v = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
  } catch {
    return null;
  }
}

// The ONE identifier a user scans for: which file / what command / what pattern.
// Shown in the (always-visible) header so the row is meaningful without expanding.
const TOOL_HEADLINE_KEYS = ['file_path', 'filePath', 'path', 'notebook_path', 'command', 'cmd', 'pattern', 'url', 'description', 'prompt', 'query', 'subject'];
export function toolHeadline(tool: any): string {
  const input = parseToolInput(tool);
  if (input) {
    for (const k of TOOL_HEADLINE_KEYS) {
      const v = input[k];
      if (v != null && String(v).trim()) return shortenToolPath(String(v).trim());
    }
  }
  const raw = String(tool?.arg || '').trim();
  // codex apply_patch:arg 是 patch 文本(非 JSON)。headline 显示被改的文件,
  // 而不是无意义的 "*** Begin Patch"。
  if (isPatchText(raw)) {
    const m = raw.match(/\*\*\*\s*(Update|Add|Delete|Move) File:\s*(.+)/);
    if (m) return `${m[1]} ${shortenToolPath(m[2].trim())}`;
  }
  return shortenToolPath((formatToolArg(tool).split('\n')[0] || '').trim());
}

// The body of a file write (Write/NotebookEdit) — large, shown only when expanded,
// in a no-wrap horizontal-scroll block (never break-all the source).
export function toolBodyContent(tool: any): string {
  const input = parseToolInput(tool);
  if (!input) return '';
  // new_string is intentionally excluded — Edit's old/new render as a diff.
  const c = input.content ?? input.new_source;
  return typeof c === 'string' ? c : '';
}

// Build an old→new diff for edit-style tools. Claude Edit uses old_string/new_string,
// MultiEdit uses edits[], and some paths precompute tool.diff — without surfacing
// any of these the tool card showed no diff at all ("edit diff 没出来").
export function toolEditDiff(tool: any): { old: string; new: string } | null {
  if (tool?.diff?.old || tool?.diff?.new) {
    return { old: String(tool.diff.old || ''), new: String(tool.diff.new || '') };
  }
  const input = parseToolInput(tool);
  if (!input) return null;
  if (typeof input.old_string === 'string' || typeof input.new_string === 'string') {
    return { old: String(input.old_string || ''), new: String(input.new_string || '') };
  }
  if (Array.isArray(input.edits) && input.edits.length) {
    const oldT = input.edits.map((e: any) => String(e?.old_string || '')).filter(Boolean).join('\n');
    const newT = input.edits.map((e: any) => String(e?.new_string || '')).filter(Boolean).join('\n');
    if (oldT || newT) return { old: oldT, new: newT };
  }
  return null;
}

// Strip Claude Code's internal annotations from a tool result so history shows
// only the meaningful output. "(file state is current … no need to Read it back)"
// is a note to the model, not the user.
export function cleanToolResult(text: string): string {
  return String(text || '')
    .replace(/\s*\(file state is current in your context[^)]*\)/gi, '')
    .replace(/\s*\(no content\)\s*/gi, '')
    .trim();
}

// exec_command that exits cleanly with no stdout used to render as an empty
// result → the expanded card looked like the click did nothing. Show a concise
// status instead (no command duplication, never an empty body).
export function exitNoOutputNote(raw: string): string {
  const m = raw.match(/Process exited with code (\d+)/);
  return m
    ? i18n.t('toolExitCodeNoOutput', { ns: 'chat', code: m[1], defaultValue: '退出码 {{code}} · 无输出' })
    : i18n.t('toolExitNoOutput', { ns: 'chat', defaultValue: '退出 · 无输出' });
}

export function formatToolResult(tool: any) {
  const name = String(tool?.name || '').trim();
  const raw = String(tool?.result || '').trim();
  if (!raw) {
    return '';
  }
  const parsed = tryParseJSONObject(raw);
  if (parsed != null) {
    if (name === 'exec_command' && parsed && typeof parsed === 'object' && !Array.isArray(parsed) && 'output' in parsed) {
      return shortenToolPath(String((parsed as any).output || '').trim());
    }
    const pretty = humanizeToolPayload(parsed);
    if (pretty) return pretty;
  }
  const marker = '\nOutput:\n';
  const index = raw.indexOf(marker);
  if (index >= 0) {
    const suffix = raw.slice(index + marker.length).trim();
    if (suffix) {
      const parsedSuffix = tryParseJSONObject(suffix);
      if (parsedSuffix != null) {
        const pretty = humanizeToolPayload(parsedSuffix);
        if (pretty) return pretty;
      }
      return shortenToolPath(suffix);
    }
    if (/Process exited with code 0\b/.test(raw)) return '';
  }
  if (name === 'exec_command' && /Process exited with code 0\b/.test(raw)) return '';
  return shortenToolPath(raw);
}

export function buildToolCardId(turnKey: string | number, stepIndex: number, tool: any, toolIndex: number) {
  const name = String(tool?.name || 'tool').trim();
  return `${turnKey}:${stepIndex}:${toolIndex}:${name}`;
}
