// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Thin wrapper around cicy-desktop's electronRPC bridge. Renderer is trusted
// (registered backend hostname) so window.electronRPC is auto-injected by
// cicy-desktop. When not in cicy-desktop (plain browser), every call rejects.
export async function electronRPC(tool: string, args: Record<string, any> = {}): Promise<any> {
  const rpc = (window as any).electronRPC;
  if (typeof rpc !== 'function') throw new Error('electronRPC not available (open this page from cicy-desktop)');
  const raw = await rpc(tool, args);
  if (raw && Array.isArray(raw.content)) {
    return raw.content.map((c: any) => c.text ?? '').join('\n');
  }
  return raw;
}

function parseElectronRPCJSON(raw: any): any {
  if (typeof raw !== 'string') return raw;
  const text = raw.trim();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function comparableTabUrl(value: string): string {
  try {
    const url = new URL(value);
    url.hash = '';
    if (url.pathname.length > 1) url.pathname = url.pathname.replace(/\/+$/, '');
    return url.toString();
  } catch {
    return value.replace(/#.*$/, '').replace(/\/+$/, '');
  }
}

/**
 * Open a URL in an Electron profile tab without creating duplicates.
 * Returns false outside cicy-desktop so callers can fall back to window.open.
 */
export async function openOrActivateElectronProfileTab(url: string, accountIdx = 1): Promise<boolean> {
  if (typeof (window as any).electronRPC !== 'function') return false;

  const listed = parseElectronRPCJSON(await electronRPC('electron_tabs', { accountIdx }));
  const tabs: any[] = Array.isArray(listed?.tabs) ? listed.tabs : [];
  const wanted = comparableTabUrl(url);
  const existing = tabs.find((tab) =>
    typeof tab?.url === 'string' && comparableTabUrl(tab.url) === wanted,
  );

  if (existing && typeof existing.webContentsId === 'number') {
    await electronRPC('electron_tab_activate', { webContentsId: existing.webContentsId });
  } else {
    await electronRPC('electron_tab_open', { accountIdx, url });
  }
  return true;
}

// exec_shell has a ~30s hard timeout on the cicy-desktop side. Use this for
// short queries only (curl probes, version checks, config writes).
export async function execShell(command: string): Promise<string> {
  const out = await electronRPC('exec_shell', { command });
  return typeof out === 'string' ? out : JSON.stringify(out ?? '');
}

// For commands that may exceed 30s (apt install, large downloads), spawn into
// the background and stream output to a log file. Caller polls the log with
// tailLog(). Returns a token (the log path) to use with tailLog/isAlive.
export async function execShellBackground(command: string, label: string): Promise<string> {
  const safe = label.replace(/[^a-zA-Z0-9._-]/g, '_');
  const log = `/tmp/cicy-speedup-${safe}-${Date.now()}.log`;
  const wrapped = `nohup bash -lc ${JSON.stringify(command + ' ; echo __DONE__:$?')} > ${log} 2>&1 & disown; echo ${log}`;
  await execShell(wrapped);
  return log;
}

export async function tailLog(path: string, lines = 200): Promise<string> {
  return await execShell(`tail -n ${lines} ${path} 2>/dev/null || true`);
}

export function isLogDone(content: string): { done: boolean; exitCode: number | null } {
  const m = content.match(/__DONE__:(-?\d+)/);
  if (!m) return { done: false, exitCode: null };
  return { done: true, exitCode: parseInt(m[1], 10) };
}
