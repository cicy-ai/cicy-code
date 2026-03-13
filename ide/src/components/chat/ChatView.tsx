import React, { useState, useEffect, useRef } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import config from '../../config';

interface ChatViewProps {
  paneId: string;
  token: string;
}

// IndexedDB cache
const DB_NAME = 'chat_cache';
const STORE_NAME = 'history';

const openDB = (): Promise<IDBDatabase> => new Promise((resolve, reject) => {
  const req = indexedDB.open(DB_NAME, 1);
  req.onerror = () => reject(req.error);
  req.onsuccess = () => resolve(req.result);
  req.onupgradeneeded = (e) => {
    const db = (e.target as IDBOpenDBRequest).result;
    if (!db.objectStoreNames.contains(STORE_NAME)) db.createObjectStore(STORE_NAME, { keyPath: 'paneId' });
  };
});

const getCache = async (paneId: string): Promise<any[] | null> => {
  try {
    const db = await openDB();
    return new Promise(r => { const req = db.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(paneId); req.onsuccess = () => r(req.result?.data || null); req.onerror = () => r(null); });
  } catch { return null; }
};

const setCache = async (paneId: string, data: any[]) => {
  try { const db = await openDB(); db.transaction(STORE_NAME, 'readwrite').objectStore(STORE_NAME).put({ paneId, data, ts: Date.now() }); } catch {}
};

const TOOL_ICONS: Record<string, string> = {
  fs_read: '📄', fs_write: '✏️', execute_bash: '⚡', grep: '🔍', glob: '📂',
  code: '🧠', web_search: '🌐', web_fetch: '🌐', use_aws: '☁️', use_subagent: '🤖',
};

const ChatView: React.FC<ChatViewProps> = ({ paneId: displayPaneId, token }) => {
  const [agentType, setAgentType] = useState('AI');
  const [chatData, setChatData] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);
  const lastJsonRef = useRef('');

  // Load cache
  useEffect(() => {
    if (!displayPaneId) return;
    const short = displayPaneId.replace(':main.0', '');
    getCache(short).then(cached => {
      if (cached?.length) { 
        setChatData(cached); 
        setHasMore(cached.length > 2); 
        setLoading(false); 
      }
    });
  }, [displayPaneId]);

  // Listen for optimistic UI updates - add Q immediately to top
  useEffect(() => {
    const handler = (e: any) => {
      if (e.detail?.pane === displayPaneId) {
        setChatData(prev => [...prev, { q: e.detail.q, status: 'pending', ts: Date.now() / 1000, start_ts: Date.now() / 1000, credit: 0 }]);
      }
    };
    window.addEventListener('chat-q-sent', handler);
    return () => window.removeEventListener('chat-q-sent', handler);
  }, [displayPaneId]);

  // WS + API
  useEffect(() => {
    if (!displayPaneId || !token) return;
    const short = displayPaneId.replace(':main.0', '');
    let ws: WebSocket | null = null, dead = false, timer: ReturnType<typeof setTimeout>, fetchTimer: ReturnType<typeof setTimeout>;

    async function reload() {
      try {
        const res = await fetch(`${config.apiBase}/api/stats/chat?pane=${short}`, { headers: { Authorization: `Bearer ${token}` } });
        const json = await res.json();
        if (json.data && Array.isArray(json.data)) {
          const s = JSON.stringify(json.data);
          if (s !== lastJsonRef.current) { 
            lastJsonRef.current = s; 
            setChatData(json.data); 
            setHasMore(json.data.length > 2); 
            setCache(short, json.data); 
          }
          if (json.agentType) setAgentType(json.agentType);
        } else { setChatData([]); setHasMore(false); }
      } catch {} finally { setLoading(false); }
    }

    const debouncedReload = () => { clearTimeout(fetchTimer); fetchTimer = setTimeout(reload, 100); };
    let streaming = false;

    function connect() {
      if (dead) return;
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      const base = config.apiBase.replace(/^https?/, proto);
      ws = new WebSocket(`${base}/api/chat/ws?pane=${short}&token=${token}`);
      ws.onopen = () => reload();
      ws.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data);
          if (msg.type === 'user_q') { streaming = false; setChatData(prev => [...prev, { q: msg.data.q, status: 'pending', ts: Date.now()/1000, start_ts: Date.now()/1000, credit: 0 }]); }
          else if (msg.type === 'ai_chunk') {
            streaming = true;
            setChatData(prev => {
              if (!prev.length) return prev;
              const last = { ...prev[prev.length - 1] };
              const steps = last.steps ? [...last.steps] : [];
              if (!steps.length || steps[steps.length - 1].type !== 'text') steps.push({ type: 'text', text: msg.data.delta });
              else steps[steps.length - 1] = { ...steps[steps.length - 1], text: msg.data.delta };
              last.steps = steps; last.status = 'streaming';
              return [...prev.slice(0, -1), last];
            });
          } else if (msg.type === 'ai_done') { streaming = false; debouncedReload(); }
          else if (msg.type === 'desktop_event' && msg.data) { window.dispatchEvent(new CustomEvent('agent-desktop-event', { detail: msg.data })); }
          else { if (!streaming) debouncedReload(); }
        } catch { if (!streaming) debouncedReload(); }
      };
      ws.onclose = () => { if (!dead) timer = setTimeout(connect, 3000); };
      ws.onerror = () => ws?.close();
    }
    connect();
    return () => { dead = true; clearTimeout(timer); clearTimeout(fetchTimer); ws?.close(); };
  }, [displayPaneId, token]);

  // No auto-scroll to bottom - content starts from top

  useEffect(() => {
    const h = () => {};
    window.addEventListener('toggle-ttyd-drawer', h);
    return () => window.removeEventListener('toggle-ttyd-drawer', h);
  }, []);

  const [displayCount, setDisplayCount] = useState(1);

  const loadMore = () => {
    setDisplayCount(prev => {
      const next = prev + 2;
      if (next >= chatData.length) setHasMore(false);
      return Math.min(next, chatData.length);
    });
  };

  // Build conversation groups - take last displayCount items, then reverse (newest on top)
  const groups: { q: string; r: any }[] = [];
  const allData = chatData;
  allData.slice(-displayCount).forEach((c: any) => {
    if (!c.q) return;
    groups.push({ q: c.q, r: c });
  });

  // Reverse: newest first
  groups.reverse();

  const scrollRef = useRef<HTMLDivElement>(null);

  // Scroll to bottom = load more older messages
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      if (el.scrollTop + el.clientHeight >= el.scrollHeight - 10 && chatData.length > displayCount) {
        setDisplayCount(prev => {
          const next = Math.min(prev + 2, chatData.length);
          if (next >= chatData.length) setHasMore(false);
          return next;
        });
      }
    };
    el.addEventListener('scroll', onScroll);
    return () => el.removeEventListener('scroll', onScroll);
  }, [chatData.length, displayCount]);

  return (
    <div className="flex flex-col h-full">
      <div ref={scrollRef} className="flex-1 overflow-y-auto">
        <div className="max-w-[720px] mx-auto px-4 py-4">

          {loading ? (
            <div className="flex flex-col items-center justify-center pt-20 gap-3">
              <div className="w-6 h-6 border-2 border-vsc-accent/30 border-t-vsc-accent rounded-full animate-spin" />
              <span className="text-[11px] text-vsc-text-muted">Loading history...</span>
            </div>
          ) : groups.length === 0 ? (
            <div className="text-center pt-20">
              <div className="text-2xl mb-2 opacity-20">✦</div>
              <p className="text-xs text-vsc-text-muted">Waiting for conversation</p>
            </div>
          ) : groups.map((g, gi) => {
            const { r } = g;
            const steps: any[] = r?.steps || [];
            const ranSec = r?.ts && r?.start_ts ? Math.round(r.ts - r.start_ts) : 0;
            const isRunning = r?.status === 'tool_use';
            const isPending = r?.status === 'pending';
            const isStreaming = r?.status === 'streaming';
            const model = r?.model || '';
            const timeStr = ranSec >= 60 ? `${Math.floor(ranSec / 60)}m${ranSec % 60}s` : ranSec > 0 ? `${ranSec}s` : '';
            const hasToolStep = steps.some((s: any) => s.type === 'tool');
            const toolCount = steps.filter((s: any) => s.type === 'tool').reduce((n: number, s: any) => n + (s.tools?.filter((t: any) => t.arg)?.length || 0), 0);
            const credit = r?.credit || 0;

            return (
              <div key={gi} className="mb-5">
                {/* User message */}
                <div className="flex justify-end mb-2.5">
                  <div className="chat-markdown max-w-[85%] px-3.5 py-2 rounded-2xl rounded-br-sm text-[14px] leading-relaxed text-white/90" style={{ background: 'linear-gradient(135deg, rgba(0,122,204,0.8), rgba(17,119,187,0.6))' }}>
                    <Markdown remarkPlugins={[remarkGfm]}>{g.q.replace(/^-\n/, '').replace(/^\d+;\d+;\d+c/i, '')}</Markdown>
                  </div>
                </div>

                {/* AI response */}
                <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] overflow-hidden">
                  {/* Meta bar */}
                  <div className="flex items-center gap-1.5 px-3.5 py-2 border-b border-white/[0.04] flex-wrap">
                    <span className="text-vsc-accent text-[11px] font-semibold">✦ {agentType}</span>
                    {model && <span className="text-[9px] px-1.5 py-0.5 rounded-md bg-white/[0.04] text-vsc-text-muted">{model}</span>}
                    {timeStr && <span className="text-[9px] px-1.5 py-0.5 rounded-md bg-white/[0.04] text-vsc-text-muted">⏱{timeStr}</span>}
                    {toolCount > 0 && <span className="text-[9px] px-1.5 py-0.5 rounded-md bg-white/[0.04] text-vsc-text-muted">🔧×{toolCount}</span>}
                    <span className="flex-1" />
                    {credit > 0 && <span className="text-[9px] text-vsc-text-muted/50 font-mono">${credit.toFixed(3)}</span>}
                  </div>

                  {/* Steps */}
                  <div className="px-3.5 py-2.5">
                    {steps.map((s: any, si: number) => {
                      const isLast = si === steps.length - 1;

                      if (s.type === 'text') {
                        const isFinal = isLast && r?.status === 'text';
                        if (!isFinal && hasToolStep) {
                          return (
                            <div key={si} className="chat-markdown text-[14px] text-vsc-text-muted/80 my-2 pl-3 leading-relaxed border-l-2 border-white/[0.06]">
                              <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                            </div>
                          );
                        }
                        return (
                          <div key={si} className={`chat-markdown text-[14px] text-vsc-text leading-[1.7] ${si > 0 ? 'mt-2 pt-2 border-t border-white/[0.04]' : ''}`}>
                            <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                          </div>
                        );
                      }

                      // Tool step
                      const toolsWithArg = (s.tools || []).filter((t: any) => t.arg);
                      if (!toolsWithArg.length) return null;
                      const icons = toolsWithArg.map((t: any) => TOOL_ICONS[t.name] || '⚙️').join(' ');

                      return (
                        <details key={si} className="my-2 rounded-lg bg-white/[0.02] border border-white/[0.05] overflow-hidden">
                          <summary className="flex items-center gap-1.5 px-3 py-1.5 cursor-pointer text-[11px] text-vsc-text-muted select-none hover:bg-white/[0.02] transition-colors">
                            <span className="text-[8px] opacity-30">▶</span>
                            <span>{icons}</span>
                            <span className="opacity-40 text-[10px]">{toolsWithArg.length > 1 ? `${toolsWithArg.length} tools` : toolsWithArg[0]?.name}</span>
                          </summary>
                          <div className="border-t border-white/[0.04]">
                            {toolsWithArg.map((t: any, ti: number) => {
                              const arg = t.arg.replace(/^\/home\/\w+\//, '~/');
                              const isError = t.result?.startsWith('exit ') || t.result?.startsWith('❌');
                              const lines = t.result?.split('\n') || [];
                              const preview = lines.length > 4 ? lines.slice(0, 3).join('\n') + '\n...' : t.result;
                              return (
                                <div key={ti} className={`px-3 py-1.5 ${ti < toolsWithArg.length - 1 ? 'border-b border-white/[0.04]' : ''}`}>
                                  <div className="font-mono text-[11px] text-vsc-text/80 break-all">{arg}</div>
                                  {t.result && (
                                    <pre className={`text-[11px] mt-1 px-2 py-1 rounded font-mono whitespace-pre-wrap break-all max-h-[140px] overflow-auto leading-relaxed ${isError ? 'bg-red-500/[0.06] text-red-400 border border-red-500/10' : 'bg-emerald-500/[0.04] text-emerald-400 border border-emerald-500/[0.06]'}`}>
                                      {preview}
                                    </pre>
                                  )}
                                </div>
                              );
                            })}
                          </div>
                        </details>
                      );
                    })}

                    {/* Live status */}
                    {isPending && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="flex gap-0.5">
                          <div className="w-1.5 h-1.5 rounded-full bg-vsc-accent animate-bounce" style={{ animationDelay: '0ms' }} />
                          <div className="w-1.5 h-1.5 rounded-full bg-vsc-accent animate-bounce" style={{ animationDelay: '150ms' }} />
                          <div className="w-1.5 h-1.5 rounded-full bg-vsc-accent animate-bounce" style={{ animationDelay: '300ms' }} />
                        </div>
                        <span className="text-[11px] text-vsc-accent">Thinking...</span>
                      </div>
                    )}
                    {isRunning && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="w-3 h-3 border border-vsc-accent/40 border-t-vsc-accent rounded-full animate-spin" />
                        <span className="text-[11px] text-vsc-accent">Running{toolCount > 0 ? ` (${toolCount} tools)` : ''}...</span>
                      </div>
                    )}
                    {isStreaming && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="w-1.5 h-1.5 rounded-full bg-vsc-accent animate-pulse" />
                        <span className="text-[11px] text-vsc-accent">Streaming...</span>
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
          <div ref={endRef} />
        </div>
      </div>
    </div>
  );
};

export default ChatView;
