import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';

interface PromptProps {
  title: string;
  initialValue?: string;
  placeholder?: string;
  okLabel?: string;
  description?: React.ReactNode;
  onCancel: () => void;
  onSubmit: (value: string) => void | Promise<void>;
}

export function PromptModal({
  title,
  initialValue = '',
  placeholder,
  okLabel = '确定',
  description,
  onCancel,
  onSubmit,
}: PromptProps) {
  const [value, setValue] = useState(initialValue);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setTimeout(() => {
      const el = inputRef.current;
      if (!el) return;
      el.focus();
      // Select the basename (text before the last dot) so renames default
      // to editing the name without the extension.
      const i = initialValue.lastIndexOf('.');
      if (i > 0) el.setSelectionRange(0, i);
      else el.select();
    }, 30);
  }, [initialValue]);

  const submit = async () => {
    const v = value.trim();
    if (!v) return;
    setBusy(true);
    setError('');
    try {
      await onSubmit(v);
    } catch (e) {
      setError((e as Error).message || 'failed');
      setBusy(false);
    }
  };

  return (
    <div
      data-id="prompt-modal-backdrop"
      className="fixed inset-0 z-[2147483600] bg-black/40 backdrop-blur-sm flex items-center justify-center"
      onPointerDown={onCancel}
    >
      <div
        data-id="prompt-modal"
        className="w-full max-w-md mx-4 rounded-lg border border-zinc-700 bg-zinc-900 shadow-2xl"
        onPointerDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 py-2.5 border-b border-zinc-800">
          <span data-id="prompt-modal-title" className="text-sm text-zinc-100">{title}</span>
          <span className="flex-1" />
          <button data-id="prompt-modal-close" onClick={onCancel} className="p-1 rounded hover:bg-zinc-800" disabled={busy}>
            <X className="w-4 h-4 text-zinc-500" />
          </button>
        </div>
        <div className="p-4 space-y-3">
          {description && <div data-id="prompt-modal-description" className="text-xs text-zinc-400">{description}</div>}
          <input
            ref={inputRef}
            data-id="prompt-modal-input"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit();
              if (e.key === 'Escape') onCancel();
            }}
            placeholder={placeholder}
            className="w-full px-3 py-2 bg-zinc-950 border border-zinc-700 rounded text-sm text-zinc-100 outline-none focus:border-zinc-500"
            disabled={busy}
          />
          {error && <div data-id="prompt-modal-error" className="text-xs text-red-400">{error}</div>}
          <div className="flex justify-end gap-2 pt-1">
            <button
              data-id="prompt-modal-cancel"
              onClick={onCancel}
              className="px-3 py-1.5 rounded text-xs bg-zinc-800 hover:bg-zinc-700 text-zinc-200"
              disabled={busy}
            >
              取消
            </button>
            <button
              data-id="prompt-modal-ok"
              onClick={submit}
              className="px-3 py-1.5 rounded text-xs bg-sky-600 hover:bg-sky-500 text-white disabled:opacity-50"
              disabled={busy || !value.trim()}
            >
              {okLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

interface ConfirmProps {
  title: string;
  description?: React.ReactNode;
  okLabel?: string;
  destructive?: boolean;
  onCancel: () => void;
  onConfirm: () => void | Promise<void>;
}

export function ConfirmModal({
  title,
  description,
  okLabel = '确定',
  destructive,
  onCancel,
  onConfirm,
}: ConfirmProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
      else if (e.key === 'Enter' && !busy) run();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy]);

  const run = async () => {
    setBusy(true);
    setError('');
    try {
      await onConfirm();
    } catch (e) {
      setError((e as Error).message || 'failed');
      setBusy(false);
    }
  };

  return (
    <div
      data-id="confirm-modal-backdrop"
      className="fixed inset-0 z-[2147483600] bg-black/40 backdrop-blur-sm flex items-center justify-center"
      onPointerDown={onCancel}
    >
      <div
        data-id="confirm-modal"
        data-destructive={destructive ? 'true' : 'false'}
        className="w-full max-w-md mx-4 rounded-lg border border-zinc-700 bg-zinc-900 shadow-2xl"
        onPointerDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 py-2.5 border-b border-zinc-800">
          <span data-id="confirm-modal-title" className="text-sm text-zinc-100">{title}</span>
          <span className="flex-1" />
          <button data-id="confirm-modal-close" onClick={onCancel} className="p-1 rounded hover:bg-zinc-800" disabled={busy}>
            <X className="w-4 h-4 text-zinc-500" />
          </button>
        </div>
        <div className="p-4 space-y-3 text-xs text-zinc-300">
          {description}
          {error && <div data-id="confirm-modal-error" className="text-red-400">{error}</div>}
          <div className="flex justify-end gap-2 pt-1">
            <button
              data-id="confirm-modal-cancel"
              onClick={onCancel}
              className="px-3 py-1.5 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200"
              disabled={busy}
            >
              取消
            </button>
            <button
              data-id="confirm-modal-ok"
              onClick={run}
              className={`px-3 py-1.5 rounded text-white disabled:opacity-50 ${
                destructive ? 'bg-red-600 hover:bg-red-500' : 'bg-sky-600 hover:bg-sky-500'
              }`}
              disabled={busy}
            >
              {okLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
