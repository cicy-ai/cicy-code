

const config = {
  apiBase:        import.meta.env.VITE_API_BASE         || 'https://dev-api.cicy-ai.com',
  ttydBase:       import.meta.env.VITE_TTYD_BASE        || 'https://dev-api.cicy-ai.com',
  ideBase:        import.meta.env.VITE_IDE_BASE          || 'https://dev.cicy-ai.com',
  codeServerBase: import.meta.env.VITE_CODE_SERVER_BASE  || `${import.meta.env.VITE_API_BASE || 'https://dev-api.cicy-ai.com'}/code`,
  hostHome:       import.meta.env.VITE_HOST_HOME         || '/home/w3c_offical',
  desktopBase:    import.meta.env.VITE_DESKTOP_BASE      || 'https://dev.cicy-ai.com',
  sttBase:        import.meta.env.VITE_STT_BASE          || 'https://dev-api.cicy-ai.com',
  pollInterval:   5000,
  version:        '1.0.0-cicy-code',
} as const;

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
