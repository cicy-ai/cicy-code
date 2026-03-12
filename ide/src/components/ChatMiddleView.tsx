import React, { useState, useEffect, useRef, useCallback } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import config, { urls } from '../config';
import { WebFrame } from './WebFrame';
import { usePane } from '../contexts/PaneContext';

const DBNAME = 'cicy_chat';
const STORE = 'turns';

function openDB(pane: string): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(`${DBNAME}_${pane}`, 1);
    req.onupgradeneeded = () => { req.result.createObjectStore(STORE, { keyPath: 'ts' }); };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function idbGetAll(db: IDBDatabase): Promise<any[]> {
  return new Promise((resolve) => {
    const tx = db.transaction(STORE, 'readonly');
    const req = tx.objectStore(STORE).getAll();
    req.onsuccess = () => resolve(req.result || []);
    req.onerror = () => resolve([]);
  });
}

function idbPutAll(db: IDBDatabase, items: any[]) {
  const tx = db.transaction(STORE, 'readwrite');
  const store = tx.objectStore(STORE);
  store.clear();
  items.forEach(i => store.put(i));
}

function idbAdd(db: IDBDatabase, item: any) {
  const tx = db.transaction(STORE, 'readwrite');
  tx.objectStore(STORE).put(item);
}

const ChatMiddleView: React.FC = () => {
  const { displayPaneId, token } = usePane();
  const [chatData, setChatData] = useState<any[]>([]);
  const dbRef = useRef<IDBDatabase | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerWidth, setDrawerWidth] = useState(() => {
    const cached = localStorage.getItem('ttyd-drawer-width');
    return cached ? Number(cached) : 50;
  });
  const [isDragging, setDragging] = useState(false);
  const dragging = useRef(false);
  const endRef = useRef<HTMLDivElement>(null);

  // 1. Open IndexedDB on mount (don't load data — API is source of truth)
  useEffect(() => {
    if (!displayPaneId) return;
    const short = displayPaneId.replace(':main.0', '');
    openDB(short).then((db) => { dbRef.current = db; });
  }, [displayPaneId]);

  // 2. Listen for chat-q-sent (UI send) → optimistic update state only
  useEffect(() => {
    const handler = (e: any) => {
      if (e.detail?.pane === displayPaneId) {
        const turn = { q: e.detail.q, status: 'pending', ts: Date.now() / 1000, credit: 0 };
        setChatData(prev => [...prev, turn]);
      }
    };
    window.addEventListener('chat-q-sent', handler);
    return () => window.removeEventListener('chat-q-sent', handler);
  }, [displayPaneId]);

  // 3. WS — receive mitmproxy events → reload history from API
  useEffect(() => {
    if (!displayPaneId || !token) return;
    const short = displayPaneId.replace(':main.0', '');
    setChatData([]);
    let ws: WebSocket | null = null;
    let dead = false;
    let timer: ReturnType<typeof setTimeout>;
    let fetchTimer: ReturnType<typeof setTimeout>;

    async function reload() {
      try {
        const res = await fetch(`${config.apiBase}/api/stats/chat?pane=${short}`, { headers: { Authorization: `Bearer ${token}` } });
        const json = await res.json();
        if (json.data && Array.isArray(json.data)) {
          setChatData(json.data);
          if (dbRef.current) idbPutAll(dbRef.current, json.data);
        } else {
          setChatData([]);
        }
      } catch {}
    }

    function debouncedReload() {
      clearTimeout(fetchTimer);
      fetchTimer = setTimeout(reload, 100);
    }

    function connect() {
      if (dead) return;
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      const base = config.apiBase.replace(/^https?/, proto);
      ws = new WebSocket(`${base}/api/chat/ws?pane=${short}&token=${token}`);
      ws.onopen = () => reload();
      ws.onmessage = () => debouncedReload();
      ws.onclose = () => { if (!dead) timer = setTimeout(connect, 3000); };
      ws.onerror = () => ws?.close();
    }
    connect();
    return () => { dead = true; clearTimeout(timer); clearTimeout(fetchTimer); ws?.close(); };
  }, [displayPaneId, token]);

  // Auto-scroll
  useEffect(() => { setTimeout(() => endRef.current?.scrollIntoView({ behavior: 'smooth' }), 50); }, [chatData]);

  // ESC to close drawer
  useEffect(() => {
    if (!drawerOpen) return;
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') setDrawerOpen(false); };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [drawerOpen]);

  useEffect(() => {
    const h = () => setDrawerOpen(v => !v);
    window.addEventListener('toggle-ttyd-drawer', h);
    return () => window.removeEventListener('toggle-ttyd-drawer', h);
  }, []);

  const onDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    dragging.current = true;
    const onMove = (ev: MouseEvent) => {
      if (!dragging.current) return;
      const pct = Math.min(80, Math.max(25, ((window.innerWidth - ev.clientX) / window.innerWidth) * 100));
      setDrawerWidth(pct);
      localStorage.setItem('ttyd-drawer-width', String(Math.round(pct)));
    };
    const onUp = () => { dragging.current = false; setDragging(false); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
    setDragging(true);
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, []);

  // Group turns by q
  const groups: {q: string, rounds: any[], totalCredit: number}[] = [];
  chatData.forEach((c: any) => {
    if (c.q) groups.push({q: c.q, rounds: [c], totalCredit: c.credit || 0});
    else if (groups.length > 0) { const g = groups[groups.length - 1]; g.rounds.push(c); g.totalCredit += c.credit || 0; }
  });

  return (
    <div className="flex flex-col h-full relative">
      <div className="flex-1 overflow-y-auto">
        <div style={{maxWidth: 720, margin: '0 auto', padding: '16px 16px 8px'}}>
          {groups.length === 0 && (
            <div className="text-vsc-text-muted text-center" style={{marginTop: 80}}>
              <div style={{fontSize: 28, marginBottom: 8}}>💬</div>
              <div style={{fontSize: 13}}>No conversation yet</div>
            </div>
          )}
          {groups.map((g, gi) => {
            const lastText = [...g.rounds].reverse().find((r: any) => r.status === 'text');
            const ranSec = g.rounds.length > 1 ? Math.round(g.rounds[g.rounds.length-1].ts - g.rounds[0].ts) : Math.round((g.rounds[0]?.first_ms || 0) / 1000);
            const allTools = g.rounds.flatMap((r: any) => r.tools || []);
            const hasTools = allTools.length > 0;
            const isRunning = !lastText && g.rounds.some((r: any) => r.status === 'tool_use');
            const isPending = g.rounds.some((r: any) => r.status === 'pending');

            return (
              <div key={gi} style={{marginBottom: 20}}>
                <div style={{display: 'flex', justifyContent: 'flex-end', marginBottom: 10}}>
                  <div className="text-vsc-text" style={{borderRadius: '16px 16px 4px 16px', padding: '8px 14px', maxWidth: '85%', fontSize: 13, lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: 'linear-gradient(135deg, #0e639c, #1177bb)'}}>
                    {g.q.replace(/^-\n/, '')}
                  </div>
                </div>
                <div className="border border-vsc-border/50" style={{borderRadius: 10, padding: '12px 14px', background: 'var(--vsc-bg-secondary)'}}>
                  <div style={{display: 'flex', alignItems: 'center', gap: 6, marginBottom: hasTools ? 8 : 0}}>
                    <span className="text-vsc-accent" style={{fontSize: 12, fontWeight: 600}}>✦ AI</span>
                    {ranSec > 0 && <span className="text-vsc-text-muted" style={{fontSize: 10, background: 'var(--vsc-bg)', borderRadius: 8, padding: '1px 6px'}}>{ranSec}s</span>}
                    <span style={{flex: 1}}/>
                    {g.totalCredit > 0 && <span className="text-vsc-text-disabled" style={{fontSize: 10}}>${g.totalCredit.toFixed(3)}</span>}
                  </div>
                  {hasTools && (
                    <div className="text-vsc-text-secondary" style={{fontSize: 11, lineHeight: 1.8, marginBottom: 6}}>
                      {allTools.map((t: any, ti: number) => {
                        const icons: Record<string,string> = {fs_read:'📄',fs_write:'✏️',execute_bash:'🔨',grep:'🔍',glob:'🔍',code:'🔍',web_search:'🌐',web_fetch:'🌐'};
                        const icon = icons[t.name] || '⚙️';
                        const short = t.arg ? t.arg.replace(/^\/home\/\w+\//, '~/').substring(0, 60) : '';
                        return <div key={ti} style={{opacity: 0.85}}>{icon} <span style={{color:'var(--vsc-text-muted)',fontFamily:'monospace',fontSize:10}}>{t.name}</span>{short && <span style={{marginLeft:4,color:'var(--vsc-text)'}}>{short}</span>}</div>;
                      })}
                    </div>
                  )}
                  {isPending && <div className="text-vsc-accent" style={{fontSize: 11, padding: '2px 0'}}><span className="animate-pulse">● </span>Waiting...</div>}
                  {isRunning && <div className="text-vsc-accent" style={{fontSize: 11, padding: '2px 0'}}><span className="animate-pulse">● </span>Running...</div>}
                  {lastText?.a && (
                    <div className="chat-markdown text-vsc-text" style={{fontSize: 13, lineHeight: 1.7, wordBreak: 'break-word', borderTop: hasTools ? '1px solid var(--vsc-border)' : 'none', paddingTop: hasTools ? 8 : 0}}>
                      <Markdown remarkPlugins={[remarkGfm]}>{lastText.a}</Markdown>
                    </div>
                  )}
                </div>
              </div>
            );
          })}
          <div ref={endRef} />
        </div>
      </div>

      {displayPaneId && (
        <div className="fixed top-0 right-0 h-full bg-vsc-bg border-l border-vsc-border flex z-[9999]" style={{width: `${drawerWidth}%`, display: drawerOpen ? 'flex' : 'none'}}>
          <div className="w-1 cursor-col-resize hover:bg-vsc-accent/50 active:bg-vsc-accent flex-shrink-0" onMouseDown={onDragStart} />
          <div className="flex-1 flex flex-col min-w-0">
            <div className="h-9 bg-vsc-bg-secondary border-b border-vsc-border flex items-center justify-between px-3 flex-shrink-0">
              <span className="text-xs text-vsc-text-secondary font-medium">Terminal — {displayPaneId.replace(':main.0','')}</span>
              <button onClick={() => setDrawerOpen(false)} className="cicy-btn">
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </div>
            <div className="flex-1 relative">
              {isDragging && <div className="absolute inset-0 z-10" />}
              <WebFrame loading="lazy" src={urls.ttyd(displayPaneId, token)} className="w-full h-full" />
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default ChatMiddleView;
