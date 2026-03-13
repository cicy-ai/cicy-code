import { useState, useCallback, useEffect } from 'react';

export interface DesktopApp {
  id: string;
  label: string;
  emoji: string;
  url: string;
}

const STORAGE_KEY = 'desktop_apps';

export function useDesktopApps(paneId: string) {
  const [apps, setApps] = useState<DesktopApp[]>(() => {
    try { return JSON.parse(localStorage.getItem(`${STORAGE_KEY}_${paneId}`) || '[]'); } catch { return []; }
  });

  useEffect(() => { localStorage.setItem(`${STORAGE_KEY}_${paneId}`, JSON.stringify(apps)); }, [paneId, apps]);

  const addApp = useCallback((app: DesktopApp) => {
    setApps(prev => {
      if (prev.find(a => a.id === app.id)) return prev.map(a => a.id === app.id ? { ...a, ...app } : a);
      return [...prev, app];
    });
  }, []);

  const removeApp = useCallback((id: string) => setApps(prev => prev.filter(a => a.id !== id)), []);

  return { apps, addApp, removeApp };
}

// Call any Electron MCP tool via IPC, fallback null
export async function electronRPC(tool: string, args: Record<string, any> = {}): Promise<any> {
  try {
    const { ipcRenderer } = (window as any).require('electron');
    return await ipcRenderer.invoke('rpc', tool, args);
  } catch { return null; }
}

// Open URL: agent apps (*.de5.net/localhost) in Electron, others in system browser
export function openInElectron(url: string, _title?: string, forceElectron = false, width?: number, height?: number) {
  try {
    const u = new URL(url);
    if (forceElectron || u.hostname === 'localhost' || u.hostname.endsWith('.de5.net')) {
      const args: any = { url, reuseWindow: false };
      if (width) args.width = width;
      if (height) args.height = height;
      electronRPC('open_window', args).catch(() => window.open(url, '_blank'));
      return;
    }
  } catch {}
  electronRPC('exec_shell', { command: `open "${url}"` }).catch(() => window.open(url, '_blank'));
}
