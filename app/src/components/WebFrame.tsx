import React, { forwardRef, useState, useRef, useEffect, useCallback } from 'react';
import { Spinner } from './ui/Spinner';
import { usePointerLock } from '../lib/pointerLock';
import { WEB_FRAME_MASK_EVENT, WebFrameMaskEventDetail } from '../lib/webFrameMask';

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
  codeServer?: boolean;
}

export const WebFrame = forwardRef<HTMLIFrameElement, WebFrameProps>(
  ({ src, className, style, onLoad, loading, allowFullScreen, title, codeServer }, ref) => {
    const [isLoading, setIsLoading] = useState(true);
    const [maskActive, setMaskActive] = useState(false);
    const webviewRef = useRef<HTMLElement>(null);
    const iframeRef = useRef<HTMLIFrameElement>(null);
    const useWebview = isElectron && codeServer;
    const pointerLocked = usePointerLock();
    const activeMaskKeysRef = useRef<Set<string>>(new Set());
    const handleLoad = () => {
      setIsLoading(false);
      onLoad?.();
    };

    // code-server (VSCode) calls editor.focus() after its bootstrap, which
    // steals focus from whatever input the user is typing in. Render an
    // invisible click-mask in front of the iframe that absorbs pointer
    // events until the user explicitly clicks it. One click dismisses the
    // mask and the user can use the editor normally. Reapplies on every
    // src change (new file → new bootstrap → new focus grab).
    const [pendingActivation, setPendingActivation] = useState(!!codeServer);
    useEffect(() => { if (codeServer) setPendingActivation(true); }, [src, codeServer]);
    const dismissActivation = useCallback(() => setPendingActivation(false), []);

    // The HTML5 `inert` attribute makes a subtree non-focusable AND
    // unreachable to JS focus() calls — which is what we need to keep
    // code-server's editor.focus() inside the iframe from yanking focus
    // out of the host page on initial bootstrap. React's typings don't
    // fully support it yet, so set it imperatively via the ref.
    useEffect(() => {
      const apply = (el: HTMLElement | null) => {
        if (!el) return;
        if (pendingActivation) el.setAttribute("inert", "");
        else el.removeAttribute("inert");
      };
      apply(iframeRef.current);
      apply(webviewRef.current);
    }, [pendingActivation]);

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
        if (codeServer) {
          (wv as any).insertCSS?.('.action-item.agent-status-container{display:none!important}.panel .terminal-wrapper,.panel .terminals-list{display:none!important}');
        }
      };
      const onConsole = (e: any) => {
        const msg = e.message ?? '';
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
      return (
        <>
          {isLoading && (
            <div data-id="web-frame-loading" className="absolute inset-0 flex items-center justify-center bg-vsc-bg z-10">
              <Spinner size="md" />
            </div>
          )}
          {(pointerLocked || maskActive) && <div data-id="web-frame-interaction-mask" className="absolute inset-0 z-20" />}
          {pendingActivation && (
            <div
              data-id="web-frame-activation-mask"
              className="absolute inset-0 z-30 cursor-pointer"
              onClick={dismissActivation}
              title="Click to activate editor"
            />
          )}
          <webview
            data-id="web-frame-webview"
            ref={webviewRef as any}
            src={src}
            className={className}
            style={style}
            onClick={focusEmbeddedFrame as any}
            allowpopups={"" as any}
            partition={`persist:sandbox-0`}
            webpreferences="allowRunningInsecureContent=true"
            nodeintegration={"" as any}
            disablewebsecurity={"" as any}
          />
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
          onClick={focusEmbeddedFrame}
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
