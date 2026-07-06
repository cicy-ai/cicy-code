// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Call any Electron MCP tool via IPC, fallback null
async function electronRPC(tool: string, args: Record<string, any> = {}): Promise<any> {
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
      if (width || height) {
        args.options = {};
        if (width) args.options.width = width;
        if (height) args.options.height = height;
      }
      electronRPC('open_window', args).catch(() => window.open(url, '_blank'));
      return;
    }
  } catch {}
  electronRPC('exec_shell', { command: `open "${url}"` }).catch(() => window.open(url, '_blank'));
}
