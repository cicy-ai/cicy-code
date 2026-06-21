import axios, { AxiosError } from 'axios';
import config from '../../config';
import { TokenManager } from '../../services/tokenManager';

// Lightweight HTTP client for /api/fs/*. Mirrors the auth / baseURL
// conventions of services/api.ts but keeps the file module self-contained.
const http = axios.create({ baseURL: config.apiBase });
http.interceptors.request.use((cfg) => {
  const token = TokenManager.getToken();
  if (token) cfg.headers.Authorization = `Bearer ${token}`;
  return cfg;
});

export interface FsEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  mode: string;
  is_symlink?: boolean;
}

export interface FsListResponse {
  path: string;
  entries: FsEntry[];
  truncated?: boolean;
}

export interface FsReadResult {
  /** UTF-8 text. Empty when the file is binary. */
  text: string;
  /** Base64 body for binary files (empty for text). */
  base64: string;
  mtime: number;
  size: number;
  mime: string;
  encoding: 'utf-8' | 'base64';
}

export interface FsStatResponse {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  mode: string;
  mime?: string;
}

export interface FsWriteResult {
  mtime: number;
  size: number;
}

export interface FsSearchMatch {
  path: string;
  score: number;
}

export interface FsSearchResponse {
  matches: FsSearchMatch[];
  elapsed_ms: number;
  backend: string;
  truncated?: boolean;
}

export interface FsGrepMatch {
  path: string;
  line: number;
  col: number;
  text: string;
}

export interface FsGrepResponse {
  matches: FsGrepMatch[];
  elapsed_ms: number;
  truncated?: boolean;
}

export interface FsDiffResponse {
  a: string;
  b: string;
  mode: 'head' | 'index' | 'mtime';
}

export interface FsRoot {
  /** Stable identifier sent back to the API as ?root=… (e.g. "workspace"). */
  id: string;
  /** Human label shown as the section header. */
  label: string;
  /** Absolute base path on the host — for tooltip / debugging only. */
  path: string;
}

export interface FsFavorite {
  path: string;
  name: string;
  added: number;
}

export interface FsFavoritesResponse {
  items: FsFavorite[];
}

export type FsWatchEvent =
  | { type: 'created'; path: string }
  | { type: 'modified'; path: string; mtime?: number }
  | { type: 'deleted'; path: string }
  | { type: 'renamed'; path: string; old?: string }
  | { type: 'pong' }
  | { type: 'error'; error: string };

export class FsError extends Error {
  status: number;
  detail: string;
  /** Server-supplied actual mtime when status is 409 (mtime mismatch). */
  actualMtime?: number;

  constructor(status: number, detail: string) {
    super(detail || 'fs_error');
    this.status = status;
    this.detail = detail;
    if (status === 409) {
      const m = detail.match(/^mtime_mismatch:(\d+)$/);
      if (m) this.actualMtime = Number(m[1]);
    }
  }
}

/** Map a backend detail code to user-facing Chinese text. Falls through to
 *  the raw message for unknown codes so unexpected errors still surface. */
export function friendlyFsError(err: unknown): string {
  const msg = err instanceof Error ? err.message : String(err || '');
  const detail = err instanceof FsError ? err.detail : msg;
  switch (detail) {
    case 'not_found':                  return '文件不存在';
    case 'permission_denied':          return '没有权限';
    case 'path_outside_workspace':     return '路径不在工作区内';
    case 'path_absolute_forbidden':    return '不允许使用绝对路径';
    case 'path_symlink_escape':        return '链接指向工作区外,已拒绝';
    case 'path_write_forbidden':       return '该路径不可写入(受保护)';
    case 'file_too_large':             return '文件超过大小上限';
    case 'file_not_regular':           return '只能操作普通文件';
    case 'not_a_directory':            return '不是目录';
    case 'directory_not_empty':        return '目录不为空,无法删除';
    case 'destination_exists':         return '目标已存在';
    case 'cannot_delete_root':         return '不能删除工作区根目录';
    case 'cannot_rename_root':         return '不能重命名工作区根目录';
    case 'page_client_not_connected':  return '当前页面未连接,无法发送';
    case 'agent_workspace_unavailable':return 'agent 工作区不可用';
    case 'missing_agent_id':           return '缺少 agent_id';
    case 'ripgrep_not_installed':      return '主机未安装 ripgrep,无法做全文搜索';
    case 'exists':                     return '已存在';
    case 'internal_error':             return '服务器错误';
    case 'invalid_body':               return '请求格式错误';
    case 'method_not_allowed':         return '请求方法不允许';
  }
  if (detail?.startsWith('mtime_mismatch:')) return '磁盘上的版本已被外部修改';
  return msg || 'fs_error';
}

function toFsError(err: unknown): FsError {
  if (err instanceof AxiosError && err.response) {
    // Some callers (fsApi.read) use responseType:'text' so the error body
    // arrives as a JSON string, not an object. Try parsing before falling
    // back to axios's generic "status code N" message.
    let body: any = err.response.data;
    if (typeof body === 'string') {
      try {
        body = JSON.parse(body);
      } catch {
        const raw = body.trim();
        if (raw) return new FsError(err.response.status, raw);
      }
    }
    const detail =
      (body && typeof body === 'object' && (body.detail || body.error)) ||
      err.message ||
      'fs_error';
    return new FsError(err.response.status, String(detail));
  }
  if (err instanceof Error) return new FsError(0, err.message);
  return new FsError(0, String(err));
}

export const fsApi = {
  roots: async (agentId: string): Promise<FsRoot[]> => {
    try {
      const resp = await http.get('/api/fs/roots', {
        params: { agent_id: agentId },
      });
      return (resp.data?.roots || []) as FsRoot[];
    } catch (e) {
      throw toFsError(e);
    }
  },

  list: async (
    agentId: string,
    path: string = '',
    opts: { hidden?: boolean; signal?: AbortSignal; root?: string } = {},
  ): Promise<FsListResponse> => {
    try {
      const resp = await http.get('/api/fs/list', {
        params: {
          agent_id: agentId,
          path,
          hidden: opts.hidden ? '1' : undefined,
          root: opts.root,
        },
        signal: opts.signal,
      });
      return resp.data as FsListResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  read: async (
    agentId: string,
    path: string,
    opts: { signal?: AbortSignal; root?: string } = {},
  ): Promise<FsReadResult> => {
    try {
      const resp = await http.get('/api/fs/read', {
        params: { agent_id: agentId, path, root: opts.root },
        signal: opts.signal,
        responseType: 'text',
        transformResponse: [(data) => data],
      });
      const enc = String(resp.headers['x-file-encoding'] || 'utf-8') as
        | 'utf-8'
        | 'base64';
      const body = String(resp.data ?? '');
      return {
        text: enc === 'utf-8' ? body : '',
        base64: enc === 'base64' ? body : '',
        mtime: Number(resp.headers['x-file-mtime'] || 0),
        size: Number(resp.headers['x-file-size'] || 0),
        mime: String(resp.headers['x-file-mime'] || ''),
        encoding: enc,
      };
    } catch (e) {
      throw toFsError(e);
    }
  },

  write: async (
    agentId: string,
    path: string,
    content: string,
    expectedMtime?: number,
    opts: { root?: string } = {},
  ): Promise<FsWriteResult> => {
    try {
      const resp = await http.post(
        '/api/fs/write',
        { path, content, expected_mtime: expectedMtime ?? 0 },
        { params: { agent_id: agentId, root: opts.root } },
      );
      return resp.data as FsWriteResult;
    } catch (e) {
      throw toFsError(e);
    }
  },

  stat: async (
    agentId: string,
    path: string,
    opts: { root?: string } = {},
  ): Promise<FsStatResponse> => {
    try {
      const resp = await http.get('/api/fs/stat', {
        params: { agent_id: agentId, path, root: opts.root },
      });
      return resp.data as FsStatResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  search: async (
    agentId: string,
    q: string,
    opts: { dir?: string; limit?: number; signal?: AbortSignal } = {},
  ): Promise<FsSearchResponse> => {
    try {
      const resp = await http.get('/api/fs/search', {
        params: { agent_id: agentId, q, dir: opts.dir, limit: opts.limit },
        signal: opts.signal,
      });
      return resp.data as FsSearchResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  grep: async (
    agentId: string,
    q: string,
    opts: {
      dir?: string;
      limit?: number;
      caseSensitive?: boolean;
      regex?: boolean;
      signal?: AbortSignal;
    } = {},
  ): Promise<FsGrepResponse> => {
    try {
      const resp = await http.get('/api/fs/grep', {
        params: {
          agent_id: agentId,
          q,
          dir: opts.dir,
          limit: opts.limit,
          case: opts.caseSensitive ? '1' : undefined,
          regex: opts.regex ? '1' : undefined,
        },
        signal: opts.signal,
      });
      return resp.data as FsGrepResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  diff: async (
    agentId: string,
    path: string,
    base: 'head' | 'index' | 'mtime' = 'head',
  ): Promise<FsDiffResponse> => {
    try {
      const resp = await http.get('/api/fs/diff', {
        params: { agent_id: agentId, path, base },
      });
      return resp.data as FsDiffResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  /** Build a direct-download URL for /api/fs/download. The browser hits this
   *  URL via an anchor click; the backend streams raw bytes and sets
   *  Content-Disposition so the file is saved instead of previewed. */
  downloadUrl: (agentId: string, path: string, root?: string): string => {
    const token = TokenManager.getToken() || '';
    const base = (config.apiBase || '').replace(/\/$/, '');
    const params: Record<string, string> = { agent_id: agentId, path, token };
    if (root) params.root = root;
    const qs = new URLSearchParams(params);
    return `${base}/api/fs/download?${qs.toString()}`;
  },

  /** Build an INLINE preview URL (?inline=1) — same stream as download but the
   *  backend serves the real media Content-Type with inline disposition, so it
   *  can back an <img>/<audio>/<video> src (Range/seek supported). */
  inlineUrl: (agentId: string, path: string, root?: string): string =>
    fsApi.downloadUrl(agentId, path, root) + '&inline=1',

  /** Trigger a browser save dialog for the given path (optionally rooted). */
  download: (agentId: string, path: string, root?: string): void => {
    const a = document.createElement('a');
    a.href = fsApi.downloadUrl(agentId, path, root);
    a.rel = 'noopener';
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    setTimeout(() => a.remove(), 0);
  },

  /** Upload a single File to a path under the given root (default workspace).
   *  If targetPath is empty or ends with '/' the backend keeps the filename. */
  upload: async (
    agentId: string,
    targetPath: string,
    file: File,
    opts: { overwrite?: boolean; root?: string; onProgress?: (loaded: number, total: number) => void } = {},
  ): Promise<{ path: string; size: number; mtime: number }> => {
    const form = new FormData();
    form.append('file', file);
    try {
      const resp = await http.post('/api/fs/upload', form, {
        params: {
          agent_id: agentId,
          path: targetPath,
          overwrite: opts.overwrite ? '1' : undefined,
          root: opts.root,
        },
        onUploadProgress: (evt) => {
          if (opts.onProgress && evt.total) opts.onProgress(evt.loaded, evt.total);
        },
      });
      return resp.data as { path: string; size: number; mtime: number };
    } catch (e) {
      throw toFsError(e);
    }
  },

  rename: async (agentId: string, from: string, to: string, opts: { root?: string } = {}): Promise<void> => {
    try {
      await http.post(
        '/api/fs/rename',
        { from, to },
        { params: { agent_id: agentId, root: opts.root } },
      );
    } catch (e) {
      throw toFsError(e);
    }
  },

  delete: async (agentId: string, path: string, recursive = false, opts: { root?: string } = {}): Promise<void> => {
    try {
      await http.post(
        '/api/fs/delete',
        { path, recursive },
        { params: { agent_id: agentId, root: opts.root } },
      );
    } catch (e) {
      throw toFsError(e);
    }
  },

  mkdir: async (agentId: string, path: string, opts: { root?: string } = {}): Promise<void> => {
    try {
      await http.post(
        '/api/fs/mkdir',
        { path },
        { params: { agent_id: agentId, root: opts.root } },
      );
    } catch (e) {
      throw toFsError(e);
    }
  },

  touch: async (agentId: string, path: string, opts: { root?: string } = {}): Promise<void> => {
    try {
      await http.post(
        '/api/fs/touch',
        { path },
        { params: { agent_id: agentId, root: opts.root } },
      );
    } catch (e) {
      throw toFsError(e);
    }
  },

  favoritesList: async (agentId: string): Promise<FsFavoritesResponse> => {
    try {
      const resp = await http.get('/api/fs/favorites/list', {
        params: { agent_id: agentId },
      });
      return resp.data as FsFavoritesResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  favoritesAdd: async (
    agentId: string,
    path: string,
    name?: string,
  ): Promise<FsFavoritesResponse> => {
    try {
      const resp = await http.post(
        '/api/fs/favorites/add',
        { path, name },
        { params: { agent_id: agentId } },
      );
      return (resp.data?.favorites || { items: [] }) as FsFavoritesResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  favoritesRemove: async (
    agentId: string,
    path: string,
  ): Promise<FsFavoritesResponse> => {
    try {
      const resp = await http.post(
        '/api/fs/favorites/remove',
        { path },
        { params: { agent_id: agentId } },
      );
      return (resp.data?.favorites || { items: [] }) as FsFavoritesResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  /**
   * Broadcast a "code.send_path" chat event to every client connected to the
   * agent, telling the agent that the user wants to focus on this file.
   * Uses /api/fs/send-path which is the native-files equivalent of the old
   * code-server bridge.
   *
   * opts.pageClientId is required for the page to filter the event back to
   * itself (the Workspace handler ignores events with a mismatching id).
   * opts.range + selectionText optionally encode the editor selection into
   * the path as ":l:c-l:c" so the agent's prompt carries the highlighted span.
   */
  sendPathToAgent: async (
    agentId: string,
    path: string,
    opts: {
      fileName?: string;
      pageClientId?: string;
      selectionText?: string;
      range?: {
        startLine: number;
        startCharacter: number;
        endLine: number;
        endCharacter: number;
      };
    } = {},
  ): Promise<void> => {
    try {
      await http.post(
        '/api/fs/send-path',
        {
          path,
          file_name: opts.fileName,
          page_client_id: opts.pageClientId,
          selection_text: opts.selectionText,
          range: opts.range,
        },
        { params: { agent_id: agentId } },
      );
    } catch (e) {
      throw toFsError(e);
    }
  },
};

export function joinFsPath(parent: string, name: string): string {
  if (!parent) return name;
  return parent.endsWith('/') ? parent + name : parent + '/' + name;
}

export function fsParent(path: string): string {
  const idx = path.lastIndexOf('/');
  return idx <= 0 ? '' : path.slice(0, idx);
}

export function fsBasename(path: string): string {
  const idx = path.lastIndexOf('/');
  return idx < 0 ? path : path.slice(idx + 1);
}

/**
 * Open a WS connection to /api/fs/watch for fsnotify-driven UI refresh.
 * Caller drives subscriptions via .subscribe / .unsubscribe and listens via
 * .onEvent. Auto-reconnect with a small backoff; close() ends the connection.
 */
export interface FsWatch {
  subscribe(path: string): void;
  unsubscribe(path: string): void;
  onEvent(handler: (ev: FsWatchEvent) => void): () => void;
  close(): void;
}

export function openFsWatch(agentId: string): FsWatch {
  const baseURL = config.apiBase || '';
  // WebSocket() requires an ABSOLUTE ws(s):// URL. When apiBase is empty (the
  // SPA is served same-origin, e.g. the :8009 container), deriving the ws base
  // from apiBase yields '' → a relative '/api/fs/watch' → DOMException "URL is
  // invalid". Fall back to window.location for the same-origin case.
  const httpsLike = baseURL.startsWith('https') ||
    (typeof window !== 'undefined' && window.location?.protocol === 'https:');
  const proto = httpsLike ? 'wss' : 'ws';
  const wsBase = baseURL
    ? baseURL.replace(/^https?/, proto).replace(/\/$/, '')
    : (typeof window !== 'undefined' ? `${proto}://${window.location.host}` : '');
  const token = TokenManager.getToken();
  const url = `${wsBase}/api/fs/watch?agent_id=${encodeURIComponent(agentId)}${token ? `&token=${encodeURIComponent(token)}` : ''}`;
  const handlers = new Set<(ev: FsWatchEvent) => void>();
  const wanted = new Set<string>();
  let ws: WebSocket | null = null;
  let closed = false;
  let backoff = 1000;

  const send = (msg: any) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      try {
        ws.send(JSON.stringify(msg));
      } catch {}
    }
  };

  const connect = () => {
    if (closed) return;
    ws = new WebSocket(url);
    ws.onopen = () => {
      backoff = 1000;
      wanted.forEach((p) => send({ type: 'subscribe', path: p }));
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(String(ev.data));
        if (msg && typeof msg.type === 'string') {
          handlers.forEach((h) => h(msg));
        }
      } catch {}
    };
    ws.onclose = () => {
      if (closed) return;
      window.setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 15000);
    };
    ws.onerror = () => {
      try { ws?.close(); } catch {}
    };
  };
  connect();

  return {
    subscribe(path: string) {
      wanted.add(path);
      send({ type: 'subscribe', path });
    },
    unsubscribe(path: string) {
      wanted.delete(path);
      send({ type: 'unsubscribe', path });
    },
    onEvent(handler) {
      handlers.add(handler);
      return () => handlers.delete(handler);
    },
    close() {
      closed = true;
      try { ws?.close(); } catch {}
    },
  };
}
