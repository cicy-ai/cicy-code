// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState, useCallback } from 'react';
import { createPortal } from 'react-dom';
import apiService from '../../services/api';

// Global WeChat-bind modal. Opened by a WS-driven 'open-wechat-bind' window
// event (Workspace dispatches it on a `wechat_bind_request` chat-WS message),
// so the audit advisor (w-6001) can pop the QR-scan modal in the operator's
// browser — anywhere in the app — instead of printing a CLI link. Self-
// contained: reuses /api/im/wechat/login + status poll, portals to body.

interface WxState {
  sessionId: string;
  qrcodeUrl: string;
  state: string;
  detail?: string;
}

function qrImageFor(content: string, size = 220) {
  return `https://api.qrserver.com/v1/create-qr-code/?size=${size}x${size}&margin=8&data=${encodeURIComponent(content)}`;
}

export default function WeChatBindModal() {
  const [open, setOpen] = useState(false);
  const [wx, setWx] = useState<WxState | null>(null);
  const [error, setError] = useState('');

  const close = useCallback(() => {
    setOpen(false);
    setWx(null);
    setError('');
  }, []);

  const start = useCallback(async () => {
    setOpen(true);
    setWx(null);
    setError('');
    try {
      const r = await apiService.startWeChatLogin();
      const d = (r?.data || {}) as any;
      if (d.error) {
        setError(String(d.error));
        return;
      }
      setWx({
        sessionId: d.session_id || d.sessionId,
        qrcodeUrl: d.qrcode_url || d.qrcodeUrl,
        state: d.state || 'qr_wait',
        detail: d.detail,
      });
    } catch (e: any) {
      setError(e?.message || '获取二维码失败');
    }
  }, []);

  // open on the global WS-driven event
  useEffect(() => {
    const h = () => void start();
    window.addEventListener('open-wechat-bind', h);
    return () => window.removeEventListener('open-wechat-bind', h);
  }, [start]);

  // poll login status until bound / expired
  useEffect(() => {
    if (!open || !wx?.sessionId) return;
    let alive = true;
    const id = setInterval(async () => {
      try {
        const r = await apiService.getWeChatLoginStatus(wx.sessionId);
        const d = (r?.data || {}) as any;
        if (!alive) return;
        const next = d.state || wx.state;
        const accId = (d.account_id as number) || 0;
        setWx((cur) => (cur ? { ...cur, state: next, qrcodeUrl: d.qrcode_url || cur.qrcodeUrl, detail: d.detail } : cur));
        if (next === 'created' || next === 'connected' || accId > 0) {
          clearInterval(id);
          window.dispatchEvent(new CustomEvent('show-toast', { detail: '微信已绑定 — 审计告警将同时推送微信' }));
          close();
        } else if (next === 'expired' || next === 'error') {
          clearInterval(id);
        }
      } catch {
        /* transient — keep polling */
      }
    }, 2500);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [open, wx?.sessionId, wx?.state, close]);

  if (!open) return null;
  const expired = wx?.state === 'expired';
  const failed = wx?.state === 'error' || !!error;
  const scanned = wx?.state === 'scaned';
  const haveQR = !!wx?.qrcodeUrl;
  const loading = !haveQR && !failed && !expired;

  return createPortal(
    <div data-id="global-wx-bind-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={close}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div
        className="absolute left-1/2 top-1/2 w-[400px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
          <h2 className="text-[15px] font-semibold text-white">微信扫码绑定（审计告警通道）</h2>
          <button onClick={close} className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-200 hover:bg-white/[0.06]">✕</button>
        </div>
        <div className="px-5 py-5 flex flex-col items-center text-center">
          {loading && <div className="h-52 w-52 grid place-items-center rounded-lg border border-white/[0.06] bg-white/[0.02] text-zinc-500 text-sm">正在生成二维码…</div>}
          {!loading && haveQR && !expired && !failed && (
            <img src={qrImageFor(wx!.qrcodeUrl, 220)} alt="WeChat QR" className="h-52 w-52 rounded-lg bg-white p-2.5" />
          )}
          {!loading && expired && <div className="h-52 w-52 grid place-items-center rounded-lg border border-amber-500/25 bg-amber-500/[0.06] text-[12px] text-amber-300">二维码已过期，请重新生成</div>}
          {!loading && failed && <div className="h-52 w-52 grid place-items-center rounded-lg border border-red-500/25 bg-red-500/[0.06] text-[12px] text-red-300 px-3 whitespace-pre-wrap">{error || '扫码失败'}</div>}
          <div className="mt-3 text-[13px] font-medium text-zinc-200">
            {loading ? '正在生成二维码…' : scanned ? '已扫描，请在手机微信上确认' : '请用手机微信扫码，绑定后审计告警会同时推送到微信'}
          </div>
          {wx?.detail && !loading && <div className="mt-1 text-[11px] text-zinc-500">{wx.detail}</div>}
          {!loading && (expired || failed) && (
            <button onClick={() => void start()} className="mt-4 px-3 py-1.5 rounded-lg text-[12px] bg-white/[0.06] text-zinc-200 hover:bg-white/[0.1]">重新生成二维码</button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
