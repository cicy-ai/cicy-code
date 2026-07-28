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
  config?: { app_id?: string; last_feishu_open_id?: string };
}

interface FeishuChatBinding {
  account_id: number;
  account_name: string;
  chat_id: string;
  chat_name: string;
  binding_type: 'direct' | 'group';
  pane_id: string;
}

interface FeishuAccountBinding {
  account_id: number;
  chat_id: string;
  chat_name: string;
  binding_type: 'direct' | 'group';
  pane_id: string;
  pane_title: string;
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
    title: () => t('feishuBindTitle', '飞书会话'),
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
  const [loadError, setLoadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [busyId, setBusyId] = useState<number | null>(null);
  const [chatBindings, setChatBindings] = useState<FeishuChatBinding[] | null>(platform === 'feishu' ? null : []);
  const [accountBindings, setAccountBindings] = useState<FeishuAccountBinding[]>([]);

  const load = useCallback(async () => {
    try {
      const [res, bindingsRes] = await Promise.all([
        apiService.getIMAccounts(),
        platform === 'feishu' ? apiService.getFeishuChatBindings(paneId) : Promise.resolve(null),
      ]);
      const all = (res?.data?.accounts || []) as IMAccountRow[];
      setAccounts(all.filter((a) => a.platform === platform));
      if (platform === 'feishu') {
        setChatBindings((bindingsRes?.data?.bindings || []) as FeishuChatBinding[]);
        setAccountBindings((bindingsRes?.data?.account_bindings || []) as FeishuAccountBinding[]);
      }
      setLoadError('');
    } catch (e: any) {
      setAccounts([]);
      setLoadError(String(e?.message || e));
    }
  }, [paneId, platform]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (platform !== 'feishu' || (chatBindings || []).length > 0) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
  }, [chatBindings, load, platform]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id);
    setActionError('');
    try {
      await fn();
      setActionError('');
      await load();
    } catch (e: any) {
      setActionError(String(e?.response?.data?.detail || e?.response?.data?.error || e?.message || e));
    } finally {
      setBusyId(null);
    }
  };

  const openIMSettings = () => {
    onClose();
    window.dispatchEvent(new CustomEvent('cicy:open-settings', { detail: { section: 'im' } }));
  };

  const boundHere = (accounts || []).find((a) => a.bound_pane_id === paneId);
  const error = actionError || loadError;

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
          ) : platform === 'feishu' ? (
            <div data-id="team-panel-feishu-chat-bindings" className="space-y-2">
              {(chatBindings || []).length > 0 ? (
                <>
                  <div className="px-2 pb-1 text-[11px] text-zinc-500">
                    {t('feishuCurrentChats', '当前 Agent 已绑定的飞书会话')}
                  </div>
                  {(chatBindings || []).map((binding) => (
                    <div
                      key={`${binding.account_id}:${binding.chat_id}`}
                      data-id={`team-panel-feishu-chat-${binding.chat_id}`}
                      className="flex items-center gap-3 rounded-xl border border-emerald-500/15 bg-emerald-500/[0.04] px-3 py-2.5"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px] text-zinc-200">
                          <span
                            data-id={`team-panel-feishu-chat-type-${binding.chat_id}`}
                            className={`mr-1.5 inline-flex rounded px-1.5 py-0.5 text-[10px] ${
                              binding.binding_type === 'direct'
                                ? 'bg-blue-500/15 text-blue-300'
                                : 'bg-emerald-500/15 text-emerald-300'
                            }`}
                          >
                            {binding.binding_type === 'direct'
                              ? t('feishuDirectChatBadge', 'Bot 私聊')
                              : t('feishuGroupChatBadge', 'Agent 群聊')}
                          </span>
                          {binding.chat_name || `${title || paneId} · ${paneId}`}
                        </div>
                        <div className="truncate text-[11px] text-zinc-600">
                          {binding.account_name} · {binding.chat_id}
                        </div>
                      </div>
                      <button
                        type="button"
                        data-id={`team-panel-feishu-chat-unbind-${binding.chat_id}`}
                        disabled={busyId === binding.account_id}
                        onClick={() => act(binding.account_id, () => apiService.unbindFeishuChat(binding.account_id, binding.chat_id, paneId))}
                        className="rounded-lg bg-red-500/10 px-3 py-1.5 text-[12px] text-red-300 transition-colors hover:bg-red-500/20 disabled:opacity-50"
                      >
                        {busyId === binding.account_id ? '…' : t('wechatUnbind', '解绑')}
                      </button>
                    </div>
                  ))}
                </>
              ) : (
                <>
                  <div className="px-2 pb-1 text-[11px] leading-5 text-zinc-500">
                    {t('feishuNoChatBound', '当前 Agent 还没有绑定飞书会话。选择一个飞书应用，自动创建独立群聊并绑定。')}
                  </div>
                  {accounts.map((a) => {
                    const busy = busyId === a.id;
                    const appBindings = accountBindings.filter((binding) => binding.account_id === a.id);
                    const directBinding = appBindings.find((binding) => binding.binding_type === 'direct');
                    const groupBindings = appBindings.filter((binding) => binding.binding_type === 'group');
                    return (
                      <div
                        key={a.id}
                        data-id={`team-panel-feishu-create-chat-row-${a.id}`}
                        className="flex items-center gap-3 rounded-xl border border-white/[0.05] bg-white/[0.02] px-3 py-2.5"
                      >
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-[13px] text-zinc-200">{a.name || `feishu #${a.id}`}</div>
                          <div className="truncate text-[11px] text-zinc-600">
                            {t('feishuChatNamePreview', '将创建：{{name}} · {{id}}', { name: title || paneId, id: paneId })}
                          </div>
                          <div
                            data-id={`team-panel-feishu-app-binding-status-${a.id}`}
                            className="mt-1 flex flex-wrap gap-1 text-[10px]"
                          >
                            {appBindings.length === 0 ? (
                              <span className="rounded bg-zinc-500/10 px-1.5 py-0.5 text-zinc-500">
                                {t('feishuAppUnbound', '未绑定')}
                              </span>
                            ) : (
                              <>
                                {directBinding ? (
                                  <span className="rounded bg-blue-500/15 px-1.5 py-0.5 text-blue-300">
                                    {t('feishuDirectBoundTo', 'Bot 私聊：{{agent}}', {
                                      agent: directBinding.pane_title || directBinding.pane_id,
                                    })}
                                  </span>
                                ) : null}
                                {groupBindings.length > 0 ? (
                                  <span
                                    title={groupBindings.map((binding) => binding.pane_title || binding.pane_id).join('、')}
                                    className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-emerald-300"
                                  >
                                    {t('feishuGroupsBoundCount', 'Agent 群聊：{{count}} 个', { count: groupBindings.length })}
                                  </span>
                                ) : null}
                              </>
                            )}
                          </div>
                          {a.config?.app_id ? (
                            <div data-id={`team-panel-feishu-auth-links-${a.id}`} className="mt-1.5 flex flex-wrap gap-2 text-[10px]">
                              <a
                                data-id={`team-panel-feishu-bot-auth-link-${a.id}`}
                                href={`https://open.feishu.cn/app/${a.config.app_id}/auth?q=im:message,im:message.p2p_msg:readonly,im:resource&op_from=openapi&token_type=tenant`}
                                target="_blank"
                                rel="noreferrer"
                                className="text-blue-300 underline decoration-blue-300/40 hover:text-blue-200"
                              >
                                {t('feishuBotAuthLink', 'Bot 权限')}
                              </a>
                              <a
                                data-id={`team-panel-feishu-chat-auth-link-${a.id}`}
                                href={`https://open.feishu.cn/app/${a.config.app_id}/auth?q=im:chat:create,im:message.group_msg,im:resource&op_from=openapi&token_type=tenant`}
                                target="_blank"
                                rel="noreferrer"
                                className="text-emerald-300 underline decoration-emerald-300/40 hover:text-emerald-200"
                              >
                                {t('feishuChatAuthLink', '会话权限')}
                              </a>
                            </div>
                          ) : null}
                        </div>
                        <div data-id={`team-panel-feishu-bind-actions-${a.id}`} className="flex shrink-0 flex-col gap-1.5">
                          <button
                            type="button"
                            data-id={`team-panel-feishu-bind-direct-${a.id}`}
                            disabled={busy || !a.enabled}
                            onClick={() => act(a.id, () => apiService.createFeishuChat(a.id, paneId, 'direct'))}
                            className="rounded-lg bg-blue-500/15 px-3 py-1.5 text-[12px] text-blue-300 transition-colors hover:bg-blue-500/25 disabled:opacity-50"
                          >
                            {busy ? '…' : t('feishuBindDirectChat', '绑定 Bot 私聊')}
                          </button>
                          <button
                            type="button"
                            data-id={`team-panel-feishu-create-chat-${a.id}`}
                            disabled={busy || !a.enabled}
                            onClick={() => act(a.id, () => apiService.createFeishuChat(a.id, paneId, 'group'))}
                            className="rounded-lg bg-emerald-500/15 px-3 py-1.5 text-[12px] text-emerald-300 transition-colors hover:bg-emerald-500/25 disabled:opacity-50"
                          >
                            {busy ? '…' : t('feishuCreateAndBindChat', '新建 Agent 群聊')}
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </>
              )}
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
                    <div className="truncate text-[13px] text-zinc-200">
                      {a.name || `${platform} #${a.id}`}
                      {a.config?.app_id ? <span className="ml-1.5 font-mono text-[10.5px] text-zinc-600">…{String(a.config.app_id).slice(-6)}</span> : null}
                    </div>
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
            <div data-id={`team-panel-${platform}-bind-error`} className="relative mx-1 mt-1 rounded-lg bg-red-500/10 py-2 pl-3 pr-9 text-[12px] leading-5 text-red-300">
              <button
                type="button"
                data-id={`team-panel-${platform}-bind-error-close`}
                aria-label="关闭错误"
                onClick={() => {
                  setActionError('');
                  setLoadError('');
                }}
                className="absolute right-2 top-2 grid h-5 w-5 place-items-center rounded text-red-300/70 transition-colors hover:bg-red-500/15 hover:text-red-200"
              >
                <X className="h-3.5 w-3.5" />
              </button>
              <div data-id={`team-panel-${platform}-bind-error-content`}>
                {error.split(/(https?:\/\/\S+)/g).map((part, index) => part.startsWith('http') ? (
                  <a
                    key={`${part}-${index}`}
                    data-id={`team-panel-${platform}-bind-error-link`}
                    href={part}
                    target="_blank"
                    rel="noreferrer"
                    className="block break-all text-blue-300 underline hover:text-blue-200"
                  >
                    打开飞书授权页面
                  </a>
                ) : <span key={`${part}-${index}`}>{part}</span>)}
              </div>
            </div>
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
