
import pkg from '../package.json';

const SS_HOST_HOME = 'cicy_host_home';
const DEFAULT_CICY_ROOT = '~/cicy-ai';
const DEFAULT_HOST_HOME = import.meta.env.VITE_HOST_HOME || DEFAULT_CICY_ROOT;
const APP_VERSION = pkg.version;

// 前端一律同域名直连:apiBase 为空 → 走当前 origin(本地实例 / 自托管)。dev 可用
// VITE_API_BASE 覆盖。旧的 cicy-ai.com SaaS 域名路由(app/api/audit/workspace/tunnel)已下线。
function inferApiBase(): string {
  return import.meta.env.VITE_API_BASE || '';
}


export function getApiBase(): string {
  return inferApiBase();
}

export function getHostHome(): string {
  if (typeof window !== 'undefined') {
    try {
      const saved = sessionStorage.getItem(SS_HOST_HOME);
      if (saved) {
        config.hostHome = saved;
        return saved;
      }
    } catch {}
  }
  return config.hostHome || DEFAULT_HOST_HOME;
}

export function setHostHome(home: string) {
  const next = (home || '').trim();
  if (!next) return;
  config.hostHome = next;
  if (typeof window !== 'undefined') {
    try {
      sessionStorage.setItem(SS_HOST_HOME, next);
    } catch {}
  }
}

function inferHostHomeFromPath(path: string | null | undefined): string | null {
  const value = (path || '').trim();
  if (!value || value.startsWith('~')) return null;
  const match = value.match(/^(\/(?:home\/[^/]+|root))(?:\/|$)/);
  return match ? match[1] : null;
}

export function syncHostHomeFromPath(path: string | null | undefined): string | null {
  const inferred = inferHostHomeFromPath(path);
  if (inferred) setHostHome(inferred);
  return inferred;
}

export function toTildePath(path: string): string {
  const home = getHostHome();
  if (path === home) return '~';
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}

export function defaultWorkerWorkspace(paneId: string): string {
  return `${DEFAULT_CICY_ROOT}/workers/${paneId}`;
}

// cicy-ai.com SaaS 域名(workspace / audit)已下线 → 恒为 false。
const isWorkspace = false;
const isAudit = false;

// prod uses same-origin or inferred workspace api domain; localhost/dev can still use VITE_API_BASE
const base = getApiBase();

const config = {
  apiBase:        base,
  mgrBase:        base,
  ttydBase:       base,
  hostHome:       DEFAULT_HOST_HOME,
  sttBase:        base,
  pollInterval:   1000,
  version:        APP_VERSION,
  isWorkspace,
  isAudit,
};

console.log('[config] version', config.version);

export const urls = {
  ttyd:       (paneId: string, token: string, mode = 1, lang?: string) => {
    const base = `${config.ttydBase}/ttyd/${paneId}/?token=${token}&mode=${mode}`;
    return lang ? `${base}&lang=${encodeURIComponent(lang)}` : base;
  },
  ttydOpen:   (paneId: string, token: string, lang?: string) => {
    const base = `${config.ttydBase}/ttyd/${paneId}/?token=${token}`;
    return lang ? `${base}&lang=${encodeURIComponent(lang)}` : base;
  },
};

export default config;
