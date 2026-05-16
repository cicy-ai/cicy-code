import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import * as vscode from 'vscode';

function currentApiBase(): string {
  return 'http://127.0.0.1:8008';
}

function readCoderQuery(): Record<string, unknown> {
  try {
    const coderJsonPath = path.join(os.homedir(), '.local', 'share', 'code-server', 'coder.json');
    const raw = JSON.parse(fs.readFileSync(coderJsonPath, 'utf8')) as { query?: Record<string, unknown> };
    return raw && raw.query && typeof raw.query === 'object' ? raw.query : {};
  } catch {
    return {};
  }
}

function currentPagePane(): string {
  return String(readCoderQuery().page_pane || '').trim();
}

function currentPageClientId(): string {
  return String(readCoderQuery().client_id || '').trim();
}

function currentToken(): string {
  return String(readCoderQuery().token || '').trim();
}

interface BridgeConfig {
  token: string;
  pagePane: string;
  pageClientId: string;
}

function readBridgeConfig(): BridgeConfig | null {
  const q = readCoderQuery();
  const token = String(q.token || '').trim();
  const pagePane = String(q.page_pane || '').trim();
  const pageClientId = String(q.client_id || '').trim();
  if (!token || !pagePane || !pageClientId) return null;
  return { token, pagePane, pageClientId };
}

function currentWorkspaceFolder(): string {
  const folders = vscode.workspace.workspaceFolders;
  return folders && folders.length > 0 ? folders[0].uri.fsPath : '';
}

function isImagePath(filePath: string): boolean {
  return /\.(png|apng|jpe?g|gif|webp|bmp|ico|avif|svg)$/i.test(String(filePath || '').trim());
}

function buildExplorerPayload(uri: vscode.Uri): Record<string, unknown> | null {
  const folder = currentWorkspaceFolder();
  if (!folder) {
    return null;
  }
  return {
    folder,
    path: uri.fsPath,
  };
}

function buildEditorPayload(): Record<string, unknown> | null {
  const folder = currentWorkspaceFolder();
  const editor = vscode.window.activeTextEditor;
  if (!folder || !editor) {
    return null;
  }
  const payload: Record<string, unknown> = {
    folder,
    path: editor.document.uri.fsPath,
  };
  if (!editor.selection.isEmpty) {
    payload.range = {
      startLine: editor.selection.start.line + 1,
      startCharacter: editor.selection.start.character + 1,
      endLine: editor.selection.end.line + 1,
      endCharacter: editor.selection.end.character + 1,
    };
    payload.selectionText = editor.document.getText(editor.selection);
    payload.fileName = path.basename(editor.document.uri.fsPath);
  }
  return payload;
}

function openTextDocumentAtUri(uri: vscode.Uri): Thenable<void> {
  return vscode.workspace.openTextDocument(uri).then((doc) => {
    return vscode.window.showTextDocument(doc, { preview: false }).then(() => undefined);
  });
}

function parseFileReference(rawPath: unknown): {
  filePath: string;
  line?: number;
  column?: number;
} {
  let text = String(rawPath || '').trim();
  if (!text) {
    return { filePath: '' };
  }
  if (text.startsWith('file://')) {
    text = text.slice(7).trim();
    if (text && !text.startsWith('/') && !text.startsWith('~/') && !text.startsWith('./') && !text.startsWith('../') && !/^[A-Za-z]:[\\/]/.test(text)) {
      text = `/${text.replace(/^\/+/, '')}`;
    }
  }
  let match = text.match(/^(.*?):(\d+):(\d+)-(\d+):(\d+)$/);
  if (match) {
    const line = Number(match[2] || 0);
    const column = Number(match[3] || 0);
    if (!Number.isFinite(line) || line <= 0) {
      return { filePath: text };
    }
    return {
      filePath: String(match[1] || '').trim(),
      line,
      column: Number.isFinite(column) && column > 0 ? column : 1,
    };
  }
  match = text.match(/^(.*?):(\d+)(?::(\d+))?$/);
  if (!match) {
    return { filePath: text };
  }
  const line = Number(match[2] || 0);
  const column = Number(match[3] || 0);
  if (!Number.isFinite(line) || line <= 0) {
    return { filePath: text };
  }
  return {
    filePath: String(match[1] || '').trim(),
    line,
    column: Number.isFinite(column) && column > 0 ? column : 1,
  };
}

async function openUriReference(uri: vscode.Uri, parsed: { line?: number; column?: number }): Promise<void> {
  const stat = await vscode.workspace.fs.stat(uri);
  if ((stat.type & vscode.FileType.Directory) !== 0) {
    return;
  }
  if (!parsed.line || parsed.line <= 0) {
    if (isImagePath(uri.fsPath)) {
      await vscode.commands.executeCommand('vscode.openWith', uri, 'imagePreview.previewEditor');
      return;
    }
    await vscode.commands.executeCommand('vscode.open', uri);
    return;
  }
  const doc = await vscode.workspace.openTextDocument(uri);
  const editor = await vscode.window.showTextDocument(doc, { preview: false });
  if (parsed.line && parsed.line > 0) {
    const lineIndex = Math.max(0, Math.min(doc.lineCount - 1, parsed.line - 1));
    const lineText = doc.lineAt(lineIndex).text;
    const charIndex = Math.max(0, Math.min(lineText.length, Math.max(0, (parsed.column || 1) - 1)));
    const position = new vscode.Position(lineIndex, charIndex);
    const selection = new vscode.Selection(position, position);
    editor.selection = selection;
    editor.revealRange(new vscode.Range(position, position), vscode.TextEditorRevealType.InCenter);
  }
}

async function pathExists(uri: vscode.Uri): Promise<boolean> {
  try {
    await vscode.workspace.fs.stat(uri);
    return true;
  } catch {
    return false;
  }
}

async function resolveWorkspaceFileUri(rawPath: string): Promise<vscode.Uri | null> {
  const filePath = parseFileReference(rawPath).filePath;
  if (!filePath) {
    return null;
  }
  if (path.isAbsolute(filePath)) {
    const absoluteUri = vscode.Uri.file(filePath);
    return absoluteUri;
  }
  const workspaceFolder = currentWorkspaceFolder();
  const normalizedRelative = filePath.replace(/^\.\//, '').replace(/^\/+/, '');
  const directCandidates: vscode.Uri[] = [];
  if (workspaceFolder && normalizedRelative) {
    directCandidates.push(vscode.Uri.file(path.join(workspaceFolder, normalizedRelative)));
  }
  for (const candidate of directCandidates) {
    if (await pathExists(candidate)) {
      return candidate;
    }
  }
  if (!workspaceFolder) {
    return null;
  }
  const basename = path.basename(normalizedRelative || filePath);
  const patterns = [
    normalizedRelative && normalizedRelative !== basename ? `**/${normalizedRelative}` : '',
    basename ? `**/${basename}` : '',
  ].filter(Boolean);
  for (const pattern of patterns) {
    const matches = await vscode.workspace.findFiles(new vscode.RelativePattern(workspaceFolder, pattern), '**/{node_modules,.git}/**', 20);
    if (matches.length > 0) {
      matches.sort((a, b) => a.fsPath.length - b.fsPath.length || a.fsPath.localeCompare(b.fsPath));
      return matches[0];
    }
  }
  return null;
}

async function postSendPath(payload: Record<string, unknown>, displayPath: string): Promise<void> {
  const response = await fetch(`${currentApiBase()}/api/code-server/send-path`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    const text = await response.text().catch(() => '');
    void vscode.window.showErrorMessage(vscode.l10n.t('Failed to send file path: {0}', String(text || response.status)));
    return;
  }
  void vscode.window.showInformationMessage(vscode.l10n.t('Sent: {0}', displayPath));
}

async function sendPathToCurrentAgent(uri: vscode.Uri): Promise<void> {
  const payload = buildExplorerPayload(uri);
  if (!payload) {
    void vscode.window.showErrorMessage(vscode.l10n.t('No active workspace folder'));
    return;
  }
  await postSendPath(payload, uri.fsPath);
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return `${bytes}`;
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const display = value >= 100 ? Math.round(value).toString()
    : value >= 10 ? value.toFixed(1)
    : value.toFixed(2);
  return `${display} ${units[unitIndex]}`;
}

function formatRelativeMtime(ms: number): string {
  const diff = Date.now() - ms;
  if (diff < 0) return vscode.l10n.t('in the future');
  if (diff < 60_000) return vscode.l10n.t('just now');
  if (diff < 3_600_000) return vscode.l10n.t('{0} minutes ago', String(Math.floor(diff / 60_000)));
  if (diff < 86_400_000) return vscode.l10n.t('{0} hours ago', String(Math.floor(diff / 3_600_000)));
  if (diff < 30 * 86_400_000) return vscode.l10n.t('{0} days ago', String(Math.floor(diff / 86_400_000)));
  return new Date(ms).toLocaleDateString();
}

function formatAbsoluteMtime(ms: number): string {
  const d = new Date(ms);
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mi = String(d.getMinutes()).padStart(2, '0');
  const ss = String(d.getSeconds()).padStart(2, '0');
  return `${yyyy}-${mm}-${dd} ${hh}:${mi}:${ss}`;
}

async function showFileInfo(uri: vscode.Uri): Promise<void> {
  try {
    const stat = await fs.promises.stat(uri.fsPath);
    if (!stat.isFile()) return;
    const name = path.basename(uri.fsPath);
    const sizeStr = formatBytes(stat.size);
    const mtimeRel = formatRelativeMtime(stat.mtimeMs);
    const mtimeAbs = formatAbsoluteMtime(stat.mtimeMs);
    void vscode.window.showInformationMessage(
      vscode.l10n.t('{0} · {1} ({2} B) · modified {3} ({4})', name, sizeStr, String(stat.size), mtimeRel, mtimeAbs),
    );
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error || '');
    void vscode.window.showErrorMessage(vscode.l10n.t('Failed to read file info: {0}', message || uri.fsPath));
  }
}

async function addToFavorites(uri: vscode.Uri): Promise<void> {
  try {
    const workspaceFolder = currentWorkspaceFolder();
    if (!workspaceFolder) {
      void vscode.window.showErrorMessage(vscode.l10n.t('No active workspace folder'));
      return;
    }
    const target = uri.fsPath;
    const favoritesDir = path.join(workspaceFolder, 'favorite');
    await fs.promises.mkdir(favoritesDir, { recursive: true });

    const relToFavDir = path.relative(favoritesDir, target);
    if (target === favoritesDir || (!relToFavDir.startsWith('..') && !path.isAbsolute(relToFavDir))) {
      void vscode.window.showWarningMessage(vscode.l10n.t('Cannot favorite an item inside the favorites folder'));
      return;
    }

    const baseName = path.basename(target) || 'item';
    let linkName = baseName;
    let counter = 2;
    while (counter <= 100) {
      const linkPath = path.join(favoritesDir, linkName);
      let existing: import('fs').Stats | null = null;
      try {
        existing = await fs.promises.lstat(linkPath);
      } catch {
        existing = null;
      }
      if (!existing) {
        await fs.promises.symlink(target, linkPath);
        void vscode.window.showInformationMessage(vscode.l10n.t('Added to favorites: {0}', linkName));
        return;
      }
      if (existing.isSymbolicLink()) {
        const linkTarget = await fs.promises.readlink(linkPath);
        const resolved = path.isAbsolute(linkTarget) ? linkTarget : path.resolve(favoritesDir, linkTarget);
        if (resolved === target) {
          void vscode.window.showInformationMessage(vscode.l10n.t('Already in favorites: {0}', linkName));
          return;
        }
      }
      linkName = `${baseName} (${counter})`;
      counter += 1;
    }
    throw new Error('too many name collisions in favorites folder');
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error || '');
    void vscode.window.showErrorMessage(vscode.l10n.t('Failed to add to favorites: {0}', message));
  }
}

async function sendActiveDocumentToCurrentAgent(): Promise<void> {
  const payload = buildEditorPayload();
  if (!payload) {
    void vscode.window.showErrorMessage(vscode.l10n.t('No active document'));
    return;
  }
  await postSendPath(payload, String(payload.path || ''));
}

async function openFileFromHost(rawPath: unknown): Promise<void> {
  const parsed = parseFileReference(rawPath);
  const filePath = parsed.filePath;
  if (!filePath) {
    throw new Error('empty path');
  }
  const uri = await resolveWorkspaceFileUri(filePath);
  if (!uri) {
    void vscode.window.showErrorMessage(vscode.l10n.t('Failed to open file: {0}', filePath));
    throw new Error(filePath);
  }
  await openUriReference(uri, parsed);
}

function connectHostOpenFileBridge(context: vscode.ExtensionContext): void {
  // Read coder.json fresh on every (re)connect: the page's `client_id` can
  // change when the user reloads the cicy-code tab or the BroadcastChannel
  // tab-dedup elects a new winner. coder.json gets rewritten by code-server
  // with the new URL query params, so the bridge must always honour the
  // latest values rather than freezing them at activation time.
  const cfg = readBridgeConfig();
  if (!cfg) {
    return;
  }
  const log = vscode.window.createOutputChannel('CiCy Bridge');
  context.subscriptions.push(log);
  log.appendLine(`[bridge] start: agent=${cfg.pagePane} client=${cfg.pageClientId}`);

  let disposed = false;
  let socket: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let currentClientId = cfg.pageClientId;
  let currentPagePane = cfg.pagePane;
  let currentCodeClientId = `${currentClientId}:code-ext`;

  const buildUrl = (token: string, agent: string, codeClient: string): string => {
    const base = currentApiBase();
    const wsProtocol = base.startsWith('https://') ? 'wss://' : 'ws://';
    const wsHost = base.replace(/^https?:\/\//, '');
    return `${wsProtocol}${wsHost}/api/chat/ws?agent_id=${encodeURIComponent(agent)}&token=${encodeURIComponent(token)}&client_id=${encodeURIComponent(codeClient)}`;
  };

  const scheduleReconnect = () => {
    if (disposed || reconnectTimer) {
      return;
    }
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, 1000);
  };

  const connect = () => {
    if (disposed) {
      return;
    }
    const fresh = readBridgeConfig();
    if (!fresh) {
      // coder.json transiently unreadable (mid-write by code-server) — retry.
      scheduleReconnect();
      return;
    }
    if (fresh.pageClientId !== currentClientId || fresh.pagePane !== currentPagePane) {
      log.appendLine(`[bridge] client_id changed: ${currentClientId} -> ${fresh.pageClientId}`);
      currentClientId = fresh.pageClientId;
      currentPagePane = fresh.pagePane;
      currentCodeClientId = `${currentClientId}:code-ext`;
    }
    const wsUrl = buildUrl(fresh.token, currentPagePane, currentCodeClientId);
    try {
      socket = new WebSocket(wsUrl);
    } catch (err: any) {
      log.appendLine(`[bridge] ctor error: ${err?.message || err}`);
      scheduleReconnect();
      return;
    }
    socket.onopen = () => {
      log.appendLine(`[bridge] open ws agent=${currentPagePane} client=${currentCodeClientId}`);
    };
    socket.onclose = (event) => {
      socket = null;
      log.appendLine(`[bridge] close code=${event?.code} reason=${String(event?.reason || '')}`);
      // 4409 = superseded by another connection with the same client_id (e.g.
      // a duplicated browser tab raced for this slot). The other tab is now
      // authoritative; sleep for a moment then retry — by then the duplicate
      // should have settled and coder.json may carry a fresh client_id we can
      // honour. Don't go disposed=true forever, that was the previous bug.
      scheduleReconnect();
    };
    socket.onerror = () => {
      try {
        socket?.close();
      } catch {
      }
    };
    socket.onmessage = (event) => {
      try {
        const payload = JSON.parse(String(event.data || '')) as { type?: string; data?: Record<string, unknown> };
        if (payload?.type === 'host.open_file' && payload.data) {
          const requestId = String(payload.data?.requestId || '');
          void openFileFromHost(payload.data.path).then(() => {
            try {
              socket?.send(JSON.stringify({
                type: 'code.opened',
                data: {
                  requestId,
                  path: String(payload.data?.path || ''),
                  page_client_id: currentClientId,
                  code_client_id: currentCodeClientId,
                  page_pane: currentPagePane,
                },
              }));
            } catch {
            }
          }).catch((error) => {
            const message = error instanceof Error ? error.message : String(error || '');
            void vscode.window.showErrorMessage(vscode.l10n.t('Failed to open file: {0}', message || String(payload.data?.path || '')));
            try {
              socket?.send(JSON.stringify({
                type: 'code.open_file_error',
                data: {
                  requestId,
                  path: String(payload.data?.path || ''),
                  error: message || String(payload.data?.path || ''),
                  page_client_id: currentClientId,
                  code_client_id: currentCodeClientId,
                  page_pane: currentPagePane,
                },
              }));
            } catch {
            }
          });
          return;
        }
        if (payload?.type === 'host.ping') {
          try {
            socket?.send(JSON.stringify({
              type: 'code.pong',
              data: {
                requestId: String(payload.data?.requestId || ''),
                page_client_id: currentClientId,
                code_client_id: currentCodeClientId,
                page_pane: currentPagePane,
                version: vscode.version,
              },
            }));
          } catch {
          }
        }
        if (payload?.type === 'host.active_file' && payload.data) {
          // Return the path of the currently focused editor tab. Empty path
          // means no editor is focused (e.g. the welcome page is showing).
          const requestId = String(payload.data?.requestId || '');
          const ed = vscode.window.activeTextEditor;
          const sel = ed?.selection;
          try {
            socket?.send(JSON.stringify({
              type: 'code.active_file',
              data: {
                requestId,
                path: ed ? ed.document.uri.fsPath : '',
                language: ed ? ed.document.languageId : '',
                line: sel ? sel.active.line + 1 : 0,
                column: sel ? sel.active.character + 1 : 0,
                page_client_id: currentClientId,
                code_client_id: currentCodeClientId,
              },
            }));
          } catch {
          }
        }
        if (payload?.type === 'host.list_tabs' && payload.data) {
          // Return all open tabs across all editor groups. We surface only
          // file-backed tabs (TabInputText / TabInputCustom / TabInputNotebook)
          // — non-file panes like Settings / Welcome have no path so we skip
          // them. `isActive` and `isDirty` come from VS Code's tab state.
          const requestId = String(payload.data?.requestId || '');
          const tabs: Array<Record<string, unknown>> = [];
          try {
            for (const group of vscode.window.tabGroups.all) {
              for (const t of group.tabs) {
                let p = '';
                const inp = t.input as any;
                if (inp && typeof inp === 'object' && inp.uri && typeof inp.uri.fsPath === 'string') {
                  p = inp.uri.fsPath;
                }
                if (!p) continue;
                tabs.push({
                  path: p,
                  label: t.label,
                  isActive: t.isActive,
                  isDirty: t.isDirty,
                  group: group.viewColumn,
                });
              }
            }
          } catch {
          }
          try {
            socket?.send(JSON.stringify({
              type: 'code.tabs',
              data: {
                requestId,
                tabs,
                page_client_id: currentClientId,
                code_client_id: currentCodeClientId,
              },
            }));
          } catch {
          }
        }
      } catch {
      }
    };
  };

  // Also watch coder.json for changes — if the page reloads with a new
  // client_id while we're still connected on the old one, react immediately
  // instead of waiting for the next close/reconnect.
  try {
    const coderJsonPath = path.join(os.homedir(), '.local', 'share', 'code-server', 'coder.json');
    const watcher = fs.watch(coderJsonPath, { persistent: false }, () => {
      const fresh = readBridgeConfig();
      if (!fresh) return;
      if (fresh.pageClientId !== currentClientId || fresh.pagePane !== currentPagePane) {
        log.appendLine(`[bridge] coder.json changed: ${currentClientId} -> ${fresh.pageClientId}, reconnecting`);
        try { socket?.close(); } catch {}
      }
    });
    context.subscriptions.push({ dispose: () => watcher.close() });
  } catch (err: any) {
    log.appendLine(`[bridge] fs.watch failed: ${err?.message || err}`);
  }

  context.subscriptions.push({
    dispose: () => {
      disposed = true;
      try { socket?.close(); } catch {}
    },
  });

  connect();
  context.subscriptions.push({
    dispose: () => {
      disposed = true;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      if (socket) {
        try {
          socket.close();
        } catch {
        }
        socket = null;
      }
    },
  });
}

export function activate(context: vscode.ExtensionContext) {
  const explorerDisposable = vscode.commands.registerCommand('cicy.sendPathToCurrentAgent', async (uri: vscode.Uri) => {
    if (!uri) return;
    await sendPathToCurrentAgent(uri);
  });
  const editorDisposable = vscode.commands.registerCommand('cicy.sendActiveDocumentToCurrentAgent', async () => {
    await sendActiveDocumentToCurrentAgent();
  });
  const fileInfoDisposable = vscode.commands.registerCommand('cicy.showFileInfo', async (uri: vscode.Uri) => {
    if (!uri) return;
    await showFileInfo(uri);
  });
  const favoritesDisposable = vscode.commands.registerCommand('cicy.addToFavorites', async (uri: vscode.Uri) => {
    if (!uri) return;
    await addToFavorites(uri);
  });
  context.subscriptions.push(explorerDisposable, editorDisposable, fileInfoDisposable, favoritesDisposable);
  connectHostOpenFileBridge(context);
}

export function deactivate() {}
