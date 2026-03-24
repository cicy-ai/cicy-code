
const LS_API_BASE = 'cicy_api_base';

export function getApiBase(): string {
  // if (typeof window !== 'undefined') {
  //   const saved = localStorage.getItem(LS_API_BASE);
  //   if (saved) return saved;
  // }
  return import.meta.env.VITE_API_BASE || '';
}

export function setApiBase(base: string) {
  if (base) localStorage.setItem(LS_API_BASE, base);
  else localStorage.removeItem(LS_API_BASE);
}

// Workspace: Pro → u-xxx-api, Trial → u-xxx-free-api
const host = typeof window !== 'undefined' ? window.location.hostname : '';
const appMatch = host.match(/^(u-.+)-app\.cicy-ai\.com$/);
const isWorkspace = appMatch !== null;

const isAudit = typeof window !== 'undefined' && /^audit\./.test(window.location.hostname);

// dev-api for dev/devProxy, empty (same origin) for localhost, apiOrigin for workspace
const base = getApiBase();

const config = {
  apiBase:        base,
  mgrBase:        base,
  ttydBase:       base,
  ideBase:        base,
  codeServerBase: base ? base + '/code' : '/code',
  hostHome:       import.meta.env.VITE_HOST_HOME || '/home/w3c_offical',
  desktopBase:    base,
  sttBase:        base,
  pollInterval:   5000,
  version:        '1.0.0-cicy-code',
  isWorkspace,
  isAudit,
};

export const urls = {
  ttyd:       (paneId: string, token: string, mode = 1) => `${config.ttydBase}/ttyd/${paneId}/?token=${token}&mode=${mode}`,
  ttydOpen:   (paneId: string, token: string)            => `${config.ttydBase}/ttyd/${paneId}/?token=${token}`,
  codeServer: (folder: string, token?: string) => {
    const f = folder.replace('~', config.hostHome);
    return `${config.codeServerBase}/?folder=${encodeURIComponent(f)}${token ? '&token=' + token : ''}`;
  },
  desktop:    (token: string)                            => `${config.desktopBase}/?token=${token}`,
  idePane:    (paneId: string, token: string)            => `${config.ideBase}/ttyd/${paneId}/?token=${token}`,
  stt:        ()                                         => `${config.sttBase}/stt`,
};

export default config;
