import { useCallback, useRef, useState } from 'react';
import { Send, Loader2 } from 'lucide-react';
import CurrentHistoryView from './CurrentHistoryView';
import apiService from '../../services/api';

/*
 * DispatcherChat — dispatcher(PM) agent 的专属卡片主体(data-id="dispatcher-chat")。
 * 上 = CurrentHistoryView(网关审计驱动的对话历史,reply.json 轮询 = 流式尾巴),
 * 下 = prompt 输入条,发送走 /api/tmux/send(送进 REPL stdin,与终端/TG 同一管道)。
 * 终端不再展示——dispatcher 在 web 上就是一个聊天窗口。
 */
export default function DispatcherChat({ paneId, active }: { paneId: string; active: boolean }) {
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const composingRef = useRef(false);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);

  const send = useCallback(async () => {
    const value = text.trim();
    if (!value || sending) return;
    setSending(true);
    try {
      await apiService.sendCommand(paneId, value, true);
      setText('');
      // Nudge the history view to attach the new live turn immediately
      // instead of waiting for its idle-cadence poll.
      window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId } }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '发送失败' }));
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }, [paneId, text, sending]);

  return (
    <div data-id="dispatcher-chat" className="flex h-full w-full flex-col bg-[#0c0d10]">
      <div data-id="dispatcher-chat-history" className="min-h-0 flex-1 overflow-hidden">
        <CurrentHistoryView key={paneId} paneId={paneId} open={active} />
      </div>
      <div data-id="dispatcher-chat-input-bar" className="shrink-0 border-t border-white/[0.06] bg-black/[0.25] py-2.5">
        {/* Width-locked to the history content column (max-w-3xl px-4) so the
            prompt sits flush under the conversation, not edge-to-edge. */}
        <div data-id="dispatcher-chat-input-inner" className="mx-auto flex w-full max-w-3xl items-end gap-2 rounded-xl border border-white/[0.08] bg-white/[0.03] px-3 py-2 transition-colors focus-within:border-blue-500/40" style={{ width: 'calc(100% - 2rem)' }}>
          <textarea
            ref={inputRef}
            data-id="dispatcher-chat-input"
            value={text}
            rows={Math.min(6, Math.max(2, text.split('\n').length))}
            placeholder="跟你的项目经理说点什么…(Enter 发送,Shift+Enter 换行)"
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
            disabled={!text.trim() || sending}
            className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg transition-colors ${text.trim() && !sending ? 'bg-blue-600 text-white hover:bg-blue-500' : 'bg-white/[0.04] text-zinc-600'}`}
            title="发送"
            aria-label="Send"
          >
            {sending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
          </button>
        </div>
      </div>
    </div>
  );
}
