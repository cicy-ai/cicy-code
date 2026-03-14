import React, { useState, useRef, useCallback } from 'react';
import { Loader2, ArrowUp, CheckCircle } from 'lucide-react';
import apiService from '../../services/api';

interface CommandInputProps {
  paneId: string;
  token: string;
  agentStatus?: string;
  contextUsage?: number | null;
}

const CommandInput: React.FC<CommandInputProps> = ({ paneId, token, agentStatus = 'idle', contextUsage }) => {
  const [text, setText] = useState(() => localStorage.getItem(`v2_draft_${paneId}`) || '');
  const [history, setHistory] = useState<string[]>(() => {
    try { return JSON.parse(localStorage.getItem(`v2_hist_${paneId}`) || '[]'); } catch { return []; }
  });
  const [histIdx, setHistIdx] = useState(-1);
  const [tmpDraft, setTmpDraft] = useState('');
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const [correcting, setCorrecting] = useState(false);
  const [correction, setCorrection] = useState<[string, string] | null>(null);
  const [enterToSend, setEnterToSend] = useState(() => localStorage.getItem('enter_to_send') !== 'false');
  const ref = useRef<HTMLTextAreaElement>(null);

  const saveHist = (h: string[]) => { setHistory(h); localStorage.setItem(`v2_hist_${paneId}`, JSON.stringify(h)); };
  const saveDraft = (t: string) => { setText(t); localStorage.setItem(`v2_draft_${paneId}`, t); };

  const send = useCallback(async (cmd: string) => {
    if (!cmd.trim()) return;
    const c = cmd.trim();
    saveHist([c, ...history.filter(x => x !== c)].slice(0, 50));
    setHistIdx(-1); setTmpDraft(''); saveDraft('');
    setSending(true); setSent(false);
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneId, q: c } }));
      await apiService.sendCommand(paneId, c);
      setSent(true); setTimeout(() => setSent(false), 2000);
    } catch (e) { console.error(e); }
    finally { setSending(false); setTimeout(() => ref.current?.focus(), 50); }
  }, [paneId, history]);

  const onKey = async (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Ctrl/Cmd+Enter → English correction
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.nativeEvent.isComposing) {
      e.preventDefault();
      if (!text.trim() && correction) {
        // Send correction: Shift=Chinese, else English
        const cmd = e.shiftKey ? correction[1] : correction[0];
        setCorrection(null);
        await send(cmd);
        return;
      }
      if (text.trim() && token) {
        const cmd = text.trim();
        saveDraft('');
        setCorrecting(true);
        try {
          const { data } = await apiService.correctEnglish(cmd);
          if (data.success && Array.isArray(data.result)) {
            setCorrection(data.result);
            saveHist([cmd, ...history.filter(x => x !== cmd)].slice(0, 50));
          } else {
            saveDraft(cmd);
          }
        } catch { saveDraft(cmd); }
        finally { setCorrecting(false); }
      }
      return;
    }

    // Empty input shortcuts → forward to tmux
    if (!text) {
      if (e.key === 'Escape' || e.key === 'Backspace') {
        e.preventDefault();
        const map: Record<string, string> = { backspace: 'BSpace', escape: 'Escape' };
        await apiService.sendKeys(paneId, map[e.key.toLowerCase()]);
        return;
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c') {
        e.preventDefault();
        await apiService.sendKeys(paneId, 'C-c');
        return;
      }
    }

    // Enter → send
    if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      const shouldSend = enterToSend ? !e.shiftKey : e.shiftKey;
      if (shouldSend) {
        e.preventDefault();
        if (!text.trim() && correction) {
          saveDraft(correction[0]);
          setCorrection(null);
        } else if (text.trim()) {
          await send(text);
        } else {
          await apiService.sendKeys(paneId, 'Enter');
        }
      }
      return;
    }

    // Arrow up/down → history
    if (e.key === 'ArrowUp') {
      const ta = e.currentTarget;
      if (!ta.value.substring(0, ta.selectionStart).includes('\n') && history.length > 0) {
        e.preventDefault();
        if (histIdx === -1) { setTmpDraft(text); setHistIdx(0); setText(history[0]); }
        else if (histIdx < history.length - 1) { const n = histIdx + 1; setHistIdx(n); setText(history[n]); }
      }
    } else if (e.key === 'ArrowDown') {
      const ta = e.currentTarget;
      if (!ta.value.substring(ta.selectionStart).includes('\n')) {
        e.preventDefault();
        if (histIdx > 0) { const n = histIdx - 1; setHistIdx(n); setText(history[n]); }
        else if (histIdx === 0) { setHistIdx(-1); setText(tmpDraft); }
      }
    }
  };

  return (
    <div className="flex flex-col border-t border-vsc-border bg-vsc-bg-secondary">
      {/* Correction result */}
      {correction && (
        <div className="px-3 py-2 border-b border-vsc-border bg-vsc-bg text-xs space-y-1">
          <div className="text-emerald-400 cursor-pointer hover:underline" onClick={() => send(correction[0])}>{correction[0]}</div>
          <div className="text-vsc-text-muted cursor-pointer hover:underline" onClick={() => send(correction[1])}>{correction[1]}</div>
          <div className="text-vsc-text-muted opacity-50">⌘↵ send EN · ⌘⇧↵ send CN · Esc dismiss</div>
        </div>
      )}
      {/* Input */}
      <div className="relative p-2">
        <textarea
          ref={ref}
          value={text}
          onChange={e => { saveDraft(e.target.value); if (histIdx === -1) setTmpDraft(e.target.value); }}
          onKeyDown={onKey}
          placeholder="Type command..."
          disabled={sending}
          className="w-full bg-vsc-bg text-vsc-text rounded-md border border-vsc-border p-2.5 pr-10 focus:border-vsc-accent/50 outline-none resize-none text-sm placeholder:text-vsc-text-muted/40"
          rows={3}
        />
        <button
          onClick={() => send(text)}
          disabled={!text.trim() || sending}
          className="absolute top-4 right-4 p-1.5 bg-vsc-accent hover:bg-vsc-accent-hover text-white rounded-md disabled:opacity-30 disabled:cursor-not-allowed"
        >
          {sending ? <Loader2 size={14} className="animate-spin" /> : sent ? <CheckCircle size={14} className="text-green-400" /> : <ArrowUp size={14} />}
        </button>
      </div>
      {/* Status bar */}
      <div className="h-6 border-t border-vsc-border flex items-center px-2.5 gap-2 shrink-0">
        <span className={`w-2 h-2 rounded-full ${agentStatus === 'thinking' ? 'bg-yellow-500 animate-pulse' : 'bg-green-500'}`} />
        <span className="text-[11px] text-vsc-text-secondary">{agentStatus === 'thinking' ? 'Thinking...' : 'Idle'}</span>
        {correcting && <Loader2 size={12} className="text-purple-400 animate-spin" />}
        <button
          onClick={() => { const n = !enterToSend; setEnterToSend(n); localStorage.setItem('enter_to_send', String(n)); }}
          className="text-[10px] text-vsc-text-muted px-1.5 py-0.5 border border-vsc-border rounded hover:text-white ml-auto"
        >
          {enterToSend ? '⏎Send' : '⇧⏎Send'}
        </button>
        {contextUsage != null && <span className="text-[11px] text-vsc-text-muted">{contextUsage}%</span>}
      </div>
    </div>
  );
};

export default CommandInput;
