// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import type { HistoryTurn } from '../types';
import { getVisibleHistorySteps } from './turns';
import { classifySystemNotice, type SystemNotice } from './systemNotice';

// prepareRenderTurns turns the committed history (as stored) into what the list
// actually paints:
//   1. role:system items are classified — harness noise is dropped, a mid-turn
//      user message ("steer") becomes a real user turn, task notices and other
//      notices are kept as system turns carrying their parsed notices.
//   2. Consecutive tool-only assistant records are ONE visual run, even when
//      harness notices were interleaved between the rounds (they always are on
//      Claude Code: every tool_result carries a reminder). The notices are moved
//      AFTER the run so the run stays a single ×N group.
//   3. Adjacent system turns coalesce into one turn holding all their notices,
//      so the timeline shows one folded chip instead of a pill per reminder.
// Only the render copy changes: pagination, committed ids and live de-duping
// keep using the untouched items.

export function isToolOnlyAssistantTurn(turn: HistoryTurn) {
  if (turn?.role !== 'assistant' || turn?.outcome) return false;
  const steps = getVisibleHistorySteps(turn, false) || [];
  return steps.length > 0 && steps.every((step: any) => step?.type === 'tool');
}

function systemTurnNotices(turn: HistoryTurn): SystemNotice[] {
  if (Array.isArray(turn.notices)) return turn.notices;
  const notice = classifySystemNotice(String(turn.text || ''));
  return notice ? [notice] : [];
}

function classifyTurns(turns: HistoryTurn[]): HistoryTurn[] {
  const out: HistoryTurn[] = [];
  for (const turn of turns) {
    // cicy outcome records and /compact summaries are handled by the list itself.
    if (turn?.role !== 'system' || turn?.outcome) { out.push(turn); continue; }
    const notices = systemTurnNotices(turn);
    if (!notices.length) continue; // pure noise
    const steer = notices.filter((n) => n.kind === 'steer');
    const rest = notices.filter((n) => n.kind !== 'steer');
    for (const n of steer) {
      out.push({ ...turn, role: 'user', q: n.text, text: n.text, a: '', steps: [], steer: true, notices: undefined });
    }
    if (rest.length) out.push({ ...turn, notices: rest });
  }
  return out;
}

function mergeToolRunsAndNotices(turns: HistoryTurn[]): HistoryTurn[] {
  const merged: HistoryTurn[] = [];
  // Notices seen since the last tool-only assistant record; flushed after the
  // run ends (or before any non-tool content).
  let pendingNotices: HistoryTurn[] = [];
  const flushNotices = () => {
    if (!pendingNotices.length) return;
    const first = pendingNotices[0];
    merged.push({ ...first, notices: pendingNotices.flatMap((t) => t.notices || []) });
    pendingNotices = [];
  };
  for (const turn of turns) {
    const previous = merged[merged.length - 1];
    if (turn.role === 'system') {
      // Task notices stay where they are chronologically only when no tool run
      // is in progress; inside a run they ride along with the other notices.
      pendingNotices.push(turn);
      continue;
    }
    if (isToolOnlyAssistantTurn(turn) && previous && isToolOnlyAssistantTurn(previous)) {
      merged[merged.length - 1] = {
        ...previous,
        status: turn.status || previous.status,
        steps: [...(previous.steps || []), ...(turn.steps || [])],
      };
      continue;
    }
    if (!isToolOnlyAssistantTurn(turn)) flushNotices();
    merged.push(turn);
  }
  flushNotices();
  // Second pass: coalesce any system turns that ended up adjacent.
  const out: HistoryTurn[] = [];
  for (const turn of merged) {
    const prev = out[out.length - 1];
    if (turn.role === 'system' && prev?.role === 'system') {
      out[out.length - 1] = { ...prev, notices: [...(prev.notices || []), ...(turn.notices || [])] };
      continue;
    }
    out.push(turn);
  }
  return out;
}

export function prepareRenderTurns(turns: HistoryTurn[]): HistoryTurn[] {
  return mergeToolRunsAndNotices(classifyTurns(turns));
}
