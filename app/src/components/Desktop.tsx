import { useState, useRef, useEffect } from 'react';
import { useAuth } from '../contexts/AuthContext';
import config from '../config';

interface Message { role: 'user' | 'assistant'; content: string }
interface App { id: string; name: string; icon: string; url: string }

export default function Desktop() {
  const { token, logout } = useAuth();
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [apps, setApps] = useState<App[]>([]);
  const [creating, setCreating] = useState('');
  const bottomRef = useRef<HTMLDivElement>(null);

  const scrollToBottom = () => bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  useEffect(scrollToBottom, [messages]);

  // Load apps on mount
  useEffect(() => {
    if (!token) return;
    fetch(`${config.apiBase}/api/apps`, { headers: { Authorization: `Bearer ${token}` } })
      .then(r => r.json())
      .then(d => { if (d.apps) setApps(d.apps); })
      .catch(() => {});
  }, [token]);

  const systemPrompt = `你是 CiCy，AI 桌面操作系统的助手。

你的能力：
- 和用户自然对话，回答问题
- 当用户想创建应用时，回复必须包含 [CREATE_APP] 标记，后面跟应用描述

当用户说"帮我做..."、"创建..."、"我想要..."等创建意图时：
1. 简短确认你理解了需求
2. 在回复末尾加上 [CREATE_APP]应用的详细描述

例如用户说"帮我做个比特币价格看板"，你回复：
"好的，我来帮你创建一个比特币实时价格看板！ [CREATE_APP]比特币实时价格看板，显示BTC/USD价格，自动刷新，深色主题"

规则：
- 中文回复，简短友好
- 不要输出代码
- 只有明确的创建意图才加 [CREATE_APP]`;

  const [createStep, setCreateStep] = useState('');
  const createApp = async (prompt: string) => {
    setCreating(prompt);
    setCreateStep('🧠 理解需求...');
    const t0 = Date.now();
    const steps = [
      [2000, '🎨 设计界面...'],
      [5000, '⚙️ 生成代码...'],
      [8000, '📦 打包应用...'],
    ] as const;
    const timers = steps.map(([ms, text]) => setTimeout(() => setCreateStep(text), ms));
    try {
      const resp = await fetch(`${config.apiBase}/api/apps/create`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ prompt }),
      });
      const data = await resp.json();
      if (data.success && data.app) {
        setApps(prev => [data.app, ...prev]);
        const sec = ((Date.now() - t0) / 1000).toFixed(1);
        setMessages(prev => [...prev, { role: 'assistant', content: `✅ 「${data.app.name}」${data.app.icon} 已创建！(${sec}s)\n点击桌面图标即可打开。` }]);
      } else {
        setMessages(prev => [...prev, { role: 'assistant', content: `创建失败: ${data.detail || JSON.stringify(data)}` }]);
      }
    } catch (e: any) {
      setMessages(prev => [...prev, { role: 'assistant', content: `创建出错: ${e.message}` }]);
    } finally {
      timers.forEach(clearTimeout);
      setCreating('');
      setCreateStep('');
    }
  };

  const send = async () => {
    const text = input.trim();
    if (!text || streaming) return;
    setInput('');
    const userMsg: Message = { role: 'user', content: text };
    const newMsgs = [...messages, userMsg];
    setMessages([...newMsgs, { role: 'assistant', content: '' }]);
    setStreaming(true);

    try {
      const apiMsgs = [{ role: 'system', content: systemPrompt }, ...newMsgs].map(m => ({ role: m.role, content: m.content }));
      const resp = await fetch(`${config.apiBase}/api/ai/chat/stream`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ messages: apiMsgs }),
      });
      const data = await resp.json();
      let content = data.result || '抱歉，出了点问题。';

      // Check for [CREATE_APP] marker
      const marker = '[CREATE_APP]';
      const idx = content.indexOf(marker);
      if (idx >= 0) {
        const appPrompt = content.slice(idx + marker.length).trim();
        content = content.slice(0, idx).trim();
        setMessages(prev => [...prev.slice(0, -1), { role: 'assistant', content }]);
        setStreaming(false);
        if (appPrompt) createApp(appPrompt);
        return;
      }

      setMessages(prev => [...prev.slice(0, -1), { role: 'assistant', content }]);
    } catch {
      setMessages(prev => [...prev.slice(0, -1), { role: 'assistant', content: '连接失败，请重试。' }]);
    } finally {
      setStreaming(false);
    }
  };

  const openApp = (app: App) => {
    window.open(`${config.apiBase}${app.url}`, '_blank');
  };

  const busy = streaming || !!creating;
  const hasChat = messages.length > 0;

  return (
    <div className="h-screen bg-[#0a0a0a] text-white flex flex-col overflow-hidden relative">
      {/* Background */}
      <div className="fixed inset-0 pointer-events-none" style={{
        background: 'radial-gradient(circle at 30% 40%, rgba(59,130,246,0.06) 0%, transparent 50%), radial-gradient(circle at 70% 60%, rgba(168,85,247,0.04) 0%, transparent 50%)'
      }} />
      <div className="fixed inset-0 pointer-events-none" style={{
        backgroundImage: 'radial-gradient(rgba(255,255,255,0.03) 1px, transparent 1px)',
        backgroundSize: '24px 24px'
      }} />

      {/* Top bar */}
      <header className="relative z-10 h-12 flex items-center justify-between px-5 border-b border-white/[0.06] bg-[#0a0a0a]/80 backdrop-blur-xl shrink-0">
        <div className="flex items-center gap-2.5">
          <span className="text-xl">✨</span>
          <span className="text-sm font-semibold text-white/80">CiCy</span>
        </div>
        <div className="flex items-center gap-3">
          {creating && <span className="text-xs text-amber-400/80">● creating</span>}
          {streaming && !creating && <span className="text-xs text-blue-400/80">● thinking</span>}
          {!busy && <span className="text-xs text-white/30">● idle</span>}
          <button onClick={logout} className="w-7 h-7 rounded-full bg-white/[0.06] flex items-center justify-center text-xs text-white/40 hover:text-white/60 transition-colors cursor-pointer">✕</button>
        </div>
      </header>

      {/* Main area */}
      <main className="flex-1 relative z-5 flex flex-col items-center overflow-hidden">
        {/* App grid — always show if apps exist */}
        {apps.length > 0 && (
          <div className={`w-full max-w-2xl px-6 ${hasChat ? 'pt-4 pb-2' : 'flex-1 flex items-center justify-center'}`}>
            <div className="grid grid-cols-4 sm:grid-cols-6 gap-5">
              {apps.map(app => (
                <div key={app.id} onClick={() => openApp(app)} className="flex flex-col items-center gap-2 cursor-pointer hover:scale-105 transition-transform">
                  <div className="w-14 h-14 rounded-2xl bg-white/[0.04] border border-white/[0.06] backdrop-blur-lg flex items-center justify-center text-2xl shadow-lg">{app.icon}</div>
                  <span className="text-xs text-white/40 max-w-[72px] text-center truncate">{app.name}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Empty state */}
        {apps.length === 0 && !hasChat && (
          <div className="flex-1 flex flex-col items-center justify-center">
            <div className="text-5xl mb-4 opacity-60">✨</div>
            <div className="text-lg text-white/50 font-medium mb-2">Ask your agent to build something</div>
            <div className="text-sm text-white/20">Describe what you want, AI will create it for you</div>
          </div>
        )}

        {/* Chat messages */}
        {hasChat && (
          <div className="flex-1 w-full max-w-2xl overflow-y-auto px-4 py-4 space-y-3">
            {messages.map((msg, i) => (
              <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[80%] px-4 py-2.5 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap ${
                  msg.role === 'user'
                    ? 'bg-blue-600/20 text-white/90 rounded-br-md'
                    : 'bg-white/[0.04] text-white/70 rounded-bl-md'
                }`}>
                  {msg.content || <span className="inline-flex gap-1"><span className="w-1.5 h-1.5 rounded-full bg-blue-400/60 animate-pulse" /><span className="w-1.5 h-1.5 rounded-full bg-blue-400/60 animate-pulse [animation-delay:0.2s]" /><span className="w-1.5 h-1.5 rounded-full bg-blue-400/60 animate-pulse [animation-delay:0.4s]" /></span>}
                </div>
              </div>
            ))}
            {creating && (
              <div className="flex justify-start">
                <div className="px-4 py-2.5 rounded-2xl rounded-bl-md bg-amber-500/10 text-amber-400/80 text-sm flex items-center gap-2">
                  <span className="inline-flex gap-1"><span className="w-1.5 h-1.5 rounded-full bg-amber-400/60 animate-pulse" /><span className="w-1.5 h-1.5 rounded-full bg-amber-400/60 animate-pulse [animation-delay:0.2s]" /><span className="w-1.5 h-1.5 rounded-full bg-amber-400/60 animate-pulse [animation-delay:0.4s]" /></span>
                  {createStep || '⚡ 正在创建应用...'}
                </div>
              </div>
            )}
            <div ref={bottomRef} />
          </div>
        )}
      </main>

      {/* Input */}
      <div className="relative z-20 pb-8 pt-2 px-4 flex justify-center">
        <div className="w-full max-w-xl">
          <div className={`flex items-center gap-3 bg-[#141414]/90 border border-white/[0.08] rounded-2xl px-4 py-3 backdrop-blur-xl shadow-2xl transition-colors ${busy ? 'opacity-60' : ''} focus-within:border-blue-500/30`}>
            <span className="text-lg opacity-30">💬</span>
            <input
              className="flex-1 bg-transparent border-none outline-none text-sm text-white/80 placeholder:text-white/20 font-[inherit]"
              placeholder={hasChat ? '继续对话...' : '帮我做一个能看比特币价格的工具...'}
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && !e.shiftKey && send()}
              disabled={busy}
            />
            <button
              onClick={send}
              disabled={busy || !input.trim()}
              className="w-9 h-9 rounded-xl bg-blue-500/15 text-blue-400/80 flex items-center justify-center text-base hover:bg-blue-500/25 disabled:opacity-30 transition-colors cursor-pointer"
            >↑</button>
          </div>
          <div className="text-center mt-2.5 text-xs text-white/[0.12]">Press Enter to send · Powered by AI</div>
        </div>
      </div>
    </div>
  );
}
