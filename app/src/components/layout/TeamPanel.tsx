// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { createPortal } from 'react-dom';
import { Trans } from 'react-i18next';
import i18n from '../../i18n';
import { Users, Plus, X, MoreHorizontal, Trash2, RefreshCw, UserPlus, GitBranch, ChevronRight, ChevronDown, ClipboardList } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import type { SelectOptionAction } from '../ui/Select';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';
import { normalizeAgentType } from '../../lib/agentType';
import { metricsFromCurrentReply, type AgentLiveMetrics } from '../../lib/agentMetrics';
import { useApp } from '../../contexts/AppContext';
import { ModelTag } from '../../lib/modelTag';
import AgentAvatar from '../AgentAvatar';
import TipBelow from '../ui/TipBelow';
import Select from '../ui/Select';
import CreateAgentDialog, { CreateAgentValues } from '../CreateAgentDialog';
import ForkConfirmModal from './ForkConfirmModal';

interface Agent {
  pane_id: string;
  title?: string;
  agent_type?: string;
  role?: string;
  active?: number;
  machine_id?: number;
  machine_label?: string;
  source_kind?: string;
  source_ref?: string;
  use_custom_gateway?: boolean;
}

interface StatusInfo { status?: string; isThinking?: boolean; title?: string; }
interface Binding {
  id: number;
  pane_id: string;
  name: string;
  title?: string;
  status?: string;
  agent_type?: string;
  machine_id?: number;
  machine_label?: string;
  instance_label?: string;
  source_kind?: string;
  source_ref?: string;
}
interface Props {
  paneId: string;
  panes: Agent[];
  bindings: Binding[];
  statuses?: Record<string, StatusInfo>;
  onOpenInCurrentPane?: (paneId: string) => void;
  onLocatePane?: (paneId: string) => void;
  openedPaneIds?: string[];
  activePaneId?: string;
  onOpenSettingsPane?: (paneId: string) => void;
  onRefreshPanes: () => Promise<void>;
  onRefreshPoll: () => void;
  // Open a file (workspace-relative path) in the given agent's file editor.
  // Used by the fork-confirm modal to reveal the source's history files.
  onOpenAgentFile?: (paneId: string, relPath: string) => void;
  // Open the team roster (花名册) — the full bound/unbound agent table.
  onOpenRoster?: () => void;
  // When true, skip the fixed "current agent (master)" header card so the list
  // shows ONLY the bound members. Used by the audit view to render just the two
  // security agents without the w-1001 master card.
  hideMaster?: boolean;
}

// Live header metrics (status/model/context/cost) for every card in the panel.
//
// DUAL CHANNEL, push-first — never the old N×/api/agents/current-reply fan-out
// (N agents × every 3s would storm the server):
//   PRIMARY  — the chat WS poll_data push. `pushed` is Workspace's pollStatuses,
//              fed straight from the WS broadcast; the server now packs the full
//              header metrics into each statuses entry, so one push updates the
//              whole team with ZERO requests.
//   FALLBACK — only when the WS is disconnected OR its push has gone stale, a
//              SINGLE batched /current-reply-batch call (not N) at a slow cadence.
// sig-compare keeps unchanged agents referentially stable so cards don't churn.
const TEAM_METRICS_PUSH_STALE_MS = 12000;   // push older than this ⇒ allow a fallback poll
const TEAM_METRICS_FALLBACK_MS = 5000;      // fallback batch-poll cadence (1 request)

function useTeamLiveMetrics(wids: string[], pushed: Record<string, any>, wsConnected: boolean) {
  const [metrics, setMetrics] = useState<Record<string, AgentLiveMetrics>>({});
  const key = wids.join(',');
  const lastPushRef = useRef(0);

  const fold = useCallback((src: Record<string, any>, lookup: (wid: string) => any) => {
    setMetrics((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const wid of key.split(',').filter(Boolean)) {
        const d = lookup(wid);
        if (!d) continue;
        const m = metricsFromCurrentReply(d, prev[wid]);
        if (prev[wid]?.sig !== m.sig || prev[wid]?.model !== m.model) { next[wid] = m; changed = true; }
      }
      return changed ? next : prev;
    });
    void src;
  }, [key]);

  // PRIMARY: fold the WS-pushed statuses whenever they change. pushed is keyed by
  // full pane id (`<wid>:main.0`); tolerate either form.
  useEffect(() => {
    if (!pushed || !key) return;
    fold(pushed, (wid) => pushed[`${wid}:main.0`] || pushed[wid]);
    lastPushRef.current = Date.now();
  }, [pushed, key, fold]);

  // FALLBACK: ONE batched request, and ONLY when the push channel is down/stale.
  useEffect(() => {
    if (!key) return;
    let cancelled = false;
    const tick = async () => {
      if (document.hidden) return;
      const stale = Date.now() - lastPushRef.current > TEAM_METRICS_PUSH_STALE_MS;
      if (wsConnected && !stale) return;   // WS alive & fresh → no polling at all
      try {
        const res: any = await apiService.getAgentCurrentReplyBatch(key.split(',').filter(Boolean)).catch(() => null);
        const mp = res?.data?.metrics;
        if (cancelled || !mp || typeof mp !== 'object') return;
        fold(mp, (wid) => mp[wid]);
      } catch { /* agent 还没数据 → 保持现状 */ }
    };
    void tick();   // immediate seed when the push channel is cold/stale at mount
    const t = window.setInterval(tick, TEAM_METRICS_FALLBACK_MS);
    return () => { cancelled = true; window.clearInterval(t); };
  }, [key, wsConnected, fold]);

  return metrics;
}

// 廉价模型(deepseek 级,单轮 ~$0.0002)累计 cost 长期 < $0.05,旧阈值会把它们
// 全压成 "$0" 看着像"没成本"。按量级自适应精度:亚分成本也显示真值,只有真正的 0 才 "$0"。
const fmtCost = (cost: number) =>
  cost <= 0      ? '$0'
  : cost >= 100  ? `$${Math.round(cost)}`
  : cost >= 1    ? `$${cost.toFixed(2)}`
  : cost >= 0.05 ? `$${cost.toFixed(2)}`
  : cost >= 0.001 ? `$${cost.toFixed(3)}`
  :                `$${cost.toFixed(4)}`;

// 上下文用量圆环:一个"未满的圆"代替进度条。颜色阈值与全局一致(<50 绿 /<80 黄 /≥80 红)。
function CtxRing({ pct }: { pct: number }) {
  const r = 4.5;
  const c = 2 * Math.PI * r;
  // 低位用中性灰(不抢眼),只在值得注意时变色:>50 黄,>80 红。暗色调
  // (yellow-600 / red-700),和状态点一个亮度档。
  const color = pct > 80 ? '#b91c1c' : pct > 50 ? '#ca8a04' : '#71717a';
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" className="-rotate-90 shrink-0">
      <circle cx="6" cy="6" r={r} fill="none" stroke="rgba(255,255,255,0.10)" strokeWidth="2.5" />
      <circle cx="6" cy="6" r={r} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={`${Math.max(0.5, (pct / 100) * c)} ${c}`} strokeLinecap="round" />
    </svg>
  );
}

export default function TeamPanel({ paneId, panes = [], bindings = [], statuses = {}, onOpenInCurrentPane, onLocatePane, openedPaneIds = [], activePaneId, onOpenSettingsPane, onRefreshPanes, onRefreshPoll, onOpenAgentFile, onOpenRoster, hideMaster = false }: Props) {
  const [creating, setCreating] = useState(false);
  const [forkingId] = useState<string | null>(null);
  // Source pane id whose fork-confirm preview modal is open (null = closed).
  const [forkPreviewSrc, setForkPreviewSrc] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  // bottom-most cards: not enough room below the … button → flip dropdown upward
  const [menuDropUp, setMenuDropUp] = useState(false);
  // hover tooltip for menu items — portal-rendered to the RIGHT of the dropdown
  // (the left panel scroll container clips overflowing children, so an inline
  // absolutely-positioned tip would be swallowed; fixed+portal escapes that).
  const [menuTip, setMenuTip] = useState<{ key: string; x: number; y: number } | null>(null);
  const showMenuTip = (key: string) => (e: React.MouseEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setMenuTip({ key, x: r.right + 10, y: r.top + r.height / 2 });
  };
  const hideMenuTip = () => setMenuTip(null);
  // 每个菜单项的「这是什么 / 原理」hover 说明。
  const menuTipFor = (key: string): { title: string; desc: string } => {
    switch (key) {
      case 'unbind':
        return {
          title: i18n.t('unbind', { ns: 'teamPanel' }),
          desc: i18n.t('tipUnbind', { ns: 'teamPanel', defaultValue: '把这张卡从团队面板移除(解除绑定),不会停止或删除 agent 本身,之后可以重新绑定回来。' }),
        };
      case 'restart':
        return {
          title: i18n.t('restart', { ns: 'teamPanel' }),
          desc: i18n.t('tipRestart', { ns: 'teamPanel', defaultValue: '结束该 agent 的 CLI 进程,并在同一个 tmux 窗口重新拉起,工作目录和会话保留。适合 agent 卡死或升级后生效。' }),
        };
      case 'compact':
        return {
          title: i18n.t('menuCompact', { ns: 'teamPanel', defaultValue: '压缩对话 (/compact)' }),
          desc: i18n.t('tipCompact', { ns: 'teamPanel', defaultValue: '向 agent 发送 /compact:把较早的对话提炼成摘要后继续,释放上下文窗口、节省 token,关键信息不丢。' }),
        };
      case 'clear':
        return {
          title: i18n.t('menuClear', { ns: 'teamPanel', defaultValue: '清空对话 (/clear)' }),
          desc: i18n.t('tipClear', { ns: 'teamPanel', defaultValue: '向 agent 发送 /clear:清空全部对话历史、从零开始,不可恢复。点击后会先弹确认框。' }),
        };
      case 'fork':
        return {
          title: i18n.t('fork', { ns: 'teamPanel' }),
          desc: i18n.t('tipFork', { ns: 'teamPanel', defaultValue: '复制出一个新 agent:先用 agent-summary 把当前对话提炼成摘要,新 agent 启动后将其作为自己的「继承记忆」读入,并以「Fork of …」挂在本 agent 下面。适合把分支任务交给分身并行处理,互不占用上下文。' }),
        };
      case 'delete':
        return {
          title: i18n.t('delete', { ns: 'teamPanel' }),
          desc: i18n.t('tipDelete', { ns: 'teamPanel', defaultValue: '彻底删除该 agent:关闭其 tmux 窗口并移除绑定,不可恢复。' }),
        };
      default:
        return { title: '', desc: '' };
    }
  };
  const [dragOrder, setDragOrder] = useState<string[] | null>(null);
  const [draggingWid, setDraggingWid] = useState<string | null>(null);
  // Drop intent while dragging a node onto card `wid`: upper half = 'before'
  // (same level, insert before), lower half = 'child' (become wid's sub).
  const [dropTarget, setDropTarget] = useState<{ wid: string; mode: 'child' | 'before' } | null>(null);
  const { confirm, node: dialogsNode } = useDialogs();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  useEffect(() => {
    const closeMenu = () => setOpenMenuId(null);
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, []);
  const boundIds = new Set(bindings.map(b => shortId(b.name)));
  const available = panes.filter(a => {
    const sid = shortId(a.pane_id);
    return sid !== paneId && !boundIds.has(sid);
  });

  const bind = async (agentPaneId: string) => {
    try {
      await apiService.bindAgent({ pane_id: paneId, agent_name: shortId(agentPaneId) });
      onRefreshPoll();
    } catch {}
  };

  const unbind = async (binding: Binding) => {
    const removedPaneId = shortId(binding.name);
    const nextSelectedPaneId = (
      activePaneId && activePaneId !== removedPaneId
        ? activePaneId
        : bindings
            .map((item) => shortId(item.name))
            .find((id) => id && id !== removedPaneId)
    ) || paneId;
    try {
      await apiService.unbindAgent(binding.id);
      onOpenInCurrentPane?.(nextSelectedPaneId);
      onRefreshPoll();
    } catch {}
  };

  const createAndBind = async (values: CreateAgentValues) => {
    setCreating(true);
    try {
      // master_pane_id 让后端 create 时直接完成绑定 + 写入 master 引用到子 agent 的 CLAUDE.md/AGENTS.md。
      const { data } = await apiService.createPane({
        role: 'worker',
        title: values.title,
        agent_type: values.agent_type,
        allow_all_actions: values.allow_all_actions,
        use_custom_gateway: values.use_custom_gateway,
        use_proxy: values.use_proxy,
        master_pane_id: paneId,
        inherit_guidance: values.inherit_guidance,
        project_template: values.project_template,
        role_template: values.role_template,
        lang: values.lang,
      });
      if (data?.pane_id || data?.session) {
        setCreateDialogOpen(false);
        await onRefreshPanes();
        onRefreshPoll();
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: i18n.t('toastCreateBindFailed', { ns: 'teamPanel' }) }));
    } finally {
      setCreating(false);
    }
  };

  const getStatus = (id: string): StatusInfo => statuses[fullId(id)] || statuses[id] || {};

  // reply.json turn status: "completed" | "failed" | anything else = in flight
  // ("thinking"). Green when done, red when failed, pulsing yellow otherwise.
  // (textual status label retired — the live-metrics dot carries status now)

  const getName = (binding: Binding) => {
    const wid = shortId(binding.name);
    const s = getStatus(wid);
    return binding.title || s.title || panes.find(a => shortId(a.pane_id) === wid)?.title || wid;
  };

  const showToast = useCallback((detail: string) => {
    window.dispatchEvent(new CustomEvent('show-toast', { detail }));
  }, []);

  const restartPane = useCallback(async (wid: string, title: string, disabled?: boolean) => {
    if (disabled) {
      showToast(i18n.t('toastRestartUnsupported', { ns: 'teamPanel', title }));
      return;
    }
    const ok = await confirm({
      body: <Trans i18nKey="confirmRestart" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
    });
    if (!ok) return;
    try {
      await apiService.restartPane(wid);
      showToast(i18n.t('toastRestarting', { ns: 'teamPanel', title }));
      onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastRestartFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const deletePane = useCallback(async (binding: Binding, title: string) => {
    const wid = shortId(binding.name);
    const ok = await confirm({
      body: <Trans i18nKey="confirmDelete" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.unbindAgent(binding.id);
      await apiService.deletePane(wid);
      onOpenInCurrentPane?.(paneId);
      await onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastDeleteFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onOpenInCurrentPane, onRefreshPanes, onRefreshPoll, paneId, showToast]);
  const deleteUnboundPane = useCallback(async (agent: Agent) => {
    const wid = shortId(agent.pane_id);
    const title = agent.title || wid;
    const ok = await confirm({
      body: <Trans i18nKey="confirmDelete" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.deletePane(wid);
      await onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastDeleteFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const orderedBindings = useMemo(() => {
    if (!dragOrder || dragOrder.length === 0) return bindings;
    const byWid = new Map(bindings.map(b => [shortId(b.name), b] as const));
    const result: Binding[] = [];
    const seen = new Set<string>();
    for (const wid of dragOrder) {
      const b = byWid.get(wid);
      if (b && !seen.has(wid)) {
        result.push(b);
        seen.add(wid);
      }
    }
    for (const b of bindings) {
      const wid = shortId(b.name);
      if (!seen.has(wid)) result.push(b);
    }
    return result;
  }, [bindings, dragOrder]);

  const groupedBindings = useMemo(() => {
    const groups = new Map<string, { machineId?: number; machineLabel?: string; items: Binding[] }>();
    for (const binding of orderedBindings) {
      const key = binding.machine_id ? String(binding.machine_id) : 'local';
      if (!groups.has(key)) groups.set(key, { machineId: binding.machine_id, machineLabel: binding.instance_label || binding.machine_label, items: [] });
      groups.get(key)!.items.push(binding);
    }
    return Array.from(groups.values());
  }, [orderedBindings]);

  // Fork nesting: a fork carries source_kind='fork' + source_ref=<source wid>.
  // Hang each fork under its DIRECT parent so fork-of-fork renders as a real
  // multi-level tree (it used to be flattened onto the top-level ancestor).
  //
  // Only CHILD WORKERS nest. A fork whose title was renamed away from the
  // default "Fork of <src>" has been promoted to an independent worker — it
  // shows top-level even though the DB still records its fork origin (e.g. a
  // long-lived agent that happened to be created via fork). So a third level
  // can only appear under a node that is itself still a child worker, which is
  // exactly the "只有子 worker fork 出来的才出第三级" rule.
  //
  // When the direct parent isn't in the current list (deleted / not bound here)
  // the fork falls back to top-level so it never disappears.
  const { forksByParent, nestedWids } = useMemo(() => {
    const byWid = new Map(orderedBindings.map(b => [shortId(b.name), b] as const));
    const isFork = (b?: Binding) => !!b && String(b.source_kind || '') === 'fork' && !!b.source_ref;
    const byParent = new Map<string, Binding[]>();
    const nested = new Set<string>();
    for (const b of orderedBindings) {
      // Nest every node that carries an explicit fork parent — drag-to-reparent
      // sets source_kind='fork'+source_ref, and the tree should reflect it
      // faithfully (no title-based "promotion" that would pin a renamed node to
      // the top level and make a reparent look like it did nothing).
      if (!isFork(b)) continue;
      const parent = byWid.get(shortId(b.source_ref || ''));
      if (!parent) continue; // orphan fork (parent deleted/unbound) → stays top-level
      const parentWid = shortId(parent.name);
      if (parentWid === shortId(b.name)) continue; // self-loop guard
      if (!byParent.has(parentWid)) byParent.set(parentWid, []);
      byParent.get(parentWid)!.push(b);
      nested.add(shortId(b.name));
    }
    // Cycle guard: a wid that is somehow both nested and an ancestor of its own
    // parent chain would loop the recursive render; drop such edges to top-level.
    const reaches = (from: string, target: string, depth = 0): boolean => {
      if (depth > 16) return false;
      const kids = byParent.get(from) || [];
      return kids.some(k => shortId(k.name) === target || reaches(shortId(k.name), target, depth + 1));
    };
    for (const [parentWid, kids] of [...byParent.entries()]) {
      const safe = kids.filter(k => !reaches(shortId(k.name), parentWid));
      for (const k of kids) if (!safe.includes(k)) nested.delete(shortId(k.name));
      if (safe.length) byParent.set(parentWid, safe); else byParent.delete(parentWid);
    }
    return { forksByParent: byParent, nestedWids: nested };
  }, [orderedBindings, panes]);

  // Collapse state for tree nodes that have fork children. Persisted so the
  // tree keeps its shape across reloads. Default: expanded.
  const [collapsedWids, setCollapsedWids] = useState<Set<string>>(() => {
    try { return new Set<string>(JSON.parse(localStorage.getItem('cicy:team-panel-collapsed') || '[]')); } catch { return new Set<string>(); }
  });
  const toggleCollapsed = useCallback((wid: string) => {
    setCollapsedWids(prev => {
      const next = new Set(prev);
      if (next.has(wid)) next.delete(wid); else next.add(wid);
      try { localStorage.setItem('cicy:team-panel-collapsed', JSON.stringify([...next])); } catch {}
      return next;
    });
  }, []);
  // Subtree size (all descendants) — shown on a collapsed node so the hidden
  // fork count stays visible.
  const subtreeCount = useCallback(function count(wid: string, depth = 0): number {
    if (depth > 16) return 0;
    const kids = forksByParent.get(wid) || [];
    return kids.reduce((n, k) => n + 1 + count(shortId(k.name), depth + 1), 0);
  }, [forksByParent]);

  const handleReorderDrop = useCallback((groupKey: string, fromWid: string, toWid: string) => {
    if (!fromWid || !toWid || fromWid === toWid) return;
    const group = groupedBindings.find(g => (g.machineId ? String(g.machineId) : 'local') === groupKey);
    if (!group) return;
    const widsInGroup = group.items.map(b => shortId(b.name));
    const fromIdx = widsInGroup.indexOf(fromWid);
    const toIdx = widsInGroup.indexOf(toWid);
    if (fromIdx < 0 || toIdx < 0) return;
    const nextGroupOrder = [...widsInGroup];
    nextGroupOrder.splice(fromIdx, 1);
    nextGroupOrder.splice(toIdx, 0, fromWid);
    const overall = orderedBindings.map(b => shortId(b.name));
    const newOrder: string[] = [];
    let cursor = 0;
    for (const wid of overall) {
      if (widsInGroup.includes(wid)) {
        newOrder.push(nextGroupOrder[cursor++]);
      } else {
        newOrder.push(wid);
      }
    }
    setDragOrder(newOrder);
    void apiService.reorderAgents(paneId, newOrder).then(() => {
      onRefreshPoll();
    }).catch(() => {
      // best-effort: revert by clearing optimistic order
      setDragOrder(null);
    });
  }, [groupedBindings, orderedBindings, paneId, onRefreshPoll]);

  // Raw parent map straight from source_ref — MUST match the backend's cycle
  // check (agentIsDescendant walks source_ref). The rendered fork tree promotes
  // / drops some edges, so guarding on it disagrees with the server and lets a
  // doomed reparent through (→ 400 "would create a cycle" → toast). Guard on the
  // raw chain instead so impossible drops are blocked client-side.
  const rawParent = useMemo(() => {
    const m = new Map<string, string>();
    for (const b of bindings) {
      const w = shortId(b.name);
      const p = shortId((b as { source_ref?: string }).source_ref || '');
      if (p && p !== w) m.set(w, p);
    }
    return m;
  }, [bindings]);

  // Does `target` live in `root`'s subtree per raw source_ref? (i.e. target's
  // parent chain reaches root). Mirrors backend agentIsDescendant(target, root).
  const rawInSubtree = useCallback((root: string, target: string): boolean => {
    const seen = new Set<string>();
    for (let cur = target; cur && !seen.has(cur); cur = rawParent.get(cur) || '') {
      if (cur === root) return true;
      seen.add(cur);
    }
    return false;
  }, [rawParent]);

  // Drag-to-reparent: rewrite the moved agent's tree parent (source_ref) via the
  // reparent API, then refresh so the derived fork tree re-renders. newParentWid
  // === '' promotes to top-level.
  const handleReparentDrop = useCallback((childWid: string, newParentWid: string) => {
    if (!childWid || childWid === newParentWid) return;
    if (newParentWid && rawInSubtree(childWid, newParentWid)) return; // no cycles
    void apiService.reparentAgent(childWid, newParentWid, paneId).then(() => {
      onRefreshPanes?.();
      onRefreshPoll();
    }).catch(() => {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: i18n.t('toastReparentFailed', { ns: 'teamPanel', defaultValue: '移动失败' }) }));
    });
  }, [rawInSubtree, paneId, onRefreshPanes, onRefreshPoll]);

  const agentTypeById = useMemo(
    () => new Map(panes.map(agent => [shortId(agent.pane_id), normalizeAgentType(agent.agent_type)])),
    [panes]
  );
  // 网关/官方登录标识：use_custom_gateway 来自 /api/tmux/list。
  const gatewayById = useMemo(
    () => new Map(panes.map(agent => [shortId(agent.pane_id), !!agent.use_custom_gateway])),
    [panes]
  );

  // Current pane + every bound worker, deduped + sorted so the poll effect's
  // key is stable across reorders.
  const liveWids = useMemo(() => {
    const ids = [paneId, ...bindings.map(b => shortId(b.name))].filter(Boolean);
    return Array.from(new Set(ids)).sort();
  }, [paneId, bindings]);
  // Dual-channel metrics: WS poll_data push (via `statuses`) is primary; the hook
  // only batch-polls as fallback when the WS is down/stale.
  const { chatWsConnected } = useApp();
  const liveMetrics = useTeamLiveMetrics(liveWids, statuses, chatWsConnected);

  const currentAgent = useMemo(() => {
    const agent = panes.find(a => shortId(a.pane_id) === paneId);
    const status = getStatus(paneId);
    // 状态文字由指标行的彩点承担,副标题只留 id(+机器),model 由渲染层并入同一行。
    const subtitleParts: string[] = [paneId];
    if (agent?.machine_label) subtitleParts.push(agent.machine_label);
    return {
      title: agent?.title || status.title || paneId,
      agentType: normalizeAgentType(agent?.agent_type),
      status,
      subtitle: subtitleParts.join(' · '),
    };
  }, [paneId, panes, statuses]);

  const renderAgentCard = ({
    wid,
    title,
    agentType,
    gateway,
    subtitle,
    active = false,
    onClick,
    onRemove,
    onDelete,
    onRestart,
    onFork,
    forking = false,
    canRestart = true,
    groupKey,
    draggable = false,
    parentWid = '',
    nested = false,
    childCount = 0,
    collapsed = false,
    onToggleCollapse,
  }: {
    wid: string;
    parentWid?: string;
    title: string;
    agentType?: string;
    gateway?: boolean;
    status: StatusInfo;
    subtitle: string;
    active?: boolean;
    opened?: boolean;
    onClick: () => void;
    onRemove?: () => void;
    onDelete?: () => void;
    onRestart?: () => void;
    onFork?: () => void;
    forking?: boolean;
    onOpenSettings?: () => void;
    canRestart?: boolean;
    groupKey?: string;
    draggable?: boolean;
    nested?: boolean;
    childCount?: number;
    collapsed?: boolean;
    onToggleCollapse?: () => void;
  }) => (
    <div
      key={wid}
      data-id={`team-panel-worker-${wid}`}
      onClick={onClick}
      draggable={draggable}
      onDragStart={draggable ? (e) => {
        setDraggingWid(wid);
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', wid); } catch {}
      } : undefined}
      onDragOver={(e) => {
        // Any card is a drop target while a drag is in flight. Forbid dropping
        // onto self or into the dragged node's own subtree (would cycle).
        if (!draggingWid || draggingWid === wid || rawInSubtree(draggingWid, wid)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        const rect = e.currentTarget.getBoundingClientRect();
        // Upper half → 同级 (sibling, before this node); lower half → 子级 (child).
        const mode: 'child' | 'before' = (e.clientY - rect.top) > rect.height / 2 ? 'child' : 'before';
        setDropTarget((cur) => (cur && cur.wid === wid && cur.mode === mode) ? cur : { wid, mode });
      }}
      onDrop={(e) => {
        e.preventDefault();
        const fromWid = draggingWid || e.dataTransfer.getData('text/plain');
        const mode = (dropTarget && dropTarget.wid === wid) ? dropTarget.mode : 'before';
        setDraggingWid(null);
        setDropTarget(null);
        if (!fromWid || fromWid === wid) return;
        // Top-level sibling drop keeps the existing intra-level reorder (no-op for
        // nested fromWid — it just falls through to the reparent below).
        if (mode === 'before' && parentWid === '' && groupKey) {
          handleReorderDrop(groupKey, fromWid, wid);
        }
        // lower half → become this node's child; upper half → sibling (this node's parent).
        handleReparentDrop(fromWid, mode === 'child' ? wid : parentWid);
      }}
      onDragEnd={() => {
        setDraggingWid(null);
        setDropTarget(null);
      }}
      className={`w-full flex items-center gap-3 border rounded-xl transition-all group relative cursor-pointer ${
        nested ? 'mb-1.5 p-2' : 'mb-2 p-3'
      } ${
        active
          ? 'border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20'
          : 'bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]'
      } ${draggingWid === wid ? 'opacity-40' : ''}`}
    >
      {/* Drop indicator: a dot + horizontal bar. Indented one level for 'child'
          (子级), flush for 'before' (同级) — the indent communicates the target depth. */}
      {dropTarget?.wid === wid && draggingWid && draggingWid !== wid && (
        <div
          data-id={`team-panel-drop-line-${wid}`}
          className={`pointer-events-none absolute right-1 z-20 flex items-center ${dropTarget.mode === 'child' ? '-bottom-1 left-8' : '-top-1 left-0'}`}
        >
          <span className="h-2 w-2 shrink-0 rounded-full bg-blue-400 shadow-[0_0_0_2px_rgba(96,165,250,0.3)]" />
          <span className="h-0.5 flex-1 rounded-full bg-blue-400" />
        </div>
      )}
      <div
        data-id={`team-panel-worker-menu-${wid}`}
        className="absolute right-2 top-2 z-20"
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          data-id="team-panel-worker-menu-button"
          onClick={(e) => {
            // measure available space below the button; the dropdown is ~270px
            // tall fully populated, so flip upward when the card sits near the
            // bottom of the viewport (otherwise it gets clipped/swallowed).
            const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
            setMenuDropUp(window.innerHeight - rect.bottom < 290);
            setOpenMenuId(prev => prev === wid ? null : wid);
          }}
          className={`flex h-7 w-7 items-center justify-center rounded-lg transition-all cursor-pointer ${
            openMenuId === wid
              ? 'bg-white/[0.08] text-zinc-200'
              : 'text-zinc-700 opacity-0 group-hover:opacity-100 hover:bg-white/[0.05] hover:text-zinc-300'
          }`}
          title={i18n.t('menu', { ns: 'teamPanel' })}
        >
          <MoreHorizontal className="w-3.5 h-3.5" />
        </button>
        {openMenuId === wid ? (
          <div
            data-id="team-panel-worker-menu-dropdown"
            className={`absolute right-0 ${menuDropUp ? 'bottom-9' : 'top-9'} min-w-[220px] overflow-hidden whitespace-nowrap rounded-xl border border-white/[0.08] bg-[#111113]/98 p-1.5 shadow-2xl backdrop-blur-xl`}
          >
            {onRemove ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-unbind"
                onMouseEnter={showMenuTip('unbind')}
                onMouseLeave={hideMenuTip}
                onClick={() => {
                  setOpenMenuId(null);
                  setMenuTip(null);
                  onRemove();
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors cursor-pointer text-zinc-300 hover:bg-red-500/10 hover:text-red-300"
              >
                <X className="w-3.5 h-3.5 shrink-0" />
                <span data-id="team-panel-worker-menu-unbind-label">{i18n.t('unbind', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
            {normalizeAgentType(agentType) !== 'cicy' ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-restart"
                disabled={!onRestart || !canRestart}
                onMouseEnter={showMenuTip('restart')}
                onMouseLeave={hideMenuTip}
                onClick={() => {
                  if (!onRestart || !canRestart) return;
                  setOpenMenuId(null);
                  setMenuTip(null);
                  onRestart();
                }}
                className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors ${
                  onRestart && canRestart
                    ? 'cursor-pointer text-zinc-300 hover:bg-white/[0.06]'
                    : 'cursor-not-allowed text-zinc-600'
                }`}
              >
                <RefreshCw className="w-3.5 h-3.5 shrink-0" />
                <span data-id="team-panel-worker-menu-restart-label">{i18n.t('restart', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
            {/* /compact、/clear 已从此菜单移除 —— 这两个命令改由对话输入框的斜杠命令菜单
                (输入 `/` 弹出)触发,菜单里不再重复。 */}
            {/* Fork(分身):coding-CLI agent 走 agent-summary + 新 tmux pane 拉起
                CLI;cicy lite agent(如 w-1001 项目经理)没有终端,后端改走 headless
                路径 —— 摘要内容内嵌为新 agent 的第一条 user message(UI 折叠显示),
                in-process 跑首轮接管。两种形态都支持,入口对所有 agent 开放。 */}
            {onFork ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-fork"
                disabled={forking}
                onMouseEnter={showMenuTip('fork')}
                onMouseLeave={hideMenuTip}
                onClick={() => {
                  if (forking) return;
                  setMenuTip(null);
                  onFork();
                }}
                className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors ${
                  forking ? 'cursor-not-allowed text-zinc-600' : 'cursor-pointer text-zinc-300 hover:bg-white/[0.06]'
                }`}
              >
                {forking ? (
                  <RefreshCw className="w-3.5 h-3.5 shrink-0 animate-spin" />
                ) : (
                  <GitBranch className="w-3.5 h-3.5 shrink-0" />
                )}
                <span data-id="team-panel-worker-menu-fork-label">{forking ? i18n.t('toastForkStarted', { ns: 'teamPanel' }) : i18n.t('fork', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
            {onDelete ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-delete"
                onMouseEnter={showMenuTip('delete')}
                onMouseLeave={hideMenuTip}
                onClick={() => {
                  setOpenMenuId(null);
                  setMenuTip(null);
                  onDelete();
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-red-300 transition-colors cursor-pointer hover:bg-red-500/10 hover:text-red-200"
              >
                <Trash2 className="w-3.5 h-3.5 shrink-0" />
                <span data-id="team-panel-worker-menu-delete-label">{i18n.t('delete', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
      <div data-id="team-panel-worker-body" className="flex items-start gap-3 flex-1 min-w-0 text-left">
        {childCount > 0 && onToggleCollapse ? (
          <button
            type="button"
            data-id={`team-panel-worker-collapse-${wid}`}
            onClick={(e) => { e.stopPropagation(); onToggleCollapse(); }}
            onPointerDown={(e) => e.stopPropagation()}
            className="-ml-1.5 -mr-2 flex h-5 w-5 shrink-0 items-center justify-center self-center rounded text-zinc-600 transition-colors cursor-pointer hover:bg-white/[0.06] hover:text-zinc-300"
            title={collapsed
              ? i18n.t('treeExpand', { ns: 'teamPanel', defaultValue: '展开 {{n}} 个 fork', n: childCount })
              : i18n.t('treeCollapse', { ns: 'teamPanel', defaultValue: '收起' })}
          >
            {collapsed ? <ChevronRight className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
          </button>
        ) : (
          // No collapse chevron (leaf agent / no forks): keep the SAME footprint
          // the button would occupy (-ml-1.5 -mr-2 h-5 w-5 + the row's gap-3) so
          // the avatar lines up with cards that DO have a chevron. Without this
          // spacer, chevron-less cards' avatars sit ~18px to the left.
          <span aria-hidden data-id={`team-panel-worker-collapse-spacer-${wid}`} className="-ml-1.5 -mr-2 h-5 w-5 shrink-0 self-center" />
        )}
        <AgentAvatar
          agentType={agentType}
          title={title}
          dataId="team-panel-worker-agent-avatar"
          variant="panel"
        />
	        <div data-id="team-panel-worker-info" className="flex-1 min-w-0 pr-7">
	          <div data-id="team-panel-worker-title-row" className="flex items-center gap-1.5">
	            <h3 data-id="team-panel-worker-title" className={`text-sm font-medium truncate ${active ? 'text-blue-300' : 'text-zinc-300'}`}>{title}</h3>
	            {/* 网关标识：实心蓝点=本地网关，空心环=官方登录。刻意低调，悬停看说明。 */}
	            <span
	              data-id="team-panel-worker-gateway"
	              className={`h-1.5 w-1.5 shrink-0 rounded-full ${gateway ? 'bg-sky-400/60' : 'border border-zinc-600/60'}`}
	              title={gateway
	                ? i18n.t('gatewayBadgeOn', { ns: 'teamPanel', defaultValue: '网关模式（本地 AI Gateway）' })
	                : i18n.t('gatewayBadgeOff', { ns: 'teamPanel', defaultValue: '官方登录（直连）' })}
	            />
	            {collapsed && childCount > 0 ? (
	              <span
	                data-id={`team-panel-worker-collapsed-count-${wid}`}
	                className="flex shrink-0 items-center gap-0.5 rounded-full bg-white/[0.05] px-1.5 py-px font-mono text-[10px] text-zinc-500"
	                title={i18n.t('treeHiddenForks', { ns: 'teamPanel', defaultValue: '已收起 {{n}} 个 fork', n: childCount })}
	              >
	                <GitBranch className="h-2.5 w-2.5" />{childCount}
	              </span>
	            ) : null}
	          </div>
          {(() => {
            // 第二行(也是最后一行):状态点 · id(+机器) · model · 上下文圆环 · 成本。
            // 固定高度,数据未到时只有 id,到了原地补齐,不跳版。
            const m = liveMetrics[wid];
            return (
              <div data-id={`team-panel-worker-metrics-${wid}`} className={`mt-0.5 flex h-4 min-w-0 items-center gap-1.5 font-mono text-xs ${active ? 'text-blue-400/50' : 'text-zinc-600'}`}>
                <span
                  data-id="team-panel-worker-metrics-status"
                  className="relative flex h-2 w-2 shrink-0"
                  title={m?.working ? i18n.t('metricsWorking', { ns: 'teamPanel', defaultValue: '工作中' }) : i18n.t('metricsIdle', { ns: 'teamPanel', defaultValue: '空闲' })}
                >
                  {/* thinking = 黄 + ping;idle = 绿呼吸;未知(首拉前) = 灰 */}
                  {m?.working ? (
                    <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-yellow-600 opacity-60" />
                  ) : null}
                  <span className={`relative inline-flex h-2 w-2 rounded-full ${m ? (m.working ? 'bg-yellow-600' : 'bg-emerald-700') : 'bg-zinc-700'}`} />
                </span>
                <span data-id="team-panel-worker-subtitle" className="min-w-0 truncate">
                  {subtitle}
                </span>
                {m?.model ? <ModelTag model={m.model} className="shrink-0" /> : null}
                {m && m.ctx > 0 ? (
                  <span data-id="team-panel-worker-metrics-ctx" className="flex shrink-0 items-center" title={i18n.t('metricsContext', { ns: 'teamPanel', defaultValue: '上下文' }) + ` ${m.ctx}% / ${m.ctxK}k`}>
                    <CtxRing pct={m.ctx} />
                  </span>
                ) : null}
                {m && m.cost > 0 ? (
                  <span data-id="team-panel-worker-metrics-cost" className="shrink-0" title={i18n.t('metricsCost', { ns: 'teamPanel', defaultValue: '累计成本' })}>
                    {fmtCost(m.cost)}
                  </span>
                ) : null}
              </div>
            );
          })()}
        </div>
      </div>
    </div>
  );

  // One worker card from a binding. Shared by top-level rows and nested fork
  // sub-groups (nested = slimmer, non-draggable).
  const cardForBinding = (b: Binding, opts: { nested?: boolean; draggable?: boolean; parentWid?: string; groupKey?: string; childCount?: number; collapsed?: boolean; onToggleCollapse?: () => void }) => {
    const wid = shortId(b.name);
    const s = getStatus(wid);
    const subtitleParts: string[] = [wid];
    if (b.instance_label || b.machine_label) subtitleParts.push(b.instance_label || b.machine_label || '');
    return renderAgentCard({
      wid,
      title: getName(b),
      agentType: normalizeAgentType(b.agent_type) || agentTypeById.get(wid) || '',
      gateway: gatewayById.get(wid),
      status: s,
      subtitle: subtitleParts.join(' · '),
      active: activePaneId === wid,
      opened: openedPaneIds.includes(wid),
      draggable: !!opts.draggable,
      parentWid: opts.parentWid || '',
      nested: !!opts.nested,
      groupKey: opts.groupKey,
      childCount: opts.childCount || 0,
      collapsed: !!opts.collapsed,
      onToggleCollapse: opts.onToggleCollapse,
      onClick: () => {
        if (onLocatePane) { onLocatePane(wid); return; }
        if (onOpenInCurrentPane) { onOpenInCurrentPane(wid); return; }
        window.location.hash = `#/agent/${wid}`;
      },
      onRestart: () => restartPane(wid, getName(b)),
      onOpenSettings: () => onOpenSettingsPane?.(wid),
      canRestart: true,
      onFork: () => {
        // Open the fork-confirm preview instead of forking immediately; the fork
        // pane is created (and the prompt sent) only when the user clicks Send.
        // Activate the source first so its stack card is the visible one (only
        // the active card is rendered) — the modal anchors over that card.
        onOpenInCurrentPane?.(wid);
        setForkPreviewSrc(wid);
        setOpenMenuId(null);
      },
      forking: forkingId === wid,
      onRemove: async () => {
        const ok = await confirm({
          body: <Trans i18nKey="confirmUnbind" ns="teamPanel" values={{ name: getName(b) }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
          danger: true,
        });
        if (ok) unbind(b);
      },
      onDelete: () => deletePane(b, getName(b)),
    });
  };

  return (
    <div className="h-full w-full min-w-0 flex flex-col overflow-hidden" data-id="team-panel-root">
      {forkPreviewSrc ? (
        <ForkConfirmModal
          sourcePaneId={forkPreviewSrc}
          masterPaneId={paneId}
          onClose={() => setForkPreviewSrc(null)}
          onForked={() => { onRefreshPanes(); onRefreshPoll(); }}
          onOpenAgentFile={(pid, rel) => onOpenAgentFile?.(pid, rel)}
        />
      ) : null}
      {forkingId ? createPortal(
        <div data-id="team-panel-fork-loading" className="fixed inset-0 z-[9999] flex items-center justify-center bg-black/50 backdrop-blur-sm">
          <div className="flex flex-col items-center gap-3 rounded-2xl border border-white/[0.08] bg-[#141416] px-8 py-6 shadow-2xl">
            <RefreshCw className="w-8 h-8 text-blue-400 animate-spin" />
            <span className="text-sm text-zinc-200">{i18n.t('toastForkStarted', { ns: 'teamPanel' })}</span>
            <span className="text-xs text-zinc-500 font-mono">{forkingId}</span>
          </div>
        </div>,
        document.body
      ) : null}
      {/* 菜单项 hover 说明卡:portal+fixed 渲染到 dropdown 右侧 —— 左面板是
          overflow 滚动容器,内联 absolute 的 tip 会被吞,portal 不受裁切。
          pointer-events-none 避免它抢走菜单项的 hover。 */}
      {openMenuId && menuTip ? createPortal(
        <div
          data-id="team-panel-menu-tooltip"
          className="fixed z-[9999] w-[260px] -translate-y-1/2 rounded-xl border border-white/[0.08] bg-[#111113]/98 p-3 shadow-2xl backdrop-blur-xl pointer-events-none"
          style={{ left: menuTip.x, top: Math.min(Math.max(menuTip.y, 70), window.innerHeight - 70) }}
        >
          <div data-id="team-panel-menu-tooltip-title" className="mb-1 text-xs font-semibold text-zinc-200">{menuTipFor(menuTip.key).title}</div>
          <div data-id="team-panel-menu-tooltip-desc" className="text-xs leading-5 text-zinc-400 whitespace-normal">{menuTipFor(menuTip.key).desc}</div>
        </div>,
        document.body
      ) : null}
      {!hideMaster && <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 flex-shrink-0" data-id="team-panel-toolbar">
        <div data-id="team-panel-bind-select" className="flex-1 min-w-0">
        <Select
          options={available.map(a => ({
            value: a.pane_id,
            label: a.title || shortId(a.pane_id),
            sub: shortId(a.pane_id),
            icon: <AgentAvatar agentType={a.agent_type} title={a.title || shortId(a.pane_id)} variant="select" />,
            actions: [
              {
                id: 'delete',
                label: i18n.t('delete', { ns: 'teamPanel' }),
                icon: <Trash2 className="w-3.5 h-3.5" />,
                danger: true,
                onClick: () => deleteUnboundPane(a),
              },
            ] as SelectOptionAction[],
          }))}
          onChange={v => bind(v)}
          onOpenChange={open => { if (open) void onRefreshPanes(); }}
          placeholder={i18n.t('bindMemberPlaceholder', { ns: 'teamPanel' })}
          searchable
          className="flex-1"
          triggerIcon={<UserPlus className="w-3.5 h-3.5" />}
          dropdownMatchSelector='[data-id="left-panel-team-view"]'
        />
        </div>
        {onOpenRoster ? (
          <TipBelow label={i18n.t('rosterTitle', { ns: 'workspace', defaultValue: '团队花名册' })}>
            <button
              data-id="team-panel-open-roster"
              onClick={() => onOpenRoster()}
              className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-[var(--vsc-border)] bg-white/[0.02] text-zinc-400 transition-colors duration-150 hover:bg-white/[0.05] hover:border-white/[0.10] hover:text-zinc-200 cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25"
            >
              <ClipboardList className="w-3.5 h-3.5" />
            </button>
          </TipBelow>
        ) : null}
        <TipBelow label={i18n.t('createBindShortLabel', { ns: 'teamPanel' })}>
          <button
            data-id="team-panel-create-worker"
            onClick={() => setCreateDialogOpen(true)}
            disabled={creating}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-[var(--vsc-border)] bg-white/[0.02] text-zinc-400 transition-colors duration-150 hover:bg-white/[0.05] hover:border-white/[0.10] hover:text-zinc-200 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25"
          >
            {creating ? <Spinner size="xs" /> : <Plus className="w-3.5 h-3.5" />}
          </button>
        </TipBelow>
      </div>}

      <CreateAgentDialog
        open={createDialogOpen}
        submitting={creating}
        onClose={() => setCreateDialogOpen(false)}
        onSubmit={createAndBind}
        title={i18n.t('createTitle', { ns: 'teamPanel' })}
        submitLabel={i18n.t('createSubmit', { ns: 'teamPanel' })}
        emptyTitleOnAgentSelect={i18n.t('createEmptyTitleAgent', { ns: 'teamPanel' })}
        dialogClassName="w-[960px] max-w-[96vw]"
        agentTypeGridClassName="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3"
        masterAgentType={panes.find(p => shortId(p.pane_id) === paneId)?.agent_type}
      />


      <div className="flex-1 min-w-0 overflow-x-hidden overflow-y-auto hide-scrollbar select-none" data-id="team-panel-worker-list">
        {!hideMaster && <div className="p-1.5 border-b border-[var(--vsc-border)]" data-id="team-panel-current-agent">
          {renderAgentCard({
            wid: paneId,
            title: currentAgent.title,
            agentType: currentAgent.agentType,
            gateway: gatewayById.get(paneId),
            status: currentAgent.status,
            subtitle: currentAgent.subtitle,
            active: (activePaneId || paneId) === paneId,
            onClick: () => {
              if (onLocatePane) {
                onLocatePane(paneId);
                return;
              }
              if (onOpenInCurrentPane) {
                onOpenInCurrentPane(paneId);
                return;
              }
              window.location.hash = `#/agent/${paneId}`;
            },
            onRestart: () => restartPane(paneId, currentAgent.title),
            onOpenSettings: () => onOpenSettingsPane?.(paneId),
            canRestart: true,
            onFork: () => {
              onOpenInCurrentPane?.(paneId);
              setForkPreviewSrc(paneId);
              setOpenMenuId(null);
            },
          })}
        </div>}
        {bindings.length > 0 ? (
          <div className="flex w-full min-w-0 flex-col" data-id="team-panel-groups">
            {groupedBindings.map(group => {
              const groupKey = group.machineId ? String(group.machineId) : 'local';
              return (
              <div
                key={groupKey}
                style={{ padding: 4 }}
                className={group.machineId ? 'border-b border-[var(--vsc-border)]' : ''}
                data-id={`team-panel-group-${group.machineLabel || group.machineId || 'local'}`}
              >

                {group.items
                  .filter(b => !nestedWids.has(shortId(b.name)))
                  .map(b => {
                    // Recursive multi-level fork tree: each node renders its card +
                    // (when expanded) its DIRECT fork children, to any depth. Only
                    // top-level rows stay draggable (reorder is a top-level concept).
                    const renderTreeNode = (node: Binding, depth: number, parentWid: string): React.ReactElement => {
                      const wid = shortId(node.name);
                      const childForks = forksByParent.get(wid) || [];
                      const collapsed = collapsedWids.has(wid);
                      return (
                        <div key={`tw-${wid}`} data-id={depth === 0 ? `team-panel-worker-wrap-${wid}` : `team-panel-worker-subtree-${wid}`}>
                          {cardForBinding(node, {
                            nested: depth > 0,
                            draggable: true,
                            parentWid,
                            groupKey: depth === 0 ? groupKey : undefined,
                            childCount: childForks.length > 0 ? subtreeCount(wid) : 0,
                            collapsed,
                            onToggleCollapse: childForks.length > 0 ? () => toggleCollapsed(wid) : undefined,
                          })}
                          {childForks.length > 0 && !collapsed ? (
                            <div
                              data-id={`team-panel-worker-forks-${wid}`}
                              className={depth === 0 ? 'ml-5 mb-2 pl-2' : 'ml-4 mb-1.5 pl-1'}
                            >
                              {childForks.map((fb, i) => {
                                const isLast = i === childForks.length - 1;
                                return (
                                  <div
                                    key={`fk-${shortId(fb.name)}`}
                                    data-id={`team-panel-worker-fork-${shortId(fb.name)}`}
                                    className="relative pl-4"
                                  >
                                    {/* 竖折线:竖干 + 拐角横线(最后一个用 └ 竖线只到拐角,其余 ├ 贯通) */}
                                    <span
                                      aria-hidden
                                      className="absolute left-0 top-0 w-px bg-white/[0.14]"
                                      style={{ height: isLast ? '1.4rem' : 'calc(100% + 0.375rem)' }}
                                    />
                                    <span aria-hidden className="absolute left-0 top-[1.4rem] h-px w-3 bg-white/[0.14]" />
                                    {renderTreeNode(fb, depth + 1, wid)}
                                  </div>
                                );
                              })}
                            </div>
                          ) : null}
                        </div>
                      );
                    };
                    return renderTreeNode(b, 0, '');
                  })}
              </div>
              );
            })}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600" data-id="team-panel-empty">
            <Users className="w-8 h-8 mb-2 opacity-20" />
            <p className="text-sm">{i18n.t('emptyState', { ns: 'teamPanel' })}</p>
          </div>
        )}
      </div>
      {dialogsNode}
    </div>
  );
}
