import { useState, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../contexts/AuthContext';
import config from '../config';

interface Message { role: 'user' | 'assistant'; content: string }
interface App { id: string; name: string; icon: string; url: string }

const SYSTEM_PROMPT = `You are CiCy, the assistant for an AI-powered desktop OS.

Capabilities:
- Have natural conversations and answer questions
- When the user wants to create an app, your reply MUST include the [CREATE_APP] marker followed by an app description

When the user says things like "build me ...", "create ...", "I want ...":
1. Briefly confirm you understand
2. Append [CREATE_APP]<detailed app description> at the end of the reply

Example: when the user says "build me a bitcoin price dashboard", reply:
"Sure, I'll create a bitcoin live-price dashboard for you! [CREATE_APP]bitcoin live price dashboard, shows BTC/USD price, auto-refresh, dark theme"

Rules:
- Reply concisely and friendly
- Do not output code
- Only emit [CREATE_APP] for explicit creation intent`;

export default function Desktop() {
  const { t } = useTranslation('desktop');
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

  const [createStep, setCreateStep] = useState('');
  const createApp = async (prompt: string) => {
    setCreating(prompt);
    setCreateStep(t('thinkingStep'));
    const t0 = Date.now();
    const steps: Array<[number, string]> = [
      [2000, t('designStep')],
      [5000, t('codeStep')],
      [8000, t('packageStep')],
    ];
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
        setMessages(prev => [...prev, { role: 'assistant', content: t('appCreated', { name: data.app.name, icon: data.app.icon, seconds: sec }) }]);
      } else {
        setMessages(prev => [...prev, { role: 'assistant', content: t('createFailed', { detail: data.detail || JSON.stringify(data) }) }]);
      }
    } catch (e: any) {
      setMessages(prev => [...prev, { role: 'assistant', content: t('createErrored', { message: e.message }) }]);
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
      const apiMsgs = [{ role: 'system', content: SYSTEM_PROMPT }, ...newMsgs].map(m => ({ role: m.role, content: m.content }));
      const resp = await fetch(`${config.apiBase}/api/ai/chat/stream`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ messages: apiMsgs }),
      });
      const data = await resp.json();
      let content = data.result || t('assistantOops');

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
      setMessages(prev => [...prev.slice(0, -1), { role: 'assistant', content: t('assistantConnFailed') }]);
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
      <div className="fixed inset-0 pointer-events-none" style={{
        background: 'radial-gradient(circle at 30% 40%, rgba(59,130,246,0.06) 0%, transparent 50%), radial-gradient(circle at 70% 60%, rgba(168,85,247,0.04) 0%, transparent 50%)'
      }} />
      <div className="fixed inset-0 pointer-events-none" style={{
        backgroundImage: 'radial-gradient(rgba(255,255,255,0.03) 1px, transparent 1px)',
        backgroundSize: '24px 24px'
      }} />

      <header className="relative z-10 h-12 flex items-center justify-between px-5 border-b border-white/[0.06] bg-[#0a0a0a]/80 backdrop-blur-xl shrink-0">
        <div className="flex items-center gap-2.5">
          <span className="text-xl">✨</span>
          <span className="text-sm font-semibold text-white/80">CiCy</span>
        </div>
        <div className="flex items-center gap-3">
          {creating && <span className="text-xs text-amber-400/80">{t('statusCreating')}</span>}
          {streaming && !creating && <span className="text-xs text-blue-400/80">{t('statusThinking')}</span>}
          {!busy && <span className="text-xs text-white/30">{t('statusIdle')}</span>}
          <button onClick={logout} className="w-7 h-7 rounded-full bg-white/[0.06] flex items-center justify-center text-xs text-white/40 hover:text-white/60 transition-colors cursor-pointer">✕</button>
        </div>
      </header>

      <main className="flex-1 relative z-5 flex flex-col items-center overflow-hidden">
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

        {apps.length === 0 && !hasChat && (
          <div className="flex-1 flex flex-col items-center justify-center">
            <div className="text-5xl mb-4 opacity-60">✨</div>
            <div className="text-lg text-white/50 font-medium mb-2">{t('emptyHeadline')}</div>
            <div className="text-sm text-white/20">{t('emptySubline')}</div>
          </div>
        )}

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
                  {createStep || t('creatingApp')}
                </div>
              </div>
            )}
            <div ref={bottomRef} />
          </div>
        )}
      </main>

      <div className="relative z-20 pb-8 pt-2 px-4 flex justify-center">
        <div className="w-full max-w-xl">
          <div className={`flex items-center gap-3 bg-[#141414]/90 border border-white/[0.08] rounded-2xl px-4 py-3 backdrop-blur-xl shadow-2xl transition-colors ${busy ? 'opacity-60' : ''} focus-within:border-blue-500/30`}>
            <span className="text-lg opacity-30">💬</span>
            <input
              className="flex-1 bg-transparent border-none outline-none text-sm text-white/80 placeholder:text-white/20 font-[inherit]"
              placeholder={hasChat ? t('placeholderContinue') : t('placeholderStart')}
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
          <div className="text-center mt-2.5 text-xs text-white/[0.12]">{t('hintEnterToSend')}</div>
        </div>
      </div>
    </div>
  );
}
