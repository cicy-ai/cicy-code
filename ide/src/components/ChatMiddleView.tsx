import React, { useState, useEffect, useRef } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import config from '../config';
import { usePane } from '../contexts/PaneContext';

const ChatMiddleView: React.FC = () => {
  const { displayPaneId, token } = usePane();
  const [agentType, setAgentType] = useState('AI');
  const [chatData, setChatData] = useState<any[]>([]);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);

  // Listen for chat-q-sent (UI send) → optimistic update state only
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

  const lastJsonRef = useRef('');

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
          const s = JSON.stringify(json.data);
          if (s !== lastJsonRef.current) {
            lastJsonRef.current = s;
            setChatData(json.data);
          }
          if (json.agentType) setAgentType(json.agentType);
        } else {
          setChatData([]);
        }
      } catch {}
    }

    function debouncedReload() {
      clearTimeout(fetchTimer);
      fetchTimer = setTimeout(reload, 100);
    }

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
          if (msg.type === 'user_q') {
            streaming = false;
            setChatData(prev => [...prev, { q: msg.data.q, status: 'pending', ts: Date.now()/1000, start_ts: Date.now()/1000, credit: 0 }]);
          } else if (msg.type === 'ai_chunk') {
            streaming = true;
            setChatData(prev => {
              if (prev.length === 0) return prev;
              const last = { ...prev[prev.length - 1] };
              const steps = last.steps ? [...last.steps] : [];
              if (steps.length === 0 || steps[steps.length - 1].type !== 'text') {
                steps.push({ type: 'text', text: msg.data.delta });
              } else {
                steps[steps.length - 1] = { ...steps[steps.length - 1], text: msg.data.delta };
              }
              last.steps = steps;
              last.status = 'streaming';
              return [...prev.slice(0, -1), last];
            });
          } else if (msg.type === 'ai_done') {
            streaming = false;
            debouncedReload();
          } else {
            if (!streaming) debouncedReload();
          }
        } catch { if (!streaming) debouncedReload(); }
      };
      ws.onclose = () => { if (!dead) timer = setTimeout(connect, 3000); };
      ws.onerror = () => ws?.close();
    }
    connect();
    return () => { dead = true; clearTimeout(timer); clearTimeout(fetchTimer); ws?.close(); };
  }, [displayPaneId, token]);

  // Auto-scroll
  useEffect(() => { setTimeout(() => endRef.current?.scrollIntoView({ behavior: 'smooth' }), 50); }, [chatData]);

  useEffect(() => {
    const h = () => setDrawerOpen(v => !v);
    window.addEventListener('toggle-ttyd-drawer', h);
    return () => window.removeEventListener('toggle-ttyd-drawer', h);
  }, []);

  // Group turns by q
  const groups: {q: string, rounds: any[], totalCredit: number}[] = [];
  chatData.forEach((c: any) => {
    if (!c.q) return;
    // Skip empty turns (no steps, no pending status)
    if ((!c.steps || c.steps.length === 0) && c.status !== 'tool_use' && c.status !== 'pending') return;
    groups.push({q: c.q, rounds: [c], totalCredit: c.credit || 0});
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
            const r = g.rounds[0];
            const steps: any[] = r?.steps || [];
            const ranSec = r?.ts && r?.start_ts ? Math.round(r.ts - r.start_ts) : 0;
            const isRunning = r?.status === 'tool_use';
            const isPending = r?.status === 'pending';
            const model = r?.model || '';
            const toolIcons: Record<string,string> = {
              fs_read:'📄', fs_write:'✏️', execute_bash:'⚡', grep:'🔍', glob:'📂',
              code:'🧠', web_search:'🌐', web_fetch:'🌐', use_aws:'☁️', use_subagent:'🤖'
            };
            const timeStr = ranSec >= 60 ? `${Math.floor(ranSec/60)}m ${ranSec%60}s` : ranSec > 0 ? `${ranSec}s` : '';
            const hasToolStep = steps.some((s: any) => s.type === 'tool');
            const toolCount = steps.filter((s: any) => s.type === 'tool').reduce((n: number, s: any) => n + (s.tools?.filter((t: any) => t.arg)?.length || 0), 0);

            return (
              <div key={gi} style={{marginBottom: 24}}>
                {/* User Q */}
                <div style={{display: 'flex', justifyContent: 'flex-end', marginBottom: 12}}>
                  <div className="chat-markdown text-vsc-text" style={{borderRadius: '16px 16px 4px 16px', padding: '8px 14px', maxWidth: '85%', fontSize: 13, lineHeight: 1.5, wordBreak: 'break-word', background: 'linear-gradient(135deg, #0e639c, #1177bb)'}}>
                    <Markdown remarkPlugins={[remarkGfm]}>{g.q.replace(/^-\n/, '').replace(/^\d+;\d+;\d+c/i, '')}</Markdown>
                  </div>
                </div>
                {/* AI Response */}
                <div className="border border-vsc-border/50" style={{borderRadius: 10, padding: '14px 16px', background: 'var(--vsc-bg-secondary)'}}>
                  {/* Header */}
                  <div style={{display: 'flex', alignItems: 'center', gap: 6, marginBottom: steps.length > 0 ? 10 : 0, flexWrap: 'wrap'}}>
                    <span className="text-vsc-accent" style={{fontSize: 12, fontWeight: 600}}>✦ {agentType || 'AI'}</span>
                    {model && <span style={{fontSize: 10, color: 'var(--vsc-text-muted)', background: 'var(--vsc-bg)', borderRadius: 8, padding: '1px 6px'}}>{model}</span>}
                    {timeStr && <span style={{fontSize: 10, color: 'var(--vsc-text-muted)', background: 'var(--vsc-bg)', borderRadius: 8, padding: '1px 6px'}}>⏱ {timeStr}</span>}
                    {toolCount > 0 && <span style={{fontSize: 10, color: 'var(--vsc-text-muted)', background: 'var(--vsc-bg)', borderRadius: 8, padding: '1px 6px'}}>🔧×{toolCount}</span>}
                    <span style={{flex: 1}}/>
                    {g.totalCredit > 0 && <span style={{fontSize: 10, color: 'var(--vsc-text-disabled)'}}>${g.totalCredit.toFixed(3)}</span>}
                  </div>

                  {/* Ordered steps */}
                  {steps.map((s: any, si: number) => {
                    const isLast = si === steps.length - 1;
                    if (s.type === 'text') {
                      const isFinal = isLast && r?.status === 'text';
                      if (!isFinal && hasToolStep) {
                        // Intermediate thinking — markdown, left border
                        return <div key={si} className="chat-markdown" style={{fontSize: 12, color: 'var(--vsc-text-muted)', margin: '8px 0', padding: '4px 0 4px 10px', lineHeight: 1.6, borderLeft: '2px solid var(--vsc-border)'}}>
                          <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                        </div>;
                      }
                      // Final answer
                      return <div key={si} className="chat-markdown text-vsc-text" style={{fontSize: 13, lineHeight: 1.7, wordBreak: 'break-word', borderTop: si > 0 ? '1px solid var(--vsc-border)' : 'none', paddingTop: si > 0 ? 10 : 0, marginTop: si > 0 ? 8 : 0}}>
                        <Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown>
                      </div>;
                    }
                    // Tool step — collapsible card with topbar
                    const toolsWithArg = (s.tools || []).filter((t: any) => t.arg);
                    if (toolsWithArg.length === 0) return null;
                    const iconRow = toolsWithArg.map((t: any) => toolIcons[t.name] || '⚙️').join(' ');
                    return <details key={si} style={{margin: '8px 0', borderRadius: 8, background: 'var(--vsc-bg)', border: '1px solid var(--vsc-border)', overflow: 'hidden'}}>
                      <summary style={{padding: '5px 10px', cursor: 'pointer', fontSize: 11, color: 'var(--vsc-text-muted)', userSelect: 'none', display: 'flex', alignItems: 'center', gap: 6}}>
                        <span style={{fontSize: 8, opacity: 0.4, transition: 'transform 0.15s'}}>▶</span>
                        <span>{iconRow}</span>
                        <span style={{opacity: 0.4, fontSize: 10}}>{toolsWithArg.length > 1 ? `${toolsWithArg.length} tools` : toolsWithArg[0]?.name}</span>
                      </summary>
                      <div style={{borderTop: '1px solid var(--vsc-border)'}}>
                        {toolsWithArg.map((t: any, ti: number) => {
                          const arg = t.arg.replace(/^\/home\/\w+\//, '~/');
                          const hasResult = !!t.result;
                          const isError = t.result?.startsWith('exit ') || t.result?.startsWith('❌');
                          const resultLines = t.result?.split('\n') || [];
                          const isLong = resultLines.length > 4;
                          const preview = isLong ? resultLines.slice(0, 3).join('\n') + '\n...' : t.result;
                          return <div key={ti} style={{padding: '6px 12px', borderBottom: ti < toolsWithArg.length - 1 ? '1px solid var(--vsc-border)' : 'none'}}>
                            <div style={{display: 'flex', alignItems: 'center', gap: 6, marginBottom: hasResult ? 4 : 0}}>
                              <span style={{fontFamily: 'monospace', fontSize: 11, color: 'var(--vsc-text)', opacity: 0.85, wordBreak: 'break-all', flex: 1}}>{arg}</span>
                            </div>
                            {hasResult && <pre style={{fontSize: 11, padding: '4px 8px', borderRadius: 4, background: isError ? 'rgba(244,71,71,0.08)' : 'rgba(78,201,176,0.06)', color: isError ? '#f44747' : '#4ec9b0', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 140, overflow: 'auto', border: '1px solid ' + (isError ? 'rgba(244,71,71,0.15)' : 'rgba(78,201,176,0.1)'), margin: 0, lineHeight: 1.5}}>
                              {preview}
                            </pre>}
                          </div>;
                        })}
                      </div>
                    </details>;
                  })}

                  {/* Status */}
                  {isPending && <div className="text-vsc-accent" style={{fontSize: 12, padding: '6px 0'}}><span className="animate-pulse">● </span>Thinking...</div>}
                  {isRunning && <div className="text-vsc-accent" style={{fontSize: 12, padding: '6px 0'}}><span className="animate-pulse">● </span>Running{toolCount > 0 ? ` (${toolCount} tools)` : ''}...</div>}
                  {r?.status === 'streaming' && <div className="text-vsc-accent" style={{fontSize: 12, padding: '6px 0'}}><span className="animate-pulse">● </span>Streaming...</div>}
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

export default ChatMiddleView;
