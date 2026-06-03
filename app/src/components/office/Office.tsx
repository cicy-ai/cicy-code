import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Building2, Send, Megaphone, AtSign, X, Loader2, CheckCircle2, CircleDot, MessageSquare,
} from 'lucide-react';

/*
 * Office — 「办公室」（data-id="office"）。
 * 左栏：命令对话（上=history，下=prompt，自动 @ 选中的 worker，可广播）。
 * 右侧：所有 worker 的 chat window，整齐网格，只显示 thinking + text（不拉 tool 结果）。
 * 头像 = 各 agent 的 avatar。纯 UI 原型，mock 实时流（先不接接口）。
 */

type LineType = 'thinking' | 'text';
interface Line { t: LineType; s: string }
type Status = 'idle' | 'working' | 'done';

interface Worker {
  id: string;
  name: string;
  role: string;
  emoji: string;       // avatar 占位（接接口后换成 agent 真实 avatar）
  accent: string;
  status: Status;
  script: Line[];
  shown: number;
}

type ChatKind = 'you' | 'dispatch' | 'broadcast' | 'done' | 'note';
interface ChatMsg { id: number; kind: ChatKind; from?: string; to?: string; text: string; ts: string }

const SELF = 'w-10001';

const W = (id: string, name: string, role: string, emoji: string, accent: string, status: Status, script: Line[]): Worker =>
  ({ id, name, role, emoji, accent, status, script, shown: status === 'working' ? 0 : script.length });

const INIT_WORKERS: Worker[] = [
  W('w-10010', '架构师 Aria', 'dev-senior', '🏛️', 'sky', 'working', [
    { t: 'thinking', s: '把"画布"拆成 3 张卡：数据层 / 渲染层 / 画布层。' },
    { t: 'text', s: '定义 LiteAgentCard props + digest 端点契约。' },
    { t: 'thinking', s: 'tool_result 不传，payload 小一个数量级。' },
    { t: 'text', s: '接口写进 docs，交给 Finn。' },
    { t: 'text', s: '✅ 完成：技术任务卡 + 接口契约。' },
  ]),
  W('w-10011', '前端 Finn', 'dev-junior', '🎨', 'violet', 'working', [
    { t: 'thinking', s: '复用 normalize，搭 worker window 网格。' },
    { t: 'text', s: '左栏命令对话 + 右栏 window grid。' },
    { t: 'thinking', s: '选中 window → prompt 自动 @ 它。' },
    { t: 'text', s: 'window 加宽、对齐网格。' },
    { t: 'text', s: '✅ 完成：办公室双栏布局。' },
  ]),
  W('w-10012', '测试 Quinn', 'qa', '🧪', 'emerald', 'working', [
    { t: 'thinking', s: '核对验收标准：N window 同屏不卡。' },
    { t: 'text', s: '跑 20 window 压力，盯帧率。' },
    { t: 'thinking', s: 'thinking 太长要截断。' },
    { t: 'text', s: 'FAIL：离屏 window 仍在轮询，需门控。' },
  ]),
  W('w-10013', '运维 Ops', 'ops', '🚀', 'orange', 'idle', [
    { t: 'text', s: '待构建产物，准备部署。' },
  ]),
  W('w-10014', '安全 Sage', 'reviewer', '🛡️', 'rose', 'working', [
    { t: 'thinking', s: '扫一遍有没有把 token 渲进 window。' },
    { t: 'text', s: 'text+thinking 不含工具入参，攻击面更小。' },
    { t: 'text', s: '✅ 完成：安全结论 PASS。' },
  ]),
];

const ACCENT: Record<string, { grad: string; ring: string; chip: string; dot: string }> = {
  sky:     { grad: 'from-sky-500/40 to-sky-700/20',         ring: 'ring-sky-400/40',     chip: 'bg-sky-500/15 text-sky-300',       dot: 'text-sky-400' },
  violet:  { grad: 'from-violet-500/40 to-violet-700/20',   ring: 'ring-violet-400/40',  chip: 'bg-violet-500/15 text-violet-300', dot: 'text-violet-400' },
  emerald: { grad: 'from-emerald-500/40 to-emerald-700/20', ring: 'ring-emerald-400/40', chip: 'bg-emerald-500/15 text-emerald-300', dot: 'text-emerald-400' },
  orange:  { grad: 'from-orange-500/40 to-orange-700/20',   ring: 'ring-orange-400/40',  chip: 'bg-orange-500/15 text-orange-300', dot: 'text-orange-400' },
  rose:    { grad: 'from-rose-500/40 to-rose-700/20',       ring: 'ring-rose-400/40',    chip: 'bg-rose-500/15 text-rose-300',     dot: 'text-rose-400' },
};

const STATUS_META: Record<Status, { label: string; cls: string }> = {
  idle:    { label: '空闲',   cls: 'text-zinc-500' },
  working: { label: '工作中', cls: 'text-amber-300' },
  done:    { label: '完成',   cls: 'text-emerald-300' },
};

function nowStamp(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

function Avatar({ emoji, accent, size = 28 }: { emoji: string; accent: string; size?: number }) {
  const acc = ACCENT[accent] ?? ACCENT.sky;
  return (
    <span className={`grid shrink-0 place-items-center rounded-full bg-gradient-to-br ${acc.grad} ring-1 ring-white/10`}
      style={{ width: size, height: size, fontSize: size * 0.5 }}>{emoji}</span>
  );
}

export default function Office() {
  const [workers, setWorkers] = useState<Worker[]>(INIT_WORKERS);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [mode, setMode] = useState<'single' | 'broadcast'>('single');
  const [text, setText] = useState('');
  const [mentionOpen, setMentionOpen] = useState(false);
  const [chat, setChat] = useState<ChatMsg[]>([
    { id: 1, kind: 'note', text: '办公室就绪。点右侧某个 worker 自动 @ 他派任务；或切广播对全体喊话。', ts: nowStamp() },
  ]);
  const seq = useRef(2);
  const chatRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const byId = useMemo(() => Object.fromEntries(workers.map((w) => [w.id, w])), [workers]);
  const target = selectedId ? byId[selectedId] : null;

  // mock 实时流
  useEffect(() => {
    const t = window.setInterval(() => {
      setWorkers((prev) => prev.map((w) => {
        if (w.status === 'working') {
          if (w.shown < w.script.length) return { ...w, shown: w.shown + 1 };
          return { ...w, status: 'done' };
        }
        if (w.status === 'done' && Math.random() < 0.1) return { ...w, status: 'working', shown: 0 };
        return w;
      }));
    }, 1200);
    return () => window.clearInterval(t);
  }, []);

  useEffect(() => {
    const n = chatRef.current;
    if (n) n.scrollTop = n.scrollHeight;
  }, [chat]);

  const push = (m: Omit<ChatMsg, 'id' | 'ts'>) => setChat((c) => [...c, { id: seq.current++, ts: nowStamp(), ...m }]);

  const selectWorker = (w: Worker) => {
    setSelectedId(w.id);
    setMode('single');
    setMentionOpen(false);
    inputRef.current?.focus();
  };

  const simulateDone = (who: string, t = '✅ work done') => {
    window.setTimeout(() => {
      push({ kind: 'done', from: who, text: t });
      setWorkers((prev) => prev.map((w) => (w.id === who ? { ...w, status: 'done' } : w)));
    }, 2600);
  };

  const send = () => {
    const body = text.trim();
    if (!body) return;
    if (mode === 'broadcast') {
      push({ kind: 'broadcast', text: body });
      setText('');
      return;
    }
    if (!target) return;
    push({ kind: 'dispatch', to: target.id, text: body });
    setWorkers((prev) => prev.map((w) => (w.id === target.id ? { ...w, status: 'working', shown: 0 } : w)));
    setText('');
    simulateDone(target.id);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };
  const onChange = (v: string) => {
    setText(v);
    if (mode === 'single') setMentionOpen(/(^|\s)@$/.test(v));
  };
  const pickMention = (w: Worker) => {
    setSelectedId(w.id); setMode('single');
    setText((v) => v.replace(/@$/, ''));
    setMentionOpen(false);
    inputRef.current?.focus();
  };

  const canSend = text.trim() && (mode === 'broadcast' || !!target);

  return (
    <div data-id="office" className="absolute inset-0 flex bg-[#0A0A0A] text-zinc-300">
      {/* 左栏：命令对话 */}
      <aside data-id="office-command" className="flex w-[336px] min-w-[336px] shrink-0 flex-col border-r border-[var(--vsc-border)] bg-[#0c0c0c]">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-[var(--vsc-border)] px-4">
          <Building2 className="h-4 w-4 text-sky-400" />
          <span className="text-sm font-semibold text-zinc-100">办公室</span>
          <span className="text-[11px] text-zinc-500">· 总控 {SELF}</span>
        </div>

        {/* history */}
        <div ref={chatRef} data-id="office-command-history" className="flex-1 space-y-2.5 overflow-auto px-3 py-3">
          {chat.map((m) => <CommandMsg key={m.id} m={m} byId={byId} />)}
        </div>

        {/* prompt */}
        <div data-id="office-command-prompt" className="shrink-0 border-t border-[var(--vsc-border)] bg-[#0d0d0d] px-3 py-3">
          <div className="relative">
            <div data-id="office-mode" className="mb-2 inline-flex items-center gap-1 rounded-lg bg-white/[0.04] p-0.5 text-[12px]">
              <button data-id="office-mode-single" onClick={() => setMode('single')}
                className={`inline-flex items-center gap-1 rounded-md px-2 py-1 ${mode === 'single' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}>
                <MessageSquare className="h-3.5 w-3.5" /> 单聊
              </button>
              <button data-id="office-mode-broadcast" onClick={() => { setMode('broadcast'); setMentionOpen(false); }}
                className={`inline-flex items-center gap-1 rounded-md px-2 py-1 ${mode === 'broadcast' ? 'bg-amber-500/20 text-amber-200' : 'text-zinc-500 hover:text-zinc-300'}`}>
                <Megaphone className="h-3.5 w-3.5" /> 广播
              </button>
            </div>

            {mentionOpen && mode === 'single' && (
              <div data-id="office-mention" className="absolute bottom-full left-0 mb-2 w-full overflow-hidden rounded-xl border border-white/10 bg-[#141414] shadow-2xl">
                {workers.map((w) => (
                  <button key={w.id} data-id={`office-mention-${w.id}`} onClick={() => pickMention(w)}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white/[0.06]">
                    <Avatar emoji={w.emoji} accent={w.accent} size={22} />
                    <span className="text-[13px] text-zinc-200">{w.name}</span>
                    <span className="ml-auto font-mono text-[11px] text-zinc-500">{w.id}</span>
                  </button>
                ))}
              </div>
            )}

            <div data-id="office-target" className="mb-2 flex items-center gap-1.5">
              {mode === 'broadcast' ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-2.5 py-1 text-[12px] text-amber-200">
                  <Megaphone className="h-3 w-3" /> 广播 · 全体（{workers.length}）
                </span>
              ) : target ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-sky-500/15 py-1 pl-1.5 pr-1.5 text-[12px] text-sky-300">
                  <Avatar emoji={target.emoji} accent={target.accent} size={18} />
                  <span className="font-mono">{target.id}</span>
                  <button data-id="office-target-clear" onClick={() => setSelectedId(null)} className="rounded-full p-0.5 hover:bg-white/10"><X className="h-3 w-3" /></button>
                </span>
              ) : (
                <span className="text-[12px] text-zinc-600">点右侧 worker，或 @ 选择</span>
              )}
            </div>

            <div className="flex items-end gap-2 rounded-xl border border-white/10 bg-[#111] px-3 py-2 focus-within:border-white/20">
              <textarea ref={inputRef} data-id="office-input" rows={1} value={text}
                onChange={(e) => onChange(e.target.value)} onKeyDown={onKeyDown}
                placeholder={mode === 'broadcast' ? '向全体广播…（Enter）' : target ? `给 ${target.name} 派任务…（Enter）` : '@ 选择 worker…'}
                className="max-h-40 min-h-[24px] flex-1 resize-none bg-transparent text-[13px] leading-6 text-zinc-200 outline-none placeholder:text-zinc-600" />
              <button data-id="office-send" onClick={send} disabled={!canSend}
                className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg text-white transition-colors disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-zinc-600 ${mode === 'broadcast' ? 'bg-amber-500/90 hover:bg-amber-400' : 'bg-sky-500/90 hover:bg-sky-400'}`}>
                {mode === 'broadcast' ? <Megaphone className="h-4 w-4" /> : <Send className="h-4 w-4" />}
              </button>
            </div>
          </div>
        </div>
      </aside>

      {/* 右侧：worker chat window 网格 */}
      <main data-id="office-windows" className="min-w-0 flex-1 overflow-auto bg-[#080808] p-4">
        <div className="mb-3 flex items-center gap-2 text-[11px] text-zinc-500">
          <span className="rounded-md bg-white/[0.04] px-2 py-1">{workers.length} 个 worker</span>
          <span className="rounded-md bg-white/[0.03] px-2 py-1 text-zinc-600">只显示 thinking + text · tool 结果不拉</span>
        </div>
        <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))' }}>
          {workers.map((w) => (
            <WorkerWindow key={w.id} w={w} selected={selectedId === w.id} onSelect={() => selectWorker(w)} />
          ))}
        </div>
      </main>
    </div>
  );
}

function CommandMsg({ m, byId }: { m: ChatMsg; byId: Record<string, Worker> }) {
  if (m.kind === 'note') {
    return <div data-id={`office-msg-${m.id}`} className="text-center text-[11.5px] leading-relaxed text-zinc-600">{m.text}</div>;
  }
  if (m.kind === 'broadcast') {
    return (
      <div data-id={`office-msg-${m.id}`} className="rounded-lg border border-amber-500/20 bg-amber-500/[0.06] px-2.5 py-1.5 text-[12.5px] text-amber-50/90">
        <span className="mr-1 text-[11px] text-amber-300/80">📢 广播 · 全体</span>
        <div className="whitespace-pre-wrap">{m.text}</div>
      </div>
    );
  }
  if (m.kind === 'dispatch') {
    const w = m.to ? byId[m.to] : null;
    return (
      <div data-id={`office-msg-${m.id}`} className="flex flex-col items-end gap-0.5">
        <div className="flex items-center gap-1 text-[11px] text-zinc-500">
          <AtSign className="h-3 w-3" /><span className="font-mono">{m.to}</span>{w && <span>{w.name}</span>}
        </div>
        <div className="max-w-[88%] rounded-lg rounded-tr-sm bg-sky-500/15 px-2.5 py-1.5 text-[12.5px] text-sky-100 whitespace-pre-wrap">{m.text}</div>
      </div>
    );
  }
  // done
  const w = m.from ? byId[m.from] : null;
  return (
    <div data-id={`office-msg-${m.id}`} className="flex items-center gap-1.5">
      {w && <Avatar emoji={w.emoji} accent={w.accent} size={20} />}
      <span className="inline-flex items-center gap-1 rounded-lg bg-emerald-500/10 px-2 py-1 text-[12px] text-emerald-300">
        <CheckCircle2 className="h-3.5 w-3.5" /> {m.text}
      </span>
      <span className="ml-auto text-[10px] text-zinc-700">{m.ts}</span>
    </div>
  );
}

function WorkerWindow({ w, selected, onSelect }: { w: Worker; selected: boolean; onSelect: () => void }) {
  const acc = ACCENT[w.accent] ?? ACCENT.sky;
  const st = STATUS_META[w.status];
  const lines = w.script.slice(0, w.shown).slice(-10);
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => { const n = bodyRef.current; if (n) n.scrollTop = n.scrollHeight; }, [w.shown]);

  return (
    <button
      data-id={`office-window-${w.id}`}
      onClick={onSelect}
      className={`flex h-[260px] flex-col overflow-hidden rounded-xl border bg-[#0e0e0e] text-left transition-colors ${selected ? `ring-2 ${acc.ring} border-transparent` : 'border-white/[0.07] hover:border-white/15'}`}
    >
      <div data-id={`office-window-header-${w.id}`} className="flex shrink-0 items-center gap-2 border-b border-white/[0.06] bg-white/[0.02] px-3 py-2">
        <Avatar emoji={w.emoji} accent={w.accent} size={30} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-medium text-zinc-200">{w.name}</span>
          <span className="font-mono text-[10.5px] text-zinc-500">{w.id} · {w.role}</span>
        </span>
        <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] ${acc.chip}`}>
          {w.status === 'working' ? <Loader2 className="h-3 w-3 animate-spin" /> :
           w.status === 'done' ? <CheckCircle2 className="h-3 w-3" /> :
           <CircleDot className={`h-3 w-3 ${acc.dot}`} />}
          {st.label}
        </span>
      </div>

      <div ref={bodyRef} data-id={`office-window-body-${w.id}`} className="flex-1 space-y-1.5 overflow-auto px-3 py-2">
        {lines.length === 0 ? (
          <div className="text-[11.5px] text-zinc-600">待派活…</div>
        ) : lines.map((ln, i) => (
          ln.t === 'thinking'
            ? <div key={i} className="border-l-2 border-amber-300/25 pl-2 text-[11.5px] leading-relaxed text-amber-50/55">{ln.s}</div>
            : <div key={i} className="text-[12px] leading-relaxed text-zinc-300">{ln.s}</div>
        ))}
      </div>

      {w.status === 'done' && (
        <div className="shrink-0 border-t border-emerald-500/15 bg-emerald-500/[0.06] px-3 py-1 text-[10.5px] text-emerald-300/90">✅ work done · 等总控验收</div>
      )}
    </button>
  );
}
