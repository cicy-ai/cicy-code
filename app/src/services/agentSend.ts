// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import apiService from './api';
import { sendCommandToTmux } from './mockApi';
import { isCicyLiteAgent } from '../lib/agentType';
import { getAgentSendTarget } from './agentSendTarget';

// pane short-id → agent_type, filled lazily from /api/tmux/panes.
const agentTypeCache = new Map<string, string>();
const recentGlobalSends = new Map<string, number>();
const GLOBAL_SEND_DEDUPE_MS = 5_000;

function shortId(paneId: string): string {
  return String(paneId || '').split(':')[0];
}

function claimGlobalSend(source: 'team' | 'project', paneId: string, text: string): { duplicate: boolean; key: string } {
  const now = Date.now();
  const key = `${source}\u0000${paneId}\u0000${text}`;
  const previous = recentGlobalSends.get(key) || 0;
  if (now - previous < GLOBAL_SEND_DEDUPE_MS) return { duplicate: true, key };
  recentGlobalSends.set(key, now);
  if (recentGlobalSends.size > 100) {
    for (const [candidate, sentAt] of recentGlobalSends) {
      if (now - sentAt >= GLOBAL_SEND_DEDUPE_MS) recentGlobalSends.delete(candidate);
    }
  }
  return { duplicate: false, key };
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
 * Single entry point for UI actions that hand text to an Agent.
 *
 * Normal actions use the global selection:
 *  - Team Agent → send immediately through /api/tmux/send.
 *  - Project Agent → fill that card's footer without submitting.
 *  - no selection → show "未选中 Agent" and return false.
 *
 * Composer-owned sends use `fromComposer`; directed background workflows use
 * `routing: 'explicit'`. Both bypass the global target and retain the legacy
 * cicy-lite/terminal transport distinction. The Knowledge panel is deliberately
 * separate: it fills its knowledge-specialist DispatcherChat directly.
 */
export async function sendToAgent(
  paneId: string,
  text: string,
  opts: { submit?: boolean; agentType?: string; fromComposer?: boolean; deferUntilReady?: boolean; routing?: 'global' | 'explicit' } = {},
): Promise<boolean> {
  if (!opts.fromComposer && opts.routing !== 'explicit') {
    const target = getAgentSendTarget();
    if (!target.paneId || target.source === 'none') {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '未选中 Agent' }));
      return false;
    }
    const claim = claimGlobalSend(target.source, target.paneId, text);
    if (claim.duplicate) return true;
    if (target.source === 'project') {
      window.dispatchEvent(new CustomEvent('cicy:fill-project-composer', {
        detail: { paneId: target.paneId, text },
      }));
      return true;
    }
    try {
      await apiService.sendCommand(target.paneId, text, true, { deferUntilReady: true });
    } catch (error) {
      recentGlobalSends.delete(claim.key);
      throw error;
    }
    return true;
  }
  const submit = opts.submit ?? false;
  const type = opts.agentType ?? (await resolveAgentType(paneId));
  if (isCicyLiteAgent(type)) {
    if (submit) {
      if (opts.deferUntilReady) await apiService.sendCommand(paneId, text, true, { deferUntilReady: true });
      else await apiService.sendCommand(paneId, text, true);
    } else {
      window.dispatchEvent(new CustomEvent('cicy:fill-composer', { detail: { paneId, text } }));
    }
    return true;
  }
  if (opts.deferUntilReady) await sendCommandToTmux(text, paneId, submit, { deferUntilReady: true });
  else await sendCommandToTmux(text, paneId, submit);
  return true;
}
