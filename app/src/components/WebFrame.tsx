import React, { forwardRef, useState, useRef, useEffect } from 'react';
import { Loader2 } from 'lucide-react';

export const isElectron = navigator.userAgent.includes('Electron');

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
    const webviewRef = useRef<HTMLElement>(null);
    const useWebview = isElectron && codeServer;

    const handleLoad = () => {
      setIsLoading(false);
      onLoad?.();
    };

    useEffect(() => {
      if (!useWebview) return;
      const wv = webviewRef.current;
      if (!wv) return;

      const onDomReady = () => {
        setIsLoading(false);
        onLoad?.();
        if (codeServer) {
          (wv as any).insertCSS?.('.action-item.agent-status-container{display:none!important}.panel .terminal-wrapper,.panel .terminals-list{display:none!important}');
          (wv as any).executeJavaScript?.(`
            setTimeout(function(){
              setInterval(function(){
                var btn=document.querySelector(".codicon-auxiliarybar-right-layout-icon");
                if(btn)btn.click();
              },1000);
            },3000);
          `);
        }
      };
      wv.addEventListener('dom-ready', onDomReady);
      const unregister = registerWebview(wv);
      return () => {
        wv.removeEventListener('dom-ready', onDomReady);
        unregister();
      };
    }, [useWebview, onLoad]);

    // Update webview src when it changes
    useEffect(() => {
      if (!useWebview) return;
      const wv = webviewRef.current as any;
      if (!wv || !src) return;
      if (wv.src !== src) {
        wv.src = src;
      }
    }, [src, useWebview]);

    if (useWebview) {
      return (
        <>
          {isLoading && (
            <div className="absolute inset-0 flex items-center justify-center bg-vsc-bg z-10">
              <Loader2 className="animate-spin" />
            </div>
          )}
          <webview
            ref={webviewRef as any}
            src={src}
            className={className}
            style={style}
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
          <div className="absolute inset-0 flex items-center justify-center bg-vsc-bg z-10">
            <Loader2 className="animate-spin" />
          </div>
        )}
        <iframe
          ref={ref}
          src={src}
          className={className}
          style={style}
          onLoad={handleLoad}
          loading={loading}
          allowFullScreen={allowFullScreen}
          title={title}
          sandbox="allow-forms allow-modals allow-popups allow-presentation allow-same-origin allow-scripts allow-downloads"
          allow="clipboard-read; clipboard-write"
        />
      </>
    );
  }
);
