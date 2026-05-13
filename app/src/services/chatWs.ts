// Module-level singleton that owns the chat WebSocket. The WS lifetime is
// completely decoupled from React: components only `configure()`, `subscribe()`,
// and `send()`. configure() reconnects ONLY when URL-affecting params change;
// re-renders / StrictMode double-mounts do not disturb the connection.

type ChatWsParams = {
  apiBase: string;
  paneId: string;          // master agent id (used to build master_agent_id)
  token: string;
  clientId: string;        // pageClientId
  platform: string;        // win/darwin/linux
  userAgent: string;       // navigator.userAgent
  isElectron: boolean;
};

type MessageListener = (msg: any) => void;
type BoolListener = (v: boolean) => void;
type StrListener = (v: string | null) => void;
type NumListener = (v: number | null) => void;

class ChatWsClient {
  private ws: WebSocket | null = null;
  private params: ChatWsParams | null = null;
  private listeners = new Set<MessageListener>();
  private connectedListeners = new Set<BoolListener>();
  private clientIdListeners = new Set<StrListener>();
  private latencyListeners = new Set<NumListener>();

  private reconnectTimer: number | null = null;
  private pingTimer: number | null = null;
  private pingRequestId: string | null = null;
  private pingSentAt: number | null = null;
  private superseded = false;

  // Public observed state.
  private connectedState = false;
  private attemptsState = 0;
  private attemptsListeners = new Set<NumListener>();
  private clientIdState: string | null = null;
  private latencyState: number | null = null;

  // active_agent_id to send via register_active_channel on connect / on change.
  private activeAgentId: string | null = null;
  private extraRegisterData: Record<string, any> = {};

  configure(next: ChatWsParams): void {
    const cur = this.params;
    const urlChanged = !cur ||
      cur.apiBase !== next.apiBase ||
      cur.paneId !== next.paneId ||
      cur.token !== next.token ||
      cur.clientId !== next.clientId ||
      cur.platform !== next.platform ||
      cur.isElectron !== next.isElectron;
    this.params = next;
    if (!urlChanged) return;
    if (!next.token || !next.paneId) {
      this.shutdown();
      return;
    }
    this.superseded = false;
    this.reopen();
  }

  // Force an immediate fresh reconnect using the last configured params
  // (used by the connection gate's “reconnect” button).
  forceReconnect(): void {
    if (!this.params || !this.params.token || !this.params.paneId) return;
    this.superseded = false;
    this.setAttempts(0);
    this.reopen();
  }

  shutdown(): void {
    this.cancelReconnect();
    this.cancelPing();
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      try { ws.close(); } catch {}
    }
    this.setConnected(false);
    this.setClientId(null);
    this.setLatency(null);
  }

  setActiveAgent(agentId: string | null, extra?: Record<string, any>): void {
    this.activeAgentId = agentId && String(agentId).trim() ? String(agentId).trim() : null;
    if (extra && typeof extra === 'object') this.extraRegisterData = extra;
    this.sendRegisterActiveChannel();
  }

  send(payload: any): boolean {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    try {
      ws.send(JSON.stringify(payload));
      return true;
    } catch {
      return false;
    }
  }

  isConnected(): boolean { return this.connectedState; }
  currentAttempts(): number { return this.attemptsState; }
  currentClientId(): string | null { return this.clientIdState; }

  subscribe(fn: MessageListener): () => void {
    this.listeners.add(fn);
    return () => { this.listeners.delete(fn); };
  }

  onConnectedChange(fn: BoolListener): () => void {
    this.connectedListeners.add(fn);
    try { fn(this.connectedState); } catch {}
    return () => { this.connectedListeners.delete(fn); };
  }

  onAttemptsChange(fn: NumListener): () => void {
    this.attemptsListeners.add(fn);
    try { fn(this.attemptsState); } catch {}
    return () => { this.attemptsListeners.delete(fn); };
  }

  private setAttempts(n: number): void {
    if (this.attemptsState === n) return;
    this.attemptsState = n;
    for (const fn of this.attemptsListeners) { try { fn(n); } catch {} }
  }

  onClientIdChange(fn: StrListener): () => void {
    this.clientIdListeners.add(fn);
    try { fn(this.clientIdState); } catch {}
    return () => { this.clientIdListeners.delete(fn); };
  }

  onLatencyChange(fn: NumListener): () => void {
    this.latencyListeners.add(fn);
    try { fn(this.latencyState); } catch {}
    return () => { this.latencyListeners.delete(fn); };
  }

  // ---- internals ----

  private cancelReconnect(): void {
    if (this.reconnectTimer != null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private cancelPing(): void {
    if (this.pingTimer != null) {
      window.clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    this.pingRequestId = null;
    this.pingSentAt = null;
  }

  private setConnected(v: boolean): void {
    if (this.connectedState === v) return;
    this.connectedState = v;
    for (const fn of this.connectedListeners) {
      try { fn(v); } catch {}
    }
  }

  private setClientId(id: string | null): void {
    if (this.clientIdState === id) return;
    this.clientIdState = id;
    for (const fn of this.clientIdListeners) {
      try { fn(id); } catch {}
    }
  }

  private setLatency(ms: number | null): void {
    if (this.latencyState === ms) return;
    this.latencyState = ms;
    for (const fn of this.latencyListeners) {
      try { fn(ms); } catch {}
    }
  }

  private reopen(): void {
    // Tear down any existing socket, then connect fresh.
    this.cancelReconnect();
    this.cancelPing();
    const old = this.ws;
    this.ws = null;
    if (old) {
      try { old.close(); } catch {}
    }
    this.setConnected(false);
    this.setLatency(null);
    this.connect();
  }

  private buildUrl(): string {
    const p = this.params!;
    const proto = p.apiBase.startsWith('https')
      ? 'wss'
      : (typeof window !== 'undefined' && window.location?.protocol === 'https:' ? 'wss' : 'ws');
    const base = p.apiBase.replace(/^https?/, proto);
    const master = p.paneId.replace(/:.*$/, '');
    return `${base}/api/chat/ws`
      + `?master_agent_id=${encodeURIComponent(master)}`
      + `&token=${encodeURIComponent(p.token)}`
      + `&electron=${p.isElectron ? '1' : '0'}`
      + `&client_id=${encodeURIComponent(p.clientId)}`
      + `&platform=${encodeURIComponent(p.platform)}`;
  }

  private connect(): void {
    if (this.superseded) return;
    if (!this.params || !this.params.token || !this.params.paneId) return;
    this.setAttempts(this.attemptsState + 1);
    let ws: WebSocket;
    try {
      ws = new WebSocket(this.buildUrl());
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws) return;
      this.setAttempts(0);
      this.setClientId(this.params?.clientId ?? null);
      this.setConnected(true);
      // Initial poll snapshot.
      try { ws.send(JSON.stringify({ type: 'poll_request' })); } catch {}
      // Register the current active agent (if known) — fire-and-forget.
      this.sendRegisterActiveChannel();
      // Start latency pings.
      this.cancelPing();
      const sendLatencyPing = () => {
        if (this.ws !== ws || ws.readyState !== WebSocket.OPEN) return;
        const reqId = `ping-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
        this.pingRequestId = reqId;
        this.pingSentAt = performance.now();
        try { ws.send(JSON.stringify({ type: 'ping', data: { requestId: reqId } })); } catch {}
      };
      sendLatencyPing();
      this.pingTimer = window.setInterval(sendLatencyPing, 5000);
    };

    ws.onmessage = (event) => {
      if (this.ws !== ws) return;
      let msg: any;
      try {
        msg = JSON.parse(String(event.data || ''));
      } catch {
        return;
      }
      // Latency measurement is owned here so subscribers don't have to care.
      if (msg?.type === 'pong' && msg.data?.requestId && msg.data.requestId === this.pingRequestId && this.pingSentAt != null) {
        this.setLatency(Math.max(0, Math.round(performance.now() - this.pingSentAt)));
        this.pingSentAt = null;
      }
      for (const fn of this.listeners) {
        try { fn(msg); } catch {}
      }
    };

    ws.onclose = (event) => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.cancelPing();
      this.setConnected(false);
      this.setLatency(null);
      // 4409 = the slot is currently held by another connection — often a
      // stale half-open one left by a proxy (Cloudflare). Don't give up
      // permanently: back off and retry. The server supersedes a stale
      // holder once the same client keeps retrying.
      if (event && event.code === 4409) {
        if (this.reconnectTimer == null) {
          this.reconnectTimer = window.setTimeout(() => {
            this.reconnectTimer = null;
            this.connect();
          }, 4000);
        }
        return;
      }
      this.scheduleReconnect();
    };

    ws.onerror = () => {
      try { ws.close(); } catch {}
    };
  }

  private scheduleReconnect(): void {
    if (this.superseded) return;
    if (this.reconnectTimer != null) return;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, 3000);
  }

  private sendRegisterActiveChannel(): void {
    if (!this.connectedState) return;
    const clientId = this.clientIdState;
    if (!clientId || !this.activeAgentId) return;
    this.send({
      type: 'register_active_channel',
      data: {
        agent_id: this.activeAgentId,
        client_id: clientId,
        channel_type: 'web',
        ...this.extraRegisterData,
      },
    });
  }
}

export const chatWs = new ChatWsClient();
