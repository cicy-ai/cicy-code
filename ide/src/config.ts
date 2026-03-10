

const config = {
  apiBase:        import.meta.env.VITE_API_BASE         || 'https://g-14446.cicy.de5.net',
  ttydBase:       import.meta.env.VITE_TTYD_BASE        || 'https://g-14446.cicy.de5.net',
  ideBase:        import.meta.env.VITE_IDE_BASE          || 'https://ide.cicy.de5.net',
  codeServerBase: import.meta.env.VITE_CODE_SERVER_BASE  || 'https://code.cicy.de5.net',
  desktopBase:    import.meta.env.VITE_DESKTOP_BASE      || 'https://desktop.cicy.de5.net',
  sttBase:        import.meta.env.VITE_STT_BASE          || 'https://g-15003.cicy.de5.net',
  pollInterval:   5000,
  version:        '0.1.0-ttyd-manager',
} as const;

export const urls = {
  ttyd:       (paneId: string, token: string, mode = 1) => `${config.ttydBase}/ttyd/${paneId}/?token=${token}&mode=${mode}`,
  ttydOpen:   (paneId: string, token: string)            => `${config.ttydBase}/ttyd/${paneId}/?token=${token}`,
  codeServer: (folder: string)                           => `${config.codeServerBase}/?folder=${encodeURIComponent(folder)}`,
  desktop:    (token: string)                            => `${config.desktopBase}/?token=${token}`,
  idePane:    (paneId: string, token: string)            => `${config.ideBase}/ttyd/${paneId}/?token=${token}`,
  stt:        ()                                         => `${config.sttBase}/stt`,
};

export default config;
