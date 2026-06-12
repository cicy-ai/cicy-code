// BrowserWindowsPanel — unified "browser windows" view for the two profile
// backends (Chrome profiles + Electron sessions). It mirrors the unified
// profile standard exposed by cicy-desktop's RPC tools:
//   electron: electron_list_profiles · get_windows · electron_screenshot
//   chrome  : chrome_list_profiles  · chrome_get_targets · chrome_cdp_call(Page.captureScreenshot)
//
// Flow: pick a backend tab → list its profiles → click a profile to expand and
// fetch the live windows/tabs for that profile, each rendered with a live
// screenshot thumbnail. All data comes through window.electronRPC, which is only
// injected when this SPA runs inside cicy-desktop — outside it we show a hint.
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Chrome, Atom, RefreshCw, Loader2, ChevronRight, Camera,
  AlertCircle, Globe, MonitorOff,
} from 'lucide-react';
import { electronRPC } from '../../lib/speedup/rpc';
import { cn } from '../../lib/utils';

type Backend = 'chrome' | 'electron';

interface LoginRec { platform: string; account: string; addedAt?: string }
interface Profile {
  key: string;        // stable react/expand key
  backend: Backend;
  accountIdx: number;
  name: string;
  proxy: { url: string; enabled: boolean };
  running?: boolean;  // chrome: live debugger reachable
  meta?: string;      // chrome gmail / electron partition
  logins: LoginRec[];
}
interface WinItem {
  key: string;
  title: string;
  url: string;
  status: 'open' | 'closed';
  winId: number | null;   // electron BrowserWindow id (numeric, open only)
  targetWs?: string;      // chrome target webSocketDebuggerUrl
}

// electronRPC already collapses MCP {content:[{text}]} → a string. Parse it as
// JSON, surfacing the tool's "Error: ..." text as a thrown error.
async function rpcJSON(tool: string, args: Record<string, any> = {}): Promise<any> {
  const out = await electronRPC(tool, args);
  if (typeof out !== 'string') return out;
  const s = out.trim();
  if (/^Error:/i.test(s)) throw new Error(s.replace(/^Error:\s*/i, ''));
  try { return JSON.parse(s); } catch { throw new Error(s || 'empty response'); }
}

async function loadProfiles(backend: Backend): Promise<Profile[]> {
  if (backend === 'electron') {
    const arr = await rpcJSON('electron_list_profiles');
    if (arr && (arr as any).error) throw new Error((arr as any).error);
    return (Array.isArray(arr) ? arr : []).map((p: any) => ({
      key: `electron-${p.accountIdx}`,
      backend: 'electron' as const,
      accountIdx: p.accountIdx,
      name: p.name || `electron-${p.accountIdx}`,
      proxy: p.proxy || { url: '', enabled: false },
      meta: p.partition,
      logins: Array.isArray(p.logins) ? p.logins : [],
    }));
  }
  const r = await rpcJSON('chrome_list_profiles');
  const list: any[] = (r && r.profiles) || [];
  return list.map((p: any) => ({
    key: `chrome-${p.accountIdx}`,
    backend: 'chrome' as const,
    accountIdx: p.accountIdx,
    name: p.profileKey || `account_${p.accountIdx}`,
    proxy: { url: p.proxy || '', enabled: !!p.proxy },
    running: !!(p.liveStatus && p.liveStatus.isRunning),
    meta: p.gmail || '',
    logins: [],
  }));
}

// List the live windows/tabs for one profile.
async function loadWindows(p: Profile): Promise<WinItem[]> {
  if (p.backend === 'electron') {
    const all = await rpcJSON('get_windows');
    const mine = (Array.isArray(all) ? all : []).filter((w: any) => w.accountIdx === p.accountIdx);
    return mine.map((w: any) => ({
      key: String(w.windowKey || w.id),
      title: w.title || w.url || '(无标题)',
      url: w.url || '',
      status: w.status === 'closed' ? 'closed' : 'open',
      winId: typeof w.id === 'number' ? w.id : null,
    }));
  }
  const r = await rpcJSON('chrome_get_targets', { accountIdx: p.accountIdx });
  if (r && r.error) throw new Error(r.error);
  const targets: any[] = (r && r.targets) || [];
  return targets
    .filter((t: any) => t.type === 'page')
    .map((t: any) => ({
      key: t.id,
      title: t.title || t.url || '(无标题)',
      url: t.url || '',
      status: 'open' as const,
      winId: null,
      targetWs: t.webSocketDebuggerUrl,
    }));
}

// Capture one window → a data: URL, or throw.
async function captureWindow(p: Profile, w: WinItem): Promise<string> {
  if (p.backend === 'electron') {
    if (w.winId == null) throw new Error('窗口已关闭，无法截图');
    const r = await rpcJSON('electron_screenshot', { win_id: w.winId, format: 'jpeg' });
    if (!r || !r.base64) throw new Error('截图失败');
    return r.base64; // already a data:image/... URL
  }
  if (!w.targetWs) throw new Error('缺少 target');
  const r = await rpcJSON('chrome_cdp_call', {
    accountIdx: p.accountIdx,
    method: 'Page.captureScreenshot',
    params: { format: 'jpeg', quality: 60 },
    target: w.targetWs,
  });
  if (r && r.error) throw new Error(r.error);
  const data = r?.result?.data;
  if (!data) throw new Error('截图失败');
  return `data:image/jpeg;base64,${data}`;
}

export default function BrowserWindowsPanel() {
  const hasBridge = typeof (window as any).electronRPC === 'function';
  const [backend, setBackend] = useState<Backend>('chrome');
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);

  const refresh = useCallback(async (b: Backend) => {
    if (!hasBridge) { setError('请在 cicy-desktop 中打开本页以管理浏览器窗口'); return; }
    setLoading(true); setError('');
    try {
      setProfiles(await loadProfiles(b));
    } catch (e: any) {
      setError(e?.message || String(e));
      setProfiles([]);
    } finally {
      setLoading(false);
    }
  }, [hasBridge]);

  useEffect(() => { refresh(backend); setExpanded(null); }, [backend, refresh]);

  const TABS: { k: Backend; label: string; icon: React.ReactNode }[] = [
    { k: 'chrome', label: 'Chrome', icon: <Chrome className="w-3.5 h-3.5" /> },
    { k: 'electron', label: 'Electron', icon: <Atom className="w-3.5 h-3.5" /> },
  ];

  return (
    <div data-id="BrowserWindowsPanel" className="absolute inset-0 flex flex-col bg-[#0A0A0A]">
      {/* backend tabs + refresh */}
      <div data-id="browser-windows-tabs" className="flex items-center gap-1 px-2 py-2 border-b border-white/[0.06] shrink-0">
        {TABS.map((tabItem) => (
          <button
            key={tabItem.k}
            data-id={`browser-windows-tab-${tabItem.k}`}
            onClick={() => setBackend(tabItem.k)}
            className={cn(
              'flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[12px] font-medium transition-colors cursor-pointer',
              backend === tabItem.k ? 'bg-white/[0.07] text-zinc-200' : 'text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.03]',
            )}
          >
            {tabItem.icon}{tabItem.label}
          </button>
        ))}
        <div className="flex-1" />
        <button
          data-id="browser-windows-refresh"
          onClick={() => refresh(backend)}
          disabled={loading}
          title="刷新"
          className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer disabled:opacity-50"
        >
          {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
        </button>
      </div>

      {/* body */}
      <div data-id="browser-windows-body" className="flex-1 overflow-auto">
        {error ? (
          <div data-id="browser-windows-error" className="flex items-start gap-2 m-3 p-3 rounded-lg bg-amber-500/[0.06] border border-amber-500/20 text-[12px] text-amber-300/90">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        ) : loading && profiles.length === 0 ? (
          <div className="flex items-center gap-2 p-4 text-[12px] text-zinc-500">
            <Loader2 className="w-3.5 h-3.5 animate-spin" /> 加载 profile…
          </div>
        ) : profiles.length === 0 ? (
          <div className="p-4 text-[12px] text-zinc-600">没有 {backend === 'chrome' ? 'Chrome' : 'Electron'} profile</div>
        ) : (
          <div data-id="browser-windows-profile-list" className="py-1">
            {profiles.map((p) => (
              <ProfileRow
                key={p.key}
                profile={p}
                expanded={expanded === p.key}
                onToggle={() => setExpanded((cur) => (cur === p.key ? null : p.key))}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ProfileRow({ profile, expanded, onToggle }: { profile: Profile; expanded: boolean; onToggle: () => void }) {
  const [windows, setWindows] = useState<WinItem[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const loadedFor = useRef<string>('');

  const load = useCallback(async () => {
    setLoading(true); setError('');
    try {
      setWindows(await loadWindows(profile));
    } catch (e: any) {
      setError(e?.message || String(e));
      setWindows([]);
    } finally {
      setLoading(false);
    }
  }, [profile]);

  // Fetch windows the first time this profile is expanded (re-fetch if reopened).
  useEffect(() => {
    if (expanded && loadedFor.current !== profile.key) {
      loadedFor.current = profile.key;
      load();
    }
    if (!expanded) loadedFor.current = '';
  }, [expanded, profile.key, load]);

  const proxyOn = profile.proxy?.enabled && profile.proxy?.url;

  return (
    <div data-id="BrowserProfileRow" className="border-b border-white/[0.04]">
      <button
        data-id="browser-profile-row-toggle"
        onClick={onToggle}
        className="w-full flex items-center gap-2 px-2.5 py-2 text-left hover:bg-white/[0.03] transition-colors cursor-pointer"
      >
        <ChevronRight className={cn('w-3.5 h-3.5 shrink-0 text-zinc-600 transition-transform', expanded && 'rotate-90')} />
        <span className={cn('w-1.5 h-1.5 rounded-full shrink-0', profile.backend === 'electron' || profile.running ? 'bg-emerald-500/80' : 'bg-zinc-600')} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span data-id="browser-profile-name" className="text-[13px] text-zinc-200 truncate">{profile.name}</span>
            <span className="text-[10px] text-zinc-600">#{profile.accountIdx}</span>
          </div>
          {(profile.meta || proxyOn) && (
            <div className="flex items-center gap-1.5 mt-0.5">
              {profile.meta && <span className="text-[10px] text-zinc-600 truncate">{profile.meta}</span>}
              {proxyOn && <span className="text-[10px] px-1 rounded bg-violet-500/10 text-violet-300/80 truncate" title={profile.proxy.url}>proxy</span>}
            </div>
          )}
        </div>
      </button>

      {expanded && (
        <div data-id="browser-profile-windows" className="px-2.5 pb-2.5">
          {loading ? (
            <div className="flex items-center gap-2 py-3 text-[12px] text-zinc-500">
              <Loader2 className="w-3.5 h-3.5 animate-spin" /> 读取窗口…
            </div>
          ) : error ? (
            <div className="flex items-start gap-2 py-2 text-[11px] text-amber-300/80">
              <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
              <span>{error}{profile.backend === 'chrome' ? '（启动该 Chrome profile 后刷新）' : ''}</span>
            </div>
          ) : !windows || windows.length === 0 ? (
            <div className="flex items-center gap-2 py-3 text-[12px] text-zinc-600">
              <MonitorOff className="w-3.5 h-3.5" /> 没有窗口
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {windows.map((w) => (
                <WindowCard key={w.key} profile={profile} win={w} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function WindowCard({ profile, win }: { profile: Profile; win: WinItem }) {
  const [shot, setShot] = useState<string>('');
  const [shooting, setShooting] = useState(false);
  const [err, setErr] = useState('');

  const capture = useCallback(async () => {
    if (shooting) return;
    setShooting(true); setErr('');
    try {
      setShot(await captureWindow(profile, win));
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setShooting(false);
    }
  }, [profile, win, shooting]);

  // Auto-capture an open window once when the card mounts.
  useEffect(() => {
    if (win.status === 'open') capture();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div data-id="BrowserWindowCard" className="rounded-lg border border-white/[0.06] bg-white/[0.02] overflow-hidden">
      <div data-id="browser-window-shot" className="relative aspect-[16/10] bg-black/40 flex items-center justify-center">
        {shot ? (
          <img src={shot} alt={win.title} className="w-full h-full object-cover object-top" />
        ) : shooting ? (
          <Loader2 className="w-5 h-5 animate-spin text-zinc-600" />
        ) : err ? (
          <div className="flex flex-col items-center gap-1 px-2 text-center text-[10px] text-zinc-600">
            <MonitorOff className="w-4 h-4" />{err}
          </div>
        ) : (
          <Globe className="w-5 h-5 text-zinc-700" />
        )}
        <button
          data-id="browser-window-capture"
          onClick={capture}
          disabled={shooting || win.status === 'closed'}
          title={win.status === 'closed' ? '窗口已关闭' : '截图'}
          className="absolute bottom-1.5 right-1.5 p-1.5 rounded-md bg-black/60 text-zinc-300 hover:text-white hover:bg-black/80 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {shooting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Camera className="w-3.5 h-3.5" />}
        </button>
        {win.status === 'closed' && (
          <span className="absolute top-1.5 left-1.5 text-[10px] px-1 rounded bg-zinc-700/70 text-zinc-300">已关闭</span>
        )}
      </div>
      <div className="px-2 py-1.5">
        <div data-id="browser-window-title" className="text-[12px] text-zinc-300 truncate" title={win.title}>{win.title}</div>
        {win.url && <div className="text-[10px] text-zinc-600 truncate" title={win.url}>{win.url}</div>}
      </div>
    </div>
  );
}
