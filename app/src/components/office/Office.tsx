import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Building2, Send, Megaphone, AtSign, X, Loader2, CheckCircle2, MessageSquare,
  Plus, Minus, Maximize2, Crown, UserPlus, ChevronRight, Power, Users, Store, Copy, Check, PanelLeft,
} from 'lucide-react';
import TemplateMarket, { MarketTmpl, TeamTmpl } from './TemplateMarket';
import CurrentHistoryView from '../chat/CurrentHistoryView';
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
  tokens: number; cost: number;   // 累计 token 总量 + 累计成本($，来自 reply.cost_credit)
  status: Status; entries: Entry[]; live: Entry | null; histLoaded: boolean; sig: string; startedAt: number;
  x: number; y: number; w: number; h: number;   // entries=已落历史(慢轮询)；live=进行中 turn(快轮询)；sig 跳过无变化重渲染
}

type ChatKind = 'dispatch' | 'broadcast' | 'done' | 'note';
interface ChatMsg { id: number; kind: ChatKind; from?: string; to?: string; text: string; ts: string }

const SELF = 'w-10001';
const MIN_W = 240, MIN_H = 168;

const shortId = (id: string) => String(id || '').replace(/:main\.0$/, '');

// 模型基础上下文窗口(k tokens)。粗映射，未知给 200k。
function modelWindowK(model: string): number {
  const m = (model || '').toLowerCase();
  if (m.includes('gemini')) return 1000;
  // opus-4-8 的 1M beta 有效窗口实测约 2M:w-10053 input_tokens 到 1.075M 仍在跑,
  // 且各 agent input_tokens/2M 与 Claude Code pane 的 Context% 完全吻合(54/30/26/7)。
  if (m.includes('opus')) return 2000;
  if (m.includes('claude')) return 200;
  if (m.includes('gpt') || m.includes('o1') || m.includes('o3') || m.includes('o4')) return 256;
  if (m.includes('deepseek')) return 128;
  return 200;
}
// 整段 prompt 的 token 数(= 上下文占用)。坑:网关已把 cache_read 计进 input_tokens(input≈cache_read),
// 标准 Anthropic 则分开(input 只是新增的、cache_read 另算)。取较大解释避免重复计数把 1M 算成 2M。
function promptTokens(d: any): number {
  const inp = Number(d?.input_tokens || 0), cr = Number(d?.cache_read_input_tokens || 0), cc = Number(d?.cache_creation_input_tokens || 0);
  return inp >= cr ? inp + cc : inp + cr + cc;
}

const Z_MIN = 0.4, Z_MAX = 1.8;
const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
const hhmm = (ms: number) => { const d = new Date(ms); const p = (n: number) => String(n).padStart(2, '0'); return `${p(d.getHours())}:${p(d.getMinutes())}`; };
const elapsed = (from: number, now: number) => {
  if (!from) return '';
  const s = Math.max(0, Math.floor((now - from) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${p2(s % 60)}s`;
};
const p2 = (n: number) => String(n).padStart(2, '0');

// 复制 agent id。office 多在 cicy-desktop 里以 http(非安全上下文)打开,navigator.clipboard 与
// execCommand 都被禁;优先走 cicy-desktop 注入的 electronRPC 桥(clipboard_write_text),
// 再回退 navigator.clipboard / execCommand 以兼容普通浏览器/https。返回是否真复制成功。
async function copyTextToClipboard(text: string): Promise<boolean> {
  try {
    const rpc = (window as unknown as { electronRPC?: (tool: string, args: Record<string, unknown>) => Promise<unknown> }).electronRPC;
    if (typeof rpc === 'function') { await rpc('clipboard_write_text', { text }); return true; }
  } catch { /* 落到 web 方案 */ }
  try {
    if (window.isSecureContext && navigator.clipboard?.writeText) { await navigator.clipboard.writeText(text); return true; }
  } catch { /* 落到 execCommand */ }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed'; ta.style.top = '0'; ta.style.left = '0'; ta.style.opacity = '0';
    document.body.appendChild(ta); ta.focus(); ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch { return false; }
}

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

// 统一布点：560 宽窗 + 28 横向间距 / 360 高窗 + 36 纵向间距，3 列。所有 window 共用,保证不重叠。
const COLS = 3, GAP_X = 588, GAP_Y = 396, ORIGIN_X = 32, ORIGIN_Y = 64;
const slotPos = (slot: number) => ({ x: ORIGIN_X + (slot % COLS) * GAP_X, y: ORIGIN_Y + Math.floor(slot / COLS) * GAP_Y });

function makeWorker(id: string, name: string, role: string, model: string, slot: number, agentType = ''): Worker {
  return { id, name, role, agentType, model, ctx: 0, ctxK: modelWindowK(model), tokens: 0, cost: 0, status: 'idle', entries: [], live: null, histLoaded: false, sig: '', startedAt: 0, w: 560, h: 360, ...slotPos(slot) };
}

// 成本格式化:≥$0.01 两位小数,更小的给 4 位,0 显示为空(改用 token 兜底)。
function fmtCost(c: number): string {
  if (!c || c <= 0) return '';
  if (c >= 0.01) return `$${c.toFixed(2)}`;
  return `$${c.toFixed(4)}`;
}
function fmtTokens(n: number): string {
  if (!n || n <= 0) return '';
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

export default function Office() {
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);   // 多选:可同时选中多个 window 群发
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [mode, setMode] = useState<'single' | 'broadcast'>('single');
  const [text, setText] = useState('');
  const [mentionOpen, setMentionOpen] = useState(false);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [zoom, setZoom] = useState(1);
  const [now, setNow] = useState(() => Date.now());
  const [chat, setChat] = useState<ChatMsg[]>([
    { id: 1, kind: 'note', text: '拖标题移动卡片、拖右下角缩放、空白拖拽平移、滚轮缩放。点 worker 选中派活;Shift 点可多选群发,或切广播。', ts: hhmm(Date.now()) },
  ]);
  const [candidates, setCandidates] = useState<Cand[]>([]);
  const [rosterOpen, setRosterOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);   // 左栏指挥台默认折叠;发指令只走底部居中的浮动 prompt
  const [market, setMarket] = useState<'team' | 'agent' | null>(null);
  const idSeq = useRef(20);
  const seq = useRef(2);
  const chatRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const dragRef = useRef<null | { kind: 'pan' | 'move' | 'resize'; id?: string; sx: number; sy: number; ox: number; oy: number; ow: number; oh: number; moved: boolean }>(null);
  // 给「稳定回调 / 全局拖拽监听」读最新值,避免把 workers/zoom 进依赖导致每帧重建 listener。
  const workersRef = useRef(workers); workersRef.current = workers;
  const zoomRef = useRef(zoom); zoomRef.current = zoom;
  const canvasRef = useRef<HTMLElement>(null);
  const centeredRef = useRef(false);   // 初始化只自动居中一次,之后不干扰用户平移/缩放

  const byId = useMemo(() => Object.fromEntries(workers.map((w) => [w.id, w])), [workers]);
  const selectedSet = useMemo(() => new Set(selectedIds), [selectedIds]);
  const selectedWorkers = useMemo(() => selectedIds.map((id) => byId[id]).filter(Boolean), [selectedIds, byId]);
  const target = selectedIds.length === 1 ? byId[selectedIds[0]] : null;
  const online = workers.filter((w) => w.status !== 'idle').length;

  // 计时（仅 working 窗口用到 elapsed）
  useEffect(() => {
    const c = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(c);
  }, []);

  // 进入办公室自动聚焦指令输入框(多个 CurrentHistoryView 挂载/滚动可能抢焦点,重试几次直到聚焦成功)
  useEffect(() => {
    let n = 0; let id = 0;
    const tryFocus = () => {
      inputRef.current?.focus();
      if (++n < 6 && document.activeElement !== inputRef.current) id = window.setTimeout(tryFocus, 120);
    };
    id = window.setTimeout(tryFocus, 50);
    return () => window.clearTimeout(id);
  }, []);
  // 输入框随内容自适应高度:下限两行(68px),向下增长到 160px 后内部滚动。
  useEffect(() => {
    const el = inputRef.current; if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(Math.max(el.scrollHeight, 68), 160)}px`;
  }, [text]);

  // 初始化:把所有 agent 的窗格整体居中(并在过大时缩放适配),只做一次
  useEffect(() => {
    if (centeredRef.current || !loaded) return;
    const n = workersRef.current.length; if (!n) return;
    const el = canvasRef.current; const cw = el?.clientWidth || 0, ch = el?.clientHeight || 0;
    if (!cw || !ch) return;
    const cols = Math.min(n, COLS), rows = Math.ceil(n / COLS);
    const gridW = (cols - 1) * GAP_X + 560, gridH = (rows - 1) * GAP_Y + 360;
    const TOP = 56, BOTTOM = 132, M = 48;   // 顶栏 / 底部指令栏 / 四周留白
    const z = clamp(Math.min(1, (cw - M * 2) / gridW, (ch - TOP - BOTTOM) / gridH), Z_MIN, 1);
    const layerCx = ORIGIN_X + gridW / 2, layerCy = ORIGIN_Y + gridH / 2;
    setZoom(z);
    setPan({ x: cw / 2 - layerCx * z, y: (TOP + (ch - BOTTOM)) / 2 - layerCy * z });
    centeredRef.current = true;
  }, [loaded, workers.length]);

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
        // 注意:不带 pane 的 default_model —— 那是 cicy 配置项(网关路由用),非网关下对不上实际。
        // 模型只认 current-reply(reply.json)里真实跑的那个,拉到前显示 '—'。
        const team = panes
          .map((p: any) => ({
            id: shortId(String(p.pane_id || p.id || '')),
            title: String(p.title || p.pane_id || '').trim(),
            role: String(p.role || p.agent_type || '').trim(),
            agentType: String(p.agent_type || '').trim(),
          }))
          .filter((p) => p.id && (p.id === SELF || (subIds ? subIds.has(p.id) : true)));
        // 总控 w-10001 也作为一个 window 进画布,排在最前(不再单列在左栏)
        team.sort((a, b) => (a.id === SELF ? -1 : 0) - (b.id === SELF ? -1 : 0));
        if (cancelled) return;
        setWorkers((prev) => {
          const prevById = new Map(prev.map((w) => [w.id, w]));
          return team.map((p, i) => {
            const ex = prevById.get(p.id);
            if (ex) return { ...ex, name: p.title || ex.name, role: p.role || ex.role, agentType: p.agentType || ex.agentType };
            return makeWorker(p.id, p.title || p.id, p.role, '', i, p.agentType);
          });
        });
        setLoaded(true);
      } catch { if (!cancelled) setLoaded(true); }
    };
    loadTeam();
    const t = window.setInterval(loadTeam, 15000);
    return () => { cancelled = true; window.clearInterval(t); };
  }, []);

  // 真实实时:每 2s 拉 current-reply(轻,读 reply.json)拿 header 元信息 —— status/model/上下文/累计 cost。
  // 聊天正文不在这里拉:window body 用 CurrentHistoryView 自己轮询 current.json,避免重复加载。
  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      if (document.hidden) return;   // 页面不可见(最小化/切走)时不打后端
      const ws = workersRef.current;
      if (!ws.length) return;
      await Promise.all(ws.map(async (w) => {
        try {
          const rRes: any = await apiService.getAgentCurrentReply(w.id).catch(() => null);
          if (cancelled) return;
          const d: any = rRes?.data || {};
          // 只要没完成就算 thinking:complete=true 或终态(completed/idle/error…)或无状态 → idle,其余一律 thinking。
          const st = String(d.status || '').trim().toLowerCase();
          const done = d.complete === true || st === '' || ['completed', 'complete', 'done', 'idle', 'aborted', 'error', 'canceled', 'cancelled', 'failed', 'stopped'].includes(st);
          const working = !done;
          const model = String(d.model || w.model || '');
          const inTok = promptTokens(d);   // 整段 prompt(含 cache)
          // 优先用 Claude Code 自报的权威用量(statusline 落盘的 context_used_pct,和 pane 完全一致);
          // 没有(老 worker / codex 未落盘)才回退 input_tokens/窗口 估算。
          const realPct = d.context_used_pct;
          const useReal = typeof realPct === 'number' && realPct >= 0;
          const winK = useReal && d.context_window_size ? Math.round(d.context_window_size / 1000) : modelWindowK(model);
          const ctx = useReal
            ? clamp(Math.round(realPct), 0, 100)
            : (winK > 0 && inTok > 0 ? clamp(Math.round((inTok / (winK * 1000)) * 100), 0, 100) : w.ctx);
          const tokens = inTok;
          const cost = Number(d.cost_credit || 0) || w.cost;
          const sig = `${working ? 1 : 0}|${model}|${ctx}|${tokens}|${cost}`;
          setWorkers((prev) => prev.map((x) => {
            if (x.id !== w.id) return x;
            if (x.sig === sig) return x;   // 无变化 → 保持引用,memo 跳过重渲染
            const status: Status = working ? 'working' : 'idle';
            const startedAt = status === 'working' && x.status !== 'working' ? Date.now() : x.startedAt;
            return { ...x, status, model, ctx, ctxK: winK, tokens, cost, sig, startedAt };
          }));
        } catch { /* 该 agent 还没数据 / 404 → 保持现状 */ }
      }));
    };
    poll();
    const t = window.setInterval(poll, 2000);
    return () => { cancelled = true; window.clearInterval(t); };
  }, []);

  useEffect(() => { const n = chatRef.current; if (n) n.scrollTop = n.scrollHeight; }, [chat]);

  const push = (m: Omit<ChatMsg, 'id' | 'ts'>) => setChat((c) => [...c, { id: seq.current++, ts: hhmm(Date.now()), ...m }]);

  // 稳定回调（[] 依赖,读 ref 取最新值）→ 传给 memo 化的 WorkerWindow,避免每次 render/计时都让全部窗口重渲染。
  // 点窗口任意处即激活(active);additive(⌘/Ctrl/Shift 点)= 加入多选集合群发,否则单选替换。不抢焦点,保留 body 内文字选择
  const activateWorker = useCallback((id: string, additive: boolean) => {
    if (!id) return;
    setMode('single'); setMentionOpen(false);
    setSelectedIds((prev) => additive
      ? (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id])
      : (prev.length === 1 && prev[0] === id ? prev : [id]));
  }, []);
  const onHover = useCallback((id: string, h: boolean) => setHoverId((cur) => (h ? id : cur === id ? null : cur)), []);
  const startMove = useCallback((e: React.PointerEvent, id: string) => {
    e.stopPropagation(); e.preventDefault(); const w = workersRef.current.find((x) => x.id === id); if (!w) return;
    dragRef.current = { kind: 'move', id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false };
  }, []);
  const startResize = useCallback((e: React.PointerEvent, id: string) => {
    e.stopPropagation(); e.preventDefault(); const w = workersRef.current.find((x) => x.id === id); if (!w) return;
    dragRef.current = { kind: 'resize', id, sx: e.clientX, sy: e.clientY, ox: w.x, oy: w.y, ow: w.w, oh: w.h, moved: false };
  }, []);

  const onPointerDownBg = (e: React.PointerEvent) => { dragRef.current = { kind: 'pan', sx: e.clientX, sy: e.clientY, ox: pan.x, oy: pan.y, ow: 0, oh: 0, moved: false }; };
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = dragRef.current; if (!d) return;
      const dx = e.clientX - d.sx, dy = e.clientY - d.sy, z = zoomRef.current;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) { d.moved = true; window.getSelection?.()?.removeAllRanges(); }   // 拖动(移动/缩放)时清掉误产生的文字选区
      if (d.kind === 'pan') setPan({ x: d.ox + dx, y: d.oy + dy });
      else if (d.kind === 'move') setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, x: d.ox + dx / z, y: d.oy + dy / z } : w));
      else setWorkers((prev) => prev.map((w) => w.id === d.id ? { ...w, w: Math.max(MIN_W, d.ow + dx / z), h: Math.max(MIN_H, d.oh + dy / z) } : w));
    };
    const up = () => {
      const d = dragRef.current;
      if (d && !d.moved) { if (d.kind === 'move' && d.id) inputRef.current?.focus(); else if (d.kind === 'pan') setSelectedIds([]); }
      dragRef.current = null;
    };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
  }, []);

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

  // 发完立刻催目标 window 的 CurrentHistoryView 刷新几次(覆盖 agent 收到 tmux 输入→起一轮的时间),
  // 不用等满 2.5s 的 idle 轮询,消息能很快出现在窗口里。
  const nudgeHistory = (ids: string[]) => {
    [120, 500, 1100, 2000].forEach((ms) => window.setTimeout(() => {
      ids.forEach((id) => window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId: id } })));
    }, ms));
  };
  const send = () => {
    const body = text.trim(); if (!body) return;
    if (mode === 'broadcast') {
      push({ kind: 'broadcast', text: body });
      const ids = workersRef.current.map((w) => w.id).filter((id) => id !== SELF);   // 广播不发给总控自己
      setWorkers((prev) => prev.map((w) => (w.id === SELF ? w : { ...w, status: 'working', startedAt: Date.now() })));
      ids.forEach((id) => { (apiService as any).sendCommand(id, body, true).catch(() => { /* pane 不可达忽略 */ }); });
      nudgeHistory(ids);
      setText(''); return;
    }
    // 定向 / 群发:发给选中的 window;未选则默认发给总控 w-10001(过总控)
    const ids = selectedIds.length ? selectedIds.slice() : [SELF];
    ids.forEach((id) => push({ kind: 'dispatch', to: id, text: body }));
    setWorkers((prev) => prev.map((w) => (ids.includes(w.id) ? { ...w, status: 'working', startedAt: Date.now() } : w)));
    ids.forEach((id) => (apiService as any).sendCommand(id, body, true).catch(() => { /* pane 不可达忽略 */ }));
    nudgeHistory(ids);
    setText('');
  };
  // 关键:输入法合成中(中文/日文等拼音选词)按 Enter 是确认候选词,绝不能当发送。
  // 用 isComposing / keyCode 229 排除,否则中文每次选词都会误触发送。
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && (e as any).keyCode !== 229) { e.preventDefault(); send(); }
  };
  const onChange = (v: string) => { setText(v); if (mode === 'single') setMentionOpen(/(^|\s)@$/.test(v)); };
  const pickMention = (w: Worker) => { setMode('single'); setSelectedIds((prev) => prev.includes(w.id) ? prev : [...prev, w.id]); setText((v) => v.replace(/@$/, '')); setMentionOpen(false); inputRef.current?.focus(); };
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
  const canSend = !!text.trim();   // 广播=全体;单聊有选中=定向/群发,无选中=默认发给总控

  return (
    <div data-id="office" className="absolute inset-0 bg-[#0A0A0A] text-zinc-300">
      {/* 左栏：指挥台 —— 默认折叠,作为左侧抽屉浮层;只放历史+成员库,发送已移到底部浮动栏 */}
      {commandOpen && (
        <div data-id="office-command-backdrop" className="absolute inset-0 z-40 bg-black/30" onPointerDown={() => setCommandOpen(false)} />
      )}
      <aside data-id="office-command" className={`absolute left-0 top-0 bottom-0 z-50 flex w-[340px] flex-col border-r border-white/[0.06] bg-[#0b0b0c] shadow-2xl transition-transform duration-200 ${commandOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-white/[0.06] px-4">
          <span className="relative">
            <span className="grid h-8 w-8 place-items-center rounded-full bg-gradient-to-br from-amber-400/40 to-amber-600/15 text-base ring-1 ring-amber-300/30">🏢</span>
            <span className="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full bg-emerald-400 ring-2 ring-[#0b0b0c]" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1 text-[13px] font-semibold text-zinc-100"><Crown className="h-3 w-3 text-amber-400" /> 办公室 · 总控</div>
            <div className="font-mono text-[11px] text-zinc-500">{SELF} · 在线 {online}/{workers.length}</div>
          </div>
          <button data-id="office-command-close" onClick={() => setCommandOpen(false)} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"><X className="h-4 w-4" /></button>
        </div>

        <div ref={chatRef} data-id="office-command-history" className="flex-1 space-y-3 overflow-auto px-3.5 py-3.5">
          {chat.map((m) => <CommandMsg key={m.id} m={m} byId={byId} />)}
        </div>

        {/* 成员库（候选 + 市场入口）—— 折叠区 */}
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

      </aside>

      {/* 画布（铺满） */}
      <main ref={canvasRef} data-id="office-canvas" className="absolute inset-0 overflow-hidden bg-[#060608]"
        onPointerDown={onPointerDownBg} onWheel={onWheel}>
        {/* 暗角增加纵深 */}
        <div className="pointer-events-none absolute inset-0 z-[1]" style={{ boxShadow: 'inset 0 0 160px 40px rgba(0,0,0,0.55)' }} />

        {/* 画布层:提升为独立合成层(will-change),拖动只 GPU 合成不重绘内容;点阵网格放进本层,随 transform 走 GPU,不再每帧重绘 background-position */}
        <div data-id="office-canvas-layer" className="absolute left-0 top-0 origin-top-left will-change-transform" style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}>
          <div data-id="office-grid" className="pointer-events-none absolute" style={{ left: -12000, top: -12000, width: 24000, height: 24000, backgroundImage: 'radial-gradient(circle, rgba(255,255,255,0.045) 1px, transparent 1px)', backgroundSize: '26px 26px' }} />
          {workers.map((w) => (
            <WorkerWindow key={w.id} w={w} now={w.status === 'working' ? now : 0}
              selected={selectedSet.has(w.id)} hovered={hoverId === w.id}
              onHover={onHover} onMoveStart={startMove} onResizeStart={startResize} onSelect={activateWorker} />
          ))}
        </div>

        {workers.length === 0 && (
          <div data-id="office-canvas-empty" className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 text-zinc-600">
            {!loaded ? (
              <><Loader2 className="h-6 w-6 animate-spin" /><span className="text-[13px]">加载团队…</span></>
            ) : (
              <><Users className="h-7 w-7 opacity-50" /><span className="text-[13px]">还没有成员 agent</span><span className="text-[11.5px] text-zinc-700">点左上「总控」打开成员库添加，或用 cicy-agent 起子 agent</span></>
            )}
          </div>
        )}

        <div data-id="office-canvas-topbar" className="absolute left-4 top-4 flex items-center gap-2 text-[11px]">
          <button data-id="office-command-toggle" onClick={() => setCommandOpen((v) => !v)} title="总控指挥台（历史 / 成员库）"
            className="pointer-events-auto inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-black/40 px-2.5 py-1 text-zinc-300 backdrop-blur transition-colors hover:border-white/20 hover:text-zinc-100"><PanelLeft className="h-3.5 w-3.5" /> 总控</button>
          <span className="pointer-events-none inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-black/40 px-2.5 py-1 text-zinc-300 backdrop-blur"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> 在线 {online}/{workers.length}</span>
        </div>

        <div data-id="office-canvas-controls" className="absolute bottom-4 right-4 flex flex-col gap-1.5">
          <button data-id="office-zoom-in" onClick={() => setZoom((z) => clamp(z * 1.15, Z_MIN, Z_MAX))} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Plus className="h-4 w-4" /></button>
          <button data-id="office-zoom-out" onClick={() => setZoom((z) => clamp(z / 1.15, Z_MIN, Z_MAX))} className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Minus className="h-4 w-4" /></button>
          <button data-id="office-zoom-reset" onClick={() => { setPan({ x: 0, y: 0 }); setZoom(1); }} title="复位" className="grid h-8 w-8 place-items-center rounded-lg border border-white/10 bg-[#16161a]/90 text-zinc-400 backdrop-blur transition-colors hover:text-zinc-100"><Maximize2 className="h-4 w-4" /></button>
          <div className="text-center text-[10px] text-zinc-600">{Math.round(zoom * 100)}%</div>
        </div>

        {/* 底部居中浮动指令栏 —— 唯一发送入口 */}
        <div data-id="office-prompt-dock" className="pointer-events-none absolute inset-x-0 bottom-5 z-[70] flex justify-center px-4">
          <div data-id="office-prompt" className="pointer-events-auto relative w-full max-w-2xl rounded-2xl border border-white/10 bg-[#121214]/95 p-2 shadow-2xl backdrop-blur"
            onPointerDown={(e) => e.stopPropagation()} onWheel={(e) => e.stopPropagation()}
            onClick={() => inputRef.current?.focus()}>
            {/* 顶部：目标 / 模式 —— 标签条(非输入区),用分隔线和下方输入框区分 */}
            <div data-id="office-prompt-meta" className="mb-2 flex items-center gap-2 border-b border-white/[0.07] px-1 pb-2">
              <div data-id="office-target" className="flex min-h-[24px] min-w-0 flex-1 flex-wrap items-center gap-1.5">
                {mode === 'broadcast' ? (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-2.5 py-1 text-[12px] text-amber-200"><Megaphone className="h-3 w-3" /> 广播 · 全体（{workers.filter((w) => w.id !== SELF).length}）</span>
                ) : selectedWorkers.length === 0 ? (
                  <span className="inline-flex items-center gap-1 text-[12px] text-zinc-500"><Crown className="h-3.5 w-3.5 text-amber-400/80" /> 发给总控 {SELF} · 点 worker(Shift 多选)可定向群发</span>
                ) : (<>
                  {selectedWorkers.length > 1 && <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-medium text-sky-200">群发 {selectedWorkers.length}</span>}
                  {selectedWorkers.map((sw) => (
                    <span key={sw.id} data-id={`office-target-chip-${sw.id}`} className="inline-flex items-center gap-1.5 rounded-full border border-sky-400/40 bg-sky-500/15 py-1 pl-1 pr-1.5 text-[12px] text-sky-100 ring-1 ring-sky-400/30">
                      <Avatar model={sw.model} agentType={sw.agentType} name={sw.name} size={18} status={sw.status} />
                      <span className="font-mono text-[11px] text-sky-200/80">{sw.id}</span>
                      <button data-id={`office-target-remove-${sw.id}`} onClick={() => setSelectedIds((prev) => prev.filter((x) => x !== sw.id))} className="rounded-full p-0.5 text-zinc-500 hover:bg-white/10 hover:text-zinc-200"><X className="h-3 w-3" /></button>
                    </span>
                  ))}
                  <button data-id="office-target-clear" onClick={() => setSelectedIds([])} className="ml-0.5 rounded px-1 text-[11px] text-zinc-600 hover:text-zinc-300">清空</button>
                </>)}
              </div>
              <div data-id="office-mode" className="inline-flex shrink-0 items-center gap-0.5 rounded-lg bg-white/[0.04] p-0.5 text-[12px]">
                <button data-id="office-mode-single" onClick={() => setMode('single')} className={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 transition-colors ${mode === 'single' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}><MessageSquare className="h-3.5 w-3.5" /> 单聊</button>
                <button data-id="office-mode-broadcast" onClick={() => { setMode('broadcast'); setMentionOpen(false); }} className={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 transition-colors ${mode === 'broadcast' ? 'bg-amber-500/20 text-amber-200' : 'text-zinc-500 hover:text-zinc-300'}`}><Megaphone className="h-3.5 w-3.5" /> 广播</button>
              </div>
            </div>
            {mentionOpen && mode === 'single' && (
              <div data-id="office-mention" className="absolute bottom-full left-0 mb-2 max-h-72 w-full overflow-auto rounded-xl border border-white/10 bg-[#16161a] shadow-2xl">
                {workers.map((w) => (
                  <button key={w.id} data-id={`office-mention-${w.id}`} onClick={() => pickMention(w)} className="flex w-full items-center gap-2.5 px-3 py-2 text-left hover:bg-white/[0.06]">
                    <Avatar model={w.model} agentType={w.agentType} name={w.name} size={24} status={w.status} /><span className="text-[13px] text-zinc-200">{w.name}</span><span className="ml-auto font-mono text-[11px] text-zinc-500">{w.id}</span>
                  </button>
                ))}
              </div>
            )}
            {/* 输入框:整块文本框,文字从左上开始、多行向下增长;发送键固定右下角(不挤压文字) */}
            <div className="relative rounded-xl border border-white/10 bg-[#0a0a0c] transition-colors focus-within:border-sky-400/50 focus-within:bg-[#0c0c0f]">
              <textarea ref={inputRef} data-id="office-input" rows={2} autoFocus value={text} onChange={(e) => onChange(e.target.value)} onKeyDown={onKeyDown}
                placeholder={mode === 'broadcast' ? '向全体广播…（Enter 发送，Shift+Enter 换行）' : selectedWorkers.length > 1 ? `群发给选中的 ${selectedWorkers.length} 个 worker…（Enter 发送）` : target ? `给 ${target.name} 派任务…（Enter 发送）` : '发给总控…（Enter 发送;点 worker / Shift 多选可定向）'}
                className="block max-h-40 min-h-[68px] w-full resize-none overflow-y-auto bg-transparent px-3 py-2.5 pr-12 align-top text-[13px] leading-6 text-zinc-100 outline-none placeholder:text-zinc-600" />
              <button data-id="office-send" onClick={send} disabled={!canSend} className={`absolute bottom-2 right-2 grid h-8 w-8 shrink-0 place-items-center rounded-lg text-white transition-all disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-zinc-600 ${mode === 'broadcast' ? 'bg-amber-500 hover:bg-amber-400' : 'bg-sky-500 hover:bg-sky-400'} ${canSend ? 'shadow-lg' : ''}`}>{mode === 'broadcast' ? <Megaphone className="h-4 w-4" /> : <Send className="h-4 w-4" />}</button>
            </div>
          </div>
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
    <div data-id={`office-msg-${m.id}`} className="flex items-start gap-2">
      {w && <Avatar model={w.model} agentType={w.agentType} name={w.name} size={22} />}
      <div className="min-w-0 flex-1">
        <div className="text-[11px] text-zinc-500">{w?.name ?? m.from}</div>
        <div className="inline-flex items-center gap-1 text-[12px] text-emerald-300"><CheckCircle2 className="h-3.5 w-3.5" /> {m.text}</div>
      </div>
      <span className="self-start font-mono text-[10px] text-zinc-700">{m.ts}</span>
    </div>
  );
});

const WorkerWindow = memo(function WorkerWindow({ w, now, selected, hovered, onHover, onMoveStart, onResizeStart, onSelect }: {
  w: Worker; now: number; selected: boolean; hovered: boolean;
  onHover: (id: string, h: boolean) => void; onMoveStart: (e: React.PointerEvent, id: string) => void; onResizeStart: (e: React.PointerEvent, id: string) => void; onSelect: (id: string, additive: boolean) => void;
}) {
  const working = w.status === 'working';
  const costStr = fmtCost(w.cost);
  const tokStr = fmtTokens(w.tokens);
  const ctxColor = w.ctx > 85 ? 'bg-rose-400' : w.ctx > 60 ? 'bg-amber-400' : 'bg-zinc-400/60';
  const ctxText = w.ctx > 85 ? 'text-rose-300' : w.ctx > 60 ? 'text-amber-300' : 'text-zinc-500';
  const [copied, setCopied] = useState(false);
  const copyId = (e: React.MouseEvent) => {
    e.stopPropagation();
    void copyTextToClipboard(w.id).then((ok) => { if (ok) { setCopied(true); window.setTimeout(() => setCopied(false), 1200); } });
  };

  return (
    <div data-id={`office-window-${w.id}`}
      onPointerDownCapture={(e) => onSelect(w.id, e.metaKey || e.ctrlKey || e.shiftKey)}
      onPointerEnter={() => onHover(w.id, true)} onPointerLeave={() => onHover(w.id, false)}
      className={`absolute flex flex-col overflow-hidden rounded-2xl border bg-[#0e0e11] transition-[box-shadow,transform,border-color] duration-150
        ${selected
          ? 'border-sky-400 ring-2 ring-sky-400/70 ring-offset-2 ring-offset-[#060608] -translate-y-0.5 shadow-2xl shadow-sky-500/30'
          : working
            ? 'border-amber-400/45 shadow-xl shadow-[0_0_22px_-4px_rgba(251,191,36,0.4)]'
            : hovered ? 'border-white/15 shadow-2xl' : 'border-white/[0.07] shadow-xl'}
        ${working || selected ? '' : 'opacity-[0.97]'}`}
      style={{ left: w.x, top: w.y, width: w.w, height: w.h, zIndex: selected ? 60 : hovered ? 40 : 10 }}>
      {/* 状态色条:工作中=亮黄脉动条 / 空闲=暗绿静止条 */}
      <div className={`w-full ${working ? 'h-1 bg-amber-400 animate-pulse' : 'h-1 bg-emerald-500/30'}`} />
      <div data-id={`office-window-header-${w.id}`} onPointerDown={(e) => onMoveStart(e, w.id)}
        className="flex shrink-0 cursor-grab select-none items-center gap-2.5 border-b border-white/[0.05] bg-white/[0.015] px-3 py-2 active:cursor-grabbing">
        <Avatar model={w.model} agentType={w.agentType} name={w.name} size={30} status={w.status} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1 truncate text-[13px] font-semibold text-zinc-100">{w.id === SELF && <Crown className="h-3 w-3 shrink-0 text-amber-400" />}<span className="truncate">{w.name}</span></span>
          <span className="flex items-center gap-1 font-mono text-[10.5px] text-zinc-500">
            <button data-id={`office-window-copyid-${w.id}`} onPointerDown={(e) => e.stopPropagation()} onClick={copyId}
              title="复制 agent id" className="inline-flex items-center gap-0.5 rounded px-0.5 transition-colors hover:bg-white/10 hover:text-zinc-300">
              {w.id}{copied ? <Check className="h-2.5 w-2.5 text-emerald-400" /> : <Copy className="h-2.5 w-2.5 opacity-50" />}
            </button>
          </span>
        </span>
        {/* 状态徽标:工作中(亮黄脉动)/ 空闲(暗绿) —— 一眼可辨 */}
        {working ? (
          <span data-id={`office-window-status-${w.id}`} className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-[10.5px] font-medium text-amber-300 ring-1 ring-amber-400/30">
            <Loader2 className="h-3 w-3 animate-spin" /> 工作中 · {elapsed(w.startedAt, now)}
          </span>
        ) : (
          <span data-id={`office-window-status-${w.id}`} className="inline-flex shrink-0 items-center gap-1 rounded-full bg-white/[0.04] px-2 py-0.5 text-[10.5px] text-zinc-500">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-400/70" /> 空闲
          </span>
        )}
        {/* model + 上下文用量 + 累计成本 —— 全放到 title 这一行 */}
        <span className="max-w-[150px] shrink-0 truncate rounded bg-white/[0.05] px-1.5 py-0.5 font-mono text-[10px] text-zinc-400" title={`模型 ${w.model}`}>{w.model || '—'}</span>
        <div data-id={`office-window-ctx-${w.id}`} className="flex shrink-0 items-center gap-1.5" title={`上下文 ${w.ctx}%${tokStr ? ` · ${tokStr} tok` : ''} · 窗口 ${w.ctxK}k`}>
          <span className="text-[10px] text-zinc-600">ctx</span>
          <div className="h-1 w-12 overflow-hidden rounded-full bg-white/10"><div className={`h-full rounded-full ${ctxColor} transition-all`} style={{ width: `${w.ctx}%` }} /></div>
          <span className={`text-[10px] tabular-nums ${ctxText}`}>{w.ctx}%</span>
        </div>
        {costStr && (
          <span data-id={`office-window-cost-${w.id}`} className="shrink-0 font-mono text-[10px] tabular-nums text-emerald-300/80" title={`累计成本 ${costStr}`}>
            {costStr}
          </span>
        )}
      </div>
      {/* 聊天历史:直接复用主仓的 CurrentHistoryView(current-history-list),仅隐藏 tool card,保证渲染一致 */}
      <div data-id={`office-window-body-${w.id}`} onWheel={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}
        className="min-h-0 flex-1 overflow-hidden">
        <CurrentHistoryView paneId={w.id} open hideTools agentType={w.agentType || agentTypeForModel(w.model)} />
      </div>
      {/* resize 抓手：迷你 + 低调,仅悬停时淡显 */}
      <div data-id={`office-window-resize-${w.id}`} onPointerDown={(e) => onResizeStart(e, w.id)}
        className={`absolute bottom-0.5 right-0.5 h-2.5 w-2.5 cursor-nwse-resize transition-opacity ${hovered ? 'opacity-100' : 'opacity-0'}`}
        style={{ background: 'linear-gradient(135deg, transparent 64%, rgba(255,255,255,0.22) 64%, rgba(255,255,255,0.22) 76%, transparent 76%)' }} />
    </div>
  );
});
