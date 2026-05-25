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

function toFsError(err: unknown): FsError {
  if (err instanceof AxiosError && err.response) {
    const detail =
      (err.response.data && (err.response.data.detail || err.response.data.error)) ||
      err.message ||
      'fs_error';
    return new FsError(err.response.status, String(detail));
  }
  if (err instanceof Error) return new FsError(0, err.message);
  return new FsError(0, String(err));
}

export const fsApi = {
  list: async (
    agentId: string,
    path: string = '',
    opts: { hidden?: boolean; signal?: AbortSignal } = {},
  ): Promise<FsListResponse> => {
    try {
      const resp = await http.get('/api/fs/list', {
        params: { agent_id: agentId, path, hidden: opts.hidden ? '1' : undefined },
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
    opts: { signal?: AbortSignal } = {},
  ): Promise<FsReadResult> => {
    try {
      const resp = await http.get('/api/fs/read', {
        params: { agent_id: agentId, path },
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
  ): Promise<FsWriteResult> => {
    try {
      const resp = await http.post(
        '/api/fs/write',
        { path, content, expected_mtime: expectedMtime ?? 0 },
        { params: { agent_id: agentId } },
      );
      return resp.data as FsWriteResult;
    } catch (e) {
      throw toFsError(e);
    }
  },

  stat: async (agentId: string, path: string): Promise<FsStatResponse> => {
    try {
      const resp = await http.get('/api/fs/stat', {
        params: { agent_id: agentId, path },
      });
      return resp.data as FsStatResponse;
    } catch (e) {
      throw toFsError(e);
    }
  },

  /**
   * Broadcast a "code.send_path" chat event to every client connected to the
   * agent, telling the agent that the user wants to focus on this file.
   * Uses /api/fs/send-path which is the native-files equivalent of the old
   * code-server bridge.
   */
  sendPathToAgent: async (agentId: string, path: string, fileName?: string): Promise<void> => {
    try {
      await http.post(
        '/api/fs/send-path',
        { path, file_name: fileName },
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
