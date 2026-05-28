
const LS_API_BASE = 'cicy_api_base';
const SS_HOST_HOME = 'cicy_host_home';
const DEFAULT_CICY_ROOT = '~/cicy-ai';
const DEFAULT_HOST_HOME = import.meta.env.VITE_HOST_HOME || DEFAULT_CICY_ROOT;
const APP_VERSION = '2.1.3';

function inferApiBase(): string {
  const envBase = import.meta.env.VITE_API_BASE || '';
  if (typeof window === 'undefined') return envBase;

  const { hostname, host, origin } = window.location;
  const isTunnelHost = /^g-\d+\.cicy-ai\.com$/.test(hostname);
  const managedBase = envBase || 'https://api.cicy-ai.com';

  const saved = localStorage.getItem(LS_API_BASE);
  if (saved) {
    if (!isTunnelHost) return saved;
    try {
      const savedHost = new URL(saved).hostname;
      if (!/^g-\d+\.cicy-ai\.com$/.test(savedHost)) return saved;
    } catch {
      return saved;
    }
  }

  if (hostname === 'app.cicy-ai.com' || hostname === 'api.cicy-ai.com' || /^audit\./.test(hostname)) return origin;
  if (isTunnelHost) return managedBase;

  const proMatch = hostname.match(/^(u-.+)-app\.cicy-ai\.com$/);
  if (proMatch) return `https://${proMatch[1]}-api.cicy-ai.com`;

  const freeMatch = hostname.match(/^(u-.+)-free-app\.cicy-ai\.com$/);
  if (freeMatch) return `https://${freeMatch[1]}-free-api.cicy-ai.com`;

  if (host.startsWith('localhost:') || host.startsWith('127.0.0.1:')) return envBase || '';

  return envBase || origin;
}


export function getApiBase(): string {
  return inferApiBase();
}

export function setApiBase(base: string) {
  if (base) localStorage.setItem(LS_API_BASE, base);
  else localStorage.removeItem(LS_API_BASE);
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

function toRuntimeAbsolutePath(path: string): string {
  return path.replace(/^~(?=\/|$)/, getHostHome());
}

export function toTildePath(path: string): string {
  const home = getHostHome();
  if (path === home) return '~';
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}

export function defaultWorkerWorkspace(paneId: string): string {
  return `${DEFAULT_CICY_ROOT}/workers/${paneId}`;
}

// Workspace: Pro → u-xxx-api, Trial → u-xxx-free-api
const host = typeof window !== 'undefined' ? window.location.hostname : '';
const isWorkspace = /^(u-.+)-(app|free-app)\.cicy-ai\.com$/.test(host);

const isAudit = typeof window !== 'undefined' && /^audit\./.test(window.location.hostname);

// prod uses same-origin or inferred workspace api domain; localhost/dev can still use VITE_API_BASE
const base = getApiBase();

const config = {
  apiBase:        base,
  mgrBase:        base,
  ttydBase:       base,
  ideBase:        base,
  openClawBase:   base ? base + '/openclaw' : '/openclaw',
  hostHome:       DEFAULT_HOST_HOME,
  desktopBase:    base,
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
  openClaw:   (token?: string)                           => `${config.openClawBase}${token ? `?token=${encodeURIComponent(token)}` : ''}`,
  desktop:    (token: string)                            => `${config.desktopBase}/?token=${token}`,
  idePane:    (paneId: string, token: string)            => `${config.ideBase}/ttyd/${paneId}/?token=${token}`,
  stt:        ()                                         => `${config.sttBase}/stt`,
};

export default config;
