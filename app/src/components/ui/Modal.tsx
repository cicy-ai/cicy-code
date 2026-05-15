import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';

/* A small, app-styled replacement for window.confirm / window.prompt.
   Usage:
     const { confirm, prompt, node } = useDialogs();
     ... const ok = await confirm({ title: 'Delete?', body: <>…</>, danger: true, confirmLabel: 'Delete' });
     ... const v  = await prompt({ title: 'Paste token', placeholder: '123456:ABC…', mono: true });
   Render {node} once in the component tree. */

type ConfirmOpts = {
  title?: React.ReactNode;
  body?: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
};
type PromptOpts = ConfirmOpts & {
  placeholder?: string;
  defaultValue?: string;
  required?: boolean;
  mono?: boolean;
};

type DialogState =
  | { kind: 'confirm'; opts: ConfirmOpts; resolve: (v: boolean) => void }
  | { kind: 'prompt'; opts: PromptOpts; resolve: (v: string | null) => void }
  | null;

export function useDialogs() {
  const { t } = useTranslation('ui');
  const [state, setState] = useState<DialogState>(null);
  const [value, setValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const confirm = useCallback(
    (opts: ConfirmOpts = {}) => new Promise<boolean>((resolve) => setState({ kind: 'confirm', opts, resolve })),
    [],
  );
  const prompt = useCallback(
    (opts: PromptOpts = {}) =>
      new Promise<string | null>((resolve) => {
        setValue(opts.defaultValue || '');
        setState({ kind: 'prompt', opts, resolve });
      }),
    [],
  );

  const finish = useCallback((result: boolean | string | null) => {
    setState((s) => {
      if (s) (s.resolve as (v: any) => void)(result);
      return null;
    });
  }, []);
  const cancel = useCallback(() => {
    setState((s) => {
      if (s) (s.resolve as (v: any) => void)(s.kind === 'confirm' ? false : null);
      return null;
    });
  }, []);

  useEffect(() => {
    if (!state) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancel(); }
    };
    document.addEventListener('keydown', onKey, true);
    if (state.kind === 'prompt') {
      const t = window.setTimeout(() => { inputRef.current?.focus(); inputRef.current?.select(); }, 30);
      return () => { document.removeEventListener('keydown', onKey, true); window.clearTimeout(t); };
    }
    return () => document.removeEventListener('keydown', onKey, true);
  }, [state, cancel]);

  let node: React.ReactNode = null;
  if (state) {
    const promptInvalid = state.kind === 'prompt' && !!state.opts.required && value.trim() === '';
    const submit = () => {
      if (state.kind === 'prompt') {
        if (promptInvalid) return;
        finish(value);
      } else {
        finish(true);
      }
    };
    node = createPortal(
      <div
        data-id="modal-overlay"
        className="fixed inset-0 z-[9999999] flex items-center justify-center bg-black/55 backdrop-blur-sm animate-[fadeIn_120ms_ease-out]"
        onMouseDown={(e) => { if (e.target === e.currentTarget) cancel(); }}
      >
        <div data-id="modal-card" className="mx-4 w-full max-w-[420px] overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60">
          <div data-id="modal-header" className="flex items-start gap-3 px-5 pt-5">
            <div data-id="modal-header-body" className="min-w-0 flex-1">
              {state.opts.title && <div data-id="modal-title" className="text-[14px] font-semibold text-white">{state.opts.title}</div>}
              {state.opts.body && <div data-id="modal-body" className="mt-1.5 text-[13px] leading-relaxed text-zinc-400">{state.opts.body}</div>}
            </div>
            <button data-id="modal-close" onClick={cancel} className="grid h-6 w-6 shrink-0 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"><X size={14} /></button>
          </div>
          {state.kind === 'prompt' && (
            <div data-id="modal-prompt-wrap" className="px-5 pt-3">
              <input
                data-id="modal-prompt-input"
                ref={inputRef}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit(); } }}
                placeholder={state.opts.placeholder}
                autoComplete="off"
                spellCheck={false}
                className={`h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 ${state.opts.mono ? 'font-mono' : ''}`}
              />
            </div>
          )}
          <div data-id="modal-actions" className="mt-4 flex justify-end gap-2 px-5 pb-4">
            <button data-id="modal-cancel" onClick={cancel} className="h-8 rounded-lg px-3 text-[13px] text-zinc-400 transition-colors hover:bg-white/[0.05] hover:text-zinc-200">{state.opts.cancelLabel || t('modalCancel')}</button>
            <button
              data-id="modal-confirm"
              onClick={submit}
              disabled={promptInvalid}
              className={
                state.opts.danger
                  ? 'h-8 rounded-lg border border-red-500/25 bg-red-500/15 px-3 text-[13px] text-red-200 transition-colors hover:bg-red-500/25 disabled:opacity-50'
                  : 'h-8 rounded-lg bg-white px-3 text-[13px] font-medium text-[#0b0b0c] transition-colors hover:bg-zinc-200 disabled:bg-white/40'
              }
            >
              {state.opts.confirmLabel || t('modalConfirm')}
            </button>
          </div>
        </div>
      </div>,
      document.body,
    );
  }

  return { confirm, prompt, node };
}
