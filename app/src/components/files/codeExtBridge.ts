// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Page-side :code-ext bridge that replaces the code-server VSIX extension.
// Re-uses the existing chat-ws singleton (services/chatWs.ts) instead of
// opening a second socket; the server aliases "<pageClientId>:code-ext" to
// this page client when we send register_code_ext_bridge.
//
// Supported wire types (mirror api/code-server-extension/src/extension.ts):
//
//   host.ping        →  code.pong          { version }
//   host.open_file   →  code.opened        { path }
//                    →  code.open_file_error { error, path }
//   host.active_file →  code.active_file   { path, language, line, column }
//   host.list_tabs   →  code.tabs          { tabs: [{path,label,isActive,isDirty,group}] }
//
// Accepts paths with optional ":line[:col]" or ":l:c-l:c" suffix or "file://" prefix.

import { chatWs } from '../../services/chatWs';

export interface CodeExtTab {
  path: string;
  label: string;
  isActive: boolean;
  isDirty: boolean;
  group?: number;
}

export interface CodeExtActiveFile {
  path: string;
  language: string;
  line: number;
  column: number;
}

export interface CodeExtOps {
  /** Open path in a tab; resolves on success, rejects with Error on failure. */
  openFile(path: string, line?: number, col?: number): Promise<void>;
  /** Snapshot of the focused editor tab. Empty path = no editor focused. */
  getActiveFile(): CodeExtActiveFile;
  /** All open tabs. */
  listTabs(): CodeExtTab[];
}

interface FileRef {
  filePath: string;
  line?: number;
  column?: number;
}

function parseFileRef(text: string): FileRef {
  let t = String(text || '').trim();
  if (!t) return { filePath: '' };
  if (t.startsWith('file://')) t = t.slice(7).trim();
  let m = t.match(/^(.*?):(\d+):(\d+)-(\d+):(\d+)$/);
  if (m) {
    const line = Number(m[2]);
    const column = Number(m[3]);
    return {
      filePath: m[1],
      line: line > 0 ? line : undefined,
      column: column > 0 ? column : undefined,
    };
  }
  m = t.match(/^(.*?):(\d+)(?::(\d+))?$/);
  if (m) {
    const line = Number(m[2]);
    const column = m[3] ? Number(m[3]) : 0;
    return {
      filePath: m[1],
      line: line > 0 ? line : undefined,
      column: column > 0 ? column : undefined,
    };
  }
  return { filePath: t };
}

/**
 * Install the :code-ext bridge. Subscribes the shared chat-ws to host.*
 * traffic addressed to this page's alias, sends register_code_ext_bridge on
 * every (re)connect, and unregisters on teardown. Returns a stop function.
 *
 * opsRef is a ref so the handlers always read the freshest tab state without
 * forcing a reinstall on every render.
 */
export function installCodeExtBridge(
  pageClientId: string,
  opsRef: { current: CodeExtOps | null },
): () => void {
  if (!pageClientId) return () => {};
  const alias = `${pageClientId}:code-ext`;

  const sendRegister = () => {
    try {
      chatWs.send({ type: 'register_code_ext_bridge', data: { alias } });
    } catch {}
  };

  const reply = (type: string, requestId: string, data: Record<string, unknown>) => {
    try {
      chatWs.send({
        type,
        data: {
          ...data,
          requestId,
          page_client_id: pageClientId,
          code_client_id: alias,
        },
      });
    } catch {}
  };

  const handle = async (msg: any) => {
    if (!msg || typeof msg.type !== 'string') return;
    // Bridge messages are routed to this client via the alias; filter to
    // host.* so we don't react to broadcasts intended for the page itself.
    if (!msg.type.startsWith('host.')) return;
    const reqId = String(msg.data?.requestId || '');
    const ops = opsRef.current;
    if (!ops) return;

    switch (msg.type) {
      case 'host.ping':
        reply('code.pong', reqId, { version: 'native-files-1.0' });
        return;
      case 'host.open_file': {
        const rawPath = String(msg.data?.path || '');
        if (!rawPath) {
          reply('code.open_file_error', reqId, { path: '', error: 'empty path' });
          return;
        }
        const parsed = parseFileRef(rawPath);
        if (!parsed.filePath) {
          reply('code.open_file_error', reqId, { path: rawPath, error: 'empty path' });
          return;
        }
        try {
          await ops.openFile(parsed.filePath, parsed.line, parsed.column);
          reply('code.opened', reqId, { path: rawPath });
        } catch (e: any) {
          reply('code.open_file_error', reqId, {
            path: rawPath,
            error: String(e?.message || e || 'open failed'),
          });
        }
        return;
      }
      case 'host.active_file': {
        const a = ops.getActiveFile();
        reply('code.active_file', reqId, {
          path: a.path,
          language: a.language,
          line: a.line,
          column: a.column,
        });
        return;
      }
      case 'host.list_tabs': {
        reply('code.tabs', reqId, { tabs: ops.listTabs() });
        return;
      }
    }
  };

  const offMsg = chatWs.subscribe((msg) => {
    void handle(msg);
  });
  // (re)register on every chat-ws (re)connect. The singleton may already be
  // connected when we install — sendRegister here is a no-op if not, but
  // onConnectedChange will fire once it is.
  const offConn = chatWs.onConnectedChange((connected) => {
    if (connected) sendRegister();
  });
  sendRegister();

  return () => {
    try {
      chatWs.send({ type: 'unregister_code_ext_bridge', data: { alias } });
    } catch {}
    offMsg();
    offConn();
  };
}
