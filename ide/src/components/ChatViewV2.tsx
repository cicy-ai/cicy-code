import React, { useEffect, useRef, useState } from 'react';
import { usePane } from '../contexts/PaneContext';
import config from '../config';

interface Msg {
  id: string;
  type: 'user' | 'ai' | 'tool';
  text: string;
  toolName?: string;
  toolDone?: boolean;
}

export const ChatViewV2: React.FC = () => {
  const { displayPaneId, token } = usePane();
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [input, setInput] = useState('');
  const [status, setStatus] = useState<'idle' | 'thinking'>('idle');
  const [connected, setConnected] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!displayPaneId || !token) return;
    let dead = false;
    let timer: ReturnType<typeof setTimeout>;

    function go() {
      if (dead) return;
      const pane = displayPaneId.replace(':main.0', '');
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      const base = config.apiBase.replace(/^https?/, proto);
      const ws = new WebSocket(`${base}/api/chat/ws?pane=${pane}&token=${token}`);

      ws.onopen = () => { wsRef.current = ws; setConnected(true); };
      ws.onclose = () => { wsRef.current = null; setConnected(false); if (!dead) timer = setTimeout(go, 2000); };
      ws.onerror = () => ws.close();

      ws.onmessage = (e) => {
        const evt = JSON.parse(e.data);
        switch (evt.type) {
          case 'user_message':
            setMsgs(p => [...p, { id: evt.data.id, type: 'user', text: evt.data.text }]);
            break;
          case 'ai_chunk':
            setMsgs(p => {
              const last = p[p.length - 1];
              if (last?.type === 'ai') return [...p.slice(0, -1), { ...last, text: last.text + evt.data.delta }];
              return [...p, { id: `ai_${Date.now()}`, type: 'ai', text: evt.data.delta }];
            });
            break;
          case 'tool_start':
            setMsgs(p => [...p, { id: `t_${evt.data.id || Date.now()}`, type: 'tool', text: evt.data.name, toolName: evt.data.name, toolDone: false }]);
            break;
          case 'tool_done':
            setMsgs(p => p.map(m => m.id === `t_${evt.data.id}` ? { ...m, toolDone: true } : m));
            break;
          case 'status_change':
            setStatus(evt.data.status);
            break;
        }
      };
    }

    go();
    return () => { dead = true; clearTimeout(timer); wsRef.current?.close(); };
  }, [displayPaneId, token]);

  useEffect(() => { endRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [msgs]);

  const send = () => {
    const ws = wsRef.current;
    if (!input.trim() || !ws || ws.readyState !== WebSocket.OPEN) return;
    const text = input.trim();
    setInput('');
    setMsgs(p => [...p, { id: `u_${Date.now()}`, type: 'user', text }]);
    ws.send(JSON.stringify({ action: 'send', text }));
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: '#1e1e1e' }}>
      <div style={{ flex: 1, overflow: 'auto', padding: '12px 16px' }}>
        {msgs.map(m => (
          <div key={m.id} style={{ marginBottom: 10 }}>
            {m.type === 'tool' ? (
              <div style={{ color: '#ce9178', fontSize: 12 }}>🔧 {m.toolName} {m.toolDone ? '✅' : '⏳'}</div>
            ) : (
              <>
                <div style={{ color: m.type === 'user' ? '#4ec9b0' : '#569cd6', fontWeight: 600, fontSize: 12 }}>{m.type === 'user' ? 'You' : 'AI'}</div>
                <div style={{ color: '#d4d4d4', whiteSpace: 'pre-wrap', marginTop: 2, fontSize: 13 }}>{m.text}</div>
              </>
            )}
          </div>
        ))}
        {status === 'thinking' && msgs[msgs.length - 1]?.type !== 'ai' && (
          <div style={{ color: '#569cd6', fontSize: 12, opacity: 0.6 }}>AI is thinking...</div>
        )}
        <div ref={endRef} />
      </div>
      <div style={{ padding: '8px 12px', borderTop: '1px solid #333', display: 'flex', gap: 8, alignItems: 'center' }}>
        {!connected && <div style={{ width: 6, height: 6, borderRadius: '50%', background: '#f44' }} title="Disconnected" />}
        <input
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } }}
          placeholder="Type a message..."
          style={{ flex: 1, padding: '6px 10px', background: '#2d2d2d', border: '1px solid #3e3e3e', color: '#d4d4d4', borderRadius: 4, fontSize: 13 }}
        />
        <button
          onClick={send}
          disabled={!input.trim() || !connected}
          style={{ padding: '6px 14px', background: input.trim() && connected ? '#0e639c' : '#555', color: '#fff', border: 'none', borderRadius: 4, cursor: input.trim() && connected ? 'pointer' : 'default', fontSize: 13 }}
        >Send</button>
      </div>
    </div>
  );
};
