export interface OpenClawGatewayInfo {
  ws_url: string;
  token: string;
  session_key: string;
}

export interface OpenClawGatewayEvent {
  type: 'event';
  event: string;
  payload?: any;
  seq?: number;
  stateVersion?: number;
}

interface GatewayResponseFrame {
  type: 'res';
  id: string;
  ok: boolean;
  payload?: any;
  error?: {
    code?: string;
    message?: string;
    details?: any;
  };
}

interface GatewayRequestFrame {
  type: 'req';
  id: string;
  method: string;
  params?: any;
}

interface PendingRequest {
  resolve: (payload: any) => void;
  reject: (error: Error) => void;
}

interface PendingChallenge {
  resolve: () => void;
  reject: (error: Error) => void;
  timeoutId: ReturnType<typeof setTimeout>;
}

const CONNECT_SCOPES = [
  'operator.admin',
  'operator.read',
  'operator.write',
  'operator.approvals',
  'operator.pairing',
];
const CONTROL_UI_CLIENT_ID = 'openclaw-control-ui';

function makeId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function toError(error: unknown): Error {
  if (error instanceof Error) {
    return error;
  }
  if (typeof error === 'string') {
    return new Error(error);
  }
  return new Error('openclaw gateway request failed');
}

export class OpenClawGatewayClient {
  private ws: WebSocket | null = null;
  private pending = new Map<string, PendingRequest>();
  private pendingChallenge: PendingChallenge | null = null;
  private receivedConnectChallenge = false;
  private instanceId = makeId();
  private connected = false;

  constructor(
    private readonly opts: {
      url: string;
      token: string;
      clientName?: string;
      clientVersion?: string;
      onEvent?: (event: OpenClawGatewayEvent) => void;
      onClose?: (event: CloseEvent) => void;
    },
  ) {}

  async connect(): Promise<any> {
    if (this.connected && this.ws?.readyState === WebSocket.OPEN) {
      return;
    }

    if (this.ws && this.ws.readyState === WebSocket.CONNECTING) {
      await new Promise<void>((resolve, reject) => {
        const onOpen = () => {
          cleanup();
          resolve();
        };
        const onError = () => {
          cleanup();
          reject(new Error('openclaw gateway connect failed'));
        };
        const cleanup = () => {
          this.ws?.removeEventListener('open', onOpen);
          this.ws?.removeEventListener('error', onError);
        };
        this.ws?.addEventListener('open', onOpen);
        this.ws?.addEventListener('error', onError);
      });
      return;
    }

    return new Promise<any>((resolve, reject) => {
      const ws = new WebSocket(this.opts.url);
      this.ws = ws;
      this.receivedConnectChallenge = false;

      const cleanup = () => {
        ws.removeEventListener('open', handleOpen);
        ws.removeEventListener('error', handleError);
      };

      const handleOpen = async () => {
        cleanup();
        ws.addEventListener('message', this.handleMessage);
        ws.addEventListener('close', this.handleClose);
        try {
          await this.waitForConnectChallenge();
          const hello = await this.request('connect', {
            minProtocol: 3,
            maxProtocol: 3,
            client: {
              id: CONTROL_UI_CLIENT_ID,
              displayName: this.opts.clientName ?? CONTROL_UI_CLIENT_ID,
              version: this.opts.clientVersion ?? '1.0.0',
              platform: navigator.platform || 'web',
              mode: 'webchat',
              instanceId: this.instanceId,
            },
            role: 'operator',
            scopes: CONNECT_SCOPES,
            caps: ['tool-events'],
            auth: { token: this.opts.token },
            locale: navigator.language,
            userAgent: navigator.userAgent,
          });
          this.connected = true;
          resolve(hello);
        } catch (error) {
          reject(toError(error));
        }
      };

      const handleError = () => {
        cleanup();
        reject(new Error('openclaw gateway connect failed'));
      };

      ws.addEventListener('open', handleOpen);
      ws.addEventListener('error', handleError);
    });
  }

  request(method: string, params?: any): Promise<any> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('openclaw gateway not connected'));
    }

    const frame: GatewayRequestFrame = {
      type: 'req',
      id: makeId(),
      method,
      params,
    };

    const promise = new Promise<any>((resolve, reject) => {
      this.pending.set(frame.id, { resolve, reject });
    });

    this.ws.send(JSON.stringify(frame));
    return promise;
  }

  close() {
    this.connected = false;
    this.receivedConnectChallenge = false;
    if (this.pendingChallenge) {
      clearTimeout(this.pendingChallenge.timeoutId);
      this.pendingChallenge.reject(new Error('openclaw gateway client closed'));
      this.pendingChallenge = null;
    }
    if (this.ws) {
      this.ws.removeEventListener('message', this.handleMessage);
      this.ws.removeEventListener('close', this.handleClose);
      this.ws.close();
      this.ws = null;
    }
    this.flushPending(new Error('openclaw gateway client closed'));
  }

  private flushPending(error: Error) {
    for (const pending of this.pending.values()) {
      pending.reject(error);
    }
    this.pending.clear();
  }

  private handleClose = (event: CloseEvent) => {
    this.connected = false;
    this.receivedConnectChallenge = false;
    if (this.pendingChallenge) {
      clearTimeout(this.pendingChallenge.timeoutId);
      this.pendingChallenge.reject(new Error(`openclaw gateway closed (${event.code})`));
      this.pendingChallenge = null;
    }
    this.flushPending(new Error(`openclaw gateway closed (${event.code})`));
    this.opts.onClose?.(event);
  };

  private waitForConnectChallenge(timeoutMs = 10_000): Promise<void> {
    if (this.receivedConnectChallenge) {
      return Promise.resolve();
    }
    if (this.pendingChallenge) {
      return Promise.reject(new Error('openclaw gateway challenge already pending'));
    }

    return new Promise<void>((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        if (this.pendingChallenge?.timeoutId !== timeoutId) {
          return;
        }
        this.pendingChallenge = null;
        reject(new Error('openclaw gateway connect challenge timeout'));
      }, timeoutMs);

      this.pendingChallenge = {
        resolve: () => {
          clearTimeout(timeoutId);
          this.pendingChallenge = null;
          resolve();
        },
        reject: (error: Error) => {
          clearTimeout(timeoutId);
          this.pendingChallenge = null;
          reject(error);
        },
        timeoutId,
      };
    });
  }

  private handleMessage = (event: MessageEvent) => {
    let frame: OpenClawGatewayEvent | GatewayResponseFrame;
    try {
      frame = JSON.parse(String(event.data ?? ''));
    } catch {
      return;
    }

    if (frame.type === 'event') {
      if (frame.event === 'connect.challenge') {
        this.receivedConnectChallenge = true;
        this.pendingChallenge?.resolve();
        return;
      }
      this.opts.onEvent?.(frame);
      return;
    }

    if (frame.type !== 'res') {
      return;
    }

    const pending = this.pending.get(frame.id);
    if (!pending) {
      return;
    }
    this.pending.delete(frame.id);

    if (frame.ok) {
      pending.resolve(frame.payload);
      return;
    }

    pending.reject(
      new Error(frame.error?.message || frame.error?.code || 'openclaw gateway request failed'),
    );
  };
}

export function createOpenClawId(): string {
  return makeId();
}
