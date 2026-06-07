import React, { forwardRef, useState, useRef, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Spinner } from './ui/Spinner';
import { usePointerLock } from '../lib/pointerLock';
import { WEB_FRAME_MASK_EVENT, WebFrameMaskEventDetail } from '../lib/webFrameMask';

// Inside Electron (cicy-desktop) every WebFrame renders an Electron <webview>:
// process-isolated, inspectable via window.__cicy.webviews (openDevTools /
// getContents). In plain browsers it stays a sandboxed <iframe>. The webview
// branch was originally code-server-only and got dropped wholesale by the
// 1c73ae5 checkpoint snapshot; restored here and widened to all frames.
const isElectron = navigator.userAgent.includes('Electron');

// Global cicy super object for Electron webview control
interface CicyWebview { el: HTMLElement; src: string; openDevTools: () => void; getContents: () => any; }
interface CicyGlobal { webviews: Map<string, CicyWebview>; list: () => CicyWebview[]; devTools: (src?: string) => void; }

function getCicy(): CicyGlobal {
  if (!(window as any).__cicy) {
    const wvs = new Map<string, CicyWebview>();
    (window as any).__cicy = {
      webviews: wvs,
      list: () => Array.from(wvs.values()),
      devTools: (src?: string) => {
        if (src) {
          const w = Array.from(wvs.values()).find(v => v.src.includes(src));
          if (w) w.openDevTools(); else console.log('not found:', src);
        } else {
          wvs.forEach(v => console.log(v.src));
        }
      }
    };
  }
  return (window as any).__cicy;
}

function registerWebview(el: HTMLElement) {
  const wv = el as any;
  const src = wv.src || '';
  const entry: CicyWebview = {
    el, src,
    openDevTools: () => wv.openDevTools?.(),
    getContents: () => wv.getWebContents?.()
  };
  getCicy().webviews.set(src, entry);

  const onReady = () => {
    entry.src = wv.src;
    getCicy().webviews.delete(src);
    getCicy().webviews.set(wv.src, entry);
  };
  wv.addEventListener('dom-ready', onReady);
  return () => {
    wv.removeEventListener('dom-ready', onReady);
    getCicy().webviews.delete(wv.src);
  };
}

function blurActiveEditableElement() {
  const active = document.activeElement as HTMLElement | null;
  if (!active) return;
  const tagName = active.tagName;
  const isEditable = active.isContentEditable || tagName === 'TEXTAREA' || tagName === 'INPUT';
  if (!isEditable) return;
  try {
    active.blur();
  } catch {}
}

interface WebFrameProps {
  src: string;
  className?: string;
  style?: React.CSSProperties;
  onLoad?: () => void;
  loading?: 'lazy' | 'eager';
  allowFullScreen?: boolean;
  title?: string;
}

export const WebFrame = forwardRef<HTMLIFrameElement, WebFrameProps>(
  ({ src, className, style, onLoad, loading, allowFullScreen, title }, ref) => {
    const { t } = useTranslation('ui');
    const [isLoading, setIsLoading] = useState(true);
    const [maskActive, setMaskActive] = useState(false);
    // Electron <webview> guests get NO default right-click menu — the embedder
    // must listen to the 'context-menu' event and draw its own. Browser iframes
    // keep the native menu, so this state is webview-only.
    const [ctxMenu, setCtxMenu] = useState<null | { x: number; y: number; canCopy: boolean; canPaste: boolean }>(null);
    const webviewRef = useRef<HTMLElement>(null);
    const iframeRef = useRef<HTMLIFrameElement>(null);
    const useWebview = isElectron;
    const pointerLocked = usePointerLock();
    const activeMaskKeysRef = useRef<Set<string>>(new Set());
    const handleLoad = () => {
      setIsLoading(false);
      onLoad?.();
    };

    const setWebviewRef = useCallback((node: HTMLElement | null) => {
      (webviewRef as any).current = node;
      if (!ref) return;
      if (typeof ref === 'function') {
        ref(node as any);
        return;
      }
      (ref as any).current = node;
    }, [ref]);

    const setIframeRef = useCallback((node: HTMLIFrameElement | null) => {
      iframeRef.current = node;
      if (!ref) return;
      if (typeof ref === 'function') {
        ref(node);
        return;
      }
      ref.current = node;
    }, [ref]);

    const focusEmbeddedFrame = useCallback(() => {
      if (pointerLocked || maskActive) return;
      blurActiveEditableElement();
      try {
        if (useWebview) {
          window.requestAnimationFrame(() => {
            (webviewRef.current as any)?.focus?.();
          });
          return;
        }
        window.requestAnimationFrame(() => {
          iframeRef.current?.focus();
          iframeRef.current?.contentWindow?.focus?.();
        });
      } catch {}
    }, [maskActive, pointerLocked, useWebview]);

    useEffect(() => {
      const handleMaskEvent = (event: Event) => {
        const detail = (event as CustomEvent<WebFrameMaskEventDetail>).detail;
        if (!detail?.key) return;
        if (detail.action === 'start') activeMaskKeysRef.current.add(detail.key);
        else activeMaskKeysRef.current.delete(detail.key);
        setMaskActive(activeMaskKeysRef.current.size > 0);
      };
      window.addEventListener(WEB_FRAME_MASK_EVENT, handleMaskEvent as EventListener);
      return () => window.removeEventListener(WEB_FRAME_MASK_EVENT, handleMaskEvent as EventListener);
    }, []);

    useEffect(() => {
      if (!useWebview) return;
      const wv = webviewRef.current;
      if (!wv) return;

      const onDomReady = () => {
        clearTimeout(fallback);
        setIsLoading(false);
        onLoad?.();
      };
      const onConsole = (e: any) => {
        const msg = e.message ?? '';
        console.log(`[webview:${title || 'untitled'}]`, msg);
      };
      // Right-click inside the guest → draw our own menu at the reported spot.
      // ContextMenuParams x/y arrive in WINDOW coordinates (verified on
      // Electron 41: click at webview-local (60,60) reports (60+rect.left,
      // 60+rect.top)) — convert to frame-local and clamp so the menu never
      // lands outside the (overflow-clipped) wrapper.
      const onContextMenu = (e: any) => {
        const p = e.params || {};
        const rect = (wv as HTMLElement).getBoundingClientRect();
        let x = (p.x ?? 0) - rect.left;
        let y = (p.y ?? 0) - rect.top;
        x = Math.max(0, Math.min(x, Math.max(0, rect.width - 170)));
        y = Math.max(0, Math.min(y, Math.max(0, rect.height - 150)));
        setCtxMenu({
          x,
          y,
          canCopy: !!(p.selectionText && p.selectionText.length) || !!p.editFlags?.canCopy,
          canPaste: p.editFlags ? !!p.editFlags.canPaste : true,
        });
      };
      // Fallback: hide spinner after 8s if dom-ready never fires
      const fallback = setTimeout(() => setIsLoading(false), 8000);

      wv.addEventListener('dom-ready', onDomReady);
      wv.addEventListener('console-message', onConsole);
      wv.addEventListener('context-menu', onContextMenu);
      // Suppress ERR_ABORTED from redirects
      wv.addEventListener('did-fail-load', (e: any) => {
        if (e.errorCode === -3) return; // ERR_ABORTED is normal during redirects
        console.warn(`[webview:${title}] load failed:`, e.errorCode, e.errorDescription);
      });
      const unregister = registerWebview(wv);
      return () => {
        clearTimeout(fallback);
        wv.removeEventListener('dom-ready', onDomReady);
        wv.removeEventListener('console-message', onConsole);
        wv.removeEventListener('context-menu', onContextMenu);
        unregister();
      };
    }, [useWebview, onLoad]);

    // Browser iframes only fire `load` once EVERY subresource is down —
    // megabytes of JS for app frames — while the guest is typically painting
    // (with its own splash) long before that. Cap the overlay so it never
    // sits on top of a page that's already visible. Webviews have their own
    // dom-ready + 8s fallback above.
    useEffect(() => {
      if (useWebview || !isLoading) return;
      const cap = setTimeout(() => setIsLoading(false), 2500);
      return () => clearTimeout(cap);
    }, [useWebview, isLoading, src]);

    // Navigate on src change (initial load handled by webview src attribute)
    const prevSrc = useRef(src);
    useEffect(() => {
      if (!useWebview || src === prevSrc.current) return;
      prevSrc.current = src;
      const wv = webviewRef.current as any;
      if (!wv) return;
      setIsLoading(true);
      try { wv.loadURL(src); } catch { wv.src = src; }
    }, [src, useWebview]);

    if (useWebview) {
      // <webview> is not a typed JSX intrinsic — build it via createElement,
      // same as ArtifactPanel. NOTE: unlike the pre-1c73ae5 version this does
      // NOT set nodeintegration / disablewebsecurity — every current src is a
      // local cicy-code page (ttyd / history) and none needs node in the guest.
      return (
        <>
          {isLoading && (
            <div data-id="web-frame-loading" className="absolute inset-0 flex items-center justify-center bg-vsc-bg z-10">
              <Spinner size="md" />
            </div>
          )}
          {(pointerLocked || maskActive) && <div data-id="web-frame-interaction-mask" className="absolute inset-0 z-20" />}
          {React.createElement('webview', {
            'data-id': 'web-frame-webview',
            ref: setWebviewRef,
            src,
            className,
            style,
            onPointerDownCapture: focusEmbeddedFrame,
            onMouseDownCapture: focusEmbeddedFrame,
            allowpopups: '',
            partition: 'persist:sandbox-0',
            webpreferences: 'allowRunningInsecureContent=true',
          })}
          {ctxMenu && (
            <>
              {/* backdrop: swallow the next click anywhere to dismiss */}
              <div
                data-id="web-frame-ctx-backdrop"
                className="fixed inset-0 z-30"
                onMouseDown={() => setCtxMenu(null)}
                onContextMenu={(e) => { e.preventDefault(); setCtxMenu(null); }}
              />
              <div
                data-id="web-frame-ctx-menu"
                className="absolute z-40 min-w-[150px] select-none rounded-md border border-white/10 bg-[#1e1e1e] py-1 text-xs text-zinc-200 shadow-xl"
                style={{ left: ctxMenu.x, top: ctxMenu.y }}
              >
                <button
                  data-id="web-frame-ctx-copy"
                  type="button"
                  disabled={!ctxMenu.canCopy}
                  className="block w-full px-3 py-1.5 text-left hover:bg-white/[0.08] disabled:opacity-35 disabled:hover:bg-transparent"
                  onClick={() => { (webviewRef.current as any)?.copy?.(); setCtxMenu(null); }}
                >{t('webFrameCopy', { defaultValue: '复制' })}</button>
                <button
                  data-id="web-frame-ctx-paste"
                  type="button"
                  disabled={!ctxMenu.canPaste}
                  className="block w-full px-3 py-1.5 text-left hover:bg-white/[0.08] disabled:opacity-35 disabled:hover:bg-transparent"
                  onClick={() => { (webviewRef.current as any)?.paste?.(); setCtxMenu(null); }}
                >{t('webFramePaste', { defaultValue: '粘贴' })}</button>
                <div className="my-1 border-t border-white/[0.07]" />
                <button
                  data-id="web-frame-ctx-reload"
                  type="button"
                  className="block w-full px-3 py-1.5 text-left hover:bg-white/[0.08]"
                  onClick={() => { (webviewRef.current as any)?.reload?.(); setCtxMenu(null); }}
                >{t('webFrameReload', { defaultValue: '刷新' })}</button>
                <button
                  data-id="web-frame-ctx-devtools"
                  type="button"
                  className="block w-full px-3 py-1.5 text-left hover:bg-white/[0.08]"
                  onClick={() => { (webviewRef.current as any)?.openDevTools?.(); setCtxMenu(null); }}
                >DevTools</button>
              </div>
            </>
          )}
        </>
      );
    }

    return (
        <>
        {isLoading && (
          <div data-id="web-frame-loading" className="absolute inset-0 flex items-center justify-center bg-vsc-bg z-10">
            <Spinner size="md" />
          </div>
        )}
        {(pointerLocked || maskActive) && <div data-id="web-frame-interaction-mask" className="absolute inset-0 z-20" />}
        <iframe
          data-id="web-frame-iframe"
          ref={setIframeRef}
          src={src}
          className={className}
          style={style}
          onPointerDownCapture={focusEmbeddedFrame}
          onMouseDownCapture={focusEmbeddedFrame}
          onLoad={handleLoad}
          loading={loading}
          allowFullScreen={allowFullScreen}
          title={title}
          tabIndex={-1}
          sandbox="allow-forms allow-modals allow-popups allow-presentation allow-same-origin allow-scripts allow-downloads"
          allow="clipboard-read; clipboard-write; microphone"
        />
      </>
    );
  }
);
