// Per-agent live header metrics (status / model / context usage / cost),
// derived from /api/agents/current-reply (reply.json). Extracted from the
// retired Office window header so TeamPanel (and future surfaces) reuse the
// same battle-tested math instead of reinventing it.

export interface AgentLiveMetrics {
  working: boolean;
  model: string;
  /** context usage 0-100 (%) */
  ctx: number;
  /** context window size in k tokens (for tooltip) */
  ctxK: number;
  /** cumulative cost in $ (cost_credit) */
  cost: number;
  /** change signature — skip re-render when unchanged */
  sig: string;
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));

// 模型基础上下文窗口(k tokens)。粗映射,未知给 200k。
export function modelWindowK(model: string): number {
  const m = (model || '').toLowerCase();
  if (m.includes('gemini')) return 1000;
  // opus 的 1M beta 有效窗口实测约 2M(见 Office 时代的标定注释)。
  if (m.includes('opus')) return 2000;
  if (m.includes('claude')) return 200;
  if (m.includes('gpt') || m.includes('o1') || m.includes('o3') || m.includes('o4')) return 256;
  if (m.includes('deepseek')) return 128;
  return 200;
}

// 整段 prompt 的 token 数(= 上下文占用)。坑:网关已把 cache_read 计进 input_tokens
// (input≈cache_read),标准 Anthropic 则分开。取较大解释避免重复计数。
export function promptTokens(d: any): number {
  const inp = Number(d?.input_tokens || 0), cr = Number(d?.cache_read_input_tokens || 0), cc = Number(d?.cache_creation_input_tokens || 0);
  return inp >= cr ? inp + cc : inp + cr + cc;
}

const TERMINAL_STATUSES = ['completed', 'complete', 'done', 'idle', 'aborted', 'error', 'canceled', 'cancelled', 'failed', 'stopped'];

/** Fold one current-reply payload into metrics; prev carries last-known values. */
export function metricsFromCurrentReply(d: any, prev?: AgentLiveMetrics | null): AgentLiveMetrics {
  const st = String(d?.status || '').trim().toLowerCase();
  const done = d?.complete === true || st === '' || TERMINAL_STATUSES.includes(st);
  const working = !done;
  const model = String(d?.model || prev?.model || '');
  const inTok = promptTokens(d);
  // Claude Code 自报的权威用量(statusline 落盘)优先;没有才按 token/窗口估算。
  const realPct = d?.context_used_pct;
  const useReal = typeof realPct === 'number' && realPct >= 0;
  const winK = useReal && d?.context_window_size ? Math.round(d.context_window_size / 1000) : modelWindowK(model);
  const ctx = useReal
    ? clamp(Math.round(realPct), 0, 100)
    : (winK > 0 && inTok > 0 ? clamp(Math.round((inTok / (winK * 1000)) * 100), 0, 100) : (prev?.ctx ?? 0));
  const cost = Number(d?.cost_credit || 0) || (prev?.cost ?? 0);
  const sig = `${working ? 1 : 0}|${model}|${ctx}|${cost}`;
  return { working, model, ctx, ctxK: winK, cost, sig };
}
