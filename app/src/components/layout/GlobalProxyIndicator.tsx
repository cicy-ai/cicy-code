import { useCallback, useEffect, useRef, useState } from 'react';
import { Globe, Loader2, RefreshCw, Zap, Plus } from 'lucide-react';
import apiService from '../../services/api';
import { TokenManager } from '../../services/tokenManager';

// 🌍 Global-proxy / exit-IP indicator. Lives on the right side of the Workspace
// top toolbar. Opening it (or hitting 测速) hits GET /api/proxy/exit-info to
// compare the box's exit IP when going through mihomo vs direct, and exposes a
// node switch backed by /api/proxy/list + /api/proxy/select.

const DEFAULT_GROUP = 'default_proxy_group';

type ExitGroup = {
  via?: 'both' | 'proxy' | 'direct' | string;
  ok?: boolean;
  ip?: string;
  area?: string;
  cc?: string;
  elapsed_ms?: number;
  error?: string;
};

type ExitInfo = {
  success?: boolean;
  match?: boolean;
  current?: string;
  groups?: ExitGroup[];
};

type ProxyGroup = {
  name: string;
  type?: string;
  now?: string;
  members?: string[];
  last_delay_ms?: number;
};

type ProxyList = {
  success?: boolean;
  groups?: ProxyGroup[];
  nodes?: Array<{ name: string; type?: string; last_delay_ms?: number }>;
};

function viaLabel(via?: string) {
  if (via === 'proxy') return '代理出口';
  if (via === 'direct') return '直连出口';
  return '出口 IP';
}

function memberLabel(member: string) {
  if (member === 'default_proxy' || member === 'DIRECT') return '直连';
  return member;
}

function ExitRow({ group, highlight }: { group: ExitGroup; highlight?: boolean }) {
  const failed = group.ok === false;
  const elapsed = typeof group.elapsed_ms === 'number' ? `${group.elapsed_ms}ms` : '';
  return (
    <div
      data-id="global-proxy-exit-row"
      className={`flex items-center justify-between gap-2 rounded-lg px-2.5 py-2 ${
        highlight
          ? 'bg-emerald-500/10 ring-1 ring-emerald-500/30'
          : 'bg-white/[0.03]'
      }`}
    >
      <div data-id="global-proxy-exit-row-main" className="min-w-0">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.12em] text-zinc-500">
          {highlight ? <Zap className="h-3 w-3 text-emerald-400" /> : <Globe className="h-3 w-3" />}
          <span>{viaLabel(group.via)}</span>
        </div>
        {failed ? (
          <div className="mt-0.5 truncate text-[12px] font-medium text-red-300">
            {group.error || '探测失败'}
          </div>
        ) : (
          <div className="mt-0.5 flex items-center gap-1.5">
            <span className="font-mono text-[12px] font-medium text-zinc-100">{group.ip || '--'}</span>
            {group.area ? <span className="truncate text-[11px] text-zinc-400">{group.area}</span> : null}
          </div>
        )}
      </div>
      {elapsed ? (
        <span className={`shrink-0 font-mono text-[11px] ${failed ? 'text-red-400/70' : 'text-zinc-500'}`}>{elapsed}</span>
      ) : null}
    </div>
  );
}

export default function GlobalProxyIndicator({ placement = 'below', onManageNodes }: { placement?: 'below' | 'right' | 'up'; onManageNodes?: () => void }) {
  const [open, setOpen] = useState(false);
  const [exitLoading, setExitLoading] = useState(false);
  const [exitInfo, setExitInfo] = useState<ExitInfo | null>(null);
  const [exitError, setExitError] = useState('');
  const [group, setGroup] = useState<ProxyGroup | null>(null);
  const [switching, setSwitching] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);

  const loadExit = useCallback(async () => {
    setExitLoading(true);
    setExitError('');
    try {
      const { data } = await apiService.getProxyExitInfo();
      if (data?.success === false) {
        setExitError('探测失败');
        setExitInfo(null);
      } else {
        setExitInfo(data as ExitInfo);
      }
    } catch {
      setExitError('探测失败');
      setExitInfo(null);
    } finally {
      setExitLoading(false);
    }
  }, []);

  const loadGroup = useCallback(async () => {
    try {
      const { data } = await apiService.getProxyList();
      const list = (data || {}) as ProxyList;
      if (list.success === false) {
        setGroup(null);
        return;
      }
      const groups = Array.isArray(list.groups) ? list.groups : [];
      const primary = groups.find((g) => g.name === DEFAULT_GROUP) || groups[0] || null;
      setGroup(primary);
    } catch {
      setGroup(null);
    }
  }, []);

  // Refresh data each time the popover opens.
  useEffect(() => {
    if (!open) return;
    void loadExit();
    void loadGroup();
  }, [open, loadExit, loadGroup]);

  // Outside-click + Escape dismiss, matching other Workspace popovers.
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const handleSelect = useCallback(async (member: string) => {
    if (switching) return;
    setSwitching(member);
    try {
      const { data } = await apiService.selectProxy(member, DEFAULT_GROUP);
      if (data?.success !== false) {
        const now = String(data?.now || member);
        setGroup((prev) => (prev ? { ...prev, now } : prev));
        // Switching the node changes the exit IP — re-probe to reflect it.
        await loadExit();
      }
    } catch {
      /* tolerated */
    } finally {
      setSwitching('');
    }
  }, [switching, loadExit]);

  const groups = Array.isArray(exitInfo?.groups) ? exitInfo!.groups! : [];
  const isMatch = exitInfo?.match === true;
  const members = Array.isArray(group?.members) && group!.members!.length > 0
    ? group!.members!
    : (group?.now ? [group.now] : []);
  const now = group?.now || '';

  return (
    <div ref={rootRef} data-id="global-proxy-indicator" className="relative">
      <button
        type="button"
        data-id="global-proxy-toggle"
        onClick={() => setOpen((v) => {
          // 拿不到 token 就别打开：every panel call is authed, so opening without
          // a token just 401s. Stay closed instead.
          if (!v && !TokenManager.getToken()) return false;
          return !v;
        })}
        className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${
          open ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'
        }`}
        title="全局代理 / 出口 IP"
        aria-label="全局代理 / 出口 IP"
      >
        <Globe className="h-4 w-4" />
      </button>

      {open ? (
        <div
          data-id="global-proxy-popover"
          className={`absolute z-[180] min-w-[280px] max-w-[320px] overflow-hidden rounded-xl border border-white/[0.08] bg-[#111113]/98 p-1.5 shadow-2xl backdrop-blur-xl ${
            placement === 'right'
              ? 'bottom-0 left-[calc(100%+10px)]'
              : placement === 'up'
                ? 'right-0 bottom-[calc(100%+8px)]'
                : 'right-0 top-9'
          }`}
        >
          <div data-id="global-proxy-header" className="flex items-center justify-between gap-2 px-2 py-1.5">
            <span className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.14em] text-zinc-400">
              <Globe className="h-3.5 w-3.5" />
              出口 IP
            </span>
            <button
              type="button"
              data-id="global-proxy-refresh"
              onClick={() => { void loadExit(); }}
              disabled={exitLoading}
              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-zinc-400 transition-colors hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-50"
            >
              {exitLoading ? <Loader2 className="h-3 w-3 animate-spin" /> : <RefreshCw className="h-3 w-3" />}
              测速
            </button>
          </div>

          <div data-id="global-proxy-exit-body" className="space-y-1.5 px-1 pb-1">
            {exitLoading && groups.length === 0 ? (
              <div className="flex items-center justify-center gap-2 rounded-lg bg-white/[0.03] px-2.5 py-4 text-[12px] text-zinc-500">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                正在探测…
              </div>
            ) : exitError ? (
              <div className="rounded-lg bg-red-500/10 px-2.5 py-3 text-[12px] text-red-300">{exitError}</div>
            ) : groups.length === 0 ? (
              <div className="rounded-lg bg-white/[0.03] px-2.5 py-3 text-[12px] text-zinc-500">暂无数据</div>
            ) : isMatch ? (
              <ExitRow group={groups[0]} />
            ) : (
              groups.map((g, idx) => (
                <ExitRow key={`${g.via || 'g'}-${idx}`} group={g} highlight={g.via === 'proxy'} />
              ))
            )}
          </div>

          <div data-id="global-proxy-nodes" className="mt-1 border-t border-white/[0.06] px-1 pt-1.5">
            <div className="px-1 pb-1 text-[10px] uppercase tracking-[0.12em] text-zinc-500">节点切换</div>
            {members.length === 0 ? (
              <div className="px-1 py-1.5 text-[12px] text-zinc-500">暂无可选节点</div>
            ) : (
              <div className="flex items-center gap-2 px-1 pb-1">
                <select
                  data-id="global-proxy-node-select"
                  value={now}
                  onChange={(e) => { void handleSelect(e.target.value); }}
                  disabled={!!switching}
                  className="min-w-0 flex-1 cursor-pointer rounded-md border border-white/[0.08] bg-white/[0.04] px-2 py-1 text-[12px] text-zinc-200 outline-none transition-colors hover:bg-white/[0.06] focus:border-white/[0.2] disabled:opacity-50"
                >
                  {members.map((member) => (
                    <option key={member} value={member} className="bg-[#111113] text-zinc-200">
                      {memberLabel(member)}
                    </option>
                  ))}
                </select>
                {switching ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-zinc-400" /> : null}
              </div>
            )}
            {onManageNodes ? (
              <button
                type="button"
                data-id="global-proxy-manage"
                onClick={() => { onManageNodes(); setOpen(false); }}
                className="mt-1 flex w-full items-center justify-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-1.5 text-[11px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="让当前 agent 用 cicy-mihomo skill 添加/管理节点"
              >
                <Plus className="h-3 w-3" />
                用 skill 添加 / 管理节点
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
