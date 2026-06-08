import { useEffect, useRef, useState } from 'react';
import { Plus, Minus, Maximize2, CircleDot, Loader2, CheckCircle2, Crown } from 'lucide-react';

/*
 * AgentCanvas — 「办公室」活体看板画布原型（data-id="agent-canvas"）。
 *
 * 纯 UI 原型，mock 实时流（先不接接口）。每个 agent = 一张轻卡（LiteAgentCard），
 * 只显示 thinking + text 工作状态（不渲染 tool_result）；想看谁点谁放大。
 * 画布支持拖拽平移 / 滚轮缩放 / 拖动单卡 / 点击聚焦。w-1001 = 总控中心节点。
 *
 * 接接口时：每张卡轮询该 agent 的 current-reply（只取 type∈{thinking,text}），
 * 离屏/未聚焦降频走批量 digest（见 plan）。这里用 setInterval 模拟流。
 */

type LineType = 'thinking' | 'text';
interface Line { t: LineType; s: string }
type Status = 'idle' | 'working' | 'done';

interface CanvasAgent {
  id: string;
  name: string;
  role: string;
  emoji: string;
  accent: string;          // tailwind 颜色名片段，如 'sky'
  x: number;
  y: number;
  status: Status;
  script: Line[];
  shown: number;           // 已揭示的行数（mock 流）
  controller?: boolean;
}

const A = (id: string, name: string, role: string, emoji: string, accent: string, x: number, y: number, status: Status, script: Line[], controller = false): CanvasAgent =>
  ({ id, name, role, emoji, accent, x, y, status, script, shown: status === 'working' ? 0 : script.length, controller });

const INIT: CanvasAgent[] = [
  A('w-1001', '总控 Opus', '总负责人', '🏢', 'amber', 380, 40, 'idle', [
    { t: 'text', s: '当前 3 个任务在跑，等 work done 回报。' },
    { t: 'thinking', s: '前端原型先行，QA 待命，安全并行审。' },
    { t: 'text', s: '派给 Finn：画布原型；派给 Aria：拆任务卡。' },
  ], true),
  A('w-10010', '架构师 Aria', 'dev-senior', '🏛️', 'sky', 60, 240, 'working', [
    { t: 'thinking', s: '把"画布"需求拆成 3 张卡：数据层 / 渲染层 / 画布层。' },
    { t: 'text', s: '定义 LiteAgentCard props + digest 端点契约。' },
    { t: 'thinking', s: 'tool_result 不传，payload 能小一个数量级。' },
    { t: 'text', s: '接口写进 docs，交给 Finn 实现。' },
    { t: 'text', s: '✅ 完成：技术任务卡 + 接口契约。' },
  ]),
  A('w-10011', '前端 Finn', 'dev-junior', '🎨', 'violet', 380, 360, 'working', [
    { t: 'thinking', s: '先看现有 MeetingRoom 的布局约定，复用 normalize。' },
    { t: 'text', s: '新建 AgentCanvas.tsx，搭 pan/zoom 容器。' },
    { t: 'thinking', s: '卡片用 absolute + transform，避免重排。' },
    { t: 'text', s: 'LiteAgentCard 过滤 type∈{thinking,text}。' },
    { t: 'thinking', s: '离屏卡片要降频轮询 —— 先留 TODO。' },
    { t: 'text', s: '✅ 完成：画布骨架 + 卡片实时渲染。' },
  ]),
  A('w-10012', '测试 Quinn', 'qa', '🧪', 'emerald', 720, 300, 'working', [
    { t: 'thinking', s: '核对验收标准：N 卡同屏不卡 = 命门。' },
    { t: 'text', s: '跑 20 卡压力，盯帧率与内存。' },
    { t: 'thinking', s: 'thinking 太长会撑爆卡片，得截断。' },
    { t: 'text', s: 'FAIL：离屏卡片仍在 700ms 轮询，需门控。' },
  ]),
  A('w-10013', '运维 Ops', 'ops', '🚀', 'orange', 80, 460, 'idle', [
    { t: 'text', s: '待构建产物，准备部署到 :8008。' },
  ]),
  A('w-10014', '安全 Sage', 'reviewer', '🛡️', 'rose', 720, 80, 'working', [
    { t: 'thinking', s: '扫一遍有没有把 token 渲进卡片。' },
    { t: 'text', s: 'text+thinking 不含工具入参，攻击面更小。' },
    { t: 'text', s: '✅ 完成：安全结论 PASS。' },
  ]),
];

const ACCENT: Record<string, { ring: string; chip: string; dot: string }> = {
  amber:   { ring: 'border-amber-400/40',   chip: 'bg-amber-500/15 text-amber-200',   dot: 'text-amber-400' },
  sky:     { ring: 'border-sky-400/30',     chip: 'bg-sky-500/15 text-sky-300',       dot: 'text-sky-400' },
  violet:  { ring: 'border-violet-400/30',  chip: 'bg-violet-500/15 text-violet-300', dot: 'text-violet-400' },
  emerald: { ring: 'border-emerald-400/30', chip: 'bg-emerald-500/15 text-emerald-300', dot: 'text-emerald-400' },
  orange:  { ring: 'border-orange-400/30',  chip: 'bg-orange-500/15 text-orange-300', dot: 'text-orange-400' },
  rose:    { ring: 'border-rose-400/30',    chip: 'bg-rose-500/15 text-rose-300',     dot: 'text-rose-400' },
};

const Z_MIN = 0.4, Z_MAX = 1.8;
const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));

export default function AgentCanvas() {
  const [agents, setAgents] = useState<CanvasAgent[]>(INIT);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [zoom, setZoom] = useState(1);
  const [focusedId, setFocusedId] = useState<string | null>(null);

  const dragRef = useRef<null | {
    kind: 'pan' | 'card'; id?: string;
    startX: number; startY: number; origX: number; origY: number; moved: boolean;
  }>(null);

  // mock 实时流：每 ~1.2s 给 working 的 agent 揭示下一行；揭完转 done；偶尔重新开工保持"活"。
  useEffect(() => {
    const t = window.setInterval(() => {
      setAgents((prev) => prev.map((a) => {
        if (a.controller) return a;
        if (a.status === 'working') {
          if (a.shown < a.script.length) return { ...a, shown: a.shown + 1 };
          return { ...a, status: 'done' };
        }
        if (a.status === 'done' && Math.random() < 0.12) {
          return { ...a, status: 'working', shown: 0 };
        }
        return a;
      }));
    }, 1200);
    return () => window.clearInterval(t);
  }, []);

  // 指针：背景拖=平移，卡头拖=移卡
  const onPointerDownBg = (e: React.PointerEvent) => {
    dragRef.current = { kind: 'pan', startX: e.clientX, startY: e.clientY, origX: pan.x, origY: pan.y, moved: false };
  };
  const onPointerDownCard = (e: React.PointerEvent, a: CanvasAgent) => {
    e.stopPropagation();
    dragRef.current = { kind: 'card', id: a.id, startX: e.clientX, startY: e.clientY, origX: a.x, origY: a.y, moved: false };
  };
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = dragRef.current;
      if (!d) return;
      const dx = e.clientX - d.startX, dy = e.clientY - d.startY;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) d.moved = true;
      if (d.kind === 'pan') setPan({ x: d.origX + dx, y: d.origY + dy });
      else setAgents((prev) => prev.map((a) => a.id === d.id ? { ...a, x: d.origX + dx / zoom, y: d.origY + dy / zoom } : a));
    };
    const up = () => {
      const d = dragRef.current;
      if (d && d.kind === 'card' && !d.moved && d.id) setFocusedId((f) => (f === d.id ? null : d.id!));
      if (d && d.kind === 'pan' && !d.moved) setFocusedId(null);
      dragRef.current = null;
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
  }, [zoom]);

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
    const next = clamp(zoom * factor, Z_MIN, Z_MAX);
    // 以指针为中心缩放
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
    setPan((p) => ({ x: cx - (cx - p.x) * (next / zoom), y: cy - (cy - p.y) * (next / zoom) }));
    setZoom(next);
  };

  const zoomBy = (f: number) => setZoom((z) => clamp(z * f, Z_MIN, Z_MAX));
  const reset = () => { setPan({ x: 0, y: 0 }); setZoom(1); setFocusedId(null); };

  const workingCount = agents.filter((a) => !a.controller && a.status === 'working').length;
  const doneCount = agents.filter((a) => !a.controller && a.status === 'done').length;

  return (
    <div data-id="agent-canvas" className="absolute inset-0 overflow-hidden bg-[#080808] select-none"
      style={{ backgroundImage: 'radial-gradient(circle, rgba(255,255,255,0.04) 1px, transparent 1px)', backgroundSize: `${24 * zoom}px ${24 * zoom}px`, backgroundPosition: `${pan.x}px ${pan.y}px` }}
      onPointerDown={onPointerDownBg}
      onWheel={onWheel}
    >
      {/* 平移/缩放层 */}
      <div data-id="agent-canvas-layer" className="absolute left-0 top-0 origin-top-left"
        style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
      >
        {agents.map((a) => (
          <LiteAgentCard key={a.id} agent={a} focused={focusedId === a.id}
            onPointerDownHeader={(e) => onPointerDownCard(e, a)} />
        ))}
      </div>

      {/* HUD：统计 */}
      <div data-id="agent-canvas-stats" className="pointer-events-none absolute left-3 top-3 flex items-center gap-2 text-[11px] text-zinc-500">
        <span className="rounded-md bg-white/[0.04] px-2 py-1">{agents.length - 1} 名成员</span>
        <span className="rounded-md bg-amber-500/10 px-2 py-1 text-amber-300/80">工作中 {workingCount}</span>
        <span className="rounded-md bg-emerald-500/10 px-2 py-1 text-emerald-300/80">完成 {doneCount}</span>
        <span className="rounded-md bg-white/[0.03] px-2 py-1 text-zinc-600">只显示 thinking + text · tool 结果不拉</span>
      </div>

      {/* 缩放控件 */}
      <div data-id="agent-canvas-controls" className="absolute bottom-4 right-4 flex flex-col gap-1.5">
        <button data-id="agent-canvas-zoom-in" onClick={() => zoomBy(1.15)} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#141414] text-zinc-400 hover:text-zinc-100"><Plus className="h-4 w-4" /></button>
        <button data-id="agent-canvas-zoom-out" onClick={() => zoomBy(1 / 1.15)} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#141414] text-zinc-400 hover:text-zinc-100"><Minus className="h-4 w-4" /></button>
        <button data-id="agent-canvas-reset" onClick={reset} title="复位" className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#141414] text-zinc-400 hover:text-zinc-100"><Maximize2 className="h-4 w-4" /></button>
        <div className="text-center text-[10px] text-zinc-600">{Math.round(zoom * 100)}%</div>
      </div>
    </div>
  );
}

const STATUS_META: Record<Status, { label: string; cls: string }> = {
  idle:    { label: '空闲',   cls: 'text-zinc-500' },
  working: { label: '工作中', cls: 'text-amber-300' },
  done:    { label: '完成',   cls: 'text-emerald-300' },
};

function LiteAgentCard({ agent, focused, onPointerDownHeader }: {
  agent: CanvasAgent;
  focused: boolean;
  onPointerDownHeader: (e: React.PointerEvent) => void;
}) {
  const acc = ACCENT[agent.accent] ?? ACCENT.sky;
  const lines = agent.script.slice(0, agent.shown);
  const maxLines = focused ? 16 : 5;
  const visible = lines.slice(-maxLines);
  const bodyRef = useRef<HTMLDivElement>(null);
  const st = STATUS_META[agent.status];

  useEffect(() => {
    const n = bodyRef.current;
    if (n) n.scrollTop = n.scrollHeight;
  }, [agent.shown, focused]);

  const w = focused ? 380 : 248;
  const h = focused ? 320 : 168;

  return (
    <div
      data-id={`agent-canvas-card-${agent.id}`}
      className={`absolute flex flex-col overflow-hidden rounded-xl border bg-[#0e0e0e] shadow-xl transition-[width,height] duration-150 ${focused ? 'border-white/25 ring-1 ring-white/10' : acc.ring} ${agent.controller ? 'ring-1 ring-amber-400/30' : ''}`}
      style={{ left: agent.x, top: agent.y, width: w, height: h, zIndex: focused ? 50 : agent.controller ? 20 : 10 }}
    >
      {/* header（拖拽手柄） */}
      <div
        data-id={`agent-canvas-card-header-${agent.id}`}
        onPointerDown={onPointerDownHeader}
        className="flex cursor-grab items-center gap-2 border-b border-white/[0.06] bg-white/[0.02] px-2.5 py-2 active:cursor-grabbing"
      >
        <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-white/[0.05] text-base">{agent.emoji}</span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1">
            {agent.controller && <Crown className="h-3 w-3 text-amber-400" />}
            <span className="truncate text-[12.5px] font-medium text-zinc-200">{agent.name}</span>
          </span>
          <span className="font-mono text-[10.5px] text-zinc-500">{agent.id} · {agent.role}</span>
        </span>
        <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] ${acc.chip}`}>
          {agent.status === 'working' ? <Loader2 className="h-3 w-3 animate-spin" /> :
           agent.status === 'done' ? <CheckCircle2 className="h-3 w-3" /> :
           <CircleDot className={`h-3 w-3 ${acc.dot}`} />}
          {st.label}
        </span>
      </div>

      {/* body：thinking + text 实时流 */}
      <div ref={bodyRef} data-id={`agent-canvas-card-body-${agent.id}`} className="flex-1 space-y-1.5 overflow-auto px-2.5 py-2">
        {visible.length === 0 ? (
          <div className="text-[11.5px] text-zinc-600">{agent.controller ? '调度中…' : '待派活…'}</div>
        ) : visible.map((ln, i) => (
          ln.t === 'thinking' ? (
            <div key={i} className="border-l-2 border-amber-300/25 pl-2 text-[11.5px] leading-relaxed text-amber-50/55">{ln.s}</div>
          ) : (
            <div key={i} className="text-[11.5px] leading-relaxed text-zinc-300">{ln.s}</div>
          )
        ))}
      </div>

      {agent.status === 'done' && (
        <div className="shrink-0 border-t border-emerald-500/15 bg-emerald-500/[0.06] px-2.5 py-1 text-[10.5px] text-emerald-300/90">✅ work done · 等总控验收</div>
      )}
    </div>
  );
}
