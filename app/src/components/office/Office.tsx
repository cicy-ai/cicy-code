import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Building2, Send, Megaphone, AtSign, X, Loader2, CheckCircle2, MessageSquare,
  Plus, Minus, Maximize2, Crown, Inbox, UserPlus, ChevronRight, Power, Users, Store, Copy, Check,
} from 'lucide-react';
import TemplateMarket, { MarketTmpl, TeamTmpl } from './TemplateMarket';

/*
 * Office — 「办公室」（data-id="office"）。
 * 左栏：指挥台（总控对话，上=history，下=prompt，自动 @ 选中 worker，可广播）。
 * 右侧：可平移/缩放画布，每个 worker = 可拖动+可缩放的 chat window，
 *       只显示 thinking + text（不拉 tool 结果），头像 = agent avatar + 状态环。
 * 纯 UI 原型，mock 实时流（先不接接口）。
 */

type LineType = 'thinking' | 'text';
interface Line { t: LineType; s: string }
type Status = 'idle' | 'working' | 'done';

interface Worker {
  id: string; name: string; role: string; emoji: string; accent: string;
  model: string; ctx: number; ctxK: number;   // 模型 + 上下文用量(%) + 上下文窗口(k)
  status: Status; script: Line[]; shown: number; startedAt: number;
  x: number; y: number; w: number; h: number;
}

type ChatKind = 'dispatch' | 'broadcast' | 'done' | 'note';
interface ChatMsg { id: number; kind: ChatKind; from?: string; to?: string; text: string; ts: string }

const SELF = 'w-10001';
const MIN_W = 240, MIN_H = 168;

const W = (id: string, name: string, role: string, emoji: string, accent: string, model: string, ctx: number, ctxK: number, status: Status, x: number, y: number, script: Line[]): Worker =>
  ({ id, name, role, emoji, accent, model, ctx, ctxK, status, script, shown: status === 'working' ? 0 : script.length, startedAt: 0, x, y, w: 360, h: 248 });

const INIT_WORKERS: Worker[] = [
  W('w-10010', '架构师 Aria', 'dev-senior', '🏛️', 'sky', 'deepseek-v4-pro', 42, 256, 'working', 36, 32, [
    { t: 'thinking', s: '把"画布"拆成 3 张卡：数据层 / 渲染层 / 画布层。' },
    { t: 'text', s: '定义 LiteAgentCard props + digest 端点契约。' },
    { t: 'thinking', s: 'tool_result 不传，payload 小一个数量级。' },
    { t: 'text', s: '接口写进 docs，交给 Finn。' },
    { t: 'text', s: '✅ 完成：技术任务卡 + 接口契约。' },
  ]),
  W('w-10011', '前端 Finn', 'dev-junior', '🎨', 'violet', 'deepseek-v4-pro', 61, 256, 'working', 384, 32, [
    { t: 'thinking', s: '复用 normalize，搭可拖拽/缩放的 window。' },
    { t: 'text', s: '左栏指挥台 + 右栏画布。' },
    { t: 'thinking', s: '选中 window → prompt 自动 @ 它。' },
    { t: 'text', s: '加 drag handle + resize 抓手。' },
    { t: 'text', s: '✅ 完成：可拖拽缩放的办公室画布。' },
  ]),
  W('w-10012', '测试 Quinn', 'qa', '🧪', 'emerald', 'gpt-5.5', 38, 400, 'working', 732, 32, [
    { t: 'thinking', s: '核对验收标准：N window 同屏不卡。' },
    { t: 'text', s: '跑 20 window 压力，盯帧率。' },
    { t: 'thinking', s: 'thinking 太长要截断。' },
    { t: 'text', s: 'FAIL：离屏 window 仍在轮询，需门控。' },
  ]),
  W('w-10013', '运维 Ops', 'ops', '🚀', 'amber', 'deepseek-v4-pro', 8, 256, 'idle', 210, 296, [
    { t: 'text', s: '待构建产物，准备部署。' },
  ]),
  W('w-10014', '安全 Sage', 'reviewer', '🛡️', 'rose', 'claude-haiku-4-5', 84, 200, 'working', 558, 296, [
    { t: 'thinking', s: '扫一遍有没有把 token 渲进 window。' },
    { t: 'text', s: 'text+thinking 不含工具入参，攻击面更小。' },
    { t: 'text', s: '✅ 完成：安全结论 PASS。' },
  ]),
];

const ACCENT: Record<string, { grad: string; ring: string; chip: string; bar: string; ping: string }> = {
  sky:     { grad: 'from-sky-500/40 to-sky-700/15',         ring: 'ring-sky-400/50',     chip: 'text-sky-300',     bar: 'bg-sky-400/50',     ping: 'ring-sky-400/60' },
  violet:  { grad: 'from-violet-500/40 to-violet-700/15',   ring: 'ring-violet-400/50',  chip: 'text-violet-300',  bar: 'bg-violet-400/50',  ping: 'ring-violet-400/60' },
  emerald: { grad: 'from-emerald-500/40 to-emerald-700/15', ring: 'ring-emerald-400/50', chip: 'text-emerald-300', bar: 'bg-emerald-400/50', ping: 'ring-emerald-400/60' },
  amber:   { grad: 'from-amber-500/40 to-amber-700/15',     ring: 'ring-amber-400/50',   chip: 'text-amber-300',   bar: 'bg-amber-400/50',   ping: 'ring-amber-400/60' },
  rose:    { grad: 'from-rose-500/40 to-rose-700/15',       ring: 'ring-rose-400/50',    chip: 'text-rose-300',    bar: 'bg-rose-400/50',    ping: 'ring-rose-400/60' },
};
const Z_MIN = 0.4, Z_MAX = 1.8;
const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
const hhmm = (ms: number) => { const d = new Date(ms); const p = (n: number) => String(n).padStart(2, '0'); return `${p(d.getHours())}:${p(d.getMinutes())}`; };
const elapsed = (from: number, now: number) => {
  if (!from) return '';
  const s = Math.max(0, Math.floor((now - from) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${p2(s % 60)}s`;
};
const p2 = (n: number) => String(n).padStart(2, '0');

/* ── avatar：emoji + 状态环（working 脉冲 / done 绿环 / idle 灰）── */
function Avatar({ emoji, accent, size = 30, status }: { emoji: string; accent: string; size?: number; status?: Status }) {
  const acc = ACCENT[accent] ?? ACCENT.sky;
  const ring = status === 'done' ? 'ring-2 ring-emerald-400/70' : status === 'working' ? `ring-2 ${acc.ring}` : 'ring-1 ring-white/10';
  return (
    <span className="relative inline-grid shrink-0 place-items-center" style={{ width: size, height: size }}>
      {status === 'working' && <span className={`absolute inset-0 rounded-full ring-2 ${acc.ping} animate-ping opacity-40`} />}
      <span className={`grid h-full w-full place-items-center rounded-full bg-gradient-to-br ${acc.grad} ${ring}`} style={{ fontSize: size * 0.5 }}>{emoji}</span>
    </span>
  );
}

/* ── 候选成员（存在但未加入/未开启 = 离线）── */
interface Cand { id: string; name: string; role: string; emoji: string; accent: string; model: string; script: Line[] }

const GENERIC_SCRIPT: Line[] = [
  { t: 'thinking', s: '读取任务卡与验收标准…' },
  { t: 'text', s: '开始执行。' },
  { t: 'thinking', s: '检查边界条件与依赖。' },
  { t: 'text', s: '✅ 完成，等待验收。' },
];

const INIT_CANDIDATES: Cand[] = [
  { id: 'w-10015', name: '文案 Wendy', role: 'writer', emoji: '✍️', accent: 'violet', model: 'deepseek-v4-pro', script: GENERIC_SCRIPT },
  { id: 'w-10016', name: '数据 Dана', role: 'analyst', emoji: '📊', accent: 'sky', model: 'gpt-5.5', script: GENERIC_SCRIPT },
  { id: 'w-10017', name: '设计 Deo', role: 'designer', emoji: '🎭', accent: 'rose', model: 'deepseek-v4-pro', script: GENERIC_SCRIPT },
];

function makeWorker(id: string, name: string, role: string, emoji: string, accent: string, model: string, slot: number, script: Line[]): Worker {
  return { id, name, role, emoji, accent, model, ctx: 5, ctxK: 256, status: 'idle', script, shown: 0, startedAt: 0, w: 360, h: 248, x: 36 + (slot % 3) * 372, y: 32 + Math.floor(slot / 3) * 280 };
}

export default function Office() {
  const [workers, setWorkers] = useState<Worker[]>(() => {
    const t0 = Date.now();
    return INIT_WORKERS.map((w) => (w.status === 'working' ? { ...w, startedAt: t0 } : w));
  });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [mode, setMode] = useState<'single' | 'broadcast'>('single');
  const [text, setText] = useState('');
  const [mentionOpen, setMentionOpen] = useState(false);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [zoom, setZoom] = useState(1);
  const [now, setNow] = useState(() => Date.now());
  const [chat, setChat] = useState<ChatMsg[]>([
    { id: 1, kind: 'note', text: '拖标题移动卡片、拖右下角缩放、空白拖拽平移、滚轮缩放。点 worker 自动 @ 派任务，或切广播。', ts: hhmm(Date.now()) },
  ]);
  const [candidates, setCandidates] = useState<Cand[]>(INIT_CANDIDATES);
  const [rosterOpen, setRosterOpen] = useState(true);
  const [market, setMarket] = useState<'team' | 'agent' | null>(null);
  const idSeq = useRef(20);
  const seq = useRef(2);
  const chatRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const dragRef = useRef<null | { kind: 'pan' | 'move' | 'resize'; id?: string; sx: number; sy: number; ox: number; oy: number; ow: number; oh: number; moved: boolean }>(null);

  const byId = useMemo(() => Object.fromEntries(workers.map((w) => [w.id, w])), [workers]);
  const target = selectedId ? byId[selectedId] : null;
  const online = workers.filter((w) => w.status !== 'idle').length;

  // mock 实时流 + 计时
  useEffect(() => {
    const t = window.setInterval(() => {
      const ts = Date.now();
      setWorkers((prev) => prev.map((w) => {
        if (w.status === 'working') {
          const ctx = Math.min(99, w.ctx + 1 + Math.floor(Math.random() * 3));   // 上下文随干活增长
          return w.shown < w.script.length ? { ...w, shown: w.shown + 1, ctx } : { ...w, status: 'done', ctx };
        }
        if (w.status === 'done' && Math.random() < 0.08) return { ...w, status: 'working', shown: 0, startedAt: ts };
        return w;
      }));
    }, 1200);
    const c = window.setInterval(() => setNow(Date.now()), 1000);
    return () => { window.clearInterval(t); window.clearInterval(c); };
  }, []);

  useEffect(() => { const n = chatRef.current; if (n) n.scrollTop = n.scrollHeight; }, [chat]);

  const push = (m: Omit<ChatMsg, 'id' | 'ts'>) => setChat((c) => [...c, { id: seq.current++, ts: hhmm(Date.now()), ...m }]);

  const selectWorker = (w?: Worker) => { if (!w) return; setSelectedId(w.id); setMode('single'); setMentionOpen(false); inputRef.current?.focus(); };

  const onPointerDownBg = (e: React.PointerEvent) => { dragRef.current = { kind: 'pan', sx: e.clientX, sy: e.clientY, ox: pan.x, oy: pan.y, ow: 0, oh: 0, moved: false }; };
  const startMove = (e: React.PointerEvent, w: Worker) => { e.stopPropagation(); dragRef.current = { kind: 'move', id: w.id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false }; };
  const startResize = (e: React.PointerEvent, w: Worker) => { e.stopPropagation(); dragRef.current = { kind: 'resize', id: w.id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false }; };
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = dragRef.current; if (!d) return;
      const dx = e.clientX - d.sx, dy = e.clientY - d.sy;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) d.moved = true;
      if (d.kind === 'pan') setPan({ x: d.ox + dx, y: d.oy + dy });
      else if (d.kind === 'move') setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, x: d.ox + dx / zoom, y: d.oy + dy / zoom } : w));
      else setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, w: Math.max(MIN_W, d.ow + dx / zoom), h: Math.max(MIN_H, d.oh + dy / zoom) } : w));
    };
    const up = () => {
      const d = dragRef.current;
      if (d && !d.moved) { if (d.kind === 'move' && d.id) selectWorker(byId[d.id]); else if (d.kind === 'pan') setSelectedId(null); }
      dragRef.current = null;
    };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
  }, [zoom, byId]);

  const onWheel = (e: React.WheelEvent) => {
    const next = clamp(zoom * (e.deltaY < 0 ? 1.1 : 1 / 1.1), Z_MIN, Z_MAX);
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
    setPan((p) => ({ x: cx - (cx - p.x) * (next / zoom), y: cy - (cy - p.y) * (next / zoom) }));
    setZoom(next);
  };

  const simulateDone = (who: string, t = '✅ work done') => {
    window.setTimeout(() => {
      push({ kind: 'done', from: who, text: t });
      setWorkers((prev) => prev.map((w) => (w.id === who ? { ...w, status: 'done' } : w)));
    }, 2600);
  };
  const send = () => {
    const body = text.trim(); if (!body) return;
    if (mode === 'broadcast') { push({ kind: 'broadcast', text: body }); setText(''); return; }
    if (!target) return;
    push({ kind: 'dispatch', to: target.id, text: body });
    setWorkers((prev) => prev.map((w) => (w.id === target.id ? { ...w, status: 'working', shown: 0, startedAt: Date.now() } : w)));
    setText(''); simulateDone(target.id);
  };
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } };
  const onChange = (v: string) => { setText(v); if (mode === 'single') setMentionOpen(/(^|\s)@$/.test(v)); };
  const pickMention = (w: Worker) => { setSelectedId(w.id); setMode('single'); setText((v) => v.replace(/@$/, '')); setMentionOpen(false); inputRef.current?.focus(); };
  const joinCandidate = (c: Cand) => {
    setCandidates((prev) => prev.filter((x) => x.id !== c.id));
    setWorkers((prev) => [...prev, makeWorker(c.id, c.name, c.role, c.emoji, c.accent, c.model, prev.length, c.script)]);
    push({ kind: 'note', text: `已加入并启用 ${c.name}（${c.id}）` });
  };
  const spawn = (name: string, role: string, emoji: string, accent: string, model: string, note: string) => {
    const id = `w-100${idSeq.current++}`;
    setWorkers((prev) => [...prev, makeWorker(id, name, role, emoji, accent, model, prev.length, GENERIC_SCRIPT)]);
    push({ kind: 'note', text: `${note} ${id}` });
  };
  const pickFromMarket = (t: MarketTmpl) => spawn(t.name, t.role, t.emoji, t.accent, t.model, `已从模版市场添加「${t.name}」`);
  const pickTeam = (team: TeamTmpl) => {
    const base = idSeq.current; idSeq.current += team.members.length;
    setWorkers((prev) => [...prev, ...team.members.map((m, i) => makeWorker(`w-100${base + i}`, m.name, m.role, m.emoji, m.accent, m.model, prev.length + i, GENERIC_SCRIPT))]);
    push({ kind: 'note', text: `已组建「${team.name}」（${team.members.length} 名成员）` });
  };
  const canSend = text.trim() && (mode === 'broadcast' || !!target);

  return (
    <div data-id="office" className="absolute inset-0 flex bg-[#0A0A0A] text-zinc-300">
      {/* 左栏：指挥台 */}
      <aside data-id="office-command" className="flex w-[340px] min-w-[340px] shrink-0 flex-col border-r border-white/[0.06] bg-[#0b0b0c]">
        <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-white/[0.06] px-4">
          <span className="relative">
            <span className="grid h-8 w-8 place-items-center rounded-full bg-gradient-to-br from-amber-400/40 to-amber-600/15 text-base ring-1 ring-amber-300/30">🏢</span>
            <span className="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full bg-emerald-400 ring-2 ring-[#0b0b0c]" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1 text-[13px] font-semibold text-zinc-100"><Crown className="h-3 w-3 text-amber-400" /> 办公室 · 总控</div>
            <div className="font-mono text-[11px] text-zinc-500">{SELF} · 在线 {online}/{workers.length}</div>
          </div>
        </div>

        <div ref={chatRef} data-id="office-command-history" className="flex-1 space-y-3 overflow-auto px-3.5 py-3.5">
          {chat.map((m) => <CommandMsg key={m.id} m={m} byId={byId} />)}
        </div>

        <div data-id="office-command-prompt" className="shrink-0 border-t border-white/[0.06] bg-[#0d0d0e] px-3.5 py-3">
          <div className="relative">
            <div data-id="office-mode" className="mb-2 inline-flex items-center gap-0.5 rounded-lg bg-white/[0.04] p-0.5 text-[12px]">
              <button data-id="office-mode-single" onClick={() => setMode('single')} className={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 transition-colors ${mode === 'single' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}><MessageSquare className="h-3.5 w-3.5" /> 单聊</button>
              <button data-id="office-mode-broadcast" onClick={() => { setMode('broadcast'); setMentionOpen(false); }} className={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 transition-colors ${mode === 'broadcast' ? 'bg-amber-500/20 text-amber-200' : 'text-zinc-500 hover:text-zinc-300'}`}><Megaphone className="h-3.5 w-3.5" /> 广播</button>
            </div>
            {mentionOpen && mode === 'single' && (
              <div data-id="office-mention" className="absolute bottom-full left-0 mb-2 w-full overflow-hidden rounded-xl border border-white/10 bg-[#16161a] shadow-2xl">
                {workers.map((w) => (
                  <button key={w.id} data-id={`office-mention-${w.id}`} onClick={() => pickMention(w)} className="flex w-full items-center gap-2.5 px-3 py-2 text-left hover:bg-white/[0.06]">
                    <Avatar emoji={w.emoji} accent={w.accent} size={24} status={w.status} /><span className="text-[13px] text-zinc-200">{w.name}</span><span className="ml-auto font-mono text-[11px] text-zinc-500">{w.id}</span>
                  </button>
                ))}
              </div>
            )}
            <div data-id="office-target" className="mb-2 flex min-h-[26px] items-center gap-1.5">
              {mode === 'broadcast' ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-2.5 py-1 text-[12px] text-amber-200"><Megaphone className="h-3 w-3" /> 广播 · 全体（{workers.length}）</span>
              ) : target ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-white/[0.06] py-1 pl-1 pr-2 text-[12px] text-zinc-200"><Avatar emoji={target.emoji} accent={target.accent} size={20} status={target.status} /><span className="font-medium">{target.name}</span><span className="font-mono text-[11px] text-zinc-500">{target.id}</span><button data-id="office-target-clear" onClick={() => setSelectedId(null)} className="rounded-full p-0.5 text-zinc-500 hover:bg-white/10 hover:text-zinc-200"><X className="h-3 w-3" /></button></span>
              ) : (<span className="text-[12px] text-zinc-600">点画布里的 worker，或输入 @ 选择</span>)}
            </div>
            <div className="flex items-end gap-2 rounded-2xl border border-white/10 bg-[#121214] px-3 py-2.5 transition-colors focus-within:border-white/25">
              <textarea ref={inputRef} data-id="office-input" rows={1} value={text} onChange={(e) => onChange(e.target.value)} onKeyDown={onKeyDown}
                placeholder={mode === 'broadcast' ? '向全体广播…（Enter）' : target ? `给 ${target.name} 派任务…（Enter 发送）` : '输入 @ 选择 worker…'}
                className="max-h-40 min-h-[24px] flex-1 resize-none bg-transparent text-[13px] leading-6 text-zinc-200 outline-none placeholder:text-zinc-600" />
              <button data-id="office-send" onClick={send} disabled={!canSend} className={`grid h-8 w-8 shrink-0 place-items-center rounded-xl text-white transition-all disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-zinc-600 ${mode === 'broadcast' ? 'bg-amber-500 hover:bg-amber-400' : 'bg-sky-500 hover:bg-sky-400'} ${canSend ? 'shadow-lg' : ''}`}>{mode === 'broadcast' ? <Megaphone className="h-4 w-4" /> : <Send className="h-4 w-4" />}</button>
            </div>
          </div>
        </div>
      </aside>

      {/* 右侧：画布 */}
      <main data-id="office-canvas" className="relative min-w-0 flex-1 overflow-hidden bg-[#060608]"
        style={{ backgroundImage: 'radial-gradient(circle, rgba(255,255,255,0.045) 1px, transparent 1px)', backgroundSize: `${26 * zoom}px ${26 * zoom}px`, backgroundPosition: `${pan.x}px ${pan.y}px` }}
        onPointerDown={onPointerDownBg} onWheel={onWheel}>
        {/* 暗角增加纵深 */}
        <div className="pointer-events-none absolute inset-0" style={{ boxShadow: 'inset 0 0 160px 40px rgba(0,0,0,0.55)' }} />

        <div data-id="office-canvas-layer" className="absolute left-0 top-0 origin-top-left" style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}>
          {workers.map((w) => (
            <WorkerWindow key={w.id} w={w} now={now} selected={selectedId === w.id} hovered={hoverId === w.id}
              onHover={(h) => setHoverId((cur) => (h ? w.id : cur === w.id ? null : cur))}
              onMoveStart={(e) => startMove(e, w)} onResizeStart={(e) => startResize(e, w)} />
          ))}
        </div>

        <div data-id="office-canvas-topbar" className="pointer-events-none absolute left-4 top-4 flex items-center gap-2 text-[11px]">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-black/40 px-2.5 py-1 text-zinc-300 backdrop-blur"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> 团队工作台 · 在线 {online}/{workers.length}</span>
          <span className="rounded-full border border-white/[0.06] bg-black/30 px-2.5 py-1 text-zinc-600 backdrop-blur">仅 thinking + text · 不拉 tool 结果</span>
        </div>

        <div data-id="office-canvas-controls" className="absolute bottom-4 right-4 flex flex-col gap-1.5">
          <button data-id="office-zoom-in" onClick={() => setZoom((z) => clamp(z * 1.15, Z_MIN, Z_MAX))} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Plus className="h-4 w-4" /></button>
          <button data-id="office-zoom-out" onClick={() => setZoom((z) => clamp(z / 1.15, Z_MIN, Z_MAX))} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Minus className="h-4 w-4" /></button>
          <button data-id="office-zoom-reset" onClick={() => { setPan({ x: 0, y: 0 }); setZoom(1); }} title="复位" className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Maximize2 className="h-4 w-4" /></button>
          <div className="text-center text-[10px] text-zinc-600">{Math.round(zoom * 100)}%</div>
        </div>
      </main>

      {/* 右栏：成员库（候选 + 模版） */}
      {rosterOpen ? (
        <aside data-id="office-roster" className="flex w-[268px] min-w-[268px] shrink-0 flex-col border-l border-white/[0.06] bg-[#0b0b0c]">
          <div className="flex h-14 shrink-0 items-center gap-2 border-b border-white/[0.06] px-4">
            <Users className="h-4 w-4 text-zinc-400" />
            <span className="text-[13px] font-semibold text-zinc-100">成员库</span>
            <button data-id="office-roster-close" onClick={() => setRosterOpen(false)} className="ml-auto rounded p-1 text-zinc-500 hover:bg-white/10 hover:text-zinc-200"><ChevronRight className="h-4 w-4" /></button>
          </div>
          <div className="flex-1 space-y-5 overflow-auto px-3 py-3.5">
            <section data-id="office-roster-candidates">
              <div className="mb-2 flex items-center gap-1.5 px-1 text-[11px] font-medium uppercase tracking-wide text-zinc-600"><Power className="h-3 w-3" /> 候选 · 未开启<span className="ml-auto normal-case text-zinc-700">{candidates.length}</span></div>
              {candidates.length === 0 ? <div className="px-1 text-[11.5px] text-zinc-700">全部已加入</div> : (
                <div className="space-y-1.5">
                  {candidates.map((c) => (
                    <div key={c.id} data-id={`office-cand-${c.id}`} className="flex items-center gap-2.5 rounded-xl border border-white/[0.06] bg-white/[0.02] px-2.5 py-2">
                      <span className="opacity-50 grayscale"><Avatar emoji={c.emoji} accent={c.accent} size={28} /></span>
                      <span className="min-w-0 flex-1"><span className="block truncate text-[12.5px] text-zinc-300">{c.name}</span><span className="font-mono text-[10.5px] text-zinc-600">{c.id} · 离线</span></span>
                      <button data-id={`office-cand-join-${c.id}`} onClick={() => joinCandidate(c)} className="inline-flex items-center gap-1 rounded-lg bg-white/[0.06] px-2 py-1 text-[11.5px] text-zinc-200 transition-colors hover:bg-white/[0.12]"><UserPlus className="h-3.5 w-3.5" /> 加入</button>
                    </div>
                  ))}
                </div>
              )}
            </section>
            <div className="space-y-2">
              <button data-id="office-open-team-market" onClick={() => setMarket('team')}
                className="flex w-full items-center gap-3 rounded-xl border border-sky-400/20 bg-sky-500/10 px-3 py-3 text-left transition-colors hover:border-sky-400/40 hover:bg-sky-500/15">
                <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-sky-500/20 text-sky-300"><Users className="h-5 w-5" /></span>
                <span className="min-w-0 flex-1"><span className="block text-[13px] font-medium text-zinc-100">团队市场</span><span className="block text-[11px] text-zinc-500">一键组建整支班子</span></span>
                <ChevronRight className="h-4 w-4 text-zinc-500" />
              </button>
              <button data-id="office-open-agent-market" onClick={() => setMarket('agent')}
                className="flex w-full items-center gap-3 rounded-xl border border-white/[0.08] bg-white/[0.03] px-3 py-3 text-left transition-colors hover:border-white/15 hover:bg-white/[0.05]">
                <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-zinc-300"><Store className="h-5 w-5" /></span>
                <span className="min-w-0 flex-1"><span className="block text-[13px] font-medium text-zinc-100">Agent 市场</span><span className="block text-[11px] text-zinc-500">各行各业单个 agent 模版</span></span>
                <ChevronRight className="h-4 w-4 text-zinc-500" />
              </button>
            </div>
          </div>
        </aside>
      ) : (
        <button data-id="office-roster-open" onClick={() => setRosterOpen(true)}
          className="absolute right-0 top-1/2 z-30 flex -translate-y-1/2 items-center gap-1.5 rounded-l-lg border border-r-0 border-white/10 bg-[#16161a]/90 px-2 py-3 text-[11px] text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"
          style={{ writingMode: 'vertical-rl' }}><Users className="h-3.5 w-3.5" /> 成员库</button>
      )}

      <TemplateMarket open={market} onClose={() => setMarket(null)} onPick={pickFromMarket} onPickTeam={pickTeam} />
    </div>
  );
}

function CommandMsg({ m, byId }: { m: ChatMsg; byId: Record<string, Worker> }) {
  if (m.kind === 'note') return <div data-id={`office-msg-${m.id}`} className="px-2 text-center text-[11.5px] leading-relaxed text-zinc-600">{m.text}</div>;
  if (m.kind === 'broadcast') return (
    <div data-id={`office-msg-${m.id}`} className="overflow-hidden rounded-xl border border-amber-500/20 bg-amber-500/[0.06]">
      <div className="flex items-center gap-1 border-b border-amber-500/15 px-2.5 py-1 text-[10.5px] text-amber-300/90"><Megaphone className="h-3 w-3" /> 广播 · 全体 <span className="ml-auto font-mono text-amber-300/50">{m.ts}</span></div>
      <div className="px-2.5 py-1.5 text-[12.5px] leading-relaxed text-amber-50/90 whitespace-pre-wrap">{m.text}</div>
    </div>
  );
  if (m.kind === 'dispatch') {
    const w = m.to ? byId[m.to] : null;
    return (
      <div data-id={`office-msg-${m.id}`} className="flex flex-col items-end gap-1">
        <div className="flex items-center gap-1.5 text-[11px] text-zinc-500">派给 {w && <Avatar emoji={w.emoji} accent={w.accent} size={16} />}<span className="text-zinc-400">{w?.name ?? m.to}</span></div>
        <div className="max-w-[86%] rounded-2xl rounded-tr-md bg-sky-500/90 px-3 py-1.5 text-[12.5px] leading-relaxed text-white shadow-sm whitespace-pre-wrap">{m.text}</div>
      </div>
    );
  }
  const w = m.from ? byId[m.from] : null;
  return (
    <div data-id={`office-msg-${m.id}`} className="flex items-center gap-2">
      {w && <Avatar emoji={w.emoji} accent={w.accent} size={22} status="done" />}
      <div className="min-w-0 flex-1">
        <div className="text-[11px] text-zinc-500">{w?.name ?? m.from}</div>
        <div className="inline-flex items-center gap-1 text-[12px] text-emerald-300"><CheckCircle2 className="h-3.5 w-3.5" /> {m.text} <span className="text-zinc-600">· 待验收</span></div>
      </div>
      <span className="self-start font-mono text-[10px] text-zinc-700">{m.ts}</span>
    </div>
  );
}

function WorkerWindow({ w, now, selected, hovered, onHover, onMoveStart, onResizeStart }: {
  w: Worker; now: number; selected: boolean; hovered: boolean;
  onHover: (h: boolean) => void; onMoveStart: (e: React.PointerEvent) => void; onResizeStart: (e: React.PointerEvent) => void;
}) {
  const acc = ACCENT[w.accent] ?? ACCENT.sky;
  const lines = w.script.slice(0, w.shown).slice(-12);
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => { const n = bodyRef.current; if (n) n.scrollTop = n.scrollHeight; }, [w.shown]);
  const done = w.status === 'done';
  const working = w.status === 'working';
  const ctxColor = w.ctx > 85 ? 'bg-rose-400' : w.ctx > 60 ? 'bg-amber-400' : 'bg-zinc-400/60';
  const ctxText = w.ctx > 85 ? 'text-rose-300' : w.ctx > 60 ? 'text-amber-300' : 'text-zinc-500';
  const [copied, setCopied] = useState(false);
  const copyId = (e: React.MouseEvent) => { e.stopPropagation(); try { navigator.clipboard?.writeText(w.id); } catch { /* noop */ } setCopied(true); window.setTimeout(() => setCopied(false), 1200); };

  return (
    <div data-id={`office-window-${w.id}`}
      onPointerEnter={() => onHover(true)} onPointerLeave={() => onHover(false)}
      className={`absolute flex flex-col overflow-hidden rounded-2xl border bg-[#0e0e11] transition-[box-shadow,transform,border-color] duration-150
        ${selected ? `ring-2 ${acc.ring} border-transparent -translate-y-0.5 shadow-2xl` : hovered ? 'border-white/15 shadow-2xl' : 'border-white/[0.07] shadow-xl'}`}
      style={{ left: w.x, top: w.y, width: w.w, height: w.h, zIndex: selected ? 60 : hovered ? 40 : 10 }}>
      {/* 角色色条 */}
      <div className={`h-[3px] w-full ${done ? 'bg-emerald-400/60' : acc.bar}`} />
      <div data-id={`office-window-header-${w.id}`} onPointerDown={onMoveStart}
        className={`flex shrink-0 cursor-grab select-none items-center gap-2.5 px-3 py-2.5 active:cursor-grabbing ${done ? 'bg-emerald-500/[0.05]' : 'bg-white/[0.015]'}`}>
        <Avatar emoji={w.emoji} accent={w.accent} size={32} status={w.status} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-semibold text-zinc-100">{w.name}</span>
          <span className="flex items-center gap-1 font-mono text-[10.5px] text-zinc-500">
            <button data-id={`office-window-copyid-${w.id}`} onPointerDown={(e) => e.stopPropagation()} onClick={copyId}
              title="复制 agent id" className="inline-flex items-center gap-0.5 rounded px-0.5 transition-colors hover:bg-white/10 hover:text-zinc-300">
              {w.id}{copied ? <Check className="h-2.5 w-2.5 text-emerald-400" /> : <Copy className="h-2.5 w-2.5 opacity-50" />}
            </button>
            · {w.role}
          </span>
        </span>
        {done ? (
          <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10.5px] font-medium text-emerald-300"><CheckCircle2 className="h-3 w-3" /> 待验收</span>
        ) : working ? (
          <span className={`inline-flex items-center gap-1 text-[10.5px] ${acc.chip}`}><Loader2 className="h-3 w-3 animate-spin" /> {elapsed(w.startedAt, now)}</span>
        ) : (
          <span className="text-[10.5px] text-zinc-600">空闲</span>
        )}
      </div>
      {/* meta：模型 + 上下文用量 */}
      <div data-id={`office-window-meta-${w.id}`} className="flex items-center gap-2 border-b border-white/[0.05] px-3 py-1.5">
        <span className="truncate rounded bg-white/[0.05] px-1.5 py-0.5 font-mono text-[10px] text-zinc-400" title={`模型 ${w.model}`}>{w.model}</span>
        <div className="ml-auto flex items-center gap-1.5" title={`上下文 ${w.ctx}% · 窗口 ${w.ctxK}k`}>
          <span className="text-[10px] text-zinc-600">ctx</span>
          <div className="h-1 w-14 overflow-hidden rounded-full bg-white/10"><div className={`h-full rounded-full ${ctxColor} transition-all`} style={{ width: `${w.ctx}%` }} /></div>
          <span className={`text-[10px] tabular-nums ${ctxText}`}>{w.ctx}%</span>
        </div>
      </div>
      <div ref={bodyRef} data-id={`office-window-body-${w.id}`} className="flex-1 space-y-2 overflow-auto px-3 py-2.5">
        {lines.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-1 text-zinc-700"><Inbox className="h-5 w-5" /><span className="text-[11px]">等待派活</span></div>
        ) : lines.map((ln, i) => {
          const isLast = i === lines.length - 1;
          return ln.t === 'thinking' ? (
            <div key={i} className="border-l-2 border-amber-300/25 pl-2 text-[11.5px] italic leading-relaxed text-amber-50/50">
              {ln.s}{isLast && working && <span className="ml-0.5 animate-pulse text-amber-200/70">▍</span>}
            </div>
          ) : (
            <div key={i} className="text-[12px] leading-relaxed text-zinc-300">
              {ln.s}{isLast && working && <span className="ml-0.5 animate-pulse text-zinc-400">▍</span>}
            </div>
          );
        })}
      </div>
      {/* resize 抓手：悬停/选中才显露 */}
      <div data-id={`office-window-resize-${w.id}`} onPointerDown={onResizeStart}
        className={`absolute bottom-1 right-1 h-3.5 w-3.5 cursor-nwse-resize rounded-sm transition-opacity ${hovered || selected ? 'opacity-100' : 'opacity-0'}`}
        style={{ background: 'linear-gradient(135deg, transparent 45%, rgba(255,255,255,0.4) 45%, rgba(255,255,255,0.4) 55%, transparent 55%, transparent 70%, rgba(255,255,255,0.4) 70%, rgba(255,255,255,0.4) 80%, transparent 80%)' }} />
    </div>
  );
}
