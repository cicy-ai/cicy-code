// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense, useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { BookOpen, Check, Eye, EyeOff, Loader2, Settings, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import FilesView from '../files/FilesView';
import apiService from '../../services/api';

const KnowledgeGraphView = lazy(() => import('./KnowledgeGraphView'));

interface KnowledgePanelProps {
  open: boolean;
  onClose: () => void;
  agentId: string;
  workspaceFolder: string;
  pageClientId?: string;
  agents?: Array<{ paneId: string; title: string }>;
  pendingCount?: number;
}

// Full-page knowledge workspace:
//   left   FilesView explorer (scoped to ~/cicy-ai/knowledge)
//   center FilesView editor
//   right  knowledge graph
// FilesView already owns the explorer/editor split, so this reuses the real
// editor instead of creating a second file-selection or save path.
export default function KnowledgePanel({ open, onClose, agentId, workspaceFolder, pageClientId, agents = [], pendingCount = 0 }: KnowledgePanelProps) {
  const { t } = useTranslation('workspace');
  const [openRequest, setOpenRequest] = useState<{ path: string; root: string; nonce: number } | null>(null);
  const [configOpen, setConfigOpen] = useState(false);
  const [configLoading, setConfigLoading] = useState(false);
  const [configSaving, setConfigSaving] = useState(false);
  const [configError, setConfigError] = useState('');
  const [configSaved, setConfigSaved] = useState(false);
  const [pane, setPane] = useState('w-1001:main.0');
  const [origin, setOrigin] = useState('');
  const [token, setToken] = useState('');
  const [showToken, setShowToken] = useState(false);
  const [tokenSet, setTokenSet] = useState(false);
  const [tokenTail, setTokenTail] = useState('');

  const openGraphEntry = (entry: { id: string; path?: string }) => {
    const normalized = String(entry.path || '').replace(/\\/g, '/');
    const marker = '/knowledge/';
    const markerAt = normalized.lastIndexOf(marker);
    const relativePath = markerAt >= 0
      ? normalized.slice(markerAt + marker.length)
      : `${entry.id}.md`;
    setOpenRequest({ path: relativePath, root: 'knowledge', nonce: Date.now() });
  };

  useEffect(() => {
    if (!open) return;
    setOpenRequest({ path: 'README.md', root: 'knowledge', nonce: Date.now() });
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open, onClose]);

  const showConfig = async () => {
    setConfigOpen(true);
    setConfigLoading(true);
    setConfigError('');
    setConfigSaved(false);
    setToken('');
    setShowToken(false);
    try {
      const { data } = await apiService.getKnowledgeConfig();
      setPane(String(data?.pane || data?.default || 'w-1001:main.0'));
      setOrigin(String(data?.origin || ''));
      setTokenSet(!!data?.token_set);
      setTokenTail(String(data?.token_tail || ''));
    } catch (error: any) {
      setConfigError(error?.response?.data?.error || error?.message || '加载配置失败');
    } finally {
      setConfigLoading(false);
    }
  };

  const saveConfig = async (clearToken = false) => {
    setConfigSaving(true);
    setConfigError('');
    setConfigSaved(false);
    try {
      const { data } = await apiService.saveKnowledgeConfig({ pane, origin, token, clear_token: clearToken });
      setPane(String(data?.pane || pane));
      setOrigin(String(data?.origin || origin));
      setTokenSet(!!data?.token_set);
      setTokenTail(String(data?.token_tail || ''));
      setToken('');
      setConfigSaved(true);
    } catch (error: any) {
      setConfigError(error?.response?.data?.error || error?.message || '保存配置失败');
    } finally {
      setConfigSaving(false);
    }
  };

  const toggleTokenVisibility = async () => {
    if (showToken) {
      setShowToken(false);
      return;
    }
    if (!token && tokenSet) {
      try {
        const { data } = await apiService.getKnowledgeConfig(true);
        setToken(String(data?.token || ''));
      } catch (error: any) {
        setConfigError(error?.response?.data?.error || error?.message || '读取 Token 失败');
        return;
      }
    }
    setShowToken(true);
  };

  if (!open) return null;

  return createPortal(
    <div data-id="knowledge-modal-overlay" className="fixed inset-y-0 right-0 left-14 z-[1000] bg-[#0b0b0d]">
      <section data-id="knowledge-modal" className="absolute inset-0 flex min-h-0 flex-col overflow-hidden bg-[#0b0b0d]">
        <header data-id="knowledge-modal-header" className="flex h-12 shrink-0 items-center gap-2 border-b border-white/[0.07] px-4">
          <BookOpen className="h-4 w-4 text-zinc-400" />
          <h2 data-id="knowledge-modal-title" className="min-w-0 flex-1 truncate text-[13px] font-semibold text-zinc-100">
            {t('tabKnowledge', { defaultValue: '知识库' })}
          </h2>
          <div data-id="knowledge-governance-status" className="text-[11px] text-zinc-500">
            待治理 <span className={pendingCount > 0 ? 'font-semibold text-amber-400' : 'text-zinc-400'}>{pendingCount}</span>
          </div>
          <button
            type="button"
            data-id="knowledge-config-open"
            onClick={() => { void showConfig(); }}
            className="relative inline-flex h-8 w-8 items-center justify-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            title="知识库配置"
          >
            <Settings className="h-4 w-4" />
            {pendingCount > 0 ? <span className="absolute right-0.5 top-0.5 h-2 w-2 rounded-full bg-red-500" /> : null}
          </button>
        </header>
        <div data-id="knowledge-modal-body" className="flex min-h-0 flex-1">
          <div data-id="knowledge-modal-files-content" className="min-w-0 flex-[3] border-r border-white/[0.07]">
            <FilesView
              agentId={agentId}
              workspaceFolder={workspaceFolder}
              pageClientId={pageClientId}
              scopeRoot="knowledge"
              markdownPreviewByDefault
              className="h-full w-full"
              openRequest={openRequest}
            />
          </div>
          <aside data-id="knowledge-modal-graph" className="relative min-w-[360px] flex-[2] bg-[#08080a]">
            <Suspense fallback={<div className="flex h-full items-center justify-center text-[12px] text-zinc-600">{t('knLoadingGraph')}</div>}>
              <KnowledgeGraphView className="h-full w-full" onOpenEntry={openGraphEntry} />
            </Suspense>
          </aside>
        </div>
      </section>
      {configOpen ? (
        <div data-id="knowledge-config-overlay" className="absolute inset-0 z-20 flex items-center justify-center bg-black/55 p-6" onMouseDown={(event) => { if (event.target === event.currentTarget) setConfigOpen(false); }}>
          <section data-id="knowledge-config-modal" className="w-full max-w-lg rounded-xl border border-white/[0.09] bg-[#141416] shadow-2xl">
            <header className="flex h-12 items-center border-b border-white/[0.07] px-4">
              <Settings className="mr-2 h-4 w-4 text-zinc-400" />
              <h3 className="flex-1 text-sm font-semibold text-zinc-100">知识库配置</h3>
              <button type="button" data-id="knowledge-config-close" onClick={() => setConfigOpen(false)} className="rounded p-1.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"><X className="h-4 w-4" /></button>
            </header>
            {configLoading ? <div className="flex h-56 items-center justify-center"><Loader2 className="h-5 w-5 animate-spin text-zinc-500" /></div> : (
              <div className="space-y-4 p-4">
                <label className="block text-xs text-zinc-400">接收待治理通知的 Agent
                  <select data-id="knowledge-config-agent" value={pane} onChange={(event) => setPane(event.target.value)} className="mt-1.5 h-9 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 text-sm text-zinc-200 outline-none focus:border-sky-600">
                    {!agents.some((agent) => agent.paneId === pane) ? <option value={pane}>{pane}</option> : null}
                    {agents.map((agent) => <option key={agent.paneId} value={agent.paneId}>{agent.title || agent.paneId} · {agent.paneId.replace(/:main\.0$/, '')}</option>)}
                  </select>
                </label>
                <label className="block text-xs text-zinc-400">私有知识库仓库 origin
                  <input data-id="knowledge-config-origin" value={origin} onChange={(event) => setOrigin(event.target.value)} placeholder="https://github.com/org/private-knowledge.git" className="mt-1.5 h-9 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 font-mono text-xs text-zinc-200 outline-none focus:border-sky-600" />
                </label>
                <label className="block text-xs text-zinc-400">CICY_KNOWLEDGE_GH_TOKEN
                  <span className="relative mt-1.5 block">
                    <input data-id="knowledge-config-token" type={showToken ? 'text' : 'password'} value={token} onChange={(event) => setToken(event.target.value)} placeholder={tokenSet ? `已配置 ····${tokenTail}（留空不修改）` : 'GitHub fine-grained token'} autoComplete="new-password" className="h-9 w-full rounded-md border border-zinc-700 bg-zinc-900 py-0 pl-3 pr-10 font-mono text-xs text-zinc-200 outline-none focus:border-sky-600" />
                    <button type="button" data-id="knowledge-config-token-toggle" onClick={() => { void toggleTokenVisibility(); }} className="absolute inset-y-0 right-0 inline-flex w-9 items-center justify-center text-zinc-500 hover:text-zinc-200" title={showToken ? '隐藏 Token' : '显示 Token'} aria-label={showToken ? '隐藏 Token' : '显示 Token'}>
                      {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </span>
                </label>
                <div className="flex min-h-5 items-center text-xs">
                  {configError ? <span className="text-red-400">{configError}</span> : configSaved ? <span className="inline-flex items-center gap-1 text-emerald-400"><Check className="h-3.5 w-3.5" />已保存</span> : <span className="text-zinc-600">Token 仅保存在本机私密配置中，不写入 Git remote URL。</span>}
                </div>
                <div className="flex items-center justify-between border-t border-white/[0.07] pt-3">
                  <button type="button" disabled={!tokenSet || configSaving} onClick={() => { void saveConfig(true); }} className="text-xs text-red-400 disabled:opacity-30">清除 Token</button>
                  <button type="button" data-id="knowledge-config-save" disabled={configSaving || !pane.trim()} onClick={() => { void saveConfig(); }} className="inline-flex h-9 items-center gap-2 rounded-md bg-zinc-100 px-4 text-xs font-semibold text-zinc-900 disabled:opacity-50">{configSaving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}保存</button>
                </div>
              </div>
            )}
          </section>
        </div>
      ) : null}
    </div>,
    document.body,
  );
}
