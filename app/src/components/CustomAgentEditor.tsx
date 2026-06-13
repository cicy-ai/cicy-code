import { useEffect, useState } from 'react';
import { X, Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Spinner } from './ui/Spinner';
import { useDevRegister } from '../lib/devStore';
import apiService from '../services/api';

export interface CustomAgentDraft {
  name: string;
  tools: string[];
  model: string;
  body: string;
}

interface Props {
  open: boolean;
  toolGroups: string[];
  /** Pre-fill for editing an existing custom agent. */
  initial?: CustomAgentDraft | null;
  onClose: () => void;
  onSaved: (slug: string, name: string) => void;
}

const EMPTY: CustomAgentDraft = { name: '', tools: [], model: '', body: '' };

// Author a custom cicy agent (persona + tools + model) → POST /api/custom-agents
// → ~/cicy-ai/agents/<slug>/AGENT.md. Layered above CreateAgentDialog.
export default function CustomAgentEditor({ open, toolGroups, initial, onClose, onSaved }: Props) {
  const { t } = useTranslation('createAgent');
  const { t: tc } = useTranslation('common');
  const [draft, setDraft] = useState<CustomAgentDraft>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open) return;
    setDraft(initial ? { ...initial } : EMPTY);
    setError('');
    setSaving(false);
  }, [open, initial]);

  const canSubmit = draft.name.trim().length > 0 && !saving;
  useDevRegister('CustomAgentEditor', { open, draft, saving, canSubmit });

  if (!open) return null;

  const set = (patch: Partial<CustomAgentDraft>) => setDraft((p) => ({ ...p, ...patch }));
  const toggleTool = (g: string) =>
    setDraft((p) => ({ ...p, tools: p.tools.includes(g) ? p.tools.filter((x) => x !== g) : [...p.tools, g] }));

  const toolLabel = (g: string) => {
    const label = t(`toolGroups.${g}`, { defaultValue: '' });
    return label || g;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!draft.name.trim()) { setError(t('editorNameRequired')); return; }
    setSaving(true);
    setError('');
    try {
      const { data } = await apiService.saveCustomAgent({
        name: draft.name.trim(),
        tools: draft.tools,
        model: draft.model.trim(),
        body: draft.body,
      });
      const slug = data?.agent?.slug || draft.name.trim();
      const name = data?.agent?.name || draft.name.trim();
      onSaved(slug, name);
    } catch {
      setError(t('editorSaveFailed'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-id="custom-agent-editor-overlay" className="fixed inset-0 z-[100010] flex items-center justify-center cursor-pointer" onClick={(e) => { e.stopPropagation(); if (!saving) onClose(); }}>
      <div data-id="custom-agent-editor-backdrop" className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
      <form
        data-id="custom-agent-editor"
        className="relative w-[560px] max-w-[92vw] cursor-default overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
        onSubmit={handleSubmit}
      >
        <div data-id="custom-agent-editor-header" className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <h2 data-id="custom-agent-editor-title" className="text-[15px] font-semibold text-white">{t('editorTitle')}</h2>
          <button
            data-id="custom-agent-editor-close"
            type="button"
            onClick={onClose}
            disabled={saving}
            className="cursor-pointer rounded-lg p-1.5 text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-50"
            title={tc('close')}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div data-id="custom-agent-editor-body" className="max-h-[70vh] space-y-4 overflow-auto px-5 py-5">
          <div data-id="custom-agent-editor-name-field">
            <label data-id="custom-agent-editor-name-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('editorNameLabel')}</label>
            <input
              data-id="custom-agent-editor-name-input"
              autoFocus
              type="text"
              value={draft.name}
              onChange={(e) => set({ name: e.target.value })}
              placeholder={t('editorNamePlaceholder')}
              className="w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 outline-none transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20"
            />
          </div>

          <div data-id="custom-agent-editor-persona-field">
            <label data-id="custom-agent-editor-persona-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('editorPersonaLabel')}</label>
            <textarea
              data-id="custom-agent-editor-persona-input"
              rows={7}
              value={draft.body}
              onChange={(e) => set({ body: e.target.value })}
              placeholder={t('editorPersonaPlaceholder')}
              className="w-full resize-y rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm leading-6 text-zinc-200 outline-none transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20"
            />
          </div>

          <div data-id="custom-agent-editor-tools-field">
            <label data-id="custom-agent-editor-tools-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('editorToolsLabel')}</label>
            <div data-id="custom-agent-editor-tools-options" className="flex flex-wrap gap-2">
              {toolGroups.map((g) => {
                const on = draft.tools.includes(g);
                return (
                  <button
                    data-id={`custom-agent-editor-tool-${g}`}
                    key={g}
                    type="button"
                    onClick={() => toggleTool(g)}
                    className={`flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12px] transition-all ${on ? 'border-blue-500/40 bg-blue-500/15 text-blue-200' : 'border-white/[0.08] bg-white/[0.03] text-zinc-400 hover:bg-white/[0.06]'}`}
                  >
                    {on ? <Check className="h-3 w-3" /> : null}
                    {toolLabel(g)}
                  </button>
                );
              })}
            </div>
          </div>

          <div data-id="custom-agent-editor-model-field">
            <label data-id="custom-agent-editor-model-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('editorModelLabel')}</label>
            <input
              data-id="custom-agent-editor-model-input"
              type="text"
              value={draft.model}
              onChange={(e) => set({ model: e.target.value })}
              placeholder={t('editorModelPlaceholder')}
              className="w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 outline-none transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20"
            />
          </div>

          {error ? <p data-id="custom-agent-editor-error" className="text-[12px] text-red-400">{error}</p> : null}
        </div>

        <div data-id="custom-agent-editor-actions" className="flex justify-end gap-2 border-t border-white/[0.06] px-5 py-3">
          <button
            data-id="custom-agent-editor-cancel"
            type="button"
            onClick={onClose}
            disabled={saving}
            className="cursor-pointer rounded-lg bg-white/[0.06] px-4 py-2 text-sm text-zinc-300 transition-all hover:bg-white/[0.1] disabled:opacity-50"
          >
            {tc('cancel')}
          </button>
          <button
            data-id="custom-agent-editor-submit"
            type="submit"
            disabled={!canSubmit}
            className="flex cursor-pointer items-center gap-2 rounded-lg bg-blue-500/20 px-4 py-2 text-sm font-medium text-blue-300 transition-all hover:bg-blue-500/25 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {saving ? <Spinner size="sm" /> : <Check className="h-4 w-4" />}
            {saving ? t('editorSaving') : t('editorSave')}
          </button>
        </div>
      </form>
    </div>
  );
}
