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
  const token = currentToken();
  const pagePane = currentPagePane();
  const pageClientId = currentPageClientId();
  if (!token || !pagePane || !pageClientId) {
    return;
  }
  const codeClientId = `${pageClientId}:code-ext`;
  const base = currentApiBase();
  const wsProtocol = base.startsWith('https://') ? 'wss://' : 'ws://';
  const wsHost = base.replace(/^https?:\/\//, '');
  const wsUrl = `${wsProtocol}${wsHost}/api/chat/ws?agent_id=${encodeURIComponent(pagePane)}&token=${encodeURIComponent(token)}&client_id=${encodeURIComponent(codeClientId)}`;
  let disposed = false;
  let socket: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

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
    try {
      socket = new WebSocket(wsUrl);
    } catch {
      scheduleReconnect();
      return;
    }
    socket.onclose = (event) => {
      socket = null;
      // 4409 = superseded by another connection with the same client_id (e.g.
      // a duplicated browser tab raced for this slot). The new connection is
      // authoritative; reconnecting would just kick it back out, so we stop.
      // The other side's pageClientId dedup will eventually settle and the
      // iframe will reload with a fresh client_id, re-activating this code.
      if (event && event.code === 4409) {
        disposed = true;
        return;
      }
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
                  page_client_id: pageClientId,
                  code_client_id: codeClientId,
                  page_pane: pagePane,
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
                  page_client_id: pageClientId,
                  code_client_id: codeClientId,
                  page_pane: pagePane,
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
                page_client_id: pageClientId,
                code_client_id: codeClientId,
                page_pane: pagePane,
                version: vscode.version,
              },
            }));
          } catch {
          }
        }
      } catch {
      }
    };
  };

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
  context.subscriptions.push(explorerDisposable, editorDisposable, fileInfoDisposable);
  connectHostOpenFileBridge(context);
}

export function deactivate() {}
