// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { MessageCircle, Zap, X, ExternalLink } from 'lucide-react';
import i18n from '../../i18n';
import apiService from '../../services/api';

// WechatBindModal — bind/unbind an IM account (WeChat or Feishu) to ONE worker,
// opened from the team-panel worker ⋯ menu. It is a thin front over the existing
// IM accounts API (`/api/im/accounts/:id/bind`), which enforces one account per
// pane per platform (409) — the UI mirrors that rule by disabling rows instead
// of letting the request bounce. Account creation (WeChat QR login / Feishu
// App ID+Secret) stays in Settings → IM; the footer deep-links there via the
// `cicy:open-settings` window event.

interface IMAccountRow {
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

// 平台文案/图标:微信绿、飞书紫;飞书额外提示「会话级 /bind」玩法。
const PLATFORM_COPY = {
  wechat: {
    title: () => t('wechatBindTitle', '绑定微信'),
    loading: () => t('wechatBindLoading', '加载微信账号…'),
    empty: () => t('wechatBindEmpty', '还没有登录过微信账号。先去 IM 设置扫码登录,回来就能绑定。'),
    goto: () => t('wechatBindGotoIM', '去扫码登录'),
    manage: () => t('wechatManage', '管理微信账号(扫码登录/删除)→ IM 设置'),
    hint: () => '',
    Icon: MessageCircle,
    iconCls: 'text-emerald-400',
  },
  feishu: {
    title: () => t('feishuBindTitle', '绑定飞书'),
    loading: () => t('feishuBindLoading', '加载飞书应用…'),
    empty: () => t('feishuBindEmpty', '还没有添加飞书应用。先去 IM 设置填 App ID/App Secret(有配置向导),回来就能绑定。'),
    goto: () => t('feishuBindGotoIM', '去添加飞书应用'),
    manage: () => t('feishuManage', '管理飞书应用(添加/配置向导)→ IM 设置'),
    hint: () => t('feishuBindHint', '提示:也可以不占账号——在飞书任意会话里对机器人发 /bind 编号,按会话绑定,一个机器人服务所有 agent。'),
    Icon: Zap,
    iconCls: 'text-indigo-400',
  },
} as const;

export default function WechatBindModal({ paneId, title, onClose, platform = 'wechat' }: {
  paneId: string;
  title: string;
  onClose: () => void;
  platform?: 'wechat' | 'feishu';
}) {
  const C = PLATFORM_COPY[platform];
  const [accounts, setAccounts] = useState<IMAccountRow[] | null>(null); // null = loading
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await apiService.getIMAccounts();
      const all = (res?.data?.accounts || []) as IMAccountRow[];
      setAccounts(all.filter((a) => a.platform === platform));
      setError('');
    } catch (e: any) {
      setAccounts([]);
      setError(String(e?.message || e));
    }
  }, [platform]);
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
      data-id={`team-panel-${platform}-bind-overlay`}
      className="fixed inset-0 z-[9998] flex items-center justify-center bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        data-id={`team-panel-${platform}-bind-modal`}
        className="flex max-h-[70vh] w-[min(460px,92vw)] flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#111113] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-white/[0.06] px-4 py-3">
          <C.Icon className={`h-4 w-4 shrink-0 ${C.iconCls}`} />
          <h3 className="min-w-0 flex-1 truncate text-[14px] font-semibold text-white">
            {C.title()} · {title || paneId}
          </h3>
          <button
            type="button"
            data-id={`team-panel-${platform}-bind-close`}
            onClick={onClose}
            className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div data-id={`team-panel-${platform}-bind-list`} className="flex-1 overflow-auto p-2">
          {accounts === null ? (
            <div className="px-3 py-8 text-center text-[12px] text-zinc-600">{C.loading()}</div>
          ) : accounts.length === 0 ? (
            <div className="px-3 py-6 text-center">
              <div className="mb-3 text-[12px] leading-5 text-zinc-500">{C.empty()}</div>
              <button
                type="button"
                data-id={`team-panel-${platform}-bind-goto-im`}
                onClick={openIMSettings}
                className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500/15 px-3 py-1.5 text-[12px] text-emerald-300 transition-colors hover:bg-emerald-500/25"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {C.goto()}
              </button>
            </div>
          ) : (
            accounts.map((a) => {
              const isHere = a.bound_pane_id === paneId;
              const isElsewhere = !!a.bound_pane_id && !isHere;
              // one account per pane per platform (backend 409s a double-bind),
              // so a rebind is a sequence: unbind whatever is in the way, then
              // bind. Both "this account is bound elsewhere" and "this pane
              // already has another account" reduce to that — the button stays
              // clickable and just says 换绑 instead of 绑定.
              const isRebind = isElsewhere || (!!boundHere && boundHere.id !== a.id);
              const busy = busyId === a.id;
              return (
                <div
                  key={a.id}
                  data-id={`team-panel-${platform}-bind-row-${a.id}`}
                  className="mb-1 flex items-center gap-3 rounded-xl border border-white/[0.05] bg-white/[0.02] px-3 py-2.5"
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] text-zinc-200">{a.name || `${platform} #${a.id}`}</div>
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
                      data-id={`team-panel-${platform}-unbind-${a.id}`}
                      disabled={busy}
                      onClick={() => act(a.id, () => apiService.unbindIMAccount(a.id))}
                      className="rounded-lg bg-red-500/10 px-3 py-1.5 text-[12px] text-red-300 transition-colors hover:bg-red-500/20 disabled:opacity-50"
                    >
                      {busy ? '…' : t('wechatUnbind', '解绑')}
                    </button>
                  ) : (
                    <button
                      type="button"
                      data-id={`team-panel-${platform}-bind-${a.id}`}
                      disabled={busy}
                      title={isElsewhere
                        ? t('wechatRebindFromTip', '会先把它从 {{who}} 解绑,再绑到本 agent', { who: a.bound_pane_title || a.bound_pane_id })
                        : boundHere && boundHere.id !== a.id
                          ? t('wechatRebindSwapTip', '会先解绑本 agent 当前绑定的({{who}}),再绑定这个', { who: boundHere.name || `#${boundHere.id}` })
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
            <div data-id={`team-panel-${platform}-bind-error`} className="mx-1 mt-1 rounded-lg bg-red-500/10 px-3 py-2 text-[12px] text-red-300">{error}</div>
          ) : null}
        </div>

        <div className="border-t border-white/[0.06] px-4 py-2.5 space-y-1.5">
          {C.hint() ? <div className="text-[11px] leading-4 text-zinc-600">{C.hint()}</div> : null}
          <button
            type="button"
            data-id={`team-panel-${platform}-bind-manage`}
            onClick={openIMSettings}
            className="inline-flex items-center gap-1.5 text-[12px] text-zinc-500 transition-colors hover:text-zinc-300"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            {C.manage()}
          </button>
        </div>
      </div>
    </div>
  );
}
