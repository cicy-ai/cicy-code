export type AppViewType = 'desktop' | 'workspace' | 'audit' | 'speedup' | 'wsl-install';

export interface AppRoute {
  view: AppViewType;
  agentId: string;
  canonicalHash?: string;
}

export function parseAppHash(hashValue: string): AppRoute {
  const hash = String(hashValue || '');
  if (hash.startsWith('#/audit')) return { view: 'audit', agentId: '' };
  if (hash.startsWith('#/speedup')) return { view: 'speedup', agentId: '' };
  if (hash.startsWith('#/wsl-install')) return { view: 'wsl-install', agentId: '' };
  if (hash.startsWith('#/project/')) return { view: 'workspace', agentId: 'w-1001' };
  if (hash.startsWith('#/agent/')) {
    const match = hash.match(/\/agent\/([^/]+)/);
    return { view: 'workspace', agentId: match ? decodeURIComponent(match[1]).replace(/:.*$/, '') : 'w-1001' };
  }
  return { view: 'workspace', agentId: 'w-1001', canonicalHash: '#/project/default' };
}
