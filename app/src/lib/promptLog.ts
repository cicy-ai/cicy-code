// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0
//
// Pure helpers behind the cross-agent prompt list (PromptLogModal): turning the
// server's per-agent logs into one time-ordered list, pulling attachments out
// of a prompt's text, filtering and day-grouping.

export interface PromptLogEntry {
  key: string;
  agentId: string;
  ts: string;
  /** Epoch ms; 0 when the server sent no usable time. */
  time: number;
  content: string;
}

export interface PromptAttachment {
  name: string;
  src: string;
  image: boolean;
}

export type PromptLogRange = 'all' | 'today' | 'week';

const IMAGE_RE = /\.(png|jpe?g|gif|webp|svg|bmp|avif|heic|heif)(?:[?#].*)?$/i;
// Trailing blanks go with a removed attachment so no stray space is left behind.
const LINK_RE = /!?\[([^\]\n]*)\]\(([^)\s]+)\)[ \t]*/g;
const PASTE_TAG_RE = /<\/?pasted_content\b[^>]*>/g;

const isAttachmentPath = (src: string) => src.includes('/cicy-ai/assets/') || src.startsWith('file:') || src.startsWith('/assets/files/');

/** Splits a prompt into the words the user wrote and the files they attached. */
export function parsePromptContent(content: string): { text: string; attachments: PromptAttachment[] } {
  const attachments: PromptAttachment[] = [];
  const text = String(content || '')
    .replace(PASTE_TAG_RE, '')
    .replace(LINK_RE, (match, label: string, src: string) => {
      if (!isAttachmentPath(src)) return match;
      const name = String(label || '').trim() || decodeURIComponent(src.split(/[?#]/, 1)[0].split('/').pop() || 'file');
      attachments.push({ name, src, image: IMAGE_RE.test(src) || IMAGE_RE.test(name) });
      return '';
    })
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  return { text, attachments };
}

/** Flattens `{agentId: [{ts, content}]}` into one list, newest first. */
export function mergePromptLogs(byAgent: Record<string, Array<{ ts?: string; content?: string }> | null | undefined>): PromptLogEntry[] {
  const out: PromptLogEntry[] = [];
  for (const [agentId, rows] of Object.entries(byAgent || {})) {
    (rows || []).forEach((row, index) => {
      const content = String(row?.content || '').trim();
      if (!content) return;
      const ts = String(row?.ts || '');
      const time = Date.parse(ts);
      out.push({ key: `${agentId}:${index}:${ts}`, agentId, ts, time: Number.isFinite(time) ? time : 0, content });
    });
  }
  return out.sort((a, b) => b.time - a.time);
}

export const startOfDay = (time: number) => { const d = new Date(time); d.setHours(0, 0, 0, 0); return d.getTime(); };

export function filterPromptLog(entries: PromptLogEntry[], opts: { query?: string; agentId?: string; range?: PromptLogRange; now?: number }): PromptLogEntry[] {
  const words = String(opts.query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const today = startOfDay(opts.now ?? Date.now());
  const since = opts.range === 'today' ? today : opts.range === 'week' ? today - 6 * 86_400_000 : 0;
  return entries.filter((entry) => {
    if (opts.agentId && entry.agentId !== opts.agentId) return false;
    if (since && entry.time < since) return false;
    if (!words.length) return true;
    const haystack = entry.content.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}

/** Groups a newest-first list into days, keeping the order. */
export function groupPromptLogByDay(entries: PromptLogEntry[]): Array<{ day: number; entries: PromptLogEntry[] }> {
  const groups: Array<{ day: number; entries: PromptLogEntry[] }> = [];
  for (const entry of entries) {
    const day = entry.time ? startOfDay(entry.time) : 0;
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.entries.push(entry);
    else groups.push({ day, entries: [entry] });
  }
  return groups;
}

const pad = (n: number) => String(n).padStart(2, '0');
export const formatPromptClock = (time: number) => { if (!time) return '--:--'; const d = new Date(time); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; };
export const formatPromptDate = (time: number) => { const d = new Date(time); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`; };
export const formatPromptDateTime = (time: number) => (time ? `${formatPromptDate(time)} ${formatPromptClock(time)}:${pad(new Date(time).getSeconds())}` : '');

/** Plain-text export of a list: one block per prompt, oldest first. */
export function promptLogToText(entries: PromptLogEntry[], agentName: (id: string) => string): string {
  return [...entries].reverse()
    .map((entry) => `[${entry.time ? `${formatPromptDate(entry.time)} ${formatPromptClock(entry.time)}` : '?'}] ${agentName(entry.agentId)}\n${entry.content}`)
    .join('\n\n');
}
