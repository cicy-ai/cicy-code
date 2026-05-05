import { useEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import apiService from '../../services/api';
import { useApp } from '../../contexts/AppContext';

type HistoryTurn = {
  history_id?: number;
  role?: string;
  text?: string;
  q: string;
  a?: string;
  steps?: Array<{ type: 'text'; text: string } | { type: 'thinking'; text: string } | { type: 'tool'; tools: any[] }>;
  status?: string;
  ts?: number;
  start_ts?: number;
  credit?: number;
  model?: string;
};

function historyTurnScore(turn: HistoryTurn): number {
  const answerLen = String(turn?.a || '').trim().length;
  const stepLen = Array.isArray(turn?.steps)
    ? turn.steps.reduce((sum, step) => sum + String((step as any)?.text || '').trim().length, 0)
    : 0;
  return answerLen + stepLen;
}

function normalizeHistoryTurns(items: HistoryTurn[]): HistoryTurn[] {
  const byHistoryID = new Map<number, HistoryTurn>();
  const ordered: HistoryTurn[] = [];
  for (const item of items) {
    const historyID = Number(item?.history_id || 0);
    if (historyID > 0) {
      const prev = byHistoryID.get(historyID);
      if (!prev) {
        byHistoryID.set(historyID, item);
        ordered.push(item);
        continue;
      }
      if (historyTurnScore(item) >= historyTurnScore(prev)) {
        byHistoryID.set(historyID, item);
        const index = ordered.indexOf(prev);
        if (index >= 0) ordered[index] = item;
      }
      continue;
    }
    ordered.push(item);
  }

  const byQuestion = new Map<string, number>();
  const deduped: HistoryTurn[] = [];
  for (const item of ordered) {
    const q = String(item?.q || '').trim();
    if (!q) {
      deduped.push(item);
      continue;
    }
    const existingIndex = byQuestion.get(q);
    if (existingIndex == null) {
      byQuestion.set(q, deduped.length);
      deduped.push(item);
      continue;
    }
    if (historyTurnScore(item) >= historyTurnScore(deduped[existingIndex])) {
      deduped[existingIndex] = item;
    }
  }
  return deduped;
}

async function getCurrentHistory(paneId: string, params: { limit?: number; before?: number; conversation_id?: string } = {}) {
  const { data } = await apiService.getAgentCurrentHistory(paneId, params);
  return data;
}

function CollapsibleQ({ text }: { text: string }) {
  return (
    <div className="mb-2.5 flex justify-end">
      <div className="max-w-[95%] rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
        <Markdown remarkPlugins={[remarkGfm]}>{String(text || '').replace(/^\-\n/, '')}</Markdown>
      </div>
    </div>
  );
}

function ToolCard({ tool }: { tool: any }) {
  const [open, setOpen] = useState(false);
  const hasDiff = !!tool?.diff?.old || !!tool?.diff?.new;
  return (
    <div className="overflow-hidden rounded-lg border border-emerald-300/[0.08] bg-emerald-950/[0.12]">
      <div className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-zinc-500 hover:bg-white/[0.025] hover:text-zinc-400" onClick={() => setOpen((value) => !value)}>
        <span className="text-xs text-emerald-400/70">✓</span>
        <span className="text-xs opacity-60">⚙️</span>
        <span className="rounded border border-white/[0.04] bg-white/[0.035] px-1 py-0.5 text-xs text-zinc-400">{tool?.name || 'tool'}</span>
        {!open ? <span className="flex-1 truncate font-mono text-xs text-zinc-500">{tool?.arg || ''}</span> : null}
        <span className="text-xs text-zinc-600">{open ? '▼' : '▶'}</span>
      </div>
      {open && tool?.arg ? <div className="border-b border-white/[0.04] px-3 py-1.5 font-mono text-sm text-zinc-400/80 whitespace-pre-wrap break-all">{tool.arg}</div> : null}
      {open && hasDiff ? (
        <div className="mx-2 mb-2 max-h-[300px] overflow-auto rounded border border-white/[0.06] font-mono text-xs">
          {tool.diff.old ? tool.diff.old.split('\n').map((line: string, index: number) => <div key={`old-${index}`} className="bg-red-500/[0.08] px-2 leading-relaxed whitespace-pre-wrap break-all text-red-400/80">- {line}</div>) : null}
          {tool.diff.new ? tool.diff.new.split('\n').map((line: string, index: number) => <div key={`new-${index}`} className="bg-emerald-500/[0.08] px-2 leading-relaxed whitespace-pre-wrap break-all text-emerald-400/80">+ {line}</div>) : null}
        </div>
      ) : open && tool?.result ? (
        <pre className="mx-2 mb-2 max-h-[200px] overflow-auto rounded bg-emerald-500/[0.04] px-2.5 py-1.5 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all text-emerald-300/70">{tool.result}</pre>
      ) : null}
    </div>
  );
}

function ThinkingBlock({ text }: { text: string }) {
  return (
    <div className="mb-2 rounded-lg border border-amber-300/[0.08] bg-amber-500/[0.05] px-3 py-2">
      <div className="mb-1 text-[11px] uppercase text-amber-300/60">Thinking</div>
      <div className="chat-markdown current-history-markdown text-sm leading-[1.7] text-amber-50/75">
        <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
      </div>
    </div>
  );
}

export default function CurrentHistoryView({
  paneId,
  open,
  inspectorVersion,
}: {
  paneId: string;
  open: boolean;
  inspectorVersion?: number;
}) {
  const { subscribeChatWs } = useApp();
  const [items, setItems] = useState<HistoryTurn[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [conversationId, setConversationId] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const didInitialScrollRef = useRef(false);

  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    didInitialScrollRef.current = false;
    setLoading(true);
    getCurrentHistory(paneId, { limit: 5 })
      .then((data) => {
        if (cancelled) return;
        setItems(normalizeHistoryTurns(Array.isArray(data?.items) ? data.items : []));
        setHasMore(!!data?.has_more);
        setNextBefore(Number(data?.next_before || 0) || null);
        setConversationId(String(data?.conversation_id || ''));
      })
      .catch(() => {
        if (cancelled) return;
        setItems([]);
        setHasMore(false);
        setNextBefore(null);
        setConversationId('');
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, paneId]);

  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    const mergeLatest = async () => {
      try {
        const data = await getCurrentHistory(paneId, { limit: 2, conversation_id: conversationId || undefined });
        if (cancelled) return;
        const latestItems = Array.isArray(data?.items) ? data.items : [];
        if (!latestItems.length) return;
        setItems((prev) => {
          const next = prev.slice();
          for (const latest of latestItems) {
            const latestID = Number(latest?.history_id || 0);
            if (latestID > 0) {
              const existingIndex = next.findIndex((item) => Number(item?.history_id || 0) === latestID);
              if (existingIndex >= 0) {
                next[existingIndex] = latest;
                continue;
              }
            }
            const latestQ = String(latest?.q || '').trim();
            if (latestQ) {
              let replaced = false;
              for (let i = next.length - 1; i >= 0; i -= 1) {
                if (String(next[i]?.q || '').trim() !== latestQ) continue;
                next[i] = latest;
                replaced = true;
                break;
              }
              if (replaced) continue;
            }
            next.push(latest);
          }
          if (next.length > 5 && hasMore) {
            next.splice(0, next.length - 5);
          }
          return normalizeHistoryTurns(next);
        });
        setConversationId(String(data?.conversation_id || conversationId || ''));
        setHasMore(!!data?.has_more);
        setNextBefore(Number(data?.next_before || 0) || null);
      } catch {}
    };
    return subscribeChatWs((msg) => {
      if (cancelled) return;
      if (msg?.type !== 'current_updated') return;
      const agentID = String(msg?.data?.agent_id || '').trim();
      if (agentID !== paneId) return;
      void mergeLatest();
    });
  }, [conversationId, hasMore, open, paneId, subscribeChatWs]);

  useEffect(() => {
    if (!open || loading || didInitialScrollRef.current) return;
    const frame = window.requestAnimationFrame(() => {
      const el = scrollRef.current;
      if (!el) return;
      el.scrollTop = el.scrollHeight;
      didInitialScrollRef.current = true;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [open, loading, items.length]);

  const loadMore = async () => {
    if (loadingMore || loading || !hasMore || !nextBefore) return;
    const el = scrollRef.current;
    const prevScrollHeight = el?.scrollHeight || 0;
    const prevScrollTop = el?.scrollTop || 0;
    setLoadingMore(true);
    try {
      const data = await getCurrentHistory(paneId, {
        limit: 5,
        before: nextBefore,
        conversation_id: conversationId || undefined,
      });
      const older = Array.isArray(data?.items) ? data.items : [];
      setItems((prev) => normalizeHistoryTurns([...older, ...prev]));
      setHasMore(!!data?.has_more);
      setNextBefore(Number(data?.next_before || 0) || null);
      setConversationId(String(data?.conversation_id || conversationId || ''));
      window.requestAnimationFrame(() => {
        const nextEl = scrollRef.current;
        if (!nextEl) return;
        nextEl.scrollTop = prevScrollTop + Math.max(0, nextEl.scrollHeight - prevScrollHeight);
      });
    } catch {
      setHasMore(false);
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <div data-id="current-history-view" className="flex h-full flex-col">
      <div data-id="current-history-scroll" ref={scrollRef} className="flex-1 overflow-y-auto pb-6">
        <div data-id="current-history-list" data-agent-id={paneId || ''} className="mx-auto max-w-full px-2 py-4 font-sans text-zinc-300">
          {loading ? (
            <div data-id="current-history-loading" className="flex flex-col items-center justify-center gap-3 pt-20">
              <div className="h-6 w-6 animate-spin rounded-full border-2 border-vsc-accent/30 border-t-vsc-accent" />
              <span className="text-base text-zinc-500">正在加载历史记录...</span>
            </div>
          ) : items.length === 0 ? (
            <div data-id="current-history-empty" className="pt-20 text-center">
              <div className="mb-2 text-2xl text-zinc-700">✦</div>
              <p className="text-xs text-zinc-500">current.json 暂无可用历史</p>
            </div>
          ) : <>
            {hasMore ? (
              <div data-id="current-history-load-more-wrap" className="mb-4 flex justify-center">
                <button
                  type="button"
                  data-id="current-history-load-more"
                  onClick={() => { void loadMore(); }}
                  disabled={loadingMore}
                  className="rounded-md border border-white/[0.07] bg-white/[0.025] px-3 py-1.5 text-xs text-zinc-500 transition-colors hover:border-white/[0.12] hover:text-zinc-300 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {loadingMore ? '加载中...' : '加载更早'}
                </button>
              </div>
            ) : null}
            {items.map((turn, index) => {
            const turnKey = turn?.history_id || `${turn?.text || turn?.q || 'turn'}-${index}`;
            const steps = Array.isArray(turn?.steps) ? turn.steps : [];
            if (turn?.role === 'user') {
              return (
                <div data-id="current-history-turn" key={turnKey} className="mb-5">
                  <CollapsibleQ text={turn.text || turn.q} />
                </div>
              );
            }
            if (turn?.role === 'assistant') {
              return (
                <div data-id="current-history-turn" key={turnKey} className="mb-5">
                  <div className="overflow-hidden rounded-xl border border-white/[0.055] bg-white/[0.018]">
                    <div className="flex flex-wrap items-center gap-1.5 border-b border-white/[0.035] bg-black/[0.10] px-3.5 py-1.5">
                      <span className="text-sm font-medium text-sky-300/70">✦ AI</span>
                      {turn?.model ? <span className="rounded border border-white/[0.04] bg-white/[0.025] px-1.5 py-0.5 font-mono text-xs text-zinc-500">{turn.model}</span> : null}
                    </div>
                    <div className="px-3.5 py-2.5">
                      {steps.map((step: any, stepIndex: number) => {
                        if (step.type === 'thinking') {
                          return <ThinkingBlock key={stepIndex} text={step.text} />;
                        }
                        if (step.type === 'text') {
                          return <div key={stepIndex} className="chat-markdown current-history-markdown text-base leading-[1.7] text-zinc-300"><Markdown remarkPlugins={[remarkGfm]}>{step.text}</Markdown></div>;
                        }
                        const tools = Array.isArray(step.tools) ? step.tools : [];
                        return <div key={stepIndex} className="my-2 space-y-1.5">{tools.map((tool: any, toolIndex: number) => <ToolCard key={toolIndex} tool={tool} />)}</div>;
                      })}
                    </div>
                  </div>
                </div>
              );
            }
            return (
              <div data-id="current-history-turn" key={turnKey} className="mb-5">
                <CollapsibleQ text={turn.q} />
                <div className="overflow-hidden rounded-xl border border-white/[0.055] bg-white/[0.018]">
                  <div className="flex flex-wrap items-center gap-1.5 border-b border-white/[0.035] bg-black/[0.10] px-3.5 py-1.5">
                    <span className="text-sm font-medium text-sky-300/70">✦ AI</span>
                    {turn?.model ? <span className="rounded border border-white/[0.04] bg-white/[0.025] px-1.5 py-0.5 font-mono text-xs text-zinc-500">{turn.model}</span> : null}
                  </div>
                  <div className="px-3.5 py-2.5">
                    {steps.map((step: any, stepIndex: number) => {
                      if (step.type === 'thinking') {
                        return <ThinkingBlock key={stepIndex} text={step.text} />;
                      }
                      if (step.type === 'text') {
                        return <div key={stepIndex} className="chat-markdown current-history-markdown text-base leading-[1.7] text-zinc-300"><Markdown remarkPlugins={[remarkGfm]}>{step.text}</Markdown></div>;
                      }
                      const tools = Array.isArray(step.tools) ? step.tools : [];
                      return <div key={stepIndex} className="my-2 space-y-1.5">{tools.map((tool: any, toolIndex: number) => <ToolCard key={toolIndex} tool={tool} />)}</div>;
                    })}
                  </div>
                </div>
              </div>
            );
          })}
          </>}
        </div>
      </div>
    </div>
  );
}
