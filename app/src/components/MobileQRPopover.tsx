import { useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Smartphone, X, Copy, Check, Monitor } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';

import { TokenManager } from '../services/tokenManager';
import { useApp } from '../contexts/AppContext';
import { cn } from '../lib/utils';

type Props = {
  /** Reserved for future use (currently unused now that the QR is a plain URL). */
  workspaceTitle?: string;
};

// Top-of-activity-bar button that pops a centered modal showing a QR code
// any cicy-* client can act on:
//
//   - cicy-mobile: parses the URL, extracts ?token=, joins as a team
//   - any browser: opens the URL, TokenManager auto-saves the token from the
//     query string and signs the user in
//
// Hidden entirely when the operator hasn't set CICY_PUBLIC_URL — that env
// signals "this server is reachable from elsewhere".
export default function MobileQRPopover(_props: Props) {
  const { t } = useTranslation('workspace');
  const { globalVar } = useApp();
  const publicUrl: string = (globalVar?.public_url || '').trim();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  // In cicy-desktop's Electron shell window.cicy is the preload bridge.
  // "Open in CiCy Desktop" is pointless when the user is already there.
  const isElectron = typeof (window as any).cicy !== 'undefined';

  const payload = useMemo(() => {
    if (!publicUrl) return '';
    const token = TokenManager.getToken() || '';
    const params = new URLSearchParams();
    if (token) params.set('token', token);
    params.set('flag', 'addTeam');
    const sep = publicUrl.includes('?') ? '&' : '?';
    return `${publicUrl}${sep}${params.toString()}`;
  }, [publicUrl]);

  // Same payload reformatted as a `cicy://addTeam?...` deep link so cicy-desktop's
  // OS-level protocol handler can pick it up.
  const desktopDeepLink = useMemo(() => {
    if (!publicUrl) return '';
    const token = TokenManager.getToken() || '';
    const params = new URLSearchParams();
    params.set('url', publicUrl);
    if (token) params.set('token', token);
    return `cicy://addTeam?${params.toString()}`;
  }, [publicUrl]);

  // Esc closes the modal.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open]);

  if (!publicUrl) return null;

  async function copyPayload() {
    let ok = false;
    // Modern path — works on https origins. http origins block clipboard
    // access in most browsers, so we fall back to the deprecated execCommand
    // trick which works everywhere.
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(payload);
        ok = true;
      }
    } catch {
      ok = false;
    }
    if (!ok) {
      try {
        const ta = document.createElement('textarea');
        ta.value = payload;
        ta.setAttribute('readonly', '');
        ta.style.position = 'fixed';
        ta.style.top = '-1000px';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        ok = document.execCommand('copy');
        document.body.removeChild(ta);
      } catch {
        ok = false;
      }
    }
    if (ok) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }



  return (
    <>
      <button
        type="button"
        data-id="mobile-qr-trigger"
        onClick={() => setOpen(true)}
        title={t('mobileQrTitle')}
        className={cn(
          'group flex h-10 w-10 items-center justify-center rounded-xl border border-white/[0.06] bg-white/[0.02] text-zinc-400 transition-all hover:border-white/[0.14] hover:text-zinc-100 cursor-pointer',
          open && 'border-white/[0.14] text-zinc-100 shadow-[0_12px_30px_rgba(0,0,0,0.28)]',
        )}
      >
        <Smartphone className="h-4 w-4" />
      </button>

      {open
        ? createPortal(
            <div
              data-id="mobile-qr-modal-root"
              role="dialog"
              aria-label={t('mobileQrTitle')}
              className="fixed inset-0 z-[10000] flex items-center justify-center"
            >
              {/* Backdrop — tap to close */}
              <div
                data-id="mobile-qr-backdrop"
                className="absolute inset-0 bg-black/60 backdrop-blur-sm"
                onClick={() => setOpen(false)}
              />
              {/* Modal panel */}
              <div
                data-id="mobile-qr-modal"
                className="relative w-[360px] max-w-[92vw] overflow-hidden rounded-2xl border border-white/[0.08] bg-gradient-to-b from-[#15151a] to-[#0e0e12] shadow-[0_30px_80px_-20px_rgba(0,0,0,0.6)]"
                onClick={(e) => e.stopPropagation()}
              >
                <div className="flex items-center justify-between gap-2 border-b border-white/[0.06] px-5 py-4">
                  <div data-id="mobile-qr-title" className="text-base font-semibold text-zinc-100">
                    {t('mobileQrTitle')}
                  </div>
                  <button
                    type="button"
                    data-id="mobile-qr-close"
                    onClick={() => setOpen(false)}
                    className="rounded-md p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 cursor-pointer"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>

                <div className="px-5 pb-5 pt-4">
                  {/* Hint above the QR — clarifies WHO can scan this. */}
                  <div data-id="mobile-qr-hint" className="mb-3 text-center text-[12px] leading-relaxed text-zinc-500">
                    {t('mobileQrScanHint')}
                  </div>

                  <div
                    data-id="mobile-qr-canvas"
                    className="relative flex aspect-square w-full items-center justify-center rounded-xl bg-white p-3 shadow-[0_2px_12px_rgba(0,0,0,0.35)] ring-1 ring-black/5"
                  >
                    <QRCodeSVG value={payload} size={260} level="M" includeMargin={false} />
                  </div>

                  {/* Divider only shown when the desktop button is visible. */}
                  {!isElectron && (
                  <div
                    data-id="mobile-qr-divider"
                    className="my-5 flex items-center gap-3 text-[10px] uppercase tracking-[0.18em] text-zinc-600"
                  >
                    <span className="h-px flex-1 bg-gradient-to-r from-transparent to-white/[0.08]" />
                    <span>{t('mobileQrOr')}</span>
                    <span className="h-px flex-1 bg-gradient-to-l from-transparent to-white/[0.08]" />
                  </div>
                  )}

                  <div className="flex flex-col gap-2">
                    {!isElectron && (
                    <a
                      href={desktopDeepLink}
                      target="_blank"
                      rel="noopener noreferrer"
                      data-id="mobile-qr-open-desktop"
                      className="group flex w-full items-center justify-center gap-2 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2.5 text-sm text-zinc-200 transition-colors hover:border-white/[0.18] hover:bg-white/[0.08] hover:text-zinc-50 no-underline"
                    >
                      <Monitor className="h-4 w-4 text-zinc-400 transition-colors group-hover:text-zinc-200" />
                      {t('mobileQrOpenDesktop')}
                    </a>
                    )}
                    <button
                      type="button"
                      data-id="mobile-qr-copy"
                      onClick={copyPayload}
                      className={cn(
                        'flex w-full items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-xs transition-all cursor-pointer',
                        copied
                          ? 'border-emerald-500/40 bg-emerald-500/[0.10] text-emerald-200'
                          : 'border-white/[0.06] bg-transparent text-zinc-400 hover:border-white/[0.12] hover:bg-white/[0.04] hover:text-zinc-200',
                      )}
                    >
                      {copied ? (
                        <>
                          <Check className="h-3.5 w-3.5" /> {t('mobileQrCopied')}
                        </>
                      ) : (
                        <>
                          <Copy className="h-3.5 w-3.5" /> {t('mobileQrCopyLink')}
                        </>
                      )}
                    </button>
                  </div>
                </div>
              </div>
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
