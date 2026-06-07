import { useCallback, useEffect, useRef, useState } from 'react';
import { Send, Loader2 } from 'lucide-react';
import CurrentHistoryView from './CurrentHistoryView';
import apiService from '../../services/api';

/*
 * DispatcherChat — dispatcher(PM) agent 的专属卡片主体(data-id="dispatcher-chat")。
 * 上 = CurrentHistoryView(网关审计驱动的对话历史,reply.json 轮询 = 流式尾巴),
 * 下 = prompt 输入条,发送走 /api/tmux/send(送进 REPL stdin,与终端/TG 同一管道)。
 * 终端不再展示——dispatcher 在 web 上就是一个聊天窗口。
 */
export default function DispatcherChat({ paneId, active, agentType = 'dispatcher' }: { paneId: string; active: boolean; agentType?: string }) {
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  // 回复进行中(busy)→ 锁发送、显示 waiting。只有 reply complete / fail 才解锁。
  // 信号来自 CurrentHistoryView 的轮询(cicy:dispatcher-busy)。
  const [busy, setBusy] = useState(false);
  const composingRef = useRef(false);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    const onBusy = (e: Event) => {
      const detail = (e as CustomEvent)?.detail || {};
      const id = String(detail.paneId || '').trim();
      if (id && id !== paneId) return;
      setBusy(!!detail.busy);
    };
    window.addEventListener('cicy:dispatcher-busy', onBusy as EventListener);
    return () => window.removeEventListener('cicy:dispatcher-busy', onBusy as EventListener);
  }, [paneId]);

  // 切换 PM 时清空忙态,避免把上一个会话的 waiting 带过来。
  useEffect(() => { setBusy(false); }, [paneId]);

  const send = useCallback(async () => {
    const value = text.trim();
    // 回复还没结束(busy)时禁止发送 —— 等 complete / fail。
    if (!value || sending || busy) return;
    setSending(true);
    setBusy(true); // 立刻锁住,不等轮询事件回传
    setText('');
    // Paint the q bubble + reserve the a slot THIS frame — BEFORE the POST
    // round-trips — so the question shows the instant you hit send. `text` in the
    // detail is the optimistic-paint signal.
    window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId, text: value } }));
    try {
      await apiService.sendCommand(paneId, value, true);
      // Nudge again (no text) so the poll starts chasing the live answer promptly.
      window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId } }));
    } catch {
      // Send failed → retract the optimistic slots, restore the draft, unlock.
      setText(value);
      setBusy(false);
      window.dispatchEvent(new CustomEvent('cicy:current-history-cancel-optimistic', { detail: { paneId } }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '发送失败' }));
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }, [paneId, text, sending, busy]);

  const canSend = !!text.trim() && !sending && !busy;

  return (
    <div data-id="dispatcher-chat" className="flex h-full w-full flex-col bg-[#0c0d10]">
      <div data-id="dispatcher-chat-history" className="min-h-0 flex-1 overflow-hidden">
        <CurrentHistoryView key={paneId} paneId={paneId} open={active} agentType={agentType} />
      </div>
      <div data-id="dispatcher-chat-input-bar" className="shrink-0 border-t border-white/[0.06] bg-black/[0.25] py-2.5">
        {/* Width-locked to the history content column (max-w-3xl px-4) so the
            prompt sits flush under the conversation, not edge-to-edge. */}
        <div data-id="dispatcher-chat-input-inner" className={`mx-auto flex w-full max-w-3xl items-end gap-2 rounded-xl border bg-white/[0.03] px-3 py-2 transition-colors ${busy ? 'border-white/[0.06] opacity-80' : 'border-white/[0.08] focus-within:border-blue-500/40'}`} style={{ width: 'calc(100% - 2rem)' }}>
          <textarea
            ref={inputRef}
            data-id="dispatcher-chat-input"
            value={text}
            rows={Math.min(6, Math.max(2, text.split('\n').length))}
            placeholder={busy ? '回复生成中,请稍候…(完成或失败后可继续发送)' : '跟你的项目经理说点什么…(Enter 发送,Shift+Enter 换行)'}
            onChange={(e) => setText(e.target.value)}
            onCompositionStart={() => { composingRef.current = true; }}
            onCompositionEnd={() => { composingRef.current = false; }}
            onKeyDown={(e) => {
              // The stack card root is role="button" and preventDefaults
              // Space/Enter for keyboard activation — stop bubbling so typing
              // in the prompt never reaches it (space was being swallowed).
              e.stopPropagation();
              if (e.key === 'Enter' && !e.shiftKey && !composingRef.current) {
                e.preventDefault();
                void send();
              }
            }}
            className="max-h-40 flex-1 resize-none self-stretch bg-transparent text-sm leading-6 text-zinc-200 outline-none placeholder:text-zinc-600"
          />
          <button
            data-id="dispatcher-chat-send"
            type="button"
            onClick={() => void send()}
            disabled={!canSend}
            className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg transition-colors ${canSend ? 'bg-blue-600 text-white hover:bg-blue-500' : 'bg-white/[0.04] text-zinc-600'}`}
            title={busy ? '等待回复完成' : '发送'}
            aria-label={busy ? 'Waiting' : 'Send'}
          >
            {sending || busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
          </button>
        </div>
      </div>
    </div>
  );
}
