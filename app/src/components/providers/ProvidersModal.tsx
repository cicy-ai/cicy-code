// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import ProviderDashboard from './ProviderDashboard';

// Standalone LLM-provider modal: two columns — left = all providers (with a red
// "no key" badge on any keyless one), right = the selected provider's detail.
// Nothing else (no routing tab, no general-settings sections). Reuses
// ProviderDashboard in controlled `providers` mode with its tab strip hidden.
export default function ProvidersModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [left, setLeft] = useState<HTMLElement | null>(null);
  const [right, setRight] = useState<HTMLElement | null>(null);
  // Fullscreen leaves no visible overlay to click, so Esc must always work.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);
  if (!open) return null;
  return createPortal(
    <div data-id="providers-modal-overlay" className="fixed inset-0 z-[9998] bg-black/60" onClick={onClose}>
      {/* Fullscreen: the provider list + detail need the room (voice test
          panel, model tables) — a floating 80vh card kept clipping them. */}
      <div data-id="providers-modal" className="relative flex h-full w-full overflow-hidden bg-[#0b0b0c]" onClick={(e) => e.stopPropagation()}>
        <div ref={setLeft} data-id="providers-modal-left" className="h-full w-[340px] shrink-0 border-r border-white/[0.06]" />
        <div ref={setRight} data-id="providers-modal-right" className="relative h-full min-w-0 flex-1" />
        <ProviderDashboard leftMount={left} rightMount={right} tab="providers" hideTabStrip />
        {/* z-50: the detail pane is absolute z-30 inside the same stacking
            context and was burying this button — fullscreen has no clickable
            overlay left, so ✕ (and Esc) are the only exits. */}
        <button data-id="providers-modal-close" type="button" onClick={onClose} title="关闭 (Esc)" className="absolute right-3 top-3 z-50 grid h-8 w-8 place-items-center rounded-lg bg-white/[0.04] text-zinc-400 transition-colors hover:bg-white/[0.1] hover:text-zinc-100">
          <X size={16} />
        </button>
      </div>
    </div>,
    document.body,
  );
}
