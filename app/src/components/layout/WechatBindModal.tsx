// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { MessageCircle, X, ExternalLink } from 'lucide-react';
import i18n from '../../i18n';
import apiService from '../../services/api';

// WechatBindModal — bind/unbind a WeChat IM account to ONE worker, opened from
// the team-panel worker ⋯ menu. It is a thin front over the existing IM
// accounts API (`/api/im/accounts/:id/bind`), which enforces one WeChat per
// pane (409) — the UI mirrors that rule by disabling rows instead of letting
// the request bounce. Account creation (QR login) stays in Settings → IM; the
// footer deep-links there via the `cicy:open-settings` window event.

interface WxAccount {
  id: number;
  platform: string;
  name: string;
  state: string;
  state_detail?: string;
  enabled: boolean;
  bound_pane_id: string;
  bound_pane_title: string;
}

const t = (key: string, defaultValue: string, opts: Record<string, unknown> = {}) =>
  i18n.t(key, { ns: 'teamPanel', defaultValue, ...opts });

export default function WechatBindModal({ paneId, title, onClose }: {
  paneId: string;
  title: string;
  onClose: () => void;
}) {
  const [accounts, setAccounts] = useState<WxAccount[] | null>(null); // null = loading
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await apiService.getIMAccounts();
      const all = (res?.data?.accounts || []) as WxAccount[];
      setAccounts(all.filter((a) => a.platform === 'wechat'));
      setError('');
    } catch (e: any) {
      setAccounts([]);
      setError(String(e?.message || e));
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id);
    setError('');
    try {
      await fn();
      await load();
    } catch (e: any) {
      setError(String(e?.response?.data?.error || e?.message || e));
    } finally {
      setBusyId(null);
    }
  };

  const openIMSettings = () => {
    onClose();
    window.dispatchEvent(new CustomEvent('cicy:open-settings', { detail: { section: 'im' } }));
  };

  const boundHere = (accounts || []).find((a) => a.bound_pane_id === paneId);

  return (
    <div
      data-id="team-panel-wechat-bind-overlay"
      className="fixed inset-0 z-[9998] flex items-center justify-center bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        data-id="team-panel-wechat-bind-modal"
        className="flex max-h-[70vh] w-[min(460px,92vw)] flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#111113] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-white/[0.06] px-4 py-3">
          <MessageCircle className="h-4 w-4 shrink-0 text-emerald-400" />
          <h3 className="min-w-0 flex-1 truncate text-[14px] font-semibold text-white">
            {t('wechatBindTitle', '绑定微信')} · {title || paneId}
          </h3>
          <button
            type="button"
            data-id="team-panel-wechat-bind-close"
            onClick={onClose}
            className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div data-id="team-panel-wechat-bind-list" className="flex-1 overflow-auto p-2">
          {accounts === null ? (
            <div className="px-3 py-8 text-center text-[12px] text-zinc-600">{t('wechatBindLoading', '加载微信账号…')}</div>
          ) : accounts.length === 0 ? (
            <div className="px-3 py-6 text-center">
              <div className="mb-3 text-[12px] leading-5 text-zinc-500">{t('wechatBindEmpty', '还没有登录过微信账号。先去 IM 设置扫码登录,回来就能绑定。')}</div>
              <button
                type="button"
                data-id="team-panel-wechat-bind-goto-im"
                onClick={openIMSettings}
                className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500/15 px-3 py-1.5 text-[12px] text-emerald-300 transition-colors hover:bg-emerald-500/25"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t('wechatBindGotoIM', '去扫码登录')}
              </button>
            </div>
          ) : (
            accounts.map((a) => {
              const isHere = a.bound_pane_id === paneId;
              const isElsewhere = !!a.bound_pane_id && !isHere;
              // one wechat per pane (backend 409s a double-bind), so a rebind
              // is a sequence: unbind whatever is in the way, then bind. Both
              // "this account is bound elsewhere" and "this pane already has
              // another account" reduce to that — the button stays clickable
              // and just says 换绑 instead of 绑定.
              const isRebind = isElsewhere || (!!boundHere && boundHere.id !== a.id);
              const busy = busyId === a.id;
              return (
                <div
                  key={a.id}
                  data-id={`team-panel-wechat-bind-row-${a.id}`}
                  className="mb-1 flex items-center gap-3 rounded-xl border border-white/[0.05] bg-white/[0.02] px-3 py-2.5"
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] text-zinc-200">{a.name || `wechat #${a.id}`}</div>
                    <div className="truncate text-[11px] text-zinc-600">
                      {isHere
                        ? t('wechatBoundHere', '已绑定本 agent')
                        : isElsewhere
                          ? t('wechatBoundElsewhere', '已绑定 {{who}}', { who: a.bound_pane_title || a.bound_pane_id })
                          : a.state === 'ok' || a.enabled
                            ? t('wechatFree', '空闲,可绑定')
                            : (a.state_detail || a.state || t('wechatFree', '空闲,可绑定'))}
                    </div>
                  </div>
                  {isHere ? (
                    <button
                      type="button"
                      data-id={`team-panel-wechat-unbind-${a.id}`}
                      disabled={busy}
                      onClick={() => act(a.id, () => apiService.unbindIMAccount(a.id))}
                      className="rounded-lg bg-red-500/10 px-3 py-1.5 text-[12px] text-red-300 transition-colors hover:bg-red-500/20 disabled:opacity-50"
                    >
                      {busy ? '…' : t('wechatUnbind', '解绑')}
                    </button>
                  ) : (
                    <button
                      type="button"
                      data-id={`team-panel-wechat-bind-${a.id}`}
                      disabled={busy}
                      title={isElsewhere
                        ? t('wechatRebindFromTip', '会先把它从 {{who}} 解绑,再绑到本 agent', { who: a.bound_pane_title || a.bound_pane_id })
                        : boundHere && boundHere.id !== a.id
                          ? t('wechatRebindSwapTip', '会先解绑本 agent 当前的微信({{who}}),再绑定这个', { who: boundHere.name || `#${boundHere.id}` })
                          : undefined}
                      onClick={() => act(a.id, async () => {
                        // clear both possible conflicts before binding: this
                        // account's old pane, and this pane's old account.
                        if (a.bound_pane_id && a.bound_pane_id !== paneId) await apiService.unbindIMAccount(a.id);
                        if (boundHere && boundHere.id !== a.id) await apiService.unbindIMAccount(boundHere.id);
                        await apiService.bindIMAccount(a.id, paneId);
                      })}
                      className={`rounded-lg px-3 py-1.5 text-[12px] transition-colors ${
                        busy
                          ? 'cursor-not-allowed bg-white/[0.04] text-zinc-600'
                          : isRebind
                            ? 'bg-amber-500/15 text-amber-300 hover:bg-amber-500/25'
                            : 'bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25'
                      }`}
                    >
                      {busy ? '…' : isRebind ? t('wechatRebind', '换绑到此') : t('wechatBind', '绑定')}
                    </button>
                  )}
                </div>
              );
            })
          )}
          {error ? (
            <div data-id="team-panel-wechat-bind-error" className="mx-1 mt-1 rounded-lg bg-red-500/10 px-3 py-2 text-[12px] text-red-300">{error}</div>
          ) : null}
        </div>

        <div className="border-t border-white/[0.06] px-4 py-2.5">
          <button
            type="button"
            data-id="team-panel-wechat-bind-manage"
            onClick={openIMSettings}
            className="inline-flex items-center gap-1.5 text-[12px] text-zinc-500 transition-colors hover:text-zinc-300"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            {t('wechatManage', '管理微信账号(扫码登录/删除)→ IM 设置')}
          </button>
        </div>
      </div>
    </div>
  );
}
