// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import apiService from './api';
import { sendCommandToTmux } from './mockApi';
import { isCicyLiteAgent } from '../lib/agentType';

// pane short-id → agent_type, filled lazily from /api/tmux/panes.
const agentTypeCache = new Map<string, string>();

function shortId(paneId: string): string {
  return String(paneId || '').split(':')[0];
}

// Let callers pre-seed the cache (e.g. Workspace already knows pane types) so
// the first send doesn't have to round-trip getPanes.
export function primeAgentType(paneId: string, agentType: string): void {
  const id = shortId(paneId);
  if (id) agentTypeCache.set(id, String(agentType || ''));
}

async function resolveAgentType(paneId: string): Promise<string> {
  const short = shortId(paneId);
  if (agentTypeCache.has(short)) return agentTypeCache.get(short) || '';
  try {
    const { data } = await apiService.getPanes();
    const panes = Array.isArray(data) ? data : (Array.isArray(data?.panes) ? data.panes : []);
    for (const p of panes) {
      const id = shortId(String(p?.pane_id || p?.id || p?.name || ''));
      if (id) agentTypeCache.set(id, String(p?.agent_type || ''));
    }
  } catch { /* leave uncached → falls back to terminal path */ }
  return agentTypeCache.get(short) || '';
}

/**
 * Route a prompt/text to an agent by its type — the single entry point every
 * "send/ask the agent" button should use.
 *
 *  - cicy-lite agents have a CHAT COMPOSER, not a terminal:
 *      submit=false → drop the text into the composer (DispatcherChat) for the
 *                     operator to review/edit/send (`cicy:fill-composer` event).
 *      submit=true  → deliver straight to the cicy REPL (same pipe DispatcherChat
 *                     uses), i.e. auto-send.
 *  - terminal agents (claude/codex/opencode/…) → type into tmux; `submit`
 *    decides whether Enter is pressed.
 *
 * Why: many call sites used sendCommandToTmux / apiService.sendCommand(..., false)
 * directly, which is a no-op for cicy agents (the text lands in the REPL stdin
 * buffer, never the composer). Funnel them through here so cicy works everywhere.
 */
export async function sendToAgent(
  paneId: string,
  text: string,
  opts: { submit?: boolean; agentType?: string } = {},
): Promise<void> {
  const submit = opts.submit ?? false;
  const type = opts.agentType ?? (await resolveAgentType(paneId));
  if (isCicyLiteAgent(type)) {
    if (submit) {
      await apiService.sendCommand(paneId, text, true);
    } else {
      window.dispatchEvent(new CustomEvent('cicy:fill-composer', { detail: { paneId, text } }));
    }
    return;
  }
  await sendCommandToTmux(text, paneId, submit);
}
