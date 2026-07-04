// BrowserWindowsPanel — unified "browser windows" view across the two profile
// backends (Chrome profiles + Electron sessions), usable from ANY client
// (plain browser too), not just inside the Electron webview.
//
// Two pieces, laid out as two columns in Workspace:
//   • BrowserWindowsPanel (default export) — the LEFT panel: device selector,
//     Electron/Chrome backend tabs, and the profile list. Clicking a profile
//     selects it (does not expand inline).
//   • BrowserWindowsColumn (named export) — a middle column Workspace inserts
//     between the left panel and the mid panel; it shows the selected profile's
//     live windows (with screenshots) and lets you open a new window.
//
// Transport: every tool call is routed to the chosen cicy-desktop DEVICE via the
// chat-WS sync bridge —
//   POST /api/chat/push { client_id, type:'desktop_event',
//                         data:{type:'rpc_call', tool, args}, wait_ack:true }
// The server injects a requestId, pushes to that desktop client (which runs
// window.electronRPC and writes the result back), and returns the reply over
// plain HTTP. So the browser works the same as the desktop — pick the device
// (by deviceId) whose profiles you want to manage.
import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Chrome, Atom, RefreshCw, Loader2, ChevronRight, Camera,
  AlertCircle, Globe, MonitorOff, X, Plus, Settings, RotateCcw, Eye, Send, Code2, Pencil, Download, Smartphone,
  Monitor, Check, Wifi,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import i18n from '../../i18n';
import { cn } from '../../lib/utils';
import { listPhones, type MobileDevice, type MobileSel } from './MobileDevicesPanel';
import { detectRegion, detectOS } from '../../lib/speedup/detect';
import { execShell } from '../../lib/speedup/rpc';

// i18n helper for non-component (plain async/util) code paths — components use the
// useTranslation('layout') hook instead so they re-render on language change.
const tl = (key: string, opts?: Record<string, unknown>) => i18n.t(`layout:${key}`, opts || {}) as string;

type Backend = 'chrome' | 'electron' | 'android' | 'ios' | 'desktop';

interface Device {
  clientId: string;
  deviceId: string;
  platform: string;
  region: string;
  label: string;
  ip?: string;
  uptimeSec?: number;
  systemLanguage?: string;
}
// rich login record (matches profile-store schema; legacy {platform,account} is
// normalized to {name,username} by the backend before it reaches us)
interface LoginRec {
  url?: string;
  name: string;
  username?: string;
  email?: string;
  mobile?: string;
  twofa?: string;
  secondEmail?: string;
  note?: string;
  loginAt?: string;
  updatedAt?: string;
}
export interface IpInfo { ip: string; area: string; probedAt: string }
export interface Profile {
  key: string;
  backend: Backend;
  accountIdx: number;
  name: string;
  proxy: { url: string; enabled: boolean };
  running?: boolean;
  meta?: string;
  gmail?: string;
  logins: LoginRec[];
  ipInfo?: IpInfo;
}
interface WinItem {
  key: string;
  title: string;
  url: string;
  status: 'open' | 'closed';
  winId: number | null;   // electron BrowserWindow id (legacy; unused for tabs)
  webContentsId?: number; // electron tab id — the addressable unit (like a chrome target)
  windowKey?: string;     // electron persistent key (reopen closed windows)
  targetWs?: string;      // chrome target webSocketDebuggerUrl
}

// Run one cicy-desktop RPC tool on a specific device (clientId), synchronously,
// over the chat-WS sync bridge. Returns the raw text result (the tool's joined
// MCP text output) or throws the tool/transport error.
async function devicePushRaw(clientId: string, tool: string, args: Record<string, any> = {}): Promise<string> {
  let resp: any;
  try {
    resp = await apiService.chatPush({
      client_id: clientId,
      type: 'desktop_event',
      data: { type: 'rpc_call', tool, args },
      wait_ack: true,
      timeout_ms: 25000,
    });
  } catch (e: any) {
    const status = e?.response?.status;
    if (status === 404) throw new Error(tl('bwErrDeviceDisconnected'));
    if (status === 504) throw new Error(tl('bwErrDeviceTimeout'));
    throw new Error(e?.response?.data || e?.message || tl('bwErrRequestFailed'));
  }
  const inner = resp?.data?.data || {};
  if (inner.error) throw new Error(inner.error);
  const result = inner.result;
  return typeof result === 'string' ? result : (result == null ? '' : JSON.stringify(result));
}

// JSON-returning variant (most tools emit JSON text).
async function deviceCall(clientId: string, tool: string, args: Record<string, any> = {}): Promise<any> {
  const s = (await devicePushRaw(clientId, tool, args)).trim();
  if (/^Error:/i.test(s)) throw new Error(s.replace(/^Error:\s*/i, ''));
  try { return JSON.parse(s); } catch { throw new Error(s || 'empty response'); }
}

// exec_shell returns JSON { stdout, stderr, exitCode }; return its stdout.
async function deviceShell(clientId: string, command: string): Promise<string> {
  const r = await deviceCall(clientId, 'exec_shell', { command });
  if (r && r.error) throw new Error(r.error);
  return typeof r?.stdout === 'string' ? r.stdout : '';
}

// Mirror cicy-desktop's per-domain inject-script key (window-utils.js): the
// dom-ready injection file lives at ~/data/electron/extension/inject/<domain>.js
// where <domain> = host[_port] for localhost/IP, else the root domain.
function injectDomainForUrl(url: string): string | null {
  try {
    const u = new URL(url);
    const host = u.hostname;
    if (host === 'localhost' || /^\d+\.\d+\.\d+\.\d+$/.test(host)) return u.port ? `${host}_${u.port}` : host;
    const parts = host.split('.');
    return parts.length > 2 ? parts.slice(-2).join('.') : host;
  } catch { return null; }
}

// Resolve a device's $HOME, then build the absolute inject-file path for a domain.
async function resolveInjectPath(clientId: string, domain: string): Promise<string> {
  const home = (await deviceShell(clientId, 'printf %s "$HOME"')).trim();
  if (!home) throw new Error(tl('bwErrNoHome'));
  return `${home}/data/electron/extension/inject/${domain}.js`;
}
async function readInjectFile(clientId: string, absPath: string): Promise<string> {
  return await deviceShell(clientId, `cat ${JSON.stringify(absPath)} 2>/dev/null || true`);
}
async function writeInjectFile(clientId: string, absPath: string, content: string): Promise<void> {
  const r = await deviceCall(clientId, 'file_write', { path: absPath, content });
  if (r && r.error) throw new Error(r.error);
}

async function loadDevices(): Promise<Device[]> {
  const resp = await apiService.getChatClients();
  const arr: any[] = Array.isArray(resp?.data) ? resp.data : [];
  const electron = arr.filter((c) => c && c.isElectron && c.client_id);
  const byDevice = new Map<string, any>();
  for (const c of electron) {
    const key = c.device_id || c.client_id;
    const prev = byDevice.get(key);
    if (!prev || (c.uptime_sec ?? 1e9) < (prev.uptime_sec ?? 1e9)) byDevice.set(key, c);
  }
  return Array.from(byDevice.values()).map((c) => {
    const did = String(c.device_id || '');
    const region = String(c.ip_region || '').trim();
    const platform = String(c.platform || '');
    // Label: platform + device-id only (no region / no shortening), per request.
    const label = [platform || tl('bwDeviceFallback'), did || c.client_id.slice(-6)].filter(Boolean).join(' · ');
    return {
      clientId: c.client_id,
      deviceId: did,
      platform,
      region,
      label,
      ip: String(c.public_ip || '').trim(),
      uptimeSec: typeof c.uptime_sec === 'number' ? c.uptime_sec : undefined,
      systemLanguage: String(c.system_language || '').trim(),
    };
  });
}

async function loadProfiles(clientId: string, backend: Backend): Promise<Profile[]> {
  if (backend === 'electron') {
    // Union of: saved config profiles ∪ accounts that currently have windows ∪
    // account 0 (system-reserved: homepage / system windows — no config file).
    const [arrRaw, winsRaw] = await Promise.all([
      deviceCall(clientId, 'electron_list_profiles'),
      deviceCall(clientId, 'get_windows').catch(() => []),
    ]);
    if (arrRaw && (arrRaw as any).error) throw new Error((arrRaw as any).error);
    const arr: any[] = Array.isArray(arrRaw) ? arrRaw : [];
    const wins: any[] = Array.isArray(winsRaw) ? winsRaw : [];
    const byIdx = new Map<number, Profile>();
    for (const p of arr) {
      byIdx.set(p.accountIdx, {
        key: `electron-${p.accountIdx}`,
        backend: 'electron',
        accountIdx: p.accountIdx,
        name: `Profile${p.accountIdx}`,
        proxy: p.proxy || { url: '', enabled: false },
        meta: p.partition || `persist:sandbox-${p.accountIdx}`,
        gmail: p.gmail || p.accounts?.gmail?.account || p.accounts?.google?.account || '',
        logins: Array.isArray(p.logins) ? p.logins : [],
        ipInfo: p.ipInfo,
      });
    }
    const extra = new Set<number>([0]);
    for (const w of wins) { if (typeof w.accountIdx === 'number') extra.add(w.accountIdx); }
    for (const idx of extra) {
      if (byIdx.has(idx)) continue;
      byIdx.set(idx, {
        key: `electron-${idx}`,
        backend: 'electron',
        accountIdx: idx,
        name: `Profile${idx}`,
        proxy: { url: '', enabled: false },
        meta: `persist:sandbox-${idx}`,
        logins: [],
      });
    }
    return Array.from(byIdx.values()).sort((a, b) => a.accountIdx - b.accountIdx);
  }
  const r = await deviceCall(clientId, 'chrome_list_profiles');
  // chrome_list_profiles returns either an array or { profiles: [...] }
  const list: any[] = Array.isArray(r) ? r : ((r && r.profiles) || []);
  return list.map((p: any) => ({
    key: `chrome-${p.accountIdx}`,
    backend: 'chrome' as const,
    accountIdx: p.accountIdx,
    name: `Profile${p.accountIdx}`,   // 不要 account_<N>，与 Electron 命名一致
    // chrome_list_profiles returns proxy as a STRING; electron as {url,enabled}.
    proxy: typeof p.proxy === 'string' ? { url: p.proxy, enabled: !!p.proxy } : (p.proxy || { url: '', enabled: false }),
    running: !!(p.liveStatus && p.liveStatus.isRunning),
    meta: p.note || '',
    // Google identity for the row's secondary line. chrome_list_profiles resolves
    // `gmail` from the accounts map (desktop ≥ 2.1.214); resolve again client-side
    // from p.accounts as a fallback so it shows even on older fields.
    gmail: p.gmail || p.accounts?.gmail?.account || p.accounts?.google?.account || '',
    logins: Array.isArray(p.logins) ? p.logins : [],
    ipInfo: p.ipInfo,
  }));
}

// Create a new profile for the given backend (chrome_add_profile / electron_add_profile).
async function addProfile(clientId: string, backend: Backend): Promise<number | null> {
  const tool = backend === 'electron' ? 'electron_add_profile' : 'chrome_add_profile';
  const r = await deviceCall(clientId, tool, {});
  if (r && r.error) throw new Error(r.error);
  const idx = r?.accountIdx ?? r?.created?.accountIdx;
  return typeof idx === 'number' ? idx : null;
}

async function loadWindows(clientId: string, p: Profile): Promise<WinItem[]> {
  if (p.backend === 'electron') {
    // list the profile's TABS (BrowserView tabs of its tab-browser window), each
    // addressed by webContentsId — keeps the panel in sync with the tab window.
    const r = await deviceCall(clientId, 'electron_tabs', { accountIdx: p.accountIdx });
    if (r && r.error) throw new Error(r.error);
    const tabs: any[] = (r && r.tabs) || [];
    return tabs.map((t: any) => ({
      key: String(t.webContentsId),
      title: t.title || t.url || tl('bwUntitled'),
      url: t.url || '',
      status: 'open' as const,
      winId: null,
      webContentsId: typeof t.webContentsId === 'number' ? t.webContentsId : undefined,
    }));
  }
  // A stopped Chrome profile has no debugger → the call fails. That's NOT an
  // error to surface — just means "no tabs yet"; the empty state invites 新加标签
  // (which launches it). Only genuine non-connection errors would matter.
  const r = await deviceCall(clientId, 'chrome_get_targets', { accountIdx: p.accountIdx }).catch(() => null);
  if (!r || r.error) return [];
  const targets: any[] = (r && r.targets) || [];
  return targets
    .filter((t: any) => t.type === 'page')
    .map((t: any) => ({
      key: t.id,
      title: t.title || t.url || tl('bwUntitled'),
      url: t.url || '',
      status: 'open' as const,
      winId: null,
      targetWs: t.webSocketDebuggerUrl,
    }));
}

async function captureWindow(clientId: string, p: Profile, w: WinItem): Promise<string> {
  if (p.backend === 'electron') {
    if (w.webContentsId == null) throw new Error(tl('bwErrTabClosedShot'));
    // CDP-based — works for background tabs too (the inactive BrowserView blanks under capturePage)
    const r = await deviceCall(clientId, 'electron_tab_screenshot', { webContentsId: w.webContentsId, format: 'jpeg' });
    if (!r || !r.base64) throw new Error(tl('bwErrShotFailed'));
    return r.base64;
  }
  if (!w.targetWs) throw new Error(tl('bwErrNoTarget'));
  const r = await deviceCall(clientId, 'chrome_cdp_call', {
    accountIdx: p.accountIdx,
    method: 'Page.captureScreenshot',
    params: { format: 'jpeg', quality: 60 },
    target: w.targetWs,
  });
  if (r && r.error) throw new Error(r.error);
  const data = r?.result?.data;
  if (!data) throw new Error(tl('bwErrShotFailed'));
  return `data:image/jpeg;base64,${data}`;
}

// ── profile mutations (proxy + login records) — unified per backend ───────────
async function setProfileProxy(clientId: string, p: Profile, url: string): Promise<void> {
  const tool = p.backend === 'electron' ? 'set_account_proxy' : 'chrome_set_profile_proxy';
  const r = await deviceCall(clientId, tool, { accountIdx: p.accountIdx, proxy: (url || '').trim() });
  if (r && r.error) throw new Error(r.error);
}
async function listProfileLogins(clientId: string, p: Profile): Promise<LoginRec[]> {
  const tool = p.backend === 'electron' ? 'electron_profile_logins' : 'chrome_profile_logins';
  const r = await deviceCall(clientId, tool, { accountIdx: p.accountIdx });
  if (Array.isArray(r)) return r;
  if (r && Array.isArray(r.logins)) return r.logins;
  return [];
}
// upsert a rich login record (keyed by name); only the provided fields change
async function setProfileLogin(clientId: string, p: Profile, login: Partial<LoginRec> & { name: string }): Promise<void> {
  const tool = p.backend === 'electron' ? 'electron_profile_login_set' : 'chrome_profile_login_set';
  const r = await deviceCall(clientId, tool, { accountIdx: p.accountIdx, ...login });
  if (r && r.error) throw new Error(r.error);
}
async function removeProfileLogin(clientId: string, p: Profile, name: string): Promise<void> {
  const tool = p.backend === 'electron' ? 'electron_profile_login_rm' : 'chrome_profile_login_rm';
  const r = await deviceCall(clientId, tool, { accountIdx: p.accountIdx, platform: name });
  if (r && r.error) throw new Error(r.error);
}
// probe egress IP + area through the profile's proxy, store to config, return it
async function probeProfileIp(clientId: string, p: Profile): Promise<IpInfo | null> {
  const tool = p.backend === 'electron' ? 'electron_probe_ip' : 'chrome_probe_ip';
  const r = await deviceCall(clientId, tool, { accountIdx: p.accountIdx });
  if (r && r.error) throw new Error(r.error);
  return (r && r.ipInfo) || null;
}

interface ProfileDetail { name: string; note: string; proxyUrl: string; logins: LoginRec[]; ipInfo?: IpInfo }

// The `account <idx> <service> <id>` CLI records identities in the per-profile
// `accounts` map (gmail / google / groq / github → {account, password, totp}),
// a DIFFERENT ledger from the rich `logins` records (`login set`). The detail
// card only showed `logins`, so accounts looked "empty". Surface them: turn each
// account into a login row so the recorded gmail/groq/… show up too.
function accountsToLogins(accounts: any): LoginRec[] {
  if (!accounts || typeof accounts !== 'object') return [];
  return Object.entries(accounts).map(([svc, v]: [string, any]) => ({
    name: svc,
    username: (v && typeof v === 'object' ? v.account : v) || '',
    email: svc === 'gmail' || svc === 'google' ? ((v && v.account) || '') : '',
    twofa: v && v.totp ? '✓' : '',
  }));
}
// Merge manual logins with account-derived rows; a manual record of the same
// name wins (the user curated it), account rows fill the rest.
function mergeLoginsWithAccounts(logins: LoginRec[], accounts: any): LoginRec[] {
  const have = new Set((logins || []).map((l) => String(l.name).toLowerCase()));
  const extra = accountsToLogins(accounts).filter((r) => !have.has(String(r.name).toLowerCase()));
  return [...(logins || []), ...extra];
}
async function loadProfileDetail(clientId: string, p: Profile): Promise<ProfileDetail> {
  if (p.backend === 'electron') {
    const v = await deviceCall(clientId, 'electron_get_profile', { accountIdx: p.accountIdx });
    if (v && v.error) throw new Error(v.error);
    return {
      name: v?.name || p.name,
      note: typeof v?.note === 'string' ? v.note : '',
      proxyUrl: v?.proxy?.url || '',
      logins: mergeLoginsWithAccounts(Array.isArray(v?.logins) ? v.logins : [], v?.accounts),
      ipInfo: v?.ipInfo,
    };
  }
  const r = await deviceCall(clientId, 'chrome_get_profile', { accountIdx: p.accountIdx });
  if (r && r.error) throw new Error(r.error);
  const pc = r?.privateConfig || {};
  return {
    name: r?.profileKey || p.name,
    note: typeof pc.note === 'string' ? pc.note : '',
    proxyUrl: typeof pc.proxy === 'string' ? pc.proxy : (pc.proxy?.url || ''),
    logins: mergeLoginsWithAccounts(Array.isArray(pc.logins) ? pc.logins : [], pc.accounts),
    ipInfo: pc.ipInfo,
  };
}

// Persist name/note/proxy in one go (per-backend tools). Chrome name is its
// fixed profileKey, so it's read-only there — only note/proxy are written.
async function saveProfileConfig(clientId: string, p: Profile, cfg: { name?: string; note?: string; proxy?: string }): Promise<void> {
  if (p.backend === 'electron') {
    const metadata: Record<string, string> = {};
    if (cfg.name !== undefined) metadata.name = cfg.name;
    if (cfg.note !== undefined) metadata.description = cfg.note;
    if (Object.keys(metadata).length) {
      const r = await deviceCall(clientId, 'save_account_info', { accountIdx: p.accountIdx, metadata });
      if (r && r.error) throw new Error(r.error);
    }
    if (cfg.proxy !== undefined) await setProfileProxy(clientId, p, cfg.proxy);
    return;
  }
  if (cfg.note !== undefined) {
    const r = await deviceCall(clientId, 'chrome_set_profile_meta', { accountIdx: p.accountIdx, note: cfg.note });
    if (r && r.error) throw new Error(r.error);
  }
  if (cfg.proxy !== undefined) await setProfileProxy(clientId, p, cfg.proxy);
}

// New-window start page (data: URL). Under the "CiCy · New Window" heading it
// shows the agent-chrome 提示词 for THIS tab (client_id + accountIdx + live
// targetId), in a copyable box — 主人: h1 下面放提示词,删掉原来的网址输入 form。
// targetId is only known after Target.createTarget, so addWindow creates the tab
// then navigates it here with the real targetId baked in.
const startPagePromptText = (o: { clientId: string; accountIdx: number; targetId: string }) =>
  tl('bwPromptChrome', {
    clientId: o.clientId, accountIdx: o.accountIdx, targetId: o.targetId || '—',
    title: tl('bwStartTitle'), url: tl('bwStartPageData'),
    c: `agent-chrome --client ${o.clientId}`,
  });
const buildStartPageHtml = (o: { clientId: string; accountIdx: number; targetId: string }) => {
  const esc = startPagePromptText(o).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const copyLabel = tl('bwCopyPrompt', { defaultValue: '复制提示词' });
  const copiedLabel = tl('bwCopied', { defaultValue: '已复制' });
  return `<!doctype html><html lang="${i18n.language || 'en'}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>${tl('bwStartTitle')}</title>
<style>:root{color-scheme:dark}*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#0a0a0a;color:#e4e4e7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;padding:24px}
.wrap{width:min(640px,92vw);text-align:center}
.logo{width:56px;height:56px;margin:0 auto 18px;border-radius:16px;background:linear-gradient(135deg,#3b82f6,#8b5cf6);display:flex;align-items:center;justify-content:center;color:#fff;font-size:28px;font-weight:700}
h1{font-size:18px;font-weight:600;margin:0 0 16px}
pre{text-align:left;white-space:pre-wrap;word-break:break-word;background:#141414;border:1px solid rgba(255,255,255,.1);border-radius:12px;padding:16px 18px;color:#d4d4d8;font-size:13px;line-height:1.6;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;margin:0 0 14px}
button{background:rgba(255,255,255,.1);border:none;border-radius:10px;padding:10px 18px;color:#fff;font-size:14px;cursor:pointer}
button:hover{background:rgba(255,255,255,.18)}
.pid{display:inline-block;margin:0 0 16px;padding:4px 12px;border-radius:999px;background:rgba(139,92,246,.18);border:1px solid rgba(139,92,246,.35);color:#c4b5fd;font-size:13px;font-weight:600;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}</style></head>
<body><div class="wrap">
<div class="logo">&#10022;</div>
<h1>${tl('bwStartHeading')}</h1>
<div class="pid" data-id="chrome-start-profile-id">Profile #${o.accountIdx}</div>
<pre id="cicy-prompt">${esc}</pre>
<button id="cicy-copy" onclick="(function(b){var t=document.getElementById('cicy-prompt').textContent;function ok(){b.textContent='${copiedLabel}';setTimeout(function(){b.textContent='${copyLabel}'},1500)}function fb(){var a=document.createElement('textarea');a.value=t;a.style.position='fixed';a.style.opacity='0';document.body.appendChild(a);a.select();try{document.execCommand('copy')}catch(e){}document.body.removeChild(a);ok()}if(navigator.clipboard){navigator.clipboard.writeText(t).then(ok).catch(fb)}else{fb()}})(this)">${copyLabel}</button>
</div></body></html>`;
};
const startPageUrl = (o: { clientId: string; accountIdx: number; targetId: string }) => `data:text/html;charset=utf-8,${encodeURIComponent(buildStartPageHtml(o))}`;

// Open a new window/tab for a profile.
async function addWindow(clientId: string, p: Profile, url: string): Promise<void> {
  const u = (url || '').trim();
  if (p.backend === 'electron') {
    // Open as a TAB in the profile's tab browser (一个 profile 一个标签窗口),
    // not a separate BrowserWindow. Empty url → the tab browser's start page.
    const r = await deviceCall(clientId, 'electron_tab_open', { accountIdx: p.accountIdx, ...(u ? { url: u } : {}) });
    if (r && r.error) throw new Error(r.error);
    if (u) return; // user opened a real URL — no prompt page
    // Empty url → the cicyui://newtab start page. Show the agent-electron 提示词
    // (with THIS tab's live webContentsId) under "新标签页" + a copy button.
    // electron_tab_open returns manager.list() (active flag) → grab the new tab's
    // wcId, then inject the prompt via electron_tab_eval (the newtab page has no
    // preload on sandbox profiles, so the panel injects it after open).
    const tabs: any[] = (r && r.tabs) || [];
    const nt = tabs.find((t) => t.active) || tabs[tabs.length - 1];
    const wc = nt && nt.webContentsId;
    if (wc == null) return;
    const prompt = tl('bwPromptElectron', {
      clientId, accountIdx: p.accountIdx, wc,
      title: tl('bwStartPage', { defaultValue: '起始页' }), url: 'cicyui://newtab/',
      c: `agent-electron --client ${clientId}`,
    });
    const copyLabel = tl('bwCopyPrompt', { defaultValue: '复制提示词' });
    const copiedLabel = tl('bwCopied', { defaultValue: '已复制' });
    const js = `(function(){if(document.getElementById('cicy-prompt'))return;` +
      `var w=document.querySelector('.w')||document.body;` +
      `var pre=document.createElement('pre');pre.id='cicy-prompt';pre.textContent=${JSON.stringify(prompt)};` +
      `pre.style.cssText='text-align:left;white-space:pre-wrap;word-break:break-word;background:#2a2b2e;border:1px solid rgba(255,255,255,.1);border-radius:12px;padding:14px 16px;color:#cfd2d6;font-size:12px;line-height:1.6;font-family:ui-monospace,Menlo,Consolas,monospace;margin:18px auto 12px;max-width:560px';` +
      `var b=document.createElement('button');b.textContent=${JSON.stringify(copyLabel)};` +
      `b.style.cssText='background:rgba(255,255,255,.12);border:none;border-radius:10px;padding:9px 16px;color:#fff;font-size:13px;cursor:pointer';` +
      `b.onclick=function(){var t=pre.textContent;function ok(){b.textContent=${JSON.stringify(copiedLabel)};setTimeout(function(){b.textContent=${JSON.stringify(copyLabel)}},1500)}` +
      `function fb(){var a=document.createElement('textarea');a.value=t;a.style.position='fixed';a.style.opacity='0';document.body.appendChild(a);a.select();try{document.execCommand('copy')}catch(e){}document.body.removeChild(a);ok()}` +
      `if(navigator.clipboard){navigator.clipboard.writeText(t).then(ok).catch(fb)}else{fb()}};` +
      `w.appendChild(pre);w.appendChild(b);})()`;
    // small delay so cicyui://newtab has finished loading before we inject
    await new Promise((res) => setTimeout(res, 400));
    await deviceCall(clientId, 'electron_tab_eval', { webContentsId: wc, code: js }).catch(() => {});
    return;
  }
  // Chrome's own add-tab. Step 1: create the tab as about:blank and GET its
  // targetId. Try createTarget first (works whenever the debugger is up,
  // REGARDLESS of the possibly-stale p.running — that race made the 2nd add
  // re-launch+activate instead of creating a tab → "只能加一个" on Windows,
  // where Chrome 149's debugger comes up slower than macOS). If createTarget
  // fails (genuinely not running), launch the profile, then find the new tab.
  let targetId = '';
  const created = await deviceCall(clientId, 'chrome_cdp_call', {
    accountIdx: p.accountIdx, method: 'Target.createTarget', params: { url: 'about:blank' },
  }).catch((e: any) => ({ error: e?.message || String(e) }));
  if (created && !created.error && created.result && created.result.targetId) {
    targetId = created.result.targetId;
  } else {
    const r = await deviceCall(clientId, 'chrome_launch_profile', { accountIdx: p.accountIdx, url: 'about:blank' });
    if (r && r.error) throw new Error(r.error);
    const tg = await deviceCall(clientId, 'chrome_get_targets', { accountIdx: p.accountIdx }).catch(() => null);
    const pages = (((tg && tg.targets) || []) as any[]).filter((t) => t.type === 'page');
    targetId = (pages.find((t) => t.url === 'about:blank') || pages[pages.length - 1] || {}).id || '';
  }
  // Step 2: navigate the new tab to its destination — the user's URL if given,
  // else the start page carrying THIS tab's real targetId in the agent prompt.
  const dest = u || startPageUrl({ clientId, accountIdx: p.accountIdx, targetId });
  if (targetId) {
    await deviceCall(clientId, 'chrome_cdp_call', {
      accountIdx: p.accountIdx, method: 'Page.navigate', target: targetId, params: { url: dest },
    }).catch(() => {});
  }
}

// Recognize a "Chrome isn't installed" launch failure so the panel can show an
// install prompt instead of a raw error. Covers cicy-desktop's resolveChromeBinary
// message (mac/win: "Chrome/Chromium binary not found. Please configure
// chromeBinary…") and the Linux bare-command spawn failure (ENOENT on
// google-chrome/chromium).
function isChromeMissingError(msg?: string): boolean {
  if (!msg) return false;
  const s = msg.toLowerCase();
  return s.includes('binary not found')
    || s.includes('chrome/chromium')
    || s.includes('please configure chromebinary')
    || (s.includes('enoent') && (s.includes('chrome') || s.includes('chromium')));
}

// Open the official Chrome download page in the HOST's default browser via the
// electron bridge (exec_shell) — NOT a renderer anchor, which would open inside
// the cicy webview. Probe the region first (also over electron): CN gets the
// localized google.cn page (google.com/chrome is unreachable in CN), everyone
// else gets the global page. Falls back to window.open in a plain browser.
async function openOfficialChromeDownload(): Promise<void> {
  let url = 'https://www.google.com/chrome/';
  try {
    if ((await detectRegion()) === 'cn') url = 'https://www.google.cn/intl/zh-CN/chrome/';
  } catch { /* unknown region → keep global URL */ }
  const os = detectOS();
  const cmd =
    os === 'windows' ? `start "" "${url}"` :
    os === 'mac'     ? `open "${url}"` :
                       `xdg-open "${url}"`;
  try {
    await execShell(cmd);
  } catch {
    try { window.open(url, '_blank', 'noreferrer'); } catch { /* nothing else to do */ }
  }
}

// Build a prompt that hands one window to the agent, telling it which skill +
// identifiers to use so it can drive the window.
// Readable title/url for the prompt — the start page is a giant data: URL,
// useless as a "title", so collapse those to a clean label.
function tabLabel(w: WinItem): { title: string; url: string } {
  const isData = (s?: string) => !!s && s.startsWith('data:');
  const title = (w.title && !isData(w.title)) ? w.title : (isData(w.url) ? tl('bwStartPage') : (w.title || w.url || tl('bwUntitled')));
  const url = !w.url ? tl('bwEmptyUrl') : isData(w.url) ? tl('bwStartPageData') : (w.url.length > 90 ? w.url.slice(0, 90) + '…' : w.url);
  return { title, url };
}

function buildAgentPrompt(device: { clientId: string; deviceId?: string }, p: Profile, w: WinItem): string {
  const { title, url } = tabLabel(w);
  // client_id is the routing key — the agent sends rpc_call to it via
  // POST /api/chat/push (no deviceId→clientId lookup needed).
  const wc = w.webContentsId ?? '?';
  if (p.backend === 'electron') {
    const c = `agent-electron --client ${device.clientId}`;
    return tl('bwPromptElectron', { clientId: device.clientId, accountIdx: p.accountIdx, wc, title, url, c });
  }
  const c = `agent-chrome --client ${device.clientId}`;
  return tl('bwPromptChrome', { clientId: device.clientId, accountIdx: p.accountIdx, targetId: w.key, title, url, c });
}

type WinAction = 'open' | 'reload' | 'close';
// open  = bring the window/tab to front (or reopen a closed electron window)
// reload = reload its page
// close = close the window/tab
async function windowAction(clientId: string, p: Profile, w: WinItem, action: WinAction): Promise<void> {
  if (p.backend === 'electron') {
    if (action === 'open') {
      // Eye = bring the tab to front. If its webContents is gone (the tab
      // window was closed out-of-band, leaving the panel's list stale), the
      // activate fails with "tab not found" — recover by reopening the tab
      // window at this tab's URL, so the eye always reopens something instead
      // of silently doing nothing.
      let r: any = null;
      if (w.webContentsId != null) {
        r = await deviceCall(clientId, 'electron_tab_activate', { webContentsId: w.webContentsId });
      }
      if (!r || r.error) {
        const u = w.url && !w.url.startsWith('data:') ? w.url : '';
        r = await deviceCall(clientId, 'electron_tab_open', { accountIdx: p.accountIdx, ...(u ? { url: u } : {}) });
        if (r && r.error) throw new Error(r.error);
      }
      return;
    }
    if (w.webContentsId == null) throw new Error(tl('bwErrTabNotOpen'));
    let r: any;
    if (action === 'reload') {
      r = await deviceCall(clientId, 'electron_tab_eval', { webContentsId: w.webContentsId, code: 'location.reload()' });
    } else {
      r = await deviceCall(clientId, 'electron_tab_close', { webContentsId: w.webContentsId });
    }
    // Surface RPC errors like the chrome path does — a swallowed error here read
    // as "the close button does nothing".
    if (r && r.error) throw new Error(r.error);
    return;
  }
  // chrome page target
  const method = action === 'close' ? 'Target.closeTarget' : action === 'open' ? 'Target.activateTarget' : 'Page.reload';
  const args: Record<string, any> = { accountIdx: p.accountIdx, method };
  if (action === 'reload') args.target = w.targetWs;          // Page.* needs the page target
  else args.params = { targetId: w.key };                     // Target.* takes a targetId
  const r = await deviceCall(clientId, 'chrome_cdp_call', args);
  if (r && r.error) throw new Error(r.error);
}

// Loading placeholder — shimmer rows (shown while devices/profiles load).
function ProfileSkeleton() {
  return (
    <div data-id="browser-windows-skeleton" className="py-1">
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="flex items-center gap-2 px-2.5 py-2.5 border-b border-white/[0.03]">
          <span className="w-1.5 h-1.5 rounded-full bg-white/[0.06]" />
          <div className="flex-1 min-w-0">
            <div className="h-2.5 rounded bg-white/[0.06] animate-pulse" style={{ width: `${55 - i * 8}%` }} />
            <div className="h-2 mt-1.5 rounded bg-white/[0.035] animate-pulse" style={{ width: `${38 - i * 5}%` }} />
          </div>
        </div>
      ))}
    </div>
  );
}

// No desktop connected → a calm download CTA (not an alarming error box).
function NoDesktopCTA() {
  const { t } = useTranslation('layout');
  return (
    <div data-id="browser-windows-no-desktop" className="flex flex-col items-center justify-center text-center px-6 py-10 gap-3">
      <div className="w-12 h-12 rounded-2xl bg-white/[0.04] border border-white/[0.06] flex items-center justify-center">
        <MonitorOff className="w-5 h-5 text-zinc-500" />
      </div>
      <div className="text-[13px] text-zinc-300">{t('bwNoDesktopTitle')}</div>
      <div className="text-[12px] text-zinc-600 leading-relaxed max-w-[220px]">{t('bwNoDesktopDesc')}</div>
      <a
        data-id="browser-windows-download-btn"
        href="https://cicy-ai.com/#/download"
        target="_blank"
        rel="noreferrer"
        className="mt-1 inline-flex items-center gap-1.5 rounded-lg px-3.5 py-2 text-[12px] font-medium bg-white/[0.08] text-zinc-100 hover:bg-white/[0.14] transition-colors cursor-pointer"
      >
        <Download className="w-3.5 h-3.5" /> {t('bwDownloadDesktop')}
      </a>
    </div>
  );
}

// Official brand glyphs (lucide ships only a generic fruit-apple + robot, which
// read as the wrong thing). These are the real Apple and Android marks.
function AppleLogo({ className = 'w-4 h-4' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M17.05 12.536c-.03-2.69 2.2-3.98 2.3-4.05-1.25-1.83-3.2-2.08-3.89-2.11-1.66-.17-3.24.97-4.08.97-.84 0-2.14-.95-3.52-.92-1.81.03-3.48 1.05-4.41 2.67-1.88 3.26-.48 8.09 1.35 10.74.89 1.3 1.96 2.76 3.36 2.71 1.35-.05 1.86-.87 3.49-.87 1.63 0 2.09.87 3.52.84 1.45-.02 2.37-1.32 3.26-2.63 1.03-1.51 1.45-2.97 1.47-3.05-.03-.01-2.82-1.08-2.85-4.29zM14.37 4.6c.74-.9 1.24-2.15 1.1-3.4-1.07.04-2.36.71-3.13 1.61-.69.79-1.29 2.06-1.13 3.27 1.19.09 2.42-.61 3.16-1.48z" />
    </svg>
  );
}
function AndroidLogo({ className = 'w-4 h-4' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M17.523 15.341c-.551 0-.999-.449-.999-1 0-.551.448-.999.999-.999.551 0 .999.448.999.999 0 .551-.448 1-.999 1m-11.046 0c-.551 0-.999-.449-.999-1 0-.551.448-.999.999-.999.551 0 .999.448.999.999 0 .551-.448 1-.999 1m11.405-6.02l1.997-3.459a.416.416 0 00-.152-.568.416.416 0 00-.568.152l-2.022 3.503C15.59 8.244 13.853 7.851 12 7.851s-3.59.393-5.137 1.073L4.841 5.421a.416.416 0 00-.568-.152.416.416 0 00-.152.568l1.997 3.459C2.689 11.187.343 14.659 0 18.761h24c-.343-4.102-2.689-7.574-6.118-9.44" />
    </svg>
  );
}

// ── device presentation helpers (shared by the custom selector) ───────────────
// NOTE: check darwin/mac BEFORE win — "darwin" contains the substring "win"
// (dar-WIN), so a naive `includes('win')` first would mislabel Macs as Windows.
function platformIcon(platform: string, cls = 'w-3.5 h-3.5') {
  const p = (platform || '').toLowerCase();
  if (p.includes('darwin') || p.includes('mac')) return <Monitor className={cn(cls, 'text-zinc-200')} />;
  if (p.includes('linux')) return <Monitor className={cn(cls, 'text-amber-400')} />;
  if (p.includes('win')) return <Monitor className={cn(cls, 'text-sky-400')} />;
  return <Monitor className={cn(cls, 'text-zinc-500')} />;
}
function platformLabel(platform: string): string {
  const p = (platform || '').toLowerCase();
  if (p.includes('darwin') || p.includes('mac')) return 'macOS';
  if (p.includes('linux')) return 'Linux';
  if (p.includes('win')) return 'Windows';
  return platform || tl('bwDeviceFallback');
}
function fmtUptime(sec?: number): string {
  if (!sec || sec < 0) return '';
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
  if (h >= 24) return `${Math.floor(h / 24)}d`;
  if (h >= 1) return `${h}h${m ? m + 'm' : ''}`;
  return `${Math.max(1, m)}m`;
}

// Custom device selector — replaces the native <select> (whose long device-ids
// got truncated with no way to see the full detail). A trigger button shows the
// current device compactly; the dropdown lists every device with full id /
// platform / region / IP / uptime / language so you can tell them apart.
function DeviceSelect({ devices, value, onChange, loading }: {
  devices: Device[]; value: string; onChange: (clientId: string) => void; loading: boolean;
}) {
  const { t } = useTranslation('layout');
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener('mousedown', h);
    return () => document.removeEventListener('mousedown', h);
  }, [open]);
  const cur = devices.find((d) => d.clientId === value) || null;
  const empty = devices.length === 0;
  return (
    <div data-id="browser-windows-device-select" ref={ref} className="relative w-full min-w-0">
      <button
        data-id="browser-windows-device-trigger"
        onClick={() => !empty && setOpen((v) => !v)}
        disabled={loading || empty}
        className="w-full min-w-0 flex items-center gap-2 bg-[#141414] border border-white/[0.08] rounded-lg px-2 py-1.5 text-left outline-none hover:border-white/20 focus:border-white/20 cursor-pointer disabled:opacity-50"
      >
        {cur ? platformIcon(cur.platform) : <Monitor className="w-3.5 h-3.5 text-zinc-600 shrink-0" />}
        <div className="min-w-0 flex-1">
          {cur ? (
            <>
              <div className="text-[12px] text-zinc-200 truncate">{platformLabel(cur.platform)}{cur.deviceId ? ` · ${cur.deviceId}` : ''}</div>
              {(cur.region || cur.ip) ? <div className="text-[10px] text-zinc-600 truncate">{[cur.region, cur.ip].filter(Boolean).join(' · ')}</div> : null}
            </>
          ) : (
            <span className="text-[12px] text-zinc-500">{loading ? t('bwLoadingDevices') : t('bwNoDevices')}</span>
          )}
        </div>
        <ChevronRight className={cn('w-3.5 h-3.5 text-zinc-600 shrink-0 transition-transform', open ? 'rotate-90' : '')} />
      </button>
      {open && !empty && (
        <div data-id="browser-windows-device-dropdown" className="absolute z-[140] left-0 right-0 mt-1 max-h-[320px] overflow-auto rounded-lg border border-white/[0.1] bg-[#141416] shadow-[0_18px_48px_rgba(0,0,0,0.5)] py-1">
          {devices.map((d) => {
            const sel = d.clientId === value;
            return (
              <button
                key={d.clientId}
                data-id="browser-windows-device-option"
                onClick={() => { onChange(d.clientId); setOpen(false); }}
                className={cn('w-full flex items-start gap-2 px-2.5 py-2 text-left transition-colors cursor-pointer', sel ? 'bg-white/[0.07]' : 'hover:bg-white/[0.04]')}
              >
                <span className="mt-0.5 shrink-0">{platformIcon(d.platform)}</span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="text-[12px] text-zinc-100 truncate">{platformLabel(d.platform)}</span>
                    <span className="inline-flex items-center gap-0.5 text-[10px] text-emerald-400/80 shrink-0"><Wifi className="w-2.5 h-2.5" />{fmtUptime(d.uptimeSec) || t('bwOnline')}</span>
                  </div>
                  <div className="text-[10px] text-zinc-500 truncate mt-0.5">{d.deviceId || d.clientId}</div>
                  {(d.region || d.ip || d.systemLanguage) ? <div className="text-[10px] text-zinc-600 truncate">{[d.region, d.ip, d.systemLanguage].filter(Boolean).join(' · ')}</div> : null}
                </div>
                {sel && <Check className="w-3.5 h-3.5 text-emerald-400 shrink-0 mt-0.5" />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ── Desktop snapshot view (桌面 tab) ──────────────────────────────────────────
// Shows the selected device's periodic desktop screenshots: a hero (selected /
// latest) image with a click-to-zoom lightbox, a history grid, and a manual
// "capture now" button. The backend scheduler stores these to disk; this view
// just lists + serves them and pokes /snapshot-now for an immediate capture.
interface SnapItem { name: string; ts: number }

function fmtSnapTime(ms: number): string {
  try {
    return new Date(ms).toLocaleString(i18n.language || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
  } catch { return String(ms); }
}

function DesktopSnapshotView({ clientId, onSendToAgent }: { clientId: string; onSendToAgent?: (text: string) => void }) {
  const { t } = useTranslation('layout');
  const [latest, setLatest] = useState<SnapItem | null>(null);
  const [loading, setLoading] = useState(false);
  const [capturing, setCapturing] = useState(false);
  const [error, setError] = useState('');
  const [lightbox, setLightbox] = useState(false);

  const fetchLatest = useCallback(async (silent = false) => {
    if (!clientId) { setLatest(null); return; }
    if (!silent) setLoading(true);
    try {
      const resp = await apiService.getDesktopSnapshots(clientId);
      const list: SnapItem[] = Array.isArray(resp?.data?.items) ? resp.data.items : [];
      setLatest(list[0] ?? null);
    } catch (e: any) {
      if (!silent) setError(e?.response?.data || e?.message || String(e));
    } finally {
      if (!silent) setLoading(false);
    }
  }, [clientId]);

  // 不再定时截图:挂载时拉一次「最近一张」,之后只有用户点「立即截图 / 刷新」才会
  // 触发桌面端真正截图(captureNow → desktopSnapshotNow → 写盘 → 再拉 image)。
  useEffect(() => {
    setError('');
    fetchLatest();
  }, [fetchLatest]);

  const captureNow = async () => {
    if (capturing || !clientId) return;
    setCapturing(true); setError('');
    try {
      await apiService.desktopSnapshotNow(clientId);
      await fetchLatest(true);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.response?.data || e?.message || String(e));
    } finally {
      setCapturing(false);
    }
  };

  // cache-bust by ts so the same <img> updates when a new capture replaces it
  const imgUrl = (s: SnapItem) => apiService.desktopSnapshotImageUrl(clientId, s.name) + `&_=${s.ts}`;

  if (!clientId) return <div className="p-4 text-[12px] text-zinc-600">{t('bwNoDevices')}</div>;

  return (
    <div data-id="desktop-snapshot-view" className="flex flex-col h-full">
      <div data-id="desktop-snapshot-toolbar" className="flex items-center gap-2 px-2.5 py-2 border-b border-white/[0.06] shrink-0">
        <span className="text-[11px] text-zinc-500 flex-1 truncate">{latest ? fmtSnapTime(latest.ts) : ''}</span>
        <button
          data-id="desktop-snapshot-now"
          onClick={captureNow}
          disabled={capturing}
          title={t('bwSnapNow')}
          className="flex items-center gap-1 rounded-lg px-2 py-1 text-[12px] font-medium bg-white/[0.07] text-zinc-200 hover:bg-white/[0.12] transition-colors cursor-pointer disabled:opacity-50 shrink-0"
        >
          {capturing ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Camera className="w-3.5 h-3.5" />}{t('bwSnapNow')}
        </button>
        {onSendToAgent && (
          <button
            data-id="desktop-snapshot-send-agent"
            onClick={() => onSendToAgent(t('bwPromptDesktop', { clientId, c: `agent-desktop --client ${clientId}` }))}
            title={t('bwSendToAgent')}
            className="flex items-center gap-1 rounded-lg px-2 py-1 text-[12px] font-medium bg-white/[0.07] text-blue-300 hover:text-blue-200 hover:bg-white/[0.12] transition-colors cursor-pointer shrink-0"
          >
            <Send className="w-3.5 h-3.5" />{t('bwSendToAgent')}
          </button>
        )}
        <button
          data-id="desktop-snapshot-refresh"
          onClick={() => fetchLatest()}
          disabled={loading}
          title={t('bwRefreshWindows')}
          className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer disabled:opacity-50 shrink-0"
        >
          {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
        </button>
      </div>

      {error && (
        <div className="flex items-start gap-2 m-2.5 mb-0 p-2 rounded-lg bg-white/[0.03] border border-white/[0.07] text-[11px] text-zinc-400">
          <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5 text-zinc-500" />
          <span>{String(error)}</span>
        </div>
      )}

      {loading && !latest ? (
        <div className="p-2.5"><div className="aspect-video rounded-xl bg-gradient-to-b from-[#222228] to-[#16161a] animate-pulse" /></div>
      ) : !latest ? (
        <div data-id="desktop-snapshot-empty" className="flex-1 flex flex-col items-center justify-center text-center px-6 py-10 gap-2 text-zinc-600">
          <Monitor className="w-6 h-6 text-zinc-700" />
          <div className="text-[12px]">{t('bwSnapEmpty')}</div>
          <div className="text-[11px] text-zinc-700">{t('bwSnapEmptyHint')}</div>
        </div>
      ) : (
        <div className="flex-1 overflow-auto p-2.5">
          <button data-id="desktop-snapshot-hero" onClick={() => setLightbox(true)} className="block w-full rounded-xl overflow-hidden border border-white/[0.08] bg-[#0e0e0e] cursor-zoom-in">
            <img src={imgUrl(latest)} alt="desktop" className="w-full block" />
          </button>
        </div>
      )}

      {lightbox && latest && createPortal(
        <div data-id="desktop-snapshot-lightbox" className="fixed inset-0 z-[300] bg-black/85 backdrop-blur-sm flex items-center justify-center p-6" onClick={() => setLightbox(false)}>
          <img src={imgUrl(latest)} alt="desktop" className="max-w-full max-h-full rounded-lg shadow-2xl" onClick={(e) => e.stopPropagation()} />
          <button data-id="desktop-snapshot-lightbox-close" className="absolute top-4 right-4 p-2 rounded-lg bg-white/10 text-white hover:bg-white/20 cursor-pointer" onClick={() => setLightbox(false)}><X className="w-5 h-5" /></button>
        </div>,
        document.body,
      )}
    </div>
  );
}

// ── LEFT panel: device + backend tabs + selectable profile list ───────────────
export default function BrowserWindowsPanel({
  selectedKey,
  onSelect,
  openConfigRequest,
  onSelectMobile,
  selectedMobileKey,
  onSendToAgent,
}: {
  selectedKey?: string | null;
  onSelect: (sel: { clientId: string; deviceId: string; profile: Profile } | null) => void;
  openConfigRequest?: { backend: Backend; accountIdx: number; nonce: number } | null;
  // Android / iOS tabs: selecting a phone hands a mobile selection up to Workspace,
  // which renders MobileDeviceColumn instead of BrowserWindowsColumn.
  onSelectMobile?: (sel: MobileSel | null) => void;
  selectedMobileKey?: string | null; // `${clientId}:${id}`
  // Desktop tab: "send to agent" on a desktop snapshot routes a prompt to the CLI.
  onSendToAgent?: (text: string) => void;
}) {
  const { t } = useTranslation('layout');
  const [devices, setDevices] = useState<Device[]>([]);
  const [clientId, setClientId] = useState<string>('');
  const [devLoading, setDevLoading] = useState(false);
  const [devError, setDevError] = useState('');

  const [backend, setBackend] = useState<Backend>('electron');
  const isMobile = backend === 'android' || backend === 'ios';
  const isDesktop = backend === 'desktop';
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [phones, setPhones] = useState<MobileDevice[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  // Profile0 is the system/team window — hidden by default; the eye button reveals it.
  const [showSystem, setShowSystem] = useState(false);
  const isSystem = (p: Profile) => p.backend === 'electron' && p.accountIdx === 0;
  const [addingProfile, setAddingProfile] = useState(false);
  const addingProfileRef = useRef(false); // synchronous double-fire guard (see onAdd)
  // The device-actions (eye/add/refresh) are portaled up into the panel header
  // row ("设备"), so the title + actions live on one bar instead of two.
  const [headerSlot, setHeaderSlot] = useState<HTMLElement | null>(null);
  useEffect(() => { setHeaderSlot(document.getElementById('windows-header-actions')); }, []);

  const refreshDevices = useCallback(async (silent = false) => {
    if (!silent) { setDevLoading(true); setDevError(''); }
    try {
      const list = await loadDevices();
      const sig = (l: Device[]) => l.map((d) => `${d.clientId}|${d.label}`).join(',');
      setDevices((prev) => (sig(prev) === sig(list) ? prev : list));
      setClientId((cur) => (list.some((d) => d.clientId === cur) ? cur : (list[0]?.clientId || '')));
      // No devices is NOT an error — the body renders a skeleton (while loading)
      // or a download CTA. Only real fetch failures set devError.
      setDevError('');
    } catch (e: any) {
      if (!silent) { setDevError(e?.message || String(e)); setDevices([]); }
    } finally {
      if (!silent) setDevLoading(false);
    }
  }, []);

  useEffect(() => {
    refreshDevices();
    const id = setInterval(() => refreshDevices(true), 6000);
    return () => clearInterval(id);
  }, [refreshDevices]);

  const refresh = useCallback(async () => {
    if (!clientId) { setProfiles([]); setPhones([]); return; }
    // Desktop snapshots have no profile/phone list — the body renders the
    // selected device's snapshots itself, fetched on its own.
    if (backend === 'desktop') { setProfiles([]); setPhones([]); setError(''); return; }
    setLoading(true); setError('');
    try {
      if (backend === 'android' || backend === 'ios') {
        const all = await listPhones(clientId);
        setPhones(all.filter((p) => p.platform === backend));
      } else {
        setProfiles(await loadProfiles(clientId, backend));
      }
    } catch (e: any) {
      setError(e?.message || String(e));
      setProfiles([]); setPhones([]);
    } finally {
      setLoading(false);
    }
  }, [clientId, backend]);

  const onAddProfile = useCallback(async () => {
    if (!clientId || addingProfileRef.current) return;
    addingProfileRef.current = true;
    setAddingProfile(true); setError('');
    try { await addProfile(clientId, backend); await refresh(); }
    catch (e: any) { setError(e?.message || String(e)); }
    finally { addingProfileRef.current = false; setAddingProfile(false); }
  }, [clientId, backend, refresh]);

  // Reload + clear any open column (profile or phone) when the device/backend changes.
  useEffect(() => { onSelect(null); onSelectMobile?.(null); refresh(); }, [refresh]);  // eslint-disable-line react-hooks/exhaustive-deps

  // External "open profile config" request (agent-webpage send open_profile_config):
  // switch to the requested backend, then once its profiles load, select that
  // profile (mounts the column) and tell the column to open its config modal.
  const [pendingConfig, setPendingConfig] = useState<{ backend: Backend; accountIdx: number } | null>(null);
  useEffect(() => {
    if (!openConfigRequest) return;
    setBackend(openConfigRequest.backend);
    setPendingConfig({ backend: openConfigRequest.backend, accountIdx: openConfigRequest.accountIdx });
  }, [openConfigRequest]);
  useEffect(() => {
    if (!pendingConfig || pendingConfig.backend !== backend || !clientId) return;
    const prof = profiles.find((p) => p.accountIdx === pendingConfig.accountIdx);
    if (!prof) return;  // its backend's profiles not loaded yet — wait for next [profiles] tick
    setPendingConfig(null);
    onSelect({ clientId, deviceId: devices.find((d) => d.clientId === clientId)?.deviceId || '', profile: prof });
    setTimeout(() => window.dispatchEvent(new CustomEvent('cicy-open-config-modal', { detail: { key: prof.key } })), 400);
  }, [pendingConfig, profiles, backend, clientId, devices, onSelect]);

  // Icon-only segmented control (tooltips carry the names) — frees the horizontal
  // space the old icon+text tabs ate, so a 5th "桌面" tab fits without crowding.
  const TABS: { k: Backend; label: string; icon: React.ReactNode }[] = [
    { k: 'electron', label: 'Electron', icon: <Atom className="w-4 h-4" /> },
    { k: 'chrome', label: 'Chrome', icon: <Chrome className="w-4 h-4" /> },
    { k: 'android', label: 'Android', icon: <AndroidLogo className="w-4 h-4" /> },
    { k: 'ios', label: 'iOS', icon: <AppleLogo className="w-4 h-4" /> },
    { k: 'desktop', label: t('bwTabDesktop'), icon: <Monitor className="w-4 h-4" /> },
  ];

  // Device-actions (eye/add/refresh) — portaled into the panel header so they sit
  // on the "设备" title row rather than a separate bar below it.
  const deviceActions = (
    <div data-id="browser-windows-device-actions" className="flex items-center gap-1">
      {backend === 'electron' && (
        <button
          data-id="browser-windows-show-system"
          onClick={() => setShowSystem((v) => !v)}
          title={showSystem ? t('bwHideSystem') : t('bwShowSystem')}
          className={cn(
            'p-1.5 rounded-lg transition-colors cursor-pointer shrink-0',
            showSystem ? 'text-blue-400 bg-blue-500/10' : 'text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04]',
          )}
        >
          <Eye className="w-3.5 h-3.5" />
        </button>
      )}
      {!isMobile && !isDesktop && (
        <button
          data-id="browser-windows-add-profile"
          onClick={onAddProfile}
          disabled={addingProfile || !clientId}
          title={t('bwAddProfileTitle', { backend: backend === 'chrome' ? 'Chrome' : 'Electron' })}
          className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer disabled:opacity-50 shrink-0"
        >
          {addingProfile ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      )}
      <button
        data-id="browser-windows-refresh"
        onClick={() => { refreshDevices(); refresh(); }}
        disabled={devLoading || loading}
        title={t('bwRefreshProfile')}
        className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer disabled:opacity-50 shrink-0"
      >
        {(devLoading || loading) ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
      </button>
    </div>
  );

  return (
    <div data-id="BrowserWindowsPanel" className="absolute inset-0 flex flex-col bg-[#0A0A0A]">
      {headerSlot ? createPortal(deviceActions, headerSlot) : null}
      {/* device selector */}
      <div data-id="browser-windows-device" className="flex flex-col gap-2 px-2 py-2 border-b border-white/[0.06] shrink-0">
        {/* device select on its own full-width line — custom dropdown so long
            device-ids + full detail (region/IP/uptime/lang) are visible */}
        <DeviceSelect devices={devices} value={clientId} onChange={setClientId} loading={devLoading} />
      </div>

      {/* backend tabs — icon-only segmented control (tooltips name each) */}
      <div data-id="browser-windows-tabs" className="flex items-center gap-1 px-2 py-2 border-b border-white/[0.06] shrink-0">
        {TABS.map((tabItem) => (
          <button
            key={tabItem.k}
            data-id={`browser-windows-tab-${tabItem.k}`}
            onClick={() => setBackend(tabItem.k)}
            title={tabItem.label}
            aria-label={tabItem.label}
            className={cn(
              'flex flex-1 items-center justify-center rounded-lg py-1.5 transition-colors cursor-pointer',
              backend === tabItem.k ? 'bg-white/[0.07] text-zinc-100' : 'text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.03]',
            )}
          >
            {tabItem.icon}
          </button>
        ))}
      </div>

      {/* body */}
      <div data-id="browser-windows-body" className="flex-1 overflow-auto">
        {devices.length === 0 ? (
          devLoading ? (
            <ProfileSkeleton />
          ) : devError ? (
            <div data-id="browser-windows-deverror" className="flex items-start gap-2 m-3 p-3 rounded-lg bg-white/[0.03] border border-white/[0.07] text-[12px] text-zinc-400">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-zinc-500" />
              <span>{devError}</span>
            </div>
          ) : (
            <NoDesktopCTA />
          )
        ) : error ? (
          <div data-id="browser-windows-error" className="flex items-start gap-2 m-3 p-3 rounded-lg bg-white/[0.03] border border-white/[0.07] text-[12px] text-zinc-400">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-zinc-500" />
            <span>{error}</span>
          </div>
        ) : isDesktop ? (
          <DesktopSnapshotView clientId={clientId} onSendToAgent={onSendToAgent} />
        ) : isMobile ? (
          loading && phones.length === 0 ? (
            <ProfileSkeleton />
          ) : phones.length === 0 ? (
            <div className="p-4 text-[12px] text-zinc-600">没有连接的 {backend === 'android' ? 'Android' : 'iOS'} 设备</div>
          ) : (
            <div data-id="browser-windows-phone-list" className="py-1">
              {phones.map((d) => {
                const key = `${clientId}:${d.id}`;
                const unauth = d.status === 'unauthorized';
                return (
                  <button
                    key={key}
                    data-id="mobile-device-row"
                    onClick={() => { onSelect(null); onSelectMobile?.({ clientId, platform: d.platform, id: d.id, label: d.model || d.id }); }}
                    className={cn(
                      'w-full flex items-center gap-2 px-3 py-2 text-left transition-colors cursor-pointer',
                      selectedMobileKey === key ? 'bg-white/[0.07]' : 'hover:bg-white/[0.03]',
                    )}
                  >
                    <Smartphone className="w-4 h-4 text-zinc-500 shrink-0" />
                    <div className="flex-1 min-w-0">
                      <div className="text-[13px] text-zinc-200 truncate">{d.model || d.id}</div>
                      <div className="text-[11px] text-zinc-600 truncate">{d.id}{unauth ? ' · 未授权' : ''}</div>
                    </div>
                    <ChevronRight className="w-3.5 h-3.5 text-zinc-600 shrink-0" />
                  </button>
                );
              })}
            </div>
          )
        ) : loading && profiles.length === 0 ? (
          <ProfileSkeleton />
        ) : profiles.length === 0 ? (
          <div className="p-4 text-[12px] text-zinc-600">{t('bwNoProfiles', { backend: backend === 'chrome' ? 'Chrome' : 'Electron' })}</div>
        ) : (
          <div data-id="browser-windows-profile-list" className="py-1">
            {profiles.filter((p) => showSystem || !isSystem(p)).map((p) => (
              <ProfileRow
                key={p.key}
                profile={p}
                selected={selectedKey === p.key}
                onClick={() => { onSelectMobile?.(null); onSelect({ clientId, deviceId: devices.find((d) => d.clientId === clientId)?.deviceId || '', profile: p }); }}
              />
            ))}
            {!showSystem && profiles.some(isSystem) && profiles.every(isSystem) && (
              <div className="px-3 py-4 text-[12px] text-zinc-600">{t('bwSystemHiddenPre')}<Eye className="w-3 h-3 inline -mt-0.5" />{t('bwSystemHiddenPost')}</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function ProfileRow({ profile, selected, onClick }: { profile: Profile; selected: boolean; onClick: () => void }) {
  const proxyOn = profile.proxy?.enabled && profile.proxy?.url;
  // The Google identity (gmail), resolved from the accounts map, shown inline on
  // the Profile-name line.
  const gmail = profile.gmail || '';
  const ip = profile.ipInfo?.ip;
  const area = profile.ipInfo?.area;
  return (
    <button
      data-id="BrowserProfileRow"
      onClick={onClick}
      className={cn(
        'w-full flex items-center gap-2 px-2.5 py-2 text-left border-b border-white/[0.04] transition-colors cursor-pointer',
        selected ? 'bg-white/[0.07]' : 'hover:bg-white/[0.03]',
      )}
    >
      <span className={cn('w-1.5 h-1.5 rounded-full shrink-0', profile.backend === 'electron' || profile.running ? 'bg-emerald-500/80' : 'bg-zinc-600')} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span data-id="browser-profile-name" className={cn('text-[13px] truncate shrink-0', selected ? 'text-zinc-100' : 'text-zinc-200')}>{profile.name}</span>
          <span className="text-[10px] text-zinc-600 shrink-0">#{profile.accountIdx}</span>
          {gmail && <span data-id="browser-profile-gmail" className="text-[11px] text-zinc-500 truncate" title={gmail}>{gmail}</span>}
        </div>
        <div className="flex items-center gap-1.5 mt-0.5">
          {proxyOn ? (
            <span className="text-[10px] px-1 rounded bg-violet-500/10 text-violet-300/80 truncate shrink-0" title={profile.proxy.url}>proxy</span>
          ) : (
            <span className="text-[10px] px-1 rounded bg-white/[0.04] text-zinc-600 shrink-0">no proxy</span>
          )}
          {ip && (
            <span className="text-[10px] px-1 rounded bg-sky-500/10 text-sky-300/80 truncate shrink-0"
              title={`${ip}${area ? ' · ' + area : ''}`}>
              {ip}{area ? ` · ${area}` : ''}
            </span>
          )}
        </div>
      </div>
      <ChevronRight className={cn('w-3.5 h-3.5 shrink-0', selected ? 'text-zinc-300' : 'text-zinc-700')} />
    </button>
  );
}

// ── MIDDLE column: the selected profile's windows + "add window" ──────────────
export function BrowserWindowsColumn({
  clientId,
  deviceId,
  profile,
  onClose,
  onSendToAgent,
  onOpenInEditor,
}: {
  clientId: string;
  deviceId: string;
  profile: Profile;
  onClose: () => void;
  onSendToAgent: (text: string) => void;
  onOpenInEditor: (path: string) => void;
}) {
  const { t } = useTranslation('layout');
  const [windows, setWindows] = useState<WinItem[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [newUrl, setNewUrl] = useState('');
  const [adding, setAdding] = useState(false);
  // Synchronous re-entrancy guard. `adding` (state) can't gate a double-fire:
  // it commits on the next render, so two rapid clicks (or a hardware
  // double-click) both pass `if (adding)` before the button disables. On the
  // FIRST add — Chrome not yet running — each call falls to chrome_launch_profile
  // and spawns a SEPARATE Chrome ⇒ two windows. A ref flips instantly, so the
  // second invocation bails in the same tick.
  const addingRef = useRef(false);
  const [configOpen, setConfigOpen] = useState(false);
  const [, setSelKey] = useState<string | null>(null);

  const load = useCallback(async (silent = false) => {
    // silent=true skips the loading skeleton so an action-triggered refresh
    // (e.g. closing one tab) doesn't unmount every WindowCard and re-screenshot
    // all tabs. The cards stay mounted and reconcile by stable key — the closed
    // tab drops out, the rest keep their existing shots.
    if (!silent) setLoading(true);
    setError('');
    try {
      setWindows(await loadWindows(clientId, profile));
    } catch (e: any) {
      setError(e?.message || String(e));
      setWindows([]);
    } finally {
      if (!silent) setLoading(false);
    }
  }, [clientId, profile]);

  useEffect(() => { load(); }, [load]);

  // Open this profile's config modal when asked externally (open_profile_config).
  useEffect(() => {
    const h = (e: Event) => { if ((e as CustomEvent).detail?.key === profile.key) setConfigOpen(true); };
    window.addEventListener('cicy-open-config-modal', h as EventListener);
    return () => window.removeEventListener('cicy-open-config-modal', h as EventListener);
  }, [profile.key]);

  // Keep a selected tab (default first); preserve selection across refresh.
  useEffect(() => {
    if (windows && windows.length) {
      setSelKey((prev) => (prev && windows.some((w) => w.key === prev) ? prev : windows[0].key));
    } else {
      setSelKey(null);
    }
  }, [windows]);
  // Both backends open a start-page TAB ("新加标签"). For Chrome, that also
  // launches the profile if it isn't running yet.
  const addLabel = t('bwAddTab');
  const unitLabel = t('bwUnitTab');
  // Add-tab button is always shown (no per-backend/profile gating).
  const canAddTab = true;

  const onAdd = async () => {
    if (addingRef.current) return;
    addingRef.current = true;
    setAdding(true); setError('');
    try {
      await addWindow(clientId, profile, newUrl);
      setNewUrl('');
      // give the new window a beat to register, then refresh the list.
      // Silent refresh: existing cards reconcile by key (no re-screenshot); only
      // the new tab's card mounts and captures itself.
      await new Promise((r) => setTimeout(r, 600));
      await load(true);
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      addingRef.current = false;
      setAdding(false);
    }
  };

  return (
    <div data-id="BrowserWindowsColumn" className="absolute inset-0 flex flex-col bg-[#0A0A0A]">
      {/* header */}
      <div data-id="browser-windows-column-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center gap-2 px-2.5 bg-[#0e0e0e] shrink-0">
        {profile.backend === 'electron' ? <Atom className="w-3.5 h-3.5 text-zinc-500 shrink-0" /> : <Chrome className="w-3.5 h-3.5 text-zinc-500 shrink-0" />}
        <span data-id="browser-windows-column-title" className="text-[13px] text-zinc-200 truncate flex-1">{profile.name}</span>
        {/* New tab — sits to the LEFT of the profile-setting (config) button */}
        {canAddTab && (
          <button
            data-id="browser-windows-column-add-btn"
            onClick={onAdd}
            disabled={adding}
            title={t('bwAddTabTitle')}
            className="flex items-center gap-1 rounded-lg px-2 py-1 text-[12px] font-medium bg-white/[0.07] text-zinc-200 hover:bg-white/[0.12] transition-colors cursor-pointer disabled:opacity-50 shrink-0"
          >
            {adding ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}{addLabel}
          </button>
        )}
        <button data-id="browser-windows-column-config" onClick={() => setConfigOpen(true)} title={t('bwConfigProfile')}
          className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer">
          <Settings className="w-3.5 h-3.5" />
        </button>
        <button data-id="browser-windows-column-refresh" onClick={() => load()} disabled={loading} title={t('bwRefreshWindows')}
          className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.04] transition-colors cursor-pointer disabled:opacity-50">
          {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
        </button>
        <button data-id="browser-windows-column-close" onClick={onClose} title={t('bwClose')}
          className="p-1 rounded text-zinc-600 hover:text-zinc-300 transition-colors cursor-pointer"><X className="w-3.5 h-3.5" /></button>
      </div>

      {configOpen && <ProfileConfigModal clientId={clientId} profile={profile} onClose={() => setConfigOpen(false)} />}

      {/* windows: Chrome-style compact tab list + preview pane for the selected one */}
      <div data-id="browser-windows-column-body" className="flex-1 flex flex-col overflow-hidden">
        {error && (
          isChromeMissingError(error) && profile.backend === 'chrome' ? (
            <div data-id="browser-windows-chrome-missing" className="flex items-start gap-2 m-2.5 mb-0 p-3 rounded-lg bg-amber-950/30 border border-amber-900/40 text-[12px] text-amber-200/90">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-amber-400" />
              <div className="min-w-0 flex-1">
                <div className="font-medium text-amber-100">{t('bwChromeMissingTitle')}</div>
                <div className="mt-0.5 text-amber-200/80">{t('bwChromeMissingDesc')}</div>
                <button
                  type="button"
                  data-id="browser-windows-chrome-install-link"
                  onClick={() => { void openOfficialChromeDownload(); }}
                  className="mt-2 inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] font-medium bg-amber-500/20 text-amber-100 hover:bg-amber-500/30 transition-colors cursor-pointer"
                >
                  <Download className="w-3 h-3" />{t('bwChromeInstallBtn')}
                </button>
              </div>
            </div>
          ) : (
            <div className="flex items-start gap-2 m-2.5 mb-0 p-2 rounded-lg bg-white/[0.03] border border-white/[0.07] text-[11px] text-zinc-400">
              <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5 text-zinc-500" />
              <span>{error}{profile.backend === 'chrome' ? t('bwChromeRefreshHint') : ''}</span>
            </div>
          )
        )}
        {loading ? (
          <div data-id="browser-windows-cards-loading" className="flex-1 flex items-center justify-center py-10">
            <Loader2 className="w-5 h-5 animate-spin text-zinc-600" />
          </div>
        ) : !windows || windows.length === 0 ? (
          !error && (
            <div className="flex-1 flex flex-col items-center justify-center text-center px-6 py-10 gap-2 text-zinc-600">
              <MonitorOff className="w-6 h-6 text-zinc-700" />
              <div className="text-[12px]">{t('bwNoTabsYet', { unit: unitLabel })}</div>
              <div className="text-[11px] text-zinc-700">{t('bwNoTabsHint', { label: addLabel })}</div>
            </div>
          )
        ) : (
          /* each tab shown as a screenshot card */
          <div data-id="browser-windows-cards" className="flex-1 overflow-auto p-2.5 flex flex-col gap-2.5">
            <div className="text-[10px] text-zinc-600 px-0.5">{t('bwTabCount', { count: windows.length, unit: profile.backend === 'chrome' ? t('bwUnitTab') : t('bwUnitWindow') })}</div>
            {windows.map((w) => (
              <WindowCard
                key={w.key}
                clientId={clientId}
                deviceId={deviceId}
                profile={profile}
                win={w}
                onRefresh={() => load(true)}
                onSendToAgent={onSendToAgent}
                onOpenInEditor={onOpenInEditor}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// ── login table (rich schema: url/name/username/email/mobile/2FA/backup-email/note/loginAt/updatedAt) ──
type TFn = (key: string, opts?: Record<string, unknown>) => string;
const getLoginCols = (t: TFn): { key: keyof LoginRec; label: string; editable: boolean; w: number }[] => [
  { key: 'url', label: t('bwColUrl'), editable: true, w: 240 },
  { key: 'name', label: t('bwColName'), editable: false, w: 110 },
  { key: 'username', label: t('bwColUsername'), editable: true, w: 140 },
  { key: 'email', label: t('bwColEmail'), editable: true, w: 190 },
  { key: 'mobile', label: t('bwColMobile'), editable: true, w: 130 },
  { key: 'twofa', label: t('bwColTwofa'), editable: true, w: 130 },
  { key: 'secondEmail', label: t('bwColSecondEmail'), editable: true, w: 190 },
  { key: 'note', label: t('bwColNote'), editable: true, w: 150 },
  { key: 'loginAt', label: t('bwColLoginAt'), editable: false, w: 120 },
  { key: 'updatedAt', label: t('bwColUpdatedAt'), editable: false, w: 120 },
];

function fmtLoginTime(s?: string): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString(i18n.language || undefined, { year: '2-digit', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
  } catch { return s; }
}

// editable cell: holds local draft, commits on blur/Enter only if changed
function LoginCell({ value, disabled, onSave }: { value: string; disabled?: boolean; onSave: (v: string) => void }) {
  const [v, setV] = useState(value);
  useEffect(() => { setV(value); }, [value]);
  return (
    <input
      data-id="login-cell-input"
      value={v}
      disabled={disabled}
      onChange={(e) => setV(e.target.value)}
      onBlur={() => { if (v !== value) onSave(v); }}
      onKeyDown={(e) => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur(); }}
      className="w-full bg-transparent px-1.5 py-1 text-[11px] text-zinc-300 outline-none rounded focus:bg-white/[0.06] disabled:opacity-50"
    />
  );
}

function LoginTable({ logins, busy, onSet, onRemove }: {
  logins: LoginRec[];
  busy: boolean;
  onSet: (login: Partial<LoginRec> & { name: string }) => void;
  onRemove: (name: string) => void;
}) {
  const { t } = useTranslation('layout');
  const LOGIN_COLS = getLoginCols(t);
  return (
    <div data-id="login-table-wrap" className="h-full overflow-auto border border-white/[0.07] rounded-lg">
      <table data-id="login-table" className="w-full border-collapse text-[11px]">
        <thead className="sticky top-0 z-10">
          <tr className="text-zinc-400">
            {LOGIN_COLS.map((c) => (
              <th key={c.key} style={{ minWidth: c.w }} className="px-2 py-2 text-left font-medium whitespace-nowrap bg-[#161616] border-b border-white/[0.08]">{c.label}</th>
            ))}
            <th className="px-1.5 bg-[#161616] border-b border-white/[0.08]" />
          </tr>
        </thead>
        <tbody>
          {logins.map((l) => (
            <tr key={l.name} data-id="login-table-row" className="border-t border-white/[0.05] hover:bg-white/[0.02]">
              {LOGIN_COLS.map((c) => (
                <td key={c.key} style={{ minWidth: c.w }} className="px-0.5 py-0.5 align-middle">
                  {c.key === 'loginAt' || c.key === 'updatedAt' ? (
                    <span className="px-1.5 text-zinc-500 whitespace-nowrap">{fmtLoginTime(l[c.key])}</span>
                  ) : c.editable ? (
                    <LoginCell value={(l[c.key] as string) || ''} disabled={busy} onSave={(v) => onSet({ name: l.name, [c.key]: v })} />
                  ) : (
                    <span className="px-1.5 text-zinc-100 font-medium whitespace-nowrap">{(l[c.key] as string) || '—'}</span>
                  )}
                </td>
              ))}
              <td className="px-1">
                <button data-id="login-table-rm" onClick={() => onRemove(l.name)} disabled={busy}
                  className="text-zinc-600 hover:text-red-400 cursor-pointer disabled:opacity-50"><X className="w-3.5 h-3.5" /></button>
              </td>
            </tr>
          ))}
          {logins.length === 0 && (
            <tr><td colSpan={LOGIN_COLS.length + 1} className="px-2 py-3 text-center text-zinc-600">{t('bwNoLogins')}</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

// Full profile config in a modal: 名称 / 备注 / 代理 (one Save) + 登录记录表格
// (rich schema, inline-editable). Backend-aware: Chrome name is its fixed
// profileKey → read-only. Routed to the selected device like everything else.
function ProfileConfigModal({ clientId, profile, onClose }: { clientId: string; profile: Profile; onClose: () => void }) {
  const { t } = useTranslation('layout');
  const isElectron = profile.backend === 'electron';
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');

  const [name, setName] = useState('');
  const [note, setNote] = useState('');
  const [proxyUrl, setProxyUrl] = useState('');
  const [saving, setSaving] = useState(false);
  const [savedMsg, setSavedMsg] = useState('');

  const [ipInfo, setIpInfo] = useState<IpInfo | null>(null);
  const [probing, setProbing] = useState(false);
  const [logins, setLogins] = useState<LoginRec[]>([]);
  const [np, setNp] = useState('');
  const [na, setNa] = useState('');
  const [busyLogin, setBusyLogin] = useState(false);

  const reloadLogins = useCallback(async () => {
    try { setLogins(await listProfileLogins(clientId, profile)); } catch { /* keep prior */ }
  }, [clientId, profile]);

  useEffect(() => {
    let alive = true;
    (async () => {
      setLoading(true); setErr('');
      try {
        const d = await loadProfileDetail(clientId, profile);
        if (!alive) return;
        setName(d.name); setNote(d.note); setProxyUrl(d.proxyUrl); setLogins(d.logins); setIpInfo(d.ipInfo || null);
      } catch (e: any) {
        if (alive) setErr(e?.message || String(e));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => { alive = false; };
  }, [clientId, profile]);

  const save = async () => {
    if (saving) return;
    setSaving(true); setErr(''); setSavedMsg('');
    try {
      await saveProfileConfig(clientId, profile, {
        ...(isElectron ? { name } : {}),
        note,
        proxy: proxyUrl,
      });
      setSavedMsg(t('bwSaved')); setTimeout(() => setSavedMsg(''), 2000);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setSaving(false);
    }
  };

  const onProbeIp = async () => {
    if (probing) return;
    setProbing(true); setErr('');
    try { setIpInfo(await probeProfileIp(clientId, profile)); }
    catch (e: any) { setErr(e?.message || String(e)); }
    finally { setProbing(false); }
  };

  const onSetLogin = async (login: Partial<LoginRec> & { name: string }) => {
    if (busyLogin) return;
    setBusyLogin(true); setErr('');
    try { await setProfileLogin(clientId, profile, login); await reloadLogins(); }
    catch (e: any) { setErr(e?.message || String(e)); }
    finally { setBusyLogin(false); }
  };
  const onAddLogin = async () => {
    const name = np.trim();
    if (!name || busyLogin) return;
    await onSetLogin({ name, ...(na.trim() ? { url: na.trim() } : {}) });
    setNp(''); setNa('');
  };
  const onRemoveLogin = async (name: string) => {
    if (busyLogin) return;
    setBusyLogin(true); setErr('');
    try { await removeProfileLogin(clientId, profile, name); await reloadLogins(); }
    catch (e: any) { setErr(e?.message || String(e)); }
    finally { setBusyLogin(false); }
  };

  const labelCls = 'text-[11px] text-zinc-500 mb-1';
  const inputCls = 'w-full bg-[#141414] border border-white/[0.08] rounded-lg px-2.5 py-2 text-[13px] text-zinc-200 placeholder:text-zinc-600 outline-none focus:border-white/20';

  return createPortal(
    <div data-id="ProfileConfigModal" className="fixed inset-0 z-[1000] flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onClose}>
      <div
        data-id="profile-config-card"
        className="w-[1080px] max-w-[96vw] h-[82vh] max-h-[88vh] flex flex-col rounded-2xl border border-white/[0.08] bg-[#0e0e0e] shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div data-id="profile-config-header" className="h-12 px-4 flex items-center gap-2 border-b border-white/[0.06] shrink-0">
          {isElectron ? <Atom className="w-4 h-4 text-zinc-400" /> : <Chrome className="w-4 h-4 text-zinc-400" />}
          <span className="text-[13px] text-zinc-100 flex-1 truncate">{t('bwConfigTitle', { name: profile.name })}</span>
          <span className="text-[10px] text-zinc-600">#{profile.accountIdx} · {profile.backend}</span>
          <button data-id="profile-config-close" onClick={onClose} className="p-1 rounded text-zinc-600 hover:text-zinc-300 cursor-pointer"><X className="w-4 h-4" /></button>
        </div>

        <div className="flex-1 min-h-0 overflow-hidden p-5 flex flex-col gap-4">
          {loading ? (
            <div className="flex items-center gap-2 text-[12px] text-zinc-500 py-6 justify-center"><Loader2 className="w-4 h-4 animate-spin" />{t('bwLoadingConfig')}</div>
          ) : (
            <>
              {err && <div className="text-[12px] text-zinc-300 bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 shrink-0">{err}</div>}

              {/* top fields — two roomy columns */}
              <div className="grid grid-cols-2 gap-x-5 gap-y-3 shrink-0">
                <div>
                  <div className={labelCls}>{t('bwFieldName')}{!isElectron && t('bwChromeReadonly')}</div>
                  <input data-id="profile-config-name" value={name} onChange={(e) => setName(e.target.value)} disabled={!isElectron}
                    className={cn(inputCls, !isElectron && 'opacity-60 cursor-not-allowed')} placeholder={`Profile${profile.accountIdx}`} />
                </div>
                <div>
                  <div className={labelCls}>{t('bwFieldProxy')}{!isElectron && t('bwProxyNextLaunch')}</div>
                  <input data-id="profile-config-proxy" value={proxyUrl} onChange={(e) => setProxyUrl(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') save(); }} className={inputCls} placeholder="socks5://127.0.0.1:20001" />
                </div>
                <div>
                  <div className={labelCls}>{t('bwFieldNote')}</div>
                  <input data-id="profile-config-note" value={note} onChange={(e) => setNote(e.target.value)}
                    className={inputCls} placeholder={t('bwNotePlaceholder')} />
                </div>
                <div>
                  <div className={cn(labelCls, 'flex items-center gap-2')}>
                    <span>{t('bwFieldIp')}</span>
                    <button data-id="profile-config-probe-ip" onClick={onProbeIp} disabled={probing}
                      className="ml-auto rounded-md px-2 py-0.5 text-[11px] bg-white/[0.07] text-zinc-200 hover:bg-white/[0.12] cursor-pointer disabled:opacity-50 inline-flex items-center gap-1">
                      {probing ? <Loader2 className="w-3 h-3 animate-spin" /> : <RefreshCw className="w-3 h-3" />}{ipInfo && ipInfo.ip ? t('bwReprobe') : t('bwProbe')}
                    </button>
                  </div>
                  <div data-id="profile-config-ipinfo" className="flex items-center gap-3 text-[12px] bg-[#141414] border border-white/[0.08] rounded-lg px-2.5 h-[38px]">
                    {ipInfo && ipInfo.ip ? (
                      <>
                        <span className="font-mono text-zinc-100">{ipInfo.ip}</span>
                        <span className="text-zinc-400 truncate">{ipInfo.area || '—'}</span>
                        <span className="ml-auto shrink-0 text-[11px] text-zinc-600">{t('bwProbedAt', { time: fmtLoginTime(ipInfo.probedAt) })}</span>
                      </>
                    ) : (
                      <span className="text-zinc-600">{t('bwNotProbed')}{probing ? t('bwProbingSuffix') : ''}</span>
                    )}
                  </div>
                </div>
              </div>

              {/* login records — fills the rest; the table scrolls internally with a sticky header */}
              <div className="flex-1 min-h-0 flex flex-col gap-2">
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-[12px] text-zinc-300 font-medium">{t('bwLoginsTitle')}</span>
                  <span className="text-[11px] text-zinc-600">{t('bwLoginsSubtitle', { count: logins.length })}</span>
                </div>
                <div className="flex-1 min-h-0">
                  <LoginTable logins={logins} busy={busyLogin} onSet={onSetLogin} onRemove={onRemoveLogin} />
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                  <input data-id="profile-config-login-name" value={np} onChange={(e) => setNp(e.target.value)} placeholder={t('bwLoginNamePlaceholder')}
                    className="w-44 shrink-0 bg-[#141414] border border-white/[0.08] rounded-lg px-2.5 py-2 text-[12px] text-zinc-200 placeholder:text-zinc-600 outline-none focus:border-white/20" />
                  <input data-id="profile-config-login-url" value={na} onChange={(e) => setNa(e.target.value)} placeholder={t('bwLoginUrlPlaceholder')}
                    onKeyDown={(e) => { if (e.key === 'Enter') onAddLogin(); }}
                    className="flex-1 min-w-0 bg-[#141414] border border-white/[0.08] rounded-lg px-2.5 py-2 text-[12px] text-zinc-200 placeholder:text-zinc-600 outline-none focus:border-white/20" />
                  <button data-id="profile-config-login-add" onClick={onAddLogin} disabled={busyLogin || !np.trim()}
                    className="rounded-lg px-3 py-2 text-[12px] bg-white/[0.08] text-zinc-100 hover:bg-white/[0.14] cursor-pointer disabled:opacity-50 shrink-0">{t('bwAdd')}</button>
                </div>
              </div>
            </>
          )}
        </div>

        {!loading && (
          <div data-id="profile-config-footer" className="h-14 px-4 flex items-center justify-end gap-2 border-t border-white/[0.06] shrink-0">
            <span className="text-[12px] text-emerald-400 mr-auto">{savedMsg}</span>
            <button data-id="profile-config-cancel" onClick={onClose} className="rounded-lg px-3 py-1.5 text-[13px] text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.04] cursor-pointer">{t('bwClose')}</button>
            <button data-id="profile-config-save" onClick={save} disabled={saving}
              className="flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-[13px] font-medium bg-white/[0.10] text-zinc-100 hover:bg-white/[0.16] cursor-pointer disabled:opacity-50">
              {saving && <Loader2 className="w-3.5 h-3.5 animate-spin" />}{t('bwSave')}
            </button>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

// Per-window dom-ready inject script (the "extension"): ~/data/electron/
// extension/inject/<domain>.js on the device. Read/edit/save here, open in the
// native editor, or hand to the agent to edit.
function InjectScriptModal({
  clientId, win, onClose, onOpenInEditor, onSendToAgent,
}: {
  clientId: string; win: WinItem; onClose: () => void;
  onOpenInEditor: (path: string) => void;
  onSendToAgent: (text: string) => void;
}) {
  const { t } = useTranslation('layout');
  const domain = injectDomainForUrl(win.url);
  const [absPath, setAbsPath] = useState('');
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');

  useEffect(() => {
    let alive = true;
    (async () => {
      setLoading(true); setErr('');
      if (!domain) { setErr(t('bwErrNoDomain')); setLoading(false); return; }
      try {
        const p = await resolveInjectPath(clientId, domain);
        const c = await readInjectFile(clientId, p);
        if (!alive) return;
        setAbsPath(p); setContent(c);
      } catch (e: any) { if (alive) setErr(e?.message || String(e)); }
      finally { if (alive) setLoading(false); }
    })();
    return () => { alive = false; };
  }, [clientId, domain]);

  const save = async () => {
    if (saving || !absPath) return;
    setSaving(true); setErr(''); setMsg('');
    try { await writeInjectFile(clientId, absPath, content); setMsg(t('bwSaved')); setTimeout(() => setMsg(''), 2000); }
    catch (e: any) { setErr(e?.message || String(e)); }
    finally { setSaving(false); }
  };

  const sendToAgent = () => {
    onSendToAgent(t('bwPromptInject', { domain, path: absPath || t('bwInjectPathUnresolved') }));
    onClose();
  };

  return createPortal(
    <div data-id="InjectScriptModal" className="fixed inset-0 z-[1000] flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onClose}>
      <div data-id="inject-script-card" className="w-[560px] max-w-[94vw] max-h-[88vh] flex flex-col rounded-xl border border-white/[0.08] bg-[#0e0e0e] shadow-2xl overflow-hidden" onClick={(e) => e.stopPropagation()}>
        <div className="h-12 px-4 flex items-center gap-2 border-b border-white/[0.06] shrink-0">
          <Code2 className="w-4 h-4 text-zinc-400" />
          <span className="text-[13px] text-zinc-100 flex-1 truncate">{t('bwInjectTitle')}{domain ? ` · ${domain}` : ''}</span>
          <button data-id="inject-script-close" onClick={onClose} className="p-1 rounded text-zinc-600 hover:text-zinc-300 cursor-pointer"><X className="w-4 h-4" /></button>
        </div>

        <div className="px-4 pt-3 shrink-0">
          {absPath && <div className="text-[10px] text-zinc-600 truncate mb-2" title={absPath}>{absPath}</div>}
          {err && <div className="text-[12px] text-amber-300/90 bg-amber-500/[0.06] border border-amber-500/20 rounded-lg px-3 py-2 mb-2">{err}</div>}
        </div>

        <div className="flex-1 overflow-hidden px-4 pb-1 min-h-[200px]">
          {loading ? (
            <div className="flex items-center gap-2 text-[12px] text-zinc-500 py-6 justify-center"><Loader2 className="w-4 h-4 animate-spin" />{t('bwLoadingScript')}</div>
          ) : (
            <textarea
              data-id="inject-script-editor"
              value={content}
              onChange={(e) => setContent(e.target.value)}
              spellCheck={false}
              placeholder={t('bwInjectPlaceholder')}
              className="w-full h-full min-h-[200px] resize-none bg-[#0a0a0a] border border-white/[0.08] rounded-lg px-3 py-2 text-[12px] font-mono text-zinc-200 placeholder:text-zinc-600 outline-none focus:border-white/20"
            />
          )}
        </div>

        <div className="h-14 px-4 flex items-center gap-2 border-t border-white/[0.06] shrink-0">
          <span className="text-[12px] text-emerald-400 mr-auto">{msg}</span>
          <button data-id="inject-script-open-editor" onClick={() => absPath && onOpenInEditor(absPath)} disabled={!absPath}
            className="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] text-zinc-300 hover:text-zinc-100 hover:bg-white/[0.06] cursor-pointer disabled:opacity-50">
            <Pencil className="w-3.5 h-3.5" />{t('bwOpenEditor')}
          </button>
          <button data-id="inject-script-send-agent" onClick={sendToAgent} disabled={!absPath}
            className="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] text-blue-300 hover:text-blue-200 hover:bg-white/[0.06] cursor-pointer disabled:opacity-50">
            <Send className="w-3.5 h-3.5" />{t('bwSendAgent')}
          </button>
          <button data-id="inject-script-save" onClick={save} disabled={saving || loading || !absPath}
            className="flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-[13px] font-medium bg-white/[0.10] text-zinc-100 hover:bg-white/[0.16] cursor-pointer disabled:opacity-50">
            {saving && <Loader2 className="w-3.5 h-3.5 animate-spin" />}{t('bwSave')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function WindowCard({ clientId, deviceId, profile, win, onRefresh, onSendToAgent, onOpenInEditor }: {
  clientId: string; deviceId: string; profile: Profile; win: WinItem;
  onRefresh: () => void; onSendToAgent: (text: string) => void; onOpenInEditor: (path: string) => void;
}) {
  const { t } = useTranslation('layout');
  const [shot, setShot] = useState<string>('');
  const [shooting, setShooting] = useState(false);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState<'' | WinAction>('');
  const [injectOpen, setInjectOpen] = useState(false);

  const capture = useCallback(async () => {
    setShooting(true); setErr('');
    try {
      setShot(await captureWindow(clientId, profile, win));
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setShooting(false);
    }
  }, [clientId, profile, win]);

  useEffect(() => {
    if (win.status === 'open') capture();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const act = async (action: WinAction) => {
    if (busy) return;
    setBusy(action); setErr('');
    try {
      await windowAction(clientId, profile, win, action);
      if (action === 'reload') { setTimeout(() => capture(), 1000); }   // content changed → re-shoot
      else { onRefresh(); }                                            // open/close change the list
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setBusy('');
    }
  };

  const open = win.status === 'open';
  const ActBtn = ({ action, title, icon }: { action: WinAction; title: string; icon: React.ReactNode; }) => {
    // reload/close need a live window; open is always allowed (reopen if closed)
    const disabled = !!busy || (action !== 'open' && !open);
    return (
      <button
        data-id={`browser-window-${action}`}
        onClick={() => act(action)}
        disabled={disabled}
        title={title}
        className="p-1 rounded text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.06] transition-colors cursor-pointer disabled:opacity-30 disabled:cursor-not-allowed"
      >
        {busy === action ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : icon}
      </button>
    );
  };

  return (
    <div data-id="BrowserWindowCard" className="shrink-0 rounded-xl border border-white/[0.1] bg-[#161619] overflow-hidden shadow-lg shadow-black/50 hover:border-white/20 transition-colors">
      <div data-id="browser-window-shot" className="relative aspect-[16/10] bg-gradient-to-b from-[#222228] to-[#16161a] flex items-center justify-center">
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
          title={win.status === 'closed' ? t('bwWindowClosed') : t('bwScreenshot')}
          className="absolute bottom-1.5 right-1.5 p-1.5 rounded-md bg-black/60 text-zinc-300 hover:text-white hover:bg-black/80 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {shooting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Camera className="w-3.5 h-3.5" />}
        </button>
        {win.status === 'closed' && (
          <span className="absolute top-1.5 left-1.5 text-[10px] px-1 rounded bg-zinc-700/70 text-zinc-300">{t('bwClosedBadge')}</span>
        )}
      </div>
      <div className="px-2.5 py-2 flex items-center gap-1.5 bg-white/[0.035] border-t border-white/[0.06]">
        <div className="min-w-0 flex-1">
          <div data-id="browser-window-title" className="text-[12px] text-zinc-200 font-medium truncate" title={win.title}>{win.title}</div>
          {win.url && <div className="text-[10px] text-zinc-500 truncate" title={win.url}>{win.url}</div>}
        </div>
        <div data-id="browser-window-actions" className="flex items-center gap-0.5 shrink-0">
          <button
            data-id="browser-window-send"
            onClick={() => onSendToAgent(buildAgentPrompt({ clientId, deviceId }, profile, win))}
            title={t('bwSendToAgent')}
            className="p-1 rounded text-zinc-500 hover:text-blue-300 hover:bg-white/[0.06] transition-colors cursor-pointer"
          >
            <Send className="w-3.5 h-3.5" />
          </button>
          <ActBtn action="open" title={open ? t('bwBringFront') : t('bwReopen')} icon={<Eye className="w-3.5 h-3.5" />} />
          <ActBtn action="reload" title={t('bwReloadPage')} icon={<RotateCcw className="w-3.5 h-3.5" />} />
          <ActBtn action="close" title={t('bwCloseWindow')} icon={<X className="w-3.5 h-3.5" />} />
        </div>
      </div>
      {injectOpen && (
        <InjectScriptModal
          clientId={clientId}
          win={win}
          onClose={() => setInjectOpen(false)}
          onOpenInEditor={onOpenInEditor}
          onSendToAgent={onSendToAgent}
        />
      )}
    </div>
  );
}
