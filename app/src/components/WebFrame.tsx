// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import React, { forwardRef, useState, useRef, useEffect, useCallback } from 'react';
import { Spinner } from './ui/Spinner';
import { usePointerLock } from '../lib/pointerLock';
import { WEB_FRAME_MASK_EVENT, WebFrameMaskEventDetail } from '../lib/webFrameMask';

// Inside Electron (cicy-desktop) every WebFrame renders an Electron <webview>:
// process-isolated, inspectable via window.__cicy.webviews (openDevTools /
// getContents). In plain browsers it stays a sandboxed <iframe>. The webview
// branch was originally code-server-only and got dropped wholesale by the
// 1c73ae5 checkpoint snapshot; restored here and widened to all frames.
const isElectron = navigator.userAgent.includes('Electron');

// A gotty terminal guest (separate top-level WebContents) asks the host to open
// a file in the code editor by printing this exact prefix on console.log; our
// webview `console-message` listener forwards the JSON tail to
// __cicyOpenCodeFile. Kept in sync with api/js/src/link_confirm.ts.
const CODE_FILE_CONSOLE_SENTINEL = '[[CICY_OPEN_CODE_FILE]]';

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
    const [isLoading, setIsLoading] = useState(true);
    const [maskActive, setMaskActive] = useState(false);
    // Right-click in the <webview> guest is served by cicy-desktop's NATIVE
    // electron-context-menu (复制/粘贴/重新加载/检查元素), attached to webview guests
    // since cicy-desktop ≥ 2.1.94. No custom in-app menu here — it would double-pop
    // with the native one. (In a real browser the frame is an <iframe> with its
    // own native menu, so no in-app menu is needed there either.)
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
        // gotty terminal guests can't reach the host via window.parent (they're
        // a separate top-level WebContents), so a clicked file path asks us to
        // open the editor by printing this sentinel. Match it and forward the
        // path to __cicyOpenCodeFile. Sentinel kept in sync with
        // api/js/src/link_confirm.ts (CODE_FILE_CONSOLE_SENTINEL).
        if (typeof msg === 'string') {
          const idx = msg.indexOf(CODE_FILE_CONSOLE_SENTINEL);
          if (idx !== -1) {
            try {
              const payload = JSON.parse(msg.slice(idx + CODE_FILE_CONSOLE_SENTINEL.length));
              const filePath = String(payload?.path || '').trim();
              const openFn = (window as any).__cicyOpenCodeFile;
              if (filePath && typeof openFn === 'function') openFn(filePath);
            } catch {}
            return;
          }
        }
        console.log(`[webview:${title || 'untitled'}]`, msg);
      };
      // Fallback: hide spinner after 8s if dom-ready never fires
      const fallback = setTimeout(() => setIsLoading(false), 8000);

      wv.addEventListener('dom-ready', onDomReady);
      wv.addEventListener('console-message', onConsole);
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
