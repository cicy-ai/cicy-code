// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { Suspense } from 'react';
import { FileText, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import AgentDocRoleEditor from './AgentDocRoleEditor';

export default function AgentGuidanceDetail({ paneId, title, onClose }: {
  paneId: string;
  title: string;
  onClose: () => void;
}) {
  const { t } = useTranslation('workspace');
  return (
    <section data-id="agent-guidance-detail" className="absolute inset-0 z-50 flex flex-col overflow-hidden bg-[#0b0b0d]">
      <header data-id="agent-guidance-detail-header" className="flex h-12 shrink-0 items-center gap-3 border-b border-[var(--vsc-border)] px-3">
        <span data-id="agent-guidance-detail-icon" className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-zinc-300">
          <FileText className="h-4 w-4" />
        </span>
        <div data-id="agent-guidance-detail-heading" className="min-w-0 flex-1">
          <div data-id="agent-guidance-detail-title" className="truncate text-[13px] font-medium text-zinc-100">{title}</div>
          <div data-id="agent-guidance-detail-pane-id" className="truncate font-mono text-[10px] text-zinc-500">{paneId.replace(/:.*$/, '')}</div>
        </div>
        <button
          type="button"
          data-id="agent-guidance-detail-close"
          onClick={onClose}
          className="grid h-8 w-8 shrink-0 place-items-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
          aria-label={t('close', { ns: 'common' })}
        >
          <X className="h-4 w-4" />
        </button>
      </header>
      <div data-id="agent-guidance-detail-body" className="min-h-0 flex-1">
        <Suspense fallback={<div data-id="agent-guidance-detail-loading" className="flex h-full items-center justify-center text-xs text-zinc-600">Loading…</div>}>
          <AgentDocRoleEditor paneId={paneId} className="h-full w-full" />
        </Suspense>
      </div>
    </section>
  );
}
