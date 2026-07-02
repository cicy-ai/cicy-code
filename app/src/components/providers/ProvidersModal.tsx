import { useState } from 'react';
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
  if (!open) return null;
  return createPortal(
    <div data-id="providers-modal-overlay" className="fixed inset-0 z-[9998] flex items-center justify-center bg-black/60 p-6" onClick={onClose}>
      <div data-id="providers-modal" className="relative flex h-[80vh] w-[min(1000px,92vw)] overflow-hidden rounded-2xl border border-white/[0.08] bg-[#0b0b0c] shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div ref={setLeft} data-id="providers-modal-left" className="h-full w-[340px] shrink-0 border-r border-white/[0.06]" />
        <div ref={setRight} data-id="providers-modal-right" className="relative h-full min-w-0 flex-1" />
        <ProviderDashboard leftMount={left} rightMount={right} tab="providers" hideTabStrip />
        <button data-id="providers-modal-close" type="button" onClick={onClose} title="关闭" className="absolute right-3 top-3 z-10 grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200">
          <X size={16} />
        </button>
      </div>
    </div>,
    document.body,
  );
}
