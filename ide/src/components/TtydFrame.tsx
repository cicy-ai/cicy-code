import React, { forwardRef, useImperativeHandle, useRef } from 'react';
import { urls } from '../config';
import { WebFrame } from './WebFrame';

interface TtydFrameProps {
  paneId?: string;
  port?: number;
  token?: string;
  token2?: string;
  url?: string;
  isInteractingWithOverlay: boolean;
}

export interface TtydFrameHandle {
  scrollToBottom: () => void;
  reload: () => void;
  getUrl: () => string | undefined;
}

export const TtydFrame = forwardRef<TtydFrameHandle, TtydFrameProps>(
  ({ paneId, port, token, url, isInteractingWithOverlay }, ref) => {
    const iframeRef = useRef<HTMLIFrameElement>(null);
    const ttydUrl = url || (paneId && token ? urls.ttydOpen(paneId, token) : '');

    useImperativeHandle(ref, () => ({
      scrollToBottom: () => {
        try {
          const win = iframeRef.current?.contentWindow as (Window & { term?: { scrollToBottom?: () => void } }) | null;
          win?.term?.scrollToBottom?.();
        } catch {
          // cross-origin or not ready
        }
      },
      reload: () => {
        const iframe = iframeRef.current;
        if (iframe) {
          const currentSrc = iframe.src;
          iframe.src = '';
          setTimeout(() => { iframe.src = currentSrc; }, 10);
        }
      },
      getUrl: () => ttydUrl,
    }));

    return (
      <div className="absolute inset-0 z-[1] bg-black overflow-hidden mb-9">
        <WebFrame
          loading="lazy"
          ref={iframeRef}
          src={ttydUrl}
          title="ttyd"
          className={`w-full h-full border-none absolute inset-0 ${isInteractingWithOverlay ? 'opacity-90' : 'opacity-100'}`}
          allowFullScreen
        />
      </div>
    );
  }
);
