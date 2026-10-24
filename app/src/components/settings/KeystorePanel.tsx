// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// KeystorePanel — named secrets (key / value / note) for agents.
//
// This UI is the ONLY place a value is ever entered. The server never returns
// a value to the browser (list = key + note + has_value), so editing an
// existing key shows an empty value field: leave it empty to keep the stored
// value, type to replace it. Agents use the cicy-keystore skill, which can list
// keys + notes and inject values into a child process — never print them.

import { useCallback, useEffect, useState } from 'react';
import { Eye, EyeOff, KeyRound, Loader2, Pencil, Plus, RefreshCw, Save, Trash2, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

interface KeystoreItem {
  key: string;
  note: string;
  has_value: boolean;
  updated_at: string;
}

interface Draft {
  oldKey: string; // '' = creating
  key: string;
  value: string;
  note: string;
}

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

function errorText(cause: any): string {
  return cause?.response?.data?.detail || cause?.response?.data?.error || cause?.message || String(cause);
}

export default function KeystorePanel({ active }: { active: boolean }) {
  const { t } = useTranslation('workspace');
  const { confirm, node: dialogsNode } = useDialogs();
  const [items, setItems] = useState<KeystoreItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [draft, setDraft] = useState<Draft | null>(null);
  const [showValue, setShowValue] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const { data } = await apiService.listKeystore();
      setItems(Array.isArray(data?.keys) ? data.keys : []);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { if (active) void load(); }, [active, load]);

  const startCreate = () => { setShowValue(false); setDraft({ oldKey: '', key: '', value: '', note: '' }); };
  const startEdit = (item: KeystoreItem) => { setShowValue(false); setDraft({ oldKey: item.key, key: item.key, value: '', note: item.note }); };
  const closeDraft = () => { setDraft(null); setShowValue(false); };

  const creating = draft ? !draft.oldKey : false;
  const keyValid = draft ? KEY_RE.test(draft.key.trim()) : false;
  const canSave = !!draft && keyValid && (!creating || draft.value !== '') && !saving;

  const save = async () => {
    if (!draft || !canSave) return;
    setSaving(true);
    setError('');
    try {
      const body: { key: string; old_key?: string; value?: string; note: string } = { key: draft.key.trim(), note: draft.note };
      if (draft.oldKey) body.old_key = draft.oldKey;
      if (draft.value !== '') body.value = draft.value;
      const { data } = await apiService.saveKeystoreKey(body);
      setItems(Array.isArray(data?.keys) ? data.keys : []);
      closeDraft(); // also drops the typed value from memory
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (item: KeystoreItem) => {
    const ok = await confirm({
      title: t('keystoreDeleteTitle', { defaultValue: '删除密钥' }),
      body: t('keystoreDeleteBody', { key: item.key, defaultValue: `确定删除 ${item.key}？使用它的命令将无法再获取这个值。` }),
      danger: true,
      confirmLabel: t('keystoreDelete', { defaultValue: '删除' }),
    });
    if (!ok) return;
    try {
      const { data } = await apiService.deleteKeystoreKey(item.key);
      setItems(Array.isArray(data?.keys) ? data.keys : []);
    } catch (cause) {
      setError(errorText(cause));
    }
  };

  const inputClass = 'w-full rounded-md border border-[var(--dev-border)] bg-[var(--dev-surface-2)] px-3 py-2 text-[12px] text-[var(--dev-text)] outline-none placeholder:text-[var(--dev-text-3)] focus:border-[var(--dev-accent)]';
  const ghostButton = 'inline-flex items-center gap-1.5 rounded-md border border-[var(--dev-border)] px-2.5 py-1.5 text-[11px] text-[var(--dev-text-2)] transition-colors hover:bg-[var(--dev-hover)] hover:text-[var(--dev-text)]';

  return (
    <div data-id="keystore-panel" className="flex h-full flex-col overflow-hidden bg-[var(--dev-bg)]">
      <header data-id="keystore-header" className="flex shrink-0 items-start justify-between gap-3 border-b border-[var(--dev-border)] px-4 py-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-[13px] font-semibold text-[var(--dev-text)]">
            <KeyRound className="h-4 w-4 text-[var(--dev-accent)]" />
            {t('keystoreTitle', { defaultValue: '密钥库' })}
          </h2>
          <p className="mt-1 text-[11px] leading-5 text-[var(--dev-text-3)]">
            {t('keystoreSubtitle', { defaultValue: '值只能在这里填写。Agent 通过 cicy-keystore 技能只能看到 key 和说明，看不到值。' })}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button data-id="keystore-refresh" type="button" onClick={() => void load()} className={ghostButton} title={t('keystoreRefresh', { defaultValue: '刷新' })}>
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          </button>
          <button data-id="keystore-add" type="button" onClick={startCreate} className="inline-flex items-center gap-1.5 rounded-md bg-[var(--dev-accent)] px-2.5 py-1.5 text-[11px] font-medium text-white hover:opacity-90">
            <Plus className="h-3.5 w-3.5" />
            {t('keystoreAdd', { defaultValue: '新增' })}
          </button>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
        {error ? (
          <div data-id="keystore-error" role="alert" className="mb-3 rounded-md border border-[var(--dev-live)]/30 bg-[var(--dev-live-bg)] px-3 py-2 text-[12px] text-[var(--dev-live)]">{error}</div>
        ) : null}

        {draft ? (
          <form
            data-id="keystore-form"
            className="mb-4 space-y-3 rounded-lg border border-[var(--dev-border-strong)] bg-[var(--dev-surface)] p-3"
            autoComplete="off"
            onSubmit={(e) => { e.preventDefault(); void save(); }}
          >
            <div>
              <label htmlFor="keystore-form-key" className="mb-1 block text-[11px] text-[var(--dev-text-3)]">Key</label>
              <input
                id="keystore-form-key"
                data-id="keystore-form-key"
                value={draft.key}
                onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                placeholder="OPENAI_API_KEY"
                spellCheck={false}
                className={`${inputClass} font-mono`}
                autoFocus={creating}
              />
              {draft.key && !keyValid ? (
                <p className="mt-1 text-[11px] text-[var(--dev-live)]">{t('keystoreKeyInvalid', { defaultValue: '只能用字母、数字、下划线，且不能以数字开头（最长 64）' })}</p>
              ) : null}
            </div>
            <div>
              <label htmlFor="keystore-form-value" className="mb-1 block text-[11px] text-[var(--dev-text-3)]">Value</label>
              <div className="relative">
                <input
                  id="keystore-form-value"
                  data-id="keystore-form-value"
                  type={showValue ? 'text' : 'password'}
                  value={draft.value}
                  onChange={(e) => setDraft({ ...draft, value: e.target.value })}
                  placeholder={creating
                    ? t('keystoreValuePlaceholder', { defaultValue: '填写密钥值' })
                    : t('keystoreValueKeep', { defaultValue: '已设置，留空则保持不变' })}
                  autoComplete="new-password"
                  spellCheck={false}
                  className={`${inputClass} pr-10 font-mono`}
                  autoFocus={!creating}
                />
                <button
                  type="button"
                  data-id="keystore-form-value-toggle"
                  onClick={() => setShowValue((v) => !v)}
                  aria-label={showValue ? t('keystoreHideValue', { defaultValue: '隐藏' }) : t('keystoreShowValue', { defaultValue: '显示' })}
                  className="absolute inset-y-0 right-0 px-3 text-[var(--dev-text-3)] hover:text-[var(--dev-text)]"
                >
                  {showValue ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>
            <div>
              <label htmlFor="keystore-form-note" className="mb-1 block text-[11px] text-[var(--dev-text-3)]">Note</label>
              <textarea
                id="keystore-form-note"
                data-id="keystore-form-note"
                value={draft.note}
                onChange={(e) => setDraft({ ...draft, note: e.target.value })}
                rows={2}
                placeholder={t('keystoreNotePlaceholder', { defaultValue: '用途说明，Agent 会看到这段文字' })}
                className={`${inputClass} resize-y`}
              />
            </div>
            <div className="flex items-center justify-end gap-2">
              <button data-id="keystore-form-cancel" type="button" onClick={closeDraft} className={ghostButton}>
                <X className="h-3.5 w-3.5" />
                {t('keystoreCancel', { defaultValue: '取消' })}
              </button>
              <button data-id="keystore-form-save" type="submit" disabled={!canSave} className="inline-flex items-center gap-1.5 rounded-md bg-[var(--dev-accent)] px-3 py-1.5 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-40">
                {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
                {t('keystoreSave', { defaultValue: '保存' })}
              </button>
            </div>
          </form>
        ) : null}

        {items.length === 0 && !loading && !draft ? (
          <div data-id="keystore-empty" className="rounded-lg border border-dashed border-[var(--dev-border)] px-4 py-8 text-center text-[12px] text-[var(--dev-text-3)]">
            {t('keystoreEmpty', { defaultValue: '还没有密钥。点「新增」添加第一个。' })}
          </div>
        ) : null}

        <ul data-id="keystore-list" className="divide-y divide-[var(--dev-border)] overflow-hidden rounded-lg border border-[var(--dev-border)]">
          {items.map((item) => (
            <li key={item.key} data-id={`keystore-item-${item.key}`} className="group flex items-start gap-3 bg-[var(--dev-surface)] px-3 py-2.5 hover:bg-[var(--dev-hover)]">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-mono text-[12px] font-medium text-[var(--dev-text)]">{item.key}</span>
                  <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] ${item.has_value ? 'bg-[var(--dev-ok-bg)] text-[var(--dev-ok)]' : 'bg-[var(--dev-warn-bg)] text-[var(--dev-warn)]'}`}>
                    {item.has_value ? t('keystoreSet', { defaultValue: '已设置' }) : t('keystoreUnset', { defaultValue: '未设置' })}
                  </span>
                </div>
                <p className="mt-0.5 whitespace-pre-wrap break-words text-[11px] leading-5 text-[var(--dev-text-2)]">
                  {item.note || <span className="text-[var(--dev-text-3)]">{t('keystoreNoNote', { defaultValue: '无说明' })}</span>}
                </p>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <button data-id={`keystore-edit-${item.key}`} type="button" onClick={() => startEdit(item)} className="rounded p-1.5 text-[var(--dev-text-3)] hover:bg-[var(--dev-active)] hover:text-[var(--dev-text)]" aria-label={t('keystoreEdit', { defaultValue: '编辑' })}>
                  <Pencil className="h-3.5 w-3.5" />
                </button>
                <button data-id={`keystore-delete-${item.key}`} type="button" onClick={() => void remove(item)} className="rounded p-1.5 text-[var(--dev-text-3)] hover:bg-[var(--dev-active)] hover:text-[var(--dev-live)]" aria-label={t('keystoreDelete', { defaultValue: '删除' })}>
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            </li>
          ))}
        </ul>

        <div data-id="keystore-usage" className="mt-4 rounded-lg border border-[var(--dev-border)] bg-[var(--dev-surface-2)] px-3 py-2.5 text-[11px] leading-5 text-[var(--dev-text-3)]">
          <div className="mb-1 font-medium text-[var(--dev-text-2)]">{t('keystoreUsageTitle', { defaultValue: 'Agent 如何使用' })}</div>
          <code className="block font-mono text-[var(--dev-text-2)]">cicy-keystore list</code>
          <code className="block font-mono text-[var(--dev-text-2)]">cicy-keystore exec --env OPENAI_API_KEY -- &lt;command&gt;</code>
          <p className="mt-1">{t('keystoreUsageHint', { defaultValue: 'exec 把值注入子进程的环境变量，并在其输出中把值替换为 ***。' })}</p>
        </div>
      </div>
      {dialogsNode}
    </div>
  );
}
