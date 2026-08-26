// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { Check, Eye, EyeOff, KeyRound, TriangleAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

export default function ClaudeAuthImportCard() {
  const { t } = useTranslation('workspace');
  const { confirm, node } = useDialogs();
  const [value, setValue] = useState('');
  const [shown, setShown] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ kind: 'ok' | 'error'; text: string } | null>(null);

  const restore = async () => {
    const encoded = value.trim();
    if (!encoded || saving) return;
    const accepted = await confirm({
      title: t('settingsClaudeAuthConfirmTitle', { defaultValue: '覆盖 Claude Auth？' }),
      body: t('settingsClaudeAuthConfirmBody', { defaultValue: '这会用输入内容覆盖 ~/.claude/.credentials.json。已运行的 Claude 不受影响，之后启动的 Claude 使用新凭据。' }),
      danger: true,
      confirmLabel: t('settingsClaudeAuthConfirm', { defaultValue: '确认覆盖' }),
      cancelLabel: t('settingsCancel', { defaultValue: '取消' }),
    });
    if (!accepted) return;

    setSaving(true);
    setMessage(null);
    try {
      await apiService.importClaudeAuth(encoded);
      setValue('');
      setShown(false);
      setMessage({
        kind: 'ok',
        text: t('settingsClaudeAuthSuccess', { defaultValue: '已覆盖，之后启动的 Claude 会使用新凭据。' }),
      });
    } catch (error: any) {
      setMessage({
        kind: 'error',
        text: error?.response?.data?.detail || t('settingsClaudeAuthFailed', { defaultValue: '覆盖失败，请确认输入的是有效的 Claude Auth Base64。' }),
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <section data-id="settings-claude-auth-block" className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-5">
        <div className="flex items-start gap-3">
          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-white/[0.05] text-zinc-300">
            <KeyRound className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-[13px] font-semibold text-zinc-100">
              {t('settingsClaudeAuthTitle', { defaultValue: 'Claude Auth' })}
            </div>
            <div className="mt-0.5 text-[11px] leading-5 text-zinc-500">
              {t('settingsClaudeAuthHint', { defaultValue: '粘贴 Claude Auth Base64，解码校验后覆盖 ~/.claude/.credentials.json。' })}
            </div>
            <div className="mt-3 flex items-center gap-2">
              <input
                data-id="settings-claude-auth-input"
                aria-label="Claude Auth Base64"
                type={shown ? 'text' : 'password'}
                value={value}
                onChange={(event) => { setValue(event.target.value); setMessage(null); }}
                onKeyDown={(event) => {
                  if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                  if (event.key === 'Enter' && value.trim() && !saving) void restore();
                }}
                placeholder={t('settingsClaudeAuthPlaceholder', { defaultValue: '粘贴 Base64 内容' })}
                autoComplete="off"
                spellCheck={false}
                className="min-w-0 flex-1 rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 font-mono text-[12px] text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-white/[0.18]"
              />
              <button
                data-id="settings-claude-auth-toggle"
                type="button"
                title={shown ? t('settingsHide', { defaultValue: '隐藏' }) : t('settingsShow', { defaultValue: '显示' })}
                onClick={() => setShown((current) => !current)}
                className="shrink-0 rounded-lg border border-white/[0.08] bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:text-zinc-200"
              >
                {shown ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </button>
            </div>
            <div className="mt-3 flex items-center gap-2">
              <button
                data-id="settings-claude-auth-restore"
                type="button"
                disabled={!value.trim() || saving}
                onClick={() => void restore()}
                className={`rounded-lg border px-3.5 py-2 text-[12px] font-medium transition-colors ${value.trim() && !saving ? 'border-white/[0.1] bg-white/[0.05] text-zinc-100 hover:bg-white/[0.09] hover:text-white' : 'cursor-not-allowed border-white/[0.06] bg-white/[0.02] text-zinc-600'}`}
              >
                {saving ? t('settingsClaudeAuthRestoring', { defaultValue: '覆盖中…' }) : t('settingsClaudeAuthRestore', { defaultValue: '还原并覆盖' })}
              </button>
              {message ? (
                <span data-id="settings-claude-auth-message" className={`flex items-center gap-1 text-[11px] ${message.kind === 'ok' ? 'text-emerald-400' : 'text-rose-400'}`}>
                  {message.kind === 'ok' ? <Check className="h-3.5 w-3.5" /> : <TriangleAlert className="h-3.5 w-3.5" />}
                  {message.text}
                </span>
              ) : null}
            </div>
          </div>
        </div>
      </section>
      {node}
    </>
  );
}

