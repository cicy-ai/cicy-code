import type { HistoryTurn, RawHistoryItem } from '../types';
import { normalizeRawHistoryItem } from './normalizeItem';

export function buildIdRange(lo: number, hi: number): number[] {
  const out: number[] = [];
  for (let id = lo; id <= hi; id += 1) out.push(id);
  return out;
}

export function buildTurnsFromRawItems(rawItems: RawHistoryItem[]): HistoryTurn[] {
  const toolNameByCallId = new Map<string, string>();
  // OpenAI Responses (non-gateway codex) emits each tool call as TWO top-level
  // items: a `function_call` (name + args) and a separate `function_call_output`
  // (the result). Rendered as-is that's TWO cards for one logical call. Collect
  // outputs + call-ids here so the merge below folds the output INTO the
  // function_call and drops the standalone output → one card (arg + result).
  const fnOutputByCallId = new Map<string, string>();
  const fnCallIds = new Set<string>();
  // OpenAI Chat (gateway codex) is the same split, different shape: an assistant
  // message with `tool_calls[]` (the call) followed by `role:tool` messages (the
  // results, keyed by tool_call_id). Collect results + call-ids so the result
  // folds into the assistant tool_calls[] card and the standalone tool message
  // is dropped → one card per tool, matching the non-gateway path.
  const chatToolResultByCallId = new Map<string, string>();
  const chatToolCallIds = new Set<string>();
  for (const raw of rawItems) {
    const item = raw || {};
    const it = String(item?.type || '').trim();
    // function_call (OpenAI Responses) AND custom_tool_call (codex apply_patch)
    // share the same call+output split, paired by call_id → fold identically.
    if (it === 'function_call' || it === 'custom_tool_call') {
      const cid = String(item?.call_id || item?.id || '').trim();
      if (cid) fnCallIds.add(cid);
    }
    if (it === 'function_call_output' || it === 'custom_tool_call_output') {
      const cid = String(item?.call_id || item?.tool_id || item?.id || '').trim();
      const rawOut = (item as any)?.output ?? (item as any)?.result;
      const out = typeof rawOut === 'string' ? rawOut : (rawOut != null ? JSON.stringify(rawOut) : '');
      if (cid && out) fnOutputByCallId.set(cid, out);
    }
    if (Array.isArray((item as any)?.tool_calls)) {
      for (const tc of (item as any).tool_calls as any[]) {
        const cid = String(tc?.id || '').trim();
        if (cid) chatToolCallIds.add(cid);
      }
    }
    const role = String(item?.role || '').trim();
    if (role === 'tool' || role === 'function') {
      const cid = String((item as any)?.tool_call_id || (item as any)?.tool_id || (item as any)?.call_id || '').trim();
      const rawC = (item as any)?.content ?? (item as any)?.output;
      const res = typeof rawC === 'string' ? rawC : (rawC != null ? JSON.stringify(rawC) : '');
      if (cid && res) chatToolResultByCallId.set(cid, res);
    }
  }
  for (const raw of rawItems) {
    const item = raw || {};
    if (Array.isArray(item.content)) {
      for (const part of item.content as any[]) {
        if (String(part?.type || '').trim() === 'tool_use' && String(part?.name || '').trim()) {
          const callId = String(part?.id || part?.call_id || '').trim();
          if (callId) toolNameByCallId.set(callId, String(part.name).trim());
        }
      }
    }
    if (String(item?.type || '').trim() === 'custom_tool_call' && String(item?.name || '').trim()) {
      const callId = String(item?.call_id || item?.id || '').trim();
      if (callId) toolNameByCallId.set(callId, String(item.name).trim());
    }
    // OpenAI Responses: top-level function_call carries name + call_id, so its
    // function_call_output (a separate item, name-less) can resolve the name.
    if (String(item?.type || '').trim() === 'function_call' && String(item?.name || '').trim()) {
      const callId = String(item?.call_id || item?.id || '').trim();
      if (callId) toolNameByCallId.set(callId, String(item.name).trim());
    }
    // OpenAI Chat: assistant.tool_calls[].function.name keyed by tool_call id, so
    // the matching role:tool result message can resolve the name.
    if (Array.isArray((item as any)?.tool_calls)) {
      for (const tc of (item as any).tool_calls as any[]) {
        const callId = String(tc?.id || '').trim();
        const name = String(tc?.function?.name || tc?.name || '').trim();
        if (callId && name) toolNameByCallId.set(callId, name);
      }
    }
  }
  const merged: RawHistoryItem[] = [];
  for (let i = 0; i < rawItems.length; i += 1) {
    const current = rawItems[i] || {};
    if (i + 1 < rawItems.length) {
      const next = rawItems[i + 1] || {};
      const currentRole = String(current?.role || '').trim();
      const nextRole = String(next?.role || '').trim();
      if (currentRole === 'assistant' && nextRole === 'user') {
        const currentContent = Array.isArray(current?.content) ? current.content : [];
        const nextContent = Array.isArray(next?.content) ? next.content : [];
        // Index EVERY tool_result in the next user message by its id. A turn can
        // issue PARALLEL tool calls (several tool_use blocks in one assistant
        // message → several tool_result blocks in one user message). The old code
        // matched only the FIRST tool_use ↔ FIRST tool_result and `break`ed, so
        // every parallel call after the first lost its result. Whether the agent
        // batches calls (Anthropic native, e.g. non-gateway) or serializes them
        // (one per turn, e.g. gateway-translated) is provider-dependent — folding
        // ALL results makes both render identically.
        const resultById = new Map<string, string>();
        for (const p of nextContent) {
          const t = String(p?.type || '').trim();
          if (t !== 'tool_result' && t !== 'function_call_output') continue;
          const rid = String(p?.tool_use_id || p?.tool_id || p?.call_id || '').trim();
          if (!rid) continue;
          const raw = p?.content ?? p?.output;
          const result = typeof raw === 'string' ? raw : (raw != null ? JSON.stringify(raw) : '');
          if (result) resultById.set(rid, result);
        }
        const toolUseIds = currentContent
          .filter((p: any) => String(p?.type || '').trim() === 'tool_use')
          .map((p: any) => String(p?.id || '').trim());
        if (toolUseIds.some((id) => id && resultById.has(id))) {
          const item = JSON.parse(JSON.stringify(current));
          const itemContent = Array.isArray(item?.content) ? item.content : [];
          for (let j = 0; j < itemContent.length; j += 1) {
            if (String(itemContent[j]?.type || '').trim() !== 'tool_use') continue;
            const id = String(itemContent[j]?.id || '').trim();
            const result = id ? resultById.get(id) : '';
            if (result) itemContent[j] = { ...itemContent[j], _tool_result: result };
          }
          item.content = itemContent;
          item._has_tool_result = true;
          merged.push(item);
          i += 1;
          continue;
        }
      }
    }
    // OpenAI Responses tool pairing: fold the output into its function_call and
    // drop the standalone function_call_output, so one tool = one card.
    const ct = String(current?.type || '').trim();
    if (ct === 'function_call' || ct === 'custom_tool_call') {
      const cid = String(current?.call_id || current?.id || '').trim();
      const out = cid ? fnOutputByCallId.get(cid) : '';
      if (out) { merged.push({ ...current, _tool_output: out }); continue; }
    }
    if (ct === 'function_call_output' || ct === 'custom_tool_call_output') {
      const cid = String(current?.call_id || current?.tool_id || current?.id || '').trim();
      // Folded into its call above → skip the duplicate card. Keep only orphan
      // outputs (no matching call).
      if (cid && fnCallIds.has(cid)) continue;
    }
    // OpenAI Chat: drop the standalone role:tool result message whose result was
    // folded into the assistant tool_calls[] card. Keep orphans (no matching call).
    const cRole = String(current?.role || '').trim();
    if (cRole === 'tool' || cRole === 'function') {
      const cid = String(current?.tool_call_id || current?.tool_id || current?.call_id || '').trim();
      if (cid && chatToolCallIds.has(cid)) continue;
    }
    merged.push(current);
  }
  return merged
    .map((raw) => normalizeRawHistoryItem(raw, toolNameByCallId, chatToolResultByCallId))
    .filter(Boolean) as HistoryTurn[];
}

function historyTurnScore(turn: HistoryTurn): number {
  const answerLen = String(turn?.a || '').trim().length;
  const stepLen = Array.isArray(turn?.steps)
    ? turn.steps.reduce((sum, step) => sum + String((step as any)?.text || '').trim().length, 0)
    : 0;
  return answerLen + stepLen;
}

function historyTurnOrderValue(turn: HistoryTurn): number {
  const historyID = Number(turn?.history_id || 0);
  if (historyID > 0) return historyID;
  return Number(turn?.ts || turn?.start_ts || 0);
}

function mergeHistoryTurnVersions(prev: HistoryTurn | undefined, incoming: HistoryTurn): HistoryTurn {
  if (!prev) return incoming;
  const prevScore = historyTurnScore(prev);
  const incomingScore = historyTurnScore(incoming);
  const base = incomingScore >= prevScore ? incoming : prev;
  const fallback = incomingScore >= prevScore ? prev : incoming;
  return {
    ...fallback,
    ...base,
    history_id: Number(base?.history_id || fallback?.history_id || 0) || undefined,
    conversation_id: String(base?.conversation_id || fallback?.conversation_id || ''),
    q: String(base?.q || fallback?.q || ''),
    role: String(base?.role || fallback?.role || ''),
    text: String(base?.text || fallback?.text || ''),
    a: String(base?.a || fallback?.a || ''),
    steps: Array.isArray(base?.steps) && base.steps.length ? base.steps : fallback?.steps,
    status: String(base?.status || fallback?.status || ''),
    ts: Number(base?.ts || fallback?.ts || 0) || undefined,
    start_ts: Number(base?.start_ts || fallback?.start_ts || 0) || undefined,
    credit: Number(base?.credit || fallback?.credit || 0) || undefined,
    model: String(base?.model || fallback?.model || ''),
  };
}

export function normalizeHistoryTurns(items: HistoryTurn[]): HistoryTurn[] {
  const byHistoryID = new Map<number, HistoryTurn>();
  const withoutHistoryID: HistoryTurn[] = [];
  for (const item of items) {
    const historyID = Number(item?.history_id || 0);
    if (historyID > 0) {
      byHistoryID.set(historyID, mergeHistoryTurnVersions(byHistoryID.get(historyID), item));
      continue;
    }
    withoutHistoryID.push(item);
  }
  const ordered = Array.from(byHistoryID.values()).sort((a, b) => historyTurnOrderValue(a) - historyTurnOrderValue(b));
  if (!withoutHistoryID.length) return ordered;
  return [...withoutHistoryID.sort((a, b) => historyTurnOrderValue(a) - historyTurnOrderValue(b)), ...ordered];
}

export function extractContentText(content: any): string {
  if (Array.isArray(content)) {
    const parts = content
      .map((part) => {
        if (part && typeof part === 'object') {
          return String((part as any).text || '').trim();
        }
        return '';
      })
      .filter(Boolean);
    return parts.join('\n').trim();
  }
  return String(content || '').trim();
}

export function getVisibleHistorySteps(turn: HistoryTurn, _isLatestTurn: boolean) {
  const steps = Array.isArray(turn?.steps) ? turn.steps : [];
  if (!steps.length) return [] as HistoryTurn['steps'];
  return steps.filter((step: any) => {
    const stepType = String(step?.type || '').trim();
    if (stepType === 'thinking' || stepType === 'text') {
      return String(step?.text || '').trim() !== '';
    }
    if (stepType === 'tool') {
      return Array.isArray(step?.tools) && step.tools.length > 0;
    }
    return false;
  });
}

// replyItemsToSteps turns reply.json's ordered content blocks (thinking/text/
// tool_use) into HistoryTurn steps — the SAME shape the committed history uses,
// so the last q's answer (which lives in reply.json until the next turn migrates
// it into current.json) renders through AssistantTurnView like any other.
export function replyItemsToSteps(items: any, thinking?: string, answer?: string): NonNullable<HistoryTurn['steps']> {
  const steps: NonNullable<HistoryTurn['steps']> = [];
  for (const it of Array.isArray(items) ? items : []) {
    const ty = String(it?.type || '').trim();
    if (ty === 'thinking') {
      const tx = String(it?.thinking || '');
      if (tx) steps.push({ type: 'thinking', text: tx });
    } else if (ty === 'text') {
      const tx = String(it?.text || '');
      if (tx) steps.push({ type: 'text', text: tx });
    } else if (ty === 'tool_use') {
      const inp = it?.input;
      const tool = { name: String(it?.name || ''), arg: inp == null ? '' : (typeof inp === 'string' ? inp : JSON.stringify(inp)), tool_id: String(it?.tool_id || '') };
      const last = steps[steps.length - 1] as any;
      if (last && last.type === 'tool') last.tools.push(tool);
      else steps.push({ type: 'tool', tools: [tool] } as any);
    }
  }
  if (!steps.length) {
    if (String(thinking || '').trim()) steps.push({ type: 'thinking', text: String(thinking) });
    if (String(answer || '').trim()) steps.push({ type: 'text', text: String(answer) });
  }
  return steps;
}

// Content signature of an answer turn — changes iff its rendered content changes.
// A finished turn's sig is stable across polls (→ ref reused → memo skips it);
// the streaming tail's sig grows as text/tools arrive (→ re-renders).
export function turnSig(t: HistoryTurn): string {
  const steps = ((t as any)?.steps || []) as any[];
  let len = String((t as any)?.a || '').length;
  for (const s of steps) len += String(s?.text || '').length + (Array.isArray(s?.tools) ? s.tools.length * 7 : 0);
  return `${t?.history_id || 0}|${(t as any)?.status || ''}|${steps.length}|${len}`;
}
