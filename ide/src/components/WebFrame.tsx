import React, { forwardRef, useState, useRef, useEffect } from 'react';
import { Loader2 } from 'lucide-react';

export const isElectron = navigator.userAgent.includes('Electron');

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
      };
      wv.addEventListener('dom-ready', onDomReady);
      return () => wv.removeEventListener('dom-ready', onDomReady);
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
