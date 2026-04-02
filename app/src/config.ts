
const LS_API_BASE = 'cicy_api_base';

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

  if (hostname === 'dev.cicy-ai.com') return 'https://dev-api.cicy-ai.com';
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
  const envBase = import.meta.env.VITE_API_BASE || '';
  console.log("envBase:",envBase)
      	return envBase;
}

export function setApiBase(base: string) {
  if (base) localStorage.setItem(LS_API_BASE, base);
  else localStorage.removeItem(LS_API_BASE);
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
  codeServerBase: base ? base + '/code' : '/code',
  openClawBase:   base ? base + '/openclaw' : '/openclaw',
  hostHome:       import.meta.env.VITE_HOST_HOME || '/home/w3c_offical',
  desktopBase:    base,
  sttBase:        base,
  pollInterval:   5000,
  version:        '1.0.0-cicy-code',
  isWorkspace,
  isAudit,
};

console.log('[config] version', config.version);

export const urls = {
  ttyd:       (paneId: string, token: string, mode = 1) => `${config.ttydBase}/ttyd/${paneId}/?token=${token}&mode=${mode}`,
  ttydOpen:   (paneId: string, token: string)            => `${config.ttydBase}/ttyd/${paneId}/?token=${token}`,
  codeServer: (folder: string, token?: string) => {
    const f = folder.replace('~', config.hostHome);
    return `${config.codeServerBase}/?folder=${encodeURIComponent(f)}${token ? '&token=' + token : ''}`;
  },
  openClaw:   (token?: string)                           => `${config.openClawBase}${token ? `?token=${encodeURIComponent(token)}` : ''}`,
  desktop:    (token: string)                            => `${config.desktopBase}/?token=${token}`,
  idePane:    (paneId: string, token: string)            => `${config.ideBase}/ttyd/${paneId}/?token=${token}`,
  stt:        ()                                         => `${config.sttBase}/stt`,
};

export default config;
