import React, { useState, useEffect, useRef } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import config from '../../config';
import { WebFrame } from '../WebFrame';

// Collapsible user prompt - auto-collapse if > 200px
const CollapsibleQ: React.FC<{ text: string }> = ({ text }) => {
  const ref = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [needsCollapse, setNeedsCollapse] = useState(false);
  useEffect(() => {
    if (ref.current && ref.current.scrollHeight > 200) { setNeedsCollapse(true); setCollapsed(true); }
  }, [text]);
  return (
    <div className="flex justify-end mb-2.5">
      <div className="max-w-[95%] relative">
        <div ref={ref} className={`chat-markdown px-3.5 py-2 rounded-2xl rounded-br-sm text-base leading-relaxed text-white/90 overflow-hidden transition-all ${collapsed ? 'max-h-[80px]' : ''}`} style={{ background: 'rgba(255,255,255,0.08)' }}>
          <Markdown remarkPlugins={[remarkGfm]}>{text.replace(/^-\n/, '')}</Markdown>
        </div>
        {needsCollapse && (
          <button onClick={() => setCollapsed(v => !v)} className="text-base text-white/30 hover:text-white/60 mt-1 float-right">
            {collapsed ? '展开 ▼' : '收起 ▲'}
          </button>
        )}
      </div>
    </div>
  );
};

interface ChatViewProps {
  paneId: string;
  token: string;
  commandPanel?: React.ReactNode;
  apiOnly?: boolean;
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

const TOOL_LABELS: Record<string, string> = {
  fs_read: 'Read File', fs_write: 'Write File', execute_bash: 'Command',
  grep: 'Search', glob: 'Glob', code: 'Code Intel',
  web_search: 'Web Search', web_fetch: 'Web Fetch', use_aws: 'AWS', use_subagent: 'Subagent',
};

const ToolCard: React.FC<{ tool: any; running?: boolean }> = ({ tool, running }) => {
  const [open, setOpen] = useState(false);
  const icon = TOOL_ICONS[tool.name] || '⚙️';
  const label = TOOL_LABELS[tool.name] || tool.name;
  const arg = tool.arg?.replace(/^\/home\/\w+\//, '~/') || '';
  const isError = tool.result?.startsWith('exit ') || tool.result?.startsWith('❌');
  const hasDiff = !!tool.diff?.old || !!tool.diff?.new;
  const hasContent = !!tool.result || hasDiff;
  const statusIcon = running ? '⏳' : isError ? '✗' : '✓';
  const statusColor = running ? 'text-yellow-400' : isError ? 'text-red-400' : 'text-emerald-400';
  const borderColor = running ? 'border-yellow-500/20' : isError ? 'border-red-500/15' : 'border-white/[0.06]';

  return (
    <div className={`rounded-lg bg-[#1a1a2e]/60 border ${borderColor} overflow-hidden`}>
      <div className="flex items-center gap-2 px-3 py-1.5 cursor-pointer select-none hover:bg-white/[0.03] transition-colors"
        onClick={() => setOpen(p => !p)}>
        <span className={`text-xs ${statusColor}`}>{running ? <span className="inline-block w-2.5 h-2.5 border border-yellow-400/40 border-t-yellow-400 rounded-full animate-spin" /> : statusIcon}</span>
        <span className="text-xs px-1 py-0.5 rounded bg-white/[0.04] text-vsc-text-muted/50">{label}</span>
        {!open && <span className="text-xs font-mono text-vsc-text/40 truncate flex-1" title={arg}>{arg}</span>}
        <span className="text-xs text-vsc-text-muted/30">{open ? '▼' : '▶'}</span>
      </div>
      {open && arg && (
        <div className="px-3 py-1.5 text-sm font-mono text-vsc-text/50 whitespace-pre-wrap break-all border-b border-white/[0.04]">{arg}</div>
      )}
      {open && hasDiff && (
        <div className="mx-2 mb-2 rounded overflow-hidden border border-white/[0.06] text-xs font-mono max-h-[300px] overflow-auto">
          {tool.diff.old && tool.diff.old.split('\n').map((line: string, i: number) => (
            <div key={'o'+i} className="px-2 bg-red-500/[0.08] text-red-400/80 whitespace-pre-wrap break-all leading-relaxed">- {line}</div>
          ))}
          {tool.diff.new && tool.diff.new.split('\n').map((line: string, i: number) => (
            <div key={'n'+i} className="px-2 bg-emerald-500/[0.08] text-emerald-400/80 whitespace-pre-wrap break-all leading-relaxed">+ {line}</div>
          ))}
        </div>
      )}
      {open && !hasDiff && tool.result && (
        <pre className={`text-xs mx-2 mb-2 px-2.5 py-1.5 rounded font-mono whitespace-pre-wrap break-all max-h-[200px] overflow-auto leading-relaxed ${isError ? 'bg-red-500/[0.06] text-red-400' : 'bg-emerald-500/[0.04] text-emerald-400'}`}>
          {tool.result}
        </pre>
      )}
    </div>
  );
};

const ChatView: React.FC<ChatViewProps> = ({ paneId: displayPaneId, token, commandPanel, apiOnly = false }) => {
  const [agentType, setAgentType] = useState('AI');
  const [chatData, setChatData] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [displayCount, setDisplayCount] = useState(10);
  const scrollRef = useRef<HTMLDivElement>(null);
  const latestQRef = useRef<HTMLDivElement>(null);
  const lastJsonRef = useRef('');
  const prevCountRef = useRef(0);
  const initialDone = useRef(false);
  const streamingRef = useRef(false);
  const [showTtyd, setShowTtyd] = useState(false);
  const ttydUrl = token ? `${config.ttydBase}/ttyd/${displayPaneId}/?token=${token}` : '';

  // Track container height for full-screen Q+A effect
  const [containerH, setContainerH] = useState(0);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setContainerH(el.clientHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Load cache
  useEffect(() => {
    if (!displayPaneId) return;
    const short = displayPaneId.replace(':main.0', '');
    getCache(short).then(cached => {
      if (cached?.length) { setChatData(cached); setLoading(false); }
    });
  }, [displayPaneId]);

  // Optimistic Q from CommandPanel
  useEffect(() => {
    const handler = (e: any) => {
      if (e.detail?.pane !== displayPaneId) return;
      const q: string = e.detail.q;
      if (q.startsWith('/')) {
        // Slash command — show as system message, auto-show ttyd for feedback
        setChatData(prev => [...prev, { q, status: 'done', ts: Date.now() / 1000, start_ts: Date.now() / 1000, credit: 0, system: true }]);
        if (!apiOnly) setShowTtyd(true);
      } else {
        setChatData(prev => [...prev.filter((c: any) => !c.system), { q, status: 'pending', ts: Date.now() / 1000, start_ts: Date.now() / 1000, credit: 0 }]);
      }
    };
    window.addEventListener('chat-q-sent', handler);
    return () => window.removeEventListener('chat-q-sent', handler);
  }, [displayPaneId, apiOnly]);

  // Scroll: when a NEW Q appears, scroll so Q is at top of viewport. Never auto-scroll during stream.
  useEffect(() => {
    const count = chatData.length;
    if (count > prevCountRef.current) {
      const last = chatData[count - 1];
      // Only scroll for new Q (not for stream updates to existing item)
      if (last?.q && last.status === 'pending') {
        requestAnimationFrame(() => {
          const el = latestQRef.current;
          const container = scrollRef.current;
          if (el && container) {
            container.scrollTo({ top: el.offsetTop - 8, behavior: initialDone.current ? 'smooth' : 'auto' });
          }
          initialDone.current = true;
        });
      }
    }
    // Initial load: scroll to last Q
    if (!initialDone.current && count > 0) {
      initialDone.current = true;
      requestAnimationFrame(() => {
        const el = latestQRef.current;
        const container = scrollRef.current;
        if (el && container) container.scrollTo({ top: el.offsetTop - 8 });
      });
    }
    prevCountRef.current = count;
  }, [chatData.length]);

  // WS + API
  useEffect(() => {
    if (!displayPaneId || !token) return;
    const short = displayPaneId.replace(':main.0', '');
    let ws: WebSocket | null = null, dead = false, reconnectTimer: ReturnType<typeof setTimeout>, fetchTimer: ReturnType<typeof setTimeout>;

    async function reload() {
      try {
        const res = await fetch(`${config.apiBase}/api/stats/chat?pane=${short}`, { headers: { Authorization: `Bearer ${token}` } });
        const json = await res.json();
        if (json.data && Array.isArray(json.data)) {
          const s = JSON.stringify(json.data);
          if (s !== lastJsonRef.current) {
            lastJsonRef.current = s;
            setChatData(prev => {
              const sys = prev.filter((c: any) => c.system);
              return [...json.data, ...sys];
            });
            setCache(short, json.data);
          }
          if (json.agentType) setAgentType(json.agentType);
        } else { setChatData([]); }
      } catch {} finally { setLoading(false); }
    }

    const debouncedReload = () => { clearTimeout(fetchTimer); fetchTimer = setTimeout(reload, 300); };

    function connect() {
      if (dead) return;
      const proto = config.apiBase.startsWith('https') ? 'wss' : (location.protocol === 'https:' ? 'wss' : 'ws');
      const base = config.apiBase.replace(/^https?/, proto);
      const isElectron = typeof (window as any).electronRPC === 'function' ? '1' : '0';
      ws = new WebSocket(`${base}/api/chat/ws?pane=${short}&token=${token}&electron=${isElectron}`);

      const wsSend = (data: object) => {
        if (ws?.readyState === WebSocket.OPEN) {
          console.log('[ChatView] WS send:', (data as any).type, data);
          ws.send(JSON.stringify(data));
        }
      };
      const visionHandler = (e: CustomEvent) => { wsSend({ type: 'gemini_vision_result', data: e.detail }); };
      const askHandler = (e: CustomEvent) => { wsSend({ type: 'gemini_ask_result', data: e.detail }); };
      const pongHandler = (e: CustomEvent) => { wsSend({ type: 'pong', data: e.detail }); };
      const ipcPongHandler = (e: CustomEvent) => { wsSend({ type: 'ipc_pong', data: e.detail }); };

      window.addEventListener('gemini-vision-result', visionHandler as EventListener);
      window.addEventListener('gemini-ask-result', askHandler as EventListener);
      window.addEventListener('agent-pong', pongHandler as EventListener);
      window.addEventListener('ipc-pong', ipcPongHandler as EventListener);

      const cleanup = () => {
        window.removeEventListener('gemini-vision-result', visionHandler as EventListener);
        window.removeEventListener('gemini-ask-result', askHandler as EventListener);
        window.removeEventListener('agent-pong', pongHandler as EventListener);
        window.removeEventListener('ipc-pong', ipcPongHandler as EventListener);
      };

      ws.onopen = () => { console.log('[ChatView] WS connected, pane=' + short); reload(); };

      ws.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data);
          console.log('[ChatView] WS msg:', msg.type, msg);
          if (msg.type === 'user_q') {
            streamingRef.current = false;
            window.dispatchEvent(new CustomEvent('ai-streaming', { detail: false }));
            setChatData(prev => [...prev.filter((c: any) => !c.system), { q: msg.data.q, status: 'pending', ts: Date.now()/1000, start_ts: Date.now()/1000, credit: 0 }]);
          } else if (msg.type === 'ai_chunk') {
            if (!streamingRef.current) { streamingRef.current = true; window.dispatchEvent(new CustomEvent('ai-streaming', { detail: true })); }
            setChatData(prev => {
              if (!prev.length) return prev;
              const last = { ...prev[prev.length - 1] };
              const steps = last.steps ? [...last.steps] : [];
              if (!steps.length || steps[steps.length - 1].type !== 'text') steps.push({ type: 'text', text: msg.data.delta });
              else steps[steps.length - 1] = { ...steps[steps.length - 1], text: msg.data.delta };
              last.steps = steps; last.status = 'streaming';
              return [...prev.slice(0, -1), last];
            });
          } else if (msg.type === 'ai_done') {
            streamingRef.current = false;
            window.dispatchEvent(new CustomEvent('ai-streaming', { detail: false }));
            debouncedReload();
            setChatData(prev => {
              const last = prev[prev.length - 1];
              if (last?.a) {
                const parts = Array.isArray(last.a) ? last.a : [last.a];
                const textOnly = parts.filter((s: any) => typeof s === 'string').join(' ').trim();
                if (textOnly) window.dispatchEvent(new CustomEvent('ai-reply-done', { detail: { text: textOnly } }));
              }
              return prev;
            });
          } else if (msg.type === 'desktop_event' && msg.data) {
            window.dispatchEvent(new CustomEvent('agent-desktop-event', { detail: msg.data }));
          } else if (msg.type === 'status_change' && msg.data) {
            window.dispatchEvent(new CustomEvent('agent-status-change', { detail: msg.data }));
          } else if (msg.type === 'exec_js' && msg.data?.code) {
            console.log('[exec_js] received:', msg.data.code);
            try {
              const result = eval(msg.data.code);
              console.log('[exec_js] result:', result);
              if (msg.data.requestId && ws) wsSend({ type: 'exec_js_result', data: { requestId: msg.data.requestId, result: String(result) } });
            } catch (e: any) {
              console.error('[exec_js] error:', e);
              if (msg.data.requestId && ws) wsSend({ type: 'exec_js_result', data: { requestId: msg.data.requestId, error: e.message } });
            }
          } else if (msg.type === 'webpage_ping') {
            wsSend({ type: 'webpage_pong', data: { requestId: msg.data?.requestId, version: import.meta.env.VITE_APP_VERSION } });
          } else if (msg.type === 'worker_idle') {
            const d = msg.data?.data;
            if (d) setChatData(prev => [...prev, { q: '', a: `🔔 **${d.worker || msg.data.from}** finished task (idle)`, status: 'done', ts: Date.now()/1000, start_ts: Date.now()/1000, credit: 0, system: true }]);
          } else {
            if (!streamingRef.current) debouncedReload();
          }
        } catch { if (!streamingRef.current) debouncedReload(); }
      };

      ws.onclose = () => { cleanup(); if (!dead) reconnectTimer = setTimeout(connect, 3000); };
      ws.onerror = () => ws?.close();
    }
    connect();
    return () => { dead = true; clearTimeout(reconnectTimer); clearTimeout(fetchTimer); ws?.close(); };
  }, [displayPaneId, token]);

  // Load more on scroll up
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    let ticking = false;
    const onScroll = () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => {
        if (el.scrollTop <= 30 && chatData.length > displayCount) {
          setDisplayCount(prev => Math.min(prev + 10, chatData.length));
        }
        ticking = false;
      });
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, [chatData.length, displayCount]);

  // Build groups
  const groups: { q: string; r: any }[] = [];
  chatData.slice(-displayCount).forEach((c: any) => {
    if (!c.q && !c.system) return;
    groups.push({ q: c.q || '', r: c });
  });

  return (
    <div className="flex flex-col h-full">
      <div ref={scrollRef} className="flex-1 overflow-y-auto">
        <div className="max-w-full mx-auto px-2 py-4">
          {loading ? (
            <div className="flex flex-col items-center justify-center pt-20 gap-3">
              <div className="w-6 h-6 border-2 border-vsc-accent/30 border-t-vsc-accent rounded-full animate-spin" />
              <span className="text-base text-vsc-text-muted">Loading history...</span>
            </div>
          ) : groups.length === 0 ? (
            <div className="text-center pt-20">
              <div className="text-2xl mb-2 opacity-20">✦</div>
              <p className="text-xs text-vsc-text-muted">Waiting for conversation</p>
            </div>
          ) : groups.map((g, gi) => {
            const { r } = g;
            const isLatest = gi === groups.length - 1;

            if (r?.system) return null;

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
              <div key={gi} className="mb-5" ref={isLatest ? latestQRef : undefined} style={isLatest ? { minHeight: containerH + 'px' } : undefined}>
                <CollapsibleQ text={g.q} />

                <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] overflow-hidden">
                  <div className="flex items-center gap-1.5 px-3.5 py-1 border-b border-white/[0.03] flex-wrap">
                    <span className="text-vsc-accent text-sm font-medium opacity-60">✦ {agentType}</span>
                    {model && <span className="text-xs px-1 py-0.5 rounded bg-white/[0.03] text-vsc-text-muted/40">{model}</span>}
                    {timeStr && <span className="text-xs text-vsc-text-muted/30">⏱{timeStr}</span>}
                    {toolCount > 0 && <span className="text-xs text-vsc-text-muted/30">🔧×{toolCount}</span>}
                    <span className="flex-1" />
                    {credit > 0 && <span className="text-xs text-vsc-text-muted/25 font-mono">${credit.toFixed(3)}</span>}
                  </div>

                  <div className="px-3.5 py-2.5">
                    {steps.map((s: any, si: number) => {
                      const isLast = si === steps.length - 1;

                      if (s.type === 'text') {
                        const isFinal = isLast && r?.status === 'text';
                        if (!isFinal && hasToolStep) {
                          return (
                            <div key={si} className="chat-markdown text-base text-vsc-text-muted/80 my-2 pl-3 leading-relaxed border-l-2 border-white/[0.06]">
                              <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                            </div>
                          );
                        }
                        return (
                          <div key={si} className={`chat-markdown text-base text-vsc-text leading-[1.7] ${si > 0 ? 'mt-2 pt-2 border-t border-white/[0.04]' : ''}`}>
                            <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                          </div>
                        );
                      }

                      const toolsWithArg = (s.tools || []).filter((t: any) => t.arg);
                      if (!toolsWithArg.length) return null;
                      return (
                        <div key={si} className="my-2 space-y-1.5">
                          {toolsWithArg.map((t: any, ti: number) => (
                            <ToolCard key={ti} tool={t} running={isLast && isRunning && ti === toolsWithArg.length - 1} />
                          ))}
                        </div>
                      );
                    })}

                    {isPending && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="w-2 h-2 rounded-full bg-yellow-500 animate-pulse" />
                        <span className="text-base text-yellow-400/80 animate-pulse">Thinking...</span>
                      </div>
                    )}
                    {isRunning && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="w-3 h-3 border border-yellow-400/40 border-t-yellow-400 rounded-full animate-spin" />
                        <span className="text-base text-yellow-400/80">Running{toolCount > 0 ? ` (${toolCount} tools)` : ''}...</span>
                      </div>
                    )}
                    {isStreaming && (
                      <div className="flex items-center gap-2 py-1.5">
                        <div className="w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
                        <span className="text-base text-blue-400/80">Streaming...</span>
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
      {showTtyd && !apiOnly && (
        <div className="shrink-0 h-[160px] mx-2 mb-1 rounded-lg overflow-hidden border border-white/[0.08] relative">
          <button onClick={() => setShowTtyd(false)} className="absolute top-1 right-1 z-10 w-5 h-5 flex items-center justify-center rounded bg-black/60 text-white/40 hover:text-white/80 text-xs">✕</button>
          <WebFrame
            src={ttydUrl}
            className="w-full h-full border-0 bg-black"
            title="terminal-mini"
          />
        </div>
      )}
      <div className="shrink-0 h-[180px] pb-2 px-2">
        {commandPanel}
      </div>
    </div>
  );
};

export default ChatView;
