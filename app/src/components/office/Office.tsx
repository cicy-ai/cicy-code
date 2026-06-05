import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Building2, Send, Megaphone, AtSign, X, Loader2, CheckCircle2, MessageSquare,
  Plus, Minus, Maximize2, Crown, Inbox, UserPlus, ChevronRight, Power, Users, Store, Copy, Check,
} from 'lucide-react';
import TemplateMarket, { MarketTmpl, TeamTmpl } from './TemplateMarket';
import { getAgentTypeIconMeta } from '../../lib/agentType';
import apiService from '../../services/api';

/*
 * Office — 「办公室」（data-id="office"）。
 * 左栏：指挥台（总控对话，上=history，下=prompt，自动 @ 选中 worker，可广播）。
 * 右侧：可平移/缩放画布，每个 worker = 可拖动+可缩放的 chat window，
 *       只显示 thinking + text（不拉 tool 结果），头像 = agent avatar + 状态环。
 * 接真实数据：成员 = w-10001 的子 agent（无则取全部 pane）；轮询
 * /api/agents/current-reply 取每个 agent 的 status / model / token / thinking / answer。
 * 派任务走 /api/tmux/send（sendCommand）真实下发到对应 pane。
 */

type Status = 'idle' | 'working';

// 一条历史 = 一个 turn 里抽出的 thinking+text（不含 tool 结果）；user=用户提问，assistant=agent 输出。
interface Entry { id: number; role: 'user' | 'assistant'; thinking: string; text: string; live?: boolean }

interface Worker {
  id: string; name: string; role: string; agentType: string;   // agentType = CLI 类型(claude/codex/opencode...)，决定头像图标
  model: string; ctx: number; ctxK: number;   // 模型 + 上下文用量(%) + 上下文窗口(k)
  status: Status; entries: Entry[]; sig: string; startedAt: number;   // entries=最近若干 turn 的历史；sig 用于跳过无变化重渲染
  x: number; y: number; w: number; h: number;
}

// 从 current-history 的一个 item 里抽 agent 自己的 thinking+text。
// 只取 assistant 轮：过滤 user/system/tool 注入(如自动 recap、system-reminder、工具结果),窗口只展示 agent 输出。
function extractEntry(item: any): Entry | null {
  if (item?.role !== 'assistant') return null;
  let thinking = '', text = '';
  const content = item?.content;
  if (typeof content === 'string') {
    text = content;
  } else if (Array.isArray(content)) {
    for (const c of content) {
      const t = c?.type;
      if (t === 'thinking' || t === 'reasoning') thinking += String(c?.thinking || c?.text || '');
      else if (t === 'text' || t === 'output_text') text += String(c?.text || '');
      // 跳过 tool_use / tool_result / image 等
    }
  } else if (typeof item?.text === 'string') {
    text = item.text;
  }
  thinking = thinking.trim().slice(-900);
  text = text.trim().slice(-1600);
  if (!thinking && !text) return null;
  return { id: Number(item?.history_id) || 0, role: 'assistant', thinking, text };
}

type ChatKind = 'dispatch' | 'broadcast' | 'done' | 'note';
interface ChatMsg { id: number; kind: ChatKind; from?: string; to?: string; text: string; ts: string }

const SELF = 'w-10001';
const MIN_W = 240, MIN_H = 168;

const shortId = (id: string) => String(id || '').replace(/:main\.0$/, '');

// 模型上下文窗口(k tokens)，用于把 input_tokens 估成上下文占用 %。粗映射，未知给 200k。
function modelWindowK(model: string): number {
  const m = (model || '').toLowerCase();
  if (m.includes('gemini')) return 1000;
  if (m.includes('claude')) return 200;
  if (m.includes('gpt') || m.includes('o1') || m.includes('o3') || m.includes('o4')) return 256;
  if (m.includes('deepseek')) return 128;
  return 200;
}
// current-reply.status → 是否在干活
const WORKING_STATES = new Set(['streaming', 'thinking', 'working', 'tool_call', 'running', 'generating', 'busy']);
const isWorkingStatus = (s: string) => WORKING_STATES.has(String(s || '').toLowerCase());

const Z_MIN = 0.4, Z_MAX = 1.8;
const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
const hhmm = (ms: number) => { const d = new Date(ms); const p = (n: number) => String(n).padStart(2, '0'); return `${p(d.getHours())}:${p(d.getMinutes())}`; };
const elapsed = (from: number, now: number) => {
  if (!from) return '';
  const s = Math.max(0, Math.floor((now - from) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${p2(s % 60)}s`;
};
const p2 = (n: number) => String(n).padStart(2, '0');

/* 模型 → 现成的 agent avatar 类型（claude-symbol / openai / cicy ...），复用 AgentAvatar 的官方图标。 */
function agentTypeForModel(model: string): string {
  const m = (model || '').toLowerCase();
  if (m.includes('claude')) return 'claude';
  if (m.includes('gpt') || m.includes('openai') || /\bo[0-9]\b/.test(m)) return 'codex';
  if (m.includes('gemini')) return 'codex';
  if (m.includes('cursor')) return 'cursor';
  if (m.includes('kiro')) return 'kiro-cli';
  if (m.includes('copilot')) return 'copilot';
  if (m.includes('opencode')) return 'opencode';
  return 'cicy-claude';   // deepseek 及其它走自家 CiCy 图标
}

/* ── avatar：现成的 agent 官方图标 + 状态环（working 黄脉冲 / idle 绿 / 其它灰）。
 *    直接拿 getAgentTypeIconMeta 自渲染方形图标,避免 AgentAvatar 内部固定 h-8 w-8 被我外层尺寸挤变形。 */
function Avatar({ model, agentType, name, size = 30, status }: { model: string; agentType?: string; name: string; size?: number; status?: Status }) {
  const icon = getAgentTypeIconMeta(agentType || agentTypeForModel(model));
  const ring = status === 'working' ? 'ring-2 ring-amber-400/70' : status === 'idle' ? 'ring-2 ring-emerald-400/50' : 'ring-1 ring-white/10';
  const inner = Math.round(size * 0.6);
  return (
    <span className="relative inline-grid shrink-0 place-items-center" style={{ width: size, height: size }}>
      {status === 'working' && <span className="absolute inset-0 rounded-full ring-2 ring-amber-400/70 animate-ping opacity-40" />}
      <span className={`grid h-full w-full place-items-center overflow-hidden rounded-full bg-zinc-100 ${ring}`}>
        {icon?.src ? (
          <img src={icon.src} alt={name} className="object-contain" style={{ width: inner, height: inner }} />
        ) : (
          <span className="font-semibold leading-none text-zinc-700" style={{ fontSize: Math.round(size * 0.42) }}>{icon?.text ?? (name.trim()[0] || '?')}</span>
        )}
      </span>
    </span>
  );
}

/* ── 候选成员（从市场/模版新增、尚未真实存在的占位 agent = 离线）── */
interface Cand { id: string; name: string; role: string; model: string }

// 统一布点：400 宽窗 + 28 横向间距 / 248 高窗 + 36 纵向间距，3 列。所有 window 共用,保证不重叠。
const COLS = 3, GAP_X = 428, GAP_Y = 284, ORIGIN_X = 32, ORIGIN_Y = 64;
const slotPos = (slot: number) => ({ x: ORIGIN_X + (slot % COLS) * GAP_X, y: ORIGIN_Y + Math.floor(slot / COLS) * GAP_Y });

function makeWorker(id: string, name: string, role: string, model: string, slot: number, agentType = ''): Worker {
  return { id, name, role, agentType, model, ctx: 0, ctxK: modelWindowK(model), status: 'idle', entries: [], sig: '', startedAt: 0, w: 400, h: 248, ...slotPos(slot) };
}

export default function Office() {
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [loaded, setLoaded] = useState(false);
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
  const [candidates, setCandidates] = useState<Cand[]>([]);
  const [rosterOpen, setRosterOpen] = useState(false);
  const [market, setMarket] = useState<'team' | 'agent' | null>(null);
  const idSeq = useRef(20);
  const seq = useRef(2);
  const chatRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const dragRef = useRef<null | { kind: 'pan' | 'move' | 'resize'; id?: string; sx: number; sy: number; ox: number; oy: number; ow: number; oh: number; moved: boolean }>(null);
  // 给「稳定回调 / 全局拖拽监听」读最新值,避免把 workers/zoom 进依赖导致每帧重建 listener。
  const workersRef = useRef(workers); workersRef.current = workers;
  const zoomRef = useRef(zoom); zoomRef.current = zoom;

  const byId = useMemo(() => Object.fromEntries(workers.map((w) => [w.id, w])), [workers]);
  const target = selectedId ? byId[selectedId] : null;
  const online = workers.filter((w) => w.status !== 'idle').length;

  // 计时（仅 working 窗口用到 elapsed）
  useEffect(() => {
    const c = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(c);
  }, []);

  // 真实成员：w-10001 的子 agent（pane_agents）；无则取全部 pane（排除自己）。每 15s 刷新名单。
  useEffect(() => {
    let cancelled = false;
    const loadTeam = async () => {
      try {
        const panesRes = await apiService.getPanes();
        const pd: any = panesRes.data;
        const panes: any[] = Array.isArray(pd) ? pd : (pd?.panes || []);
        let subIds: Set<string> | null = null;
        try {
          const subRes: any = await (apiService as any).getAgentsByPane(SELF);
          const sd = subRes?.data;
          const subs: any[] = Array.isArray(sd) ? sd : (sd?.agents || []);
          const ids = subs.map((b: any) => shortId(String(b.name || ''))).filter(Boolean);
          if (ids.length) subIds = new Set(ids);
        } catch { /* 没有子 agent 关系表数据 → 退回全部 pane */ }
        const team = panes
          .map((p: any) => ({
            id: shortId(String(p.pane_id || p.id || '')),
            title: String(p.title || p.pane_id || '').trim(),
            role: String(p.role || p.agent_type || '').trim(),
            model: String(p.default_model || p.model || '').trim(),
            agentType: String(p.agent_type || '').trim(),
          }))
          .filter((p) => p.id && p.id !== SELF && (subIds ? subIds.has(p.id) : true));
        if (cancelled) return;
        setWorkers((prev) => {
          const prevById = new Map(prev.map((w) => [w.id, w]));
          return team.map((p, i) => {
            const ex = prevById.get(p.id);
            if (ex) return { ...ex, name: p.title || ex.name, role: p.role || ex.role, model: p.model || ex.model, agentType: p.agentType || ex.agentType };
            return makeWorker(p.id, p.title || p.id, p.role, p.model, i, p.agentType);
          });
        });
        setLoaded(true);
      } catch { if (!cancelled) setLoaded(true); }
    };
    loadTeam();
    const t = window.setInterval(loadTeam, 15000);
    return () => { cancelled = true; window.clearInterval(t); };
  }, []);

  // 真实实时：每个 agent 取 current-history(最近若干 turn) + current-reply(实时状态/进行中turn)。每 2.5s。
  useEffect(() => {
    let cancelled = false;
    const HISTORY_LIMIT = 8;
    const poll = async () => {
      const ws = workersRef.current;
      if (!ws.length) return;
      await Promise.all(ws.map(async (w) => {
        try {
          const [hRes, rRes]: any[] = await Promise.all([
            (apiService as any).getAgentCurrentHistory(w.id, { limit: HISTORY_LIMIT }).catch(() => null),
            apiService.getAgentCurrentReply(w.id).catch(() => null),
          ]);
          if (cancelled) return;
          const items: any[] = hRes?.data?.items || [];
          const entries: Entry[] = [];
          for (const it of items) { const e = extractEntry(it); if (e) entries.push(e); }
          const d: any = rRes?.data || {};
          const working = isWorkingStatus(d.status) && d.complete !== true;
          // 进行中的那一轮（还没落历史）追加为 live 条目，避免和已落历史重复。
          if (working) {
            const t = String(d.thinking || '').trim().slice(-900);
            const a = String(d.answer || '').trim().slice(-1600);
            const liveId = Number(d.history_id) || (entries.length ? entries[entries.length - 1].id + 1 : 1);
            if ((t || a) && !entries.some((e) => e.id === liveId)) entries.push({ id: liveId, role: 'assistant', thinking: t, text: a, live: true });
          }
          const model = String(d.model || w.model || '');
          const winK = modelWindowK(model);
          const inTok = (d.input_tokens || 0) + (d.cache_read_input_tokens || 0) + (d.cache_creation_input_tokens || 0);
          const ctx = winK > 0 && inTok > 0 ? clamp(Math.round((inTok / (winK * 1000)) * 100), 0, 99) : 0;
          const sig = `${working ? 1 : 0}|${model}|${ctx}|` + entries.map((e) => `${e.id}:${e.role[0]}:${e.thinking.length}:${e.text.length}:${e.live ? 1 : 0}`).join(',');
          setWorkers((prev) => prev.map((x) => {
            if (x.id !== w.id) return x;
            if (x.sig === sig) return x;   // 无变化 → 保持引用,memo 跳过重渲染
            const status: Status = working ? 'working' : 'idle';
            const startedAt = status === 'working' && x.status !== 'working' ? Date.now() : x.startedAt;
            return { ...x, status, entries, sig, model, ctx, ctxK: winK, startedAt };
          }));
        } catch { /* 该 agent 还没数据 / 404 → 保持现状 */ }
      }));
    };
    poll();
    const t = window.setInterval(poll, 2500);
    return () => { cancelled = true; window.clearInterval(t); };
  }, []);

  useEffect(() => { const n = chatRef.current; if (n) n.scrollTop = n.scrollHeight; }, [chat]);

  const push = (m: Omit<ChatMsg, 'id' | 'ts'>) => setChat((c) => [...c, { id: seq.current++, ts: hhmm(Date.now()), ...m }]);

  // 稳定回调（[] 依赖,读 ref 取最新值）→ 传给 memo 化的 WorkerWindow,避免每次 render/计时都让全部窗口重渲染。
  const selectWorker = useCallback((id?: string) => { if (!id) return; setSelectedId(id); setMode('single'); setMentionOpen(false); inputRef.current?.focus(); }, []);
  const onHover = useCallback((id: string, h: boolean) => setHoverId((cur) => (h ? id : cur === id ? null : cur)), []);
  const startMove = useCallback((e: React.PointerEvent, id: string) => {
    e.stopPropagation(); const w = workersRef.current.find((x) => x.id === id); if (!w) return;
    dragRef.current = { kind: 'move', id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false };
  }, []);
  const startResize = useCallback((e: React.PointerEvent, id: string) => {
    e.stopPropagation(); const w = workersRef.current.find((x) => x.id === id); if (!w) return;
    dragRef.current = { kind: 'resize', id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false };
  }, []);

  const onPointerDownBg = (e: React.PointerEvent) => { dragRef.current = { kind: 'pan', sx: e.clientX, sy: e.clientY, ox: pan.x, oy: pan.y, ow: 0, oh: 0, moved: false }; };
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = dragRef.current; if (!d) return;
      const dx = e.clientX - d.sx, dy = e.clientY - d.sy, z = zoomRef.current;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) d.moved = true;
      if (d.kind === 'pan') setPan({ x: d.ox + dx, y: d.oy + dy });
      else if (d.kind === 'move') setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, x: d.ox + dx / z, y: d.oy + dy / z } : w));
      else setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, w: Math.max(MIN_W, d.ow + dx / z), h: Math.max(MIN_H, d.oh + dy / z) } : w));
    };
    const up = () => {
      const d = dragRef.current;
      if (d && !d.moved) { if (d.kind === 'move' && d.id) selectWorker(d.id); else if (d.kind === 'pan') setSelectedId(null); }
      dragRef.current = null;
    };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
  }, [selectWorker]);

  const onWheel = (e: React.WheelEvent) => {
    // 缩放与滚动量成比例并把单次倍率夹在 ±4%，避免滚轮一格跳太多/太灵敏。
    // deltaMode=1(行)的鼠标 deltaY 很小，乘 16 归一到像素量级。
    const delta = e.deltaY * (e.deltaMode === 1 ? 16 : 1);
    const factor = clamp(Math.exp(-delta * 0.0015), 0.96, 1.04);
    const next = clamp(zoom * factor, Z_MIN, Z_MAX);
    if (next === zoom) return;
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
    setPan((p) => ({ x: cx - (cx - p.x) * (next / zoom), y: cy - (cy - p.y) * (next / zoom) }));
    setZoom(next);
  };

  const send = () => {
    const body = text.trim(); if (!body) return;
    if (mode === 'broadcast') {
      push({ kind: 'broadcast', text: body });
      const ids = workersRef.current.map((w) => w.id);
      setWorkers((prev) => prev.map((w) => ({ ...w, status: 'working', startedAt: Date.now() })));
      ids.forEach((id) => { (apiService as any).sendCommand(id, body, true).catch(() => { /* pane 不可达忽略 */ }); });
      setText(''); return;
    }
    if (!target) return;
    const id = target.id;
    push({ kind: 'dispatch', to: id, text: body });
    setWorkers((prev) => prev.map((w) => (w.id === id ? { ...w, status: 'working', startedAt: Date.now() } : w)));
    (apiService as any).sendCommand(id, body, true).catch(() => { /* pane 不可达忽略 */ });
    setText('');
  };
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } };
  const onChange = (v: string) => { setText(v); if (mode === 'single') setMentionOpen(/(^|\s)@$/.test(v)); };
  const pickMention = (w: Worker) => { setSelectedId(w.id); setMode('single'); setText((v) => v.replace(/@$/, '')); setMentionOpen(false); inputRef.current?.focus(); };
  const joinCandidate = (c: Cand) => {
    setCandidates((prev) => prev.filter((x) => x.id !== c.id));
    setWorkers((prev) => [...prev, makeWorker(c.id, c.name, c.role, c.model, prev.length)]);
    push({ kind: 'note', text: `已加入 ${c.name}（${c.id}）` });
  };
  // 市场/模版新增的是占位 agent（尚未真实创建），轮询取不到 reply 会保持 idle/空。
  const spawn = (name: string, role: string, model: string, note: string) => {
    const id = `w-100${idSeq.current++}`;
    setWorkers((prev) => [...prev, makeWorker(id, name, role, model, prev.length)]);
    push({ kind: 'note', text: `${note} ${id}（占位，未真实创建）` });
  };
  const pickFromMarket = (t: MarketTmpl) => spawn(t.name, t.role, t.model, `已从模版市场添加「${t.name}」`);
  const pickTeam = (team: TeamTmpl) => {
    const base = idSeq.current; idSeq.current += team.members.length;
    setWorkers((prev) => [...prev, ...team.members.map((m, i) => makeWorker(`w-100${base + i}`, m.name, m.role, m.model, prev.length + i))]);
    push({ kind: 'note', text: `已组建「${team.name}」（${team.members.length} 名成员，占位）` });
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

        {/* 成员库（候选 + 市场入口）—— 折叠区,在 prompt 之上 */}
        <div data-id="office-roster" className="shrink-0 border-t border-white/[0.06]">
          <button data-id="office-roster-toggle" onClick={() => setRosterOpen((v) => !v)} className="flex w-full items-center gap-1.5 px-3.5 py-2 text-[11px] font-medium uppercase tracking-wide text-zinc-500 transition-colors hover:text-zinc-300">
            <Users className="h-3.5 w-3.5" /> 成员库
            <ChevronRight className={`ml-auto h-3.5 w-3.5 transition-transform ${rosterOpen ? 'rotate-90' : ''}`} />
          </button>
          {rosterOpen && (
            <div className="max-h-[42vh] space-y-2.5 overflow-auto px-3 pb-3">
              <button data-id="office-open-team-market" onClick={() => setMarket('team')}
                className="flex w-full items-center gap-3 rounded-xl border border-sky-400/20 bg-sky-500/10 px-3 py-2.5 text-left transition-colors hover:border-sky-400/40 hover:bg-sky-500/15">
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-sky-500/20 text-sky-300"><Users className="h-4 w-4" /></span>
                <span className="min-w-0 flex-1"><span className="block text-[12.5px] font-medium text-zinc-100">团队市场</span><span className="block text-[11px] text-zinc-500">一键组建整支班子</span></span>
                <ChevronRight className="h-4 w-4 text-zinc-500" />
              </button>
              <button data-id="office-open-agent-market" onClick={() => setMarket('agent')}
                className="flex w-full items-center gap-3 rounded-xl border border-white/[0.08] bg-white/[0.03] px-3 py-2.5 text-left transition-colors hover:border-white/15 hover:bg-white/[0.05]">
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-zinc-300"><Store className="h-4 w-4" /></span>
                <span className="min-w-0 flex-1"><span className="block text-[12.5px] font-medium text-zinc-100">Agent 市场</span><span className="block text-[11px] text-zinc-500">各行各业单个 agent 模版</span></span>
                <ChevronRight className="h-4 w-4 text-zinc-500" />
              </button>
              <section data-id="office-roster-candidates" className="pt-0.5">
                <div className="mb-1.5 flex items-center gap-1.5 px-1 text-[11px] font-medium uppercase tracking-wide text-zinc-600"><Power className="h-3 w-3" /> 候选 · 未开启<span className="ml-auto normal-case text-zinc-700">{candidates.length}</span></div>
                {candidates.length === 0 ? <div className="px-1 text-[11.5px] text-zinc-700">全部已加入</div> : (
                  <div className="space-y-1.5">
                    {candidates.map((c) => (
                      <div key={c.id} data-id={`office-cand-${c.id}`} className="flex items-center gap-2.5 rounded-xl border border-white/[0.06] bg-white/[0.02] px-2.5 py-2">
                        <span className="opacity-50 grayscale"><Avatar model={c.model} name={c.name} size={28} /></span>
                        <span className="min-w-0 flex-1"><span className="block truncate text-[12.5px] text-zinc-300">{c.name}</span><span className="font-mono text-[10.5px] text-zinc-600">{c.id} · 离线</span></span>
                        <button data-id={`office-cand-join-${c.id}`} onClick={() => joinCandidate(c)} className="inline-flex items-center gap-1 rounded-lg bg-white/[0.06] px-2 py-1 text-[11.5px] text-zinc-200 transition-colors hover:bg-white/[0.12]"><UserPlus className="h-3.5 w-3.5" /> 加入</button>
                      </div>
                    ))}
                  </div>
                )}
              </section>
            </div>
          )}
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
                    <Avatar model={w.model} agentType={w.agentType} name={w.name} size={24} status={w.status} /><span className="text-[13px] text-zinc-200">{w.name}</span><span className="ml-auto font-mono text-[11px] text-zinc-500">{w.id}</span>
                  </button>
                ))}
              </div>
            )}
            <div data-id="office-target" className="mb-2 flex min-h-[26px] items-center gap-1.5">
              {mode === 'broadcast' ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-2.5 py-1 text-[12px] text-amber-200"><Megaphone className="h-3 w-3" /> 广播 · 全体（{workers.length}）</span>
              ) : target ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-white/[0.06] py-1 pl-1 pr-2 text-[12px] text-zinc-200"><Avatar model={target.model} agentType={target.agentType} name={target.name} size={20} status={target.status} /><span className="font-medium">{target.name}</span><span className="font-mono text-[11px] text-zinc-500">{target.id}</span><button data-id="office-target-clear" onClick={() => setSelectedId(null)} className="rounded-full p-0.5 text-zinc-500 hover:bg-white/10 hover:text-zinc-200"><X className="h-3 w-3" /></button></span>
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
            <WorkerWindow key={w.id} w={w} now={w.status === 'working' ? now : 0}
              selected={selectedId === w.id} hovered={hoverId === w.id}
              onHover={onHover} onMoveStart={startMove} onResizeStart={startResize} />
          ))}
        </div>

        {workers.length === 0 && (
          <div data-id="office-canvas-empty" className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 text-zinc-600">
            {!loaded ? (
              <><Loader2 className="h-6 w-6 animate-spin" /><span className="text-[13px]">加载团队…</span></>
            ) : (
              <><Users className="h-7 w-7 opacity-50" /><span className="text-[13px]">还没有成员 agent</span><span className="text-[11.5px] text-zinc-700">从左下「成员库」的市场添加，或用 cicy-agent 起子 agent</span></>
            )}
          </div>
        )}

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

      <TemplateMarket open={market} onClose={() => setMarket(null)} onPick={pickFromMarket} onPickTeam={pickTeam} />
    </div>
  );
}

const CommandMsg = memo(function CommandMsg({ m, byId }: { m: ChatMsg; byId: Record<string, Worker> }) {
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
        <div className="flex items-center gap-1.5 text-[11px] text-zinc-500">派给 {w && <Avatar model={w.model} agentType={w.agentType} name={w.name} size={16} />}<span className="text-zinc-400">{w?.name ?? m.to}</span></div>
        <div className="max-w-[86%] rounded-2xl rounded-tr-md bg-sky-500/90 px-3 py-1.5 text-[12.5px] leading-relaxed text-white shadow-sm whitespace-pre-wrap">{m.text}</div>
      </div>
    );
  }
  const w = m.from ? byId[m.from] : null;
  return (
    <div data-id={`office-msg-${m.id}`} className="flex items-center gap-2">
      {w && <Avatar model={w.model} agentType={w.agentType} name={w.name} size={22} />}
      <div className="min-w-0 flex-1">
        <div className="text-[11px] text-zinc-500">{w?.name ?? m.from}</div>
        <div className="inline-flex items-center gap-1 text-[12px] text-emerald-300"><CheckCircle2 className="h-3.5 w-3.5" /> {m.text}</div>
      </div>
      <span className="self-start font-mono text-[10px] text-zinc-700">{m.ts}</span>
    </div>
  );
});

const WorkerWindow = memo(function WorkerWindow({ w, now, selected, hovered, onHover, onMoveStart, onResizeStart }: {
  w: Worker; now: number; selected: boolean; hovered: boolean;
  onHover: (id: string, h: boolean) => void; onMoveStart: (e: React.PointerEvent, id: string) => void; onResizeStart: (e: React.PointerEvent, id: string) => void;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => { const n = bodyRef.current; if (n) n.scrollTop = n.scrollHeight; }, [w.sig]);
  const working = w.status === 'working';
  const entries = w.entries;
  const ctxColor = w.ctx > 85 ? 'bg-rose-400' : w.ctx > 60 ? 'bg-amber-400' : 'bg-zinc-400/60';
  const ctxText = w.ctx > 85 ? 'text-rose-300' : w.ctx > 60 ? 'text-amber-300' : 'text-zinc-500';
  const [copied, setCopied] = useState(false);
  const copyId = (e: React.MouseEvent) => { e.stopPropagation(); try { navigator.clipboard?.writeText(w.id); } catch { /* noop */ } setCopied(true); window.setTimeout(() => setCopied(false), 1200); };

  return (
    <div data-id={`office-window-${w.id}`}
      onPointerEnter={() => onHover(w.id, true)} onPointerLeave={() => onHover(w.id, false)}
      className={`absolute flex flex-col overflow-hidden rounded-2xl border bg-[#0e0e11] transition-[box-shadow,transform,border-color] duration-150
        ${selected ? 'ring-2 ring-white/30 border-transparent -translate-y-0.5 shadow-2xl' : hovered ? 'border-white/15 shadow-2xl' : 'border-white/[0.07] shadow-xl'}`}
      style={{ left: w.x, top: w.y, width: w.w, height: w.h, zIndex: selected ? 60 : hovered ? 40 : 10 }}>
      {/* 状态色条：thinking 黄 / idle 绿 */}
      <div className={`h-[3px] w-full ${working ? 'bg-amber-400/60' : 'bg-emerald-400/45'}`} />
      <div data-id={`office-window-header-${w.id}`} onPointerDown={(e) => onMoveStart(e, w.id)}
        className="flex shrink-0 cursor-grab select-none items-center gap-2.5 bg-white/[0.015] px-3 py-2.5 active:cursor-grabbing">
        <Avatar model={w.model} agentType={w.agentType} name={w.name} size={32} status={w.status} />
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
        {working ? (
          <span className="inline-flex items-center gap-1 text-[10.5px] text-amber-300"><Loader2 className="h-3 w-3 animate-spin" /> thinking · {elapsed(w.startedAt, now)}</span>
        ) : (
          <span className="inline-flex items-center gap-1 text-[10.5px] text-emerald-300"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> idle</span>
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
      <div ref={bodyRef} data-id={`office-window-body-${w.id}`} onWheel={(e) => e.stopPropagation()} className="flex-1 space-y-3 overflow-auto px-3 py-2.5">
        {entries.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-1 text-zinc-700"><Inbox className="h-5 w-5" /><span className="text-[11px]">{working ? '思考中…' : '空闲 · 等待派活'}</span></div>
        ) : entries.map((e, i) => {
          const isLast = i === entries.length - 1;
          return (
            <div key={`${e.id}-${i}`} data-id={`office-window-entry-${w.id}-${i}`} className="space-y-1.5 border-l border-white/[0.06] pl-2">
              {e.thinking && (
                <div className="whitespace-pre-wrap text-[11px] italic leading-relaxed text-amber-50/45">
                  {e.thinking}{e.live && working && !e.text && <span className="ml-0.5 animate-pulse text-amber-200/70">▍</span>}
                </div>
              )}
              {e.text && (
                <div className="whitespace-pre-wrap text-[12px] leading-relaxed text-zinc-300">
                  {e.text}{e.live && working && <span className="ml-0.5 animate-pulse text-zinc-400">▍</span>}
                </div>
              )}
              {isLast && e.live && working && !e.thinking && !e.text && <span className="animate-pulse text-amber-200/70">▍</span>}
            </div>
          );
        })}
      </div>
      {/* resize 抓手：悬停/选中才显露 */}
      <div data-id={`office-window-resize-${w.id}`} onPointerDown={(e) => onResizeStart(e, w.id)}
        className={`absolute bottom-1 right-1 h-3.5 w-3.5 cursor-nwse-resize rounded-sm transition-opacity ${hovered || selected ? 'opacity-100' : 'opacity-0'}`}
        style={{ background: 'linear-gradient(135deg, transparent 45%, rgba(255,255,255,0.4) 45%, rgba(255,255,255,0.4) 55%, transparent 55%, transparent 70%, rgba(255,255,255,0.4) 70%, rgba(255,255,255,0.4) 80%, transparent 80%)' }} />
    </div>
  );
});
