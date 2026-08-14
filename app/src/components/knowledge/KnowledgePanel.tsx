// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense, useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { BookOpen, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import FilesView from '../files/FilesView';

const KnowledgeGraphView = lazy(() => import('./KnowledgeGraphView'));

interface KnowledgePanelProps {
  open: boolean;
  onClose: () => void;
  agentId: string;
  workspaceFolder: string;
  pageClientId?: string;
}

// Full-page knowledge workspace:
//   left   FilesView explorer (scoped to ~/cicy-ai/knowledge)
//   center FilesView editor
//   right  knowledge graph
// FilesView already owns the explorer/editor split, so this reuses the real
// editor instead of creating a second file-selection or save path.
export default function KnowledgePanel({ open, onClose, agentId, workspaceFolder, pageClientId }: KnowledgePanelProps) {
  const { t } = useTranslation('workspace');
  const [openRequest, setOpenRequest] = useState<{ path: string; root: string; nonce: number } | null>(null);

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
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div data-id="knowledge-modal-overlay" className="fixed inset-0 z-[1000] bg-[#0b0b0d]">
      <section data-id="knowledge-modal" className="absolute inset-0 flex min-h-0 flex-col overflow-hidden bg-[#0b0b0d]">
        <header data-id="knowledge-modal-header" className="flex h-12 shrink-0 items-center gap-2 border-b border-white/[0.07] px-4">
          <BookOpen className="h-4 w-4 text-zinc-400" />
          <h2 data-id="knowledge-modal-title" className="min-w-0 flex-1 truncate text-[13px] font-semibold text-zinc-100">
            {t('tabKnowledge', { defaultValue: '知识库' })}
          </h2>
          <button
            type="button"
            data-id="knowledge-modal-close"
            onClick={onClose}
            className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            title={t('close', { ns: 'common', defaultValue: '关闭' })}
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div data-id="knowledge-modal-body" className="flex min-h-0 flex-1">
          <div data-id="knowledge-modal-files-content" className="min-w-0 flex-[3] border-r border-white/[0.07]">
            <FilesView
              agentId={agentId}
              workspaceFolder={workspaceFolder}
              pageClientId={pageClientId}
              scopeRoot="knowledge"
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
    </div>,
    document.body,
  );
}
