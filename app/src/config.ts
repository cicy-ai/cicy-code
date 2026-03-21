
// Workspace: Pro → u-xxx-api, Trial → u-xxx-free-api
const host = typeof window !== 'undefined' ? window.location.hostname : '';
const appMatch = host.match(/^(u-.+)-app\.cicy-ai\.com$/);
const isWorkspace = appMatch !== null;
const slug = appMatch ? appMatch[1] : '';
const origin = isWorkspace ? window.location.origin : '';
const isTrial = slug.endsWith('-free');
const baseSlug = isTrial ? slug.replace(/-free$/, '') : slug;
const apiOrigin = isWorkspace ? `https://${baseSlug}-${isTrial ? 'free-api' : 'api'}.cicy-ai.com` : '';

const isDev = typeof window !== 'undefined' && /^(localhost|127\.)/.test(window.location.hostname);
const isDevProxy = typeof window !== 'undefined' && /^dev-p\d+\.cicy-ai\.com$/.test(window.location.hostname);
const isAudit = typeof window !== 'undefined' && /^audit\./.test(window.location.hostname);

// dev-api for dev/devProxy, empty (same origin) for localhost, apiOrigin for workspace
const base = import.meta.env.VITE_API_BASE || '';

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
