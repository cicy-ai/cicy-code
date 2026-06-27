import { useCallback, useEffect, useRef, useState } from 'react';
import { Globe, Loader2, RefreshCw, SlidersHorizontal } from 'lucide-react';
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

// Show each proxy-group member by its RAW mihomo name (cicy-gw-us, default_proxy,
// us_proxy, DIRECT, …). No relabeling: default_proxy and DIRECT used to BOTH be
// rendered as "直连", which made default_proxy_group show two identical "直连"
// entries. Raw names keep every member distinct.
function memberLabel(member: string) {
  return member;
}

// Compact exit-IP chip: shows the IP (mono, truncated) with the full value in
// title= so hover reveals it. Optional label (代理/直连) when proxy and direct
// egress differ. Falls back to a red "探测失败" when the probe had no IP.
function ExitIPBadge({ ip, label, error }: { ip: string; label?: string; error?: string }) {
  const failed = !ip;
  return (
    <span
      data-id="global-proxy-exit-ip"
      title={ip || error || ''}
      className={`inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-lg px-2.5 py-2 ${
        failed ? 'bg-red-500/10' : 'bg-white/[0.04]'
      }`}
    >
      {label ? <span className="shrink-0 text-[10px] uppercase tracking-[0.12em] text-zinc-500">{label}</span> : null}
      <span className={`truncate font-mono text-[12px] font-medium ${failed ? 'text-red-300' : 'text-zinc-100'}`}>
        {ip || (error ? '探测失败' : '--')}
      </span>
    </span>
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
      // Strictly bind the node switcher to default_proxy_group (worker/global-proxy
      // traffic flows through it). No groups[0] fallback — if it's absent we show
      // "暂无可选节点" rather than leaking some other group's nodes (e.g. a
      // chrome-profile-*-group with members the global switch must not touch).
      const primary = groups.find((g) => g.name === DEFAULT_GROUP) || null;
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

  // Escape dismiss. Outside-click is handled by the full-screen backdrop below —
  // a document pointerdown listener misses clicks landing inside iframes/webviews
  // (terminal frames), which is most of the workspace.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    return () => {
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
          data-id="global-proxy-backdrop"
          className="fixed inset-0 z-[179]"
          onPointerDown={() => setOpen(false)}
          onContextMenu={(e) => { e.preventDefault(); setOpen(false); }}
        />
      ) : null}

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
            ) : (
              (() => {
                const proxy = groups.find((g) => g.via === 'proxy');
                const direct = groups.find((g) => g.via === 'direct');
                const pIP = proxy?.ok ? (proxy.ip || '') : '';
                const dIP = direct?.ok ? (direct.ip || '') : '';
                // Same exit IP (proxy and direct egress identical, or backend
                // flagged match) → show ONE ip. Different → show both (ip1 ip2).
                // The full IP is always in title= so hover reveals it even when
                // the inline value is truncated (long IPv6).
                const same = isMatch || (!!pIP && pIP === dIP);
                if (same && (pIP || dIP)) {
                  return <ExitIPBadge ip={pIP || dIP} />;
                }
                return (
                  <div data-id="global-proxy-exit-ips" className="flex items-center gap-1.5 px-1">
                    <ExitIPBadge ip={pIP} label="代理" error={proxy?.error} />
                    <ExitIPBadge ip={dIP} label="直连" error={direct?.error} />
                  </div>
                );
              })()
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
                title="打开节点管理面板"
              >
                <SlidersHorizontal className="h-3 w-3" />
                管理节点
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
