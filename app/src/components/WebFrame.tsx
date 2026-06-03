import React, { forwardRef, useState, useRef, useEffect, useCallback } from 'react';
import { Spinner } from './ui/Spinner';
import { usePointerLock } from '../lib/pointerLock';
import { WEB_FRAME_MASK_EVENT, WebFrameMaskEventDetail } from '../lib/webFrameMask';

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
    const iframeRef = useRef<HTMLIFrameElement>(null);
    const pointerLocked = usePointerLock();
    const activeMaskKeysRef = useRef<Set<string>>(new Set());
    const handleLoad = () => {
      setIsLoading(false);
      onLoad?.();
    };

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
        window.requestAnimationFrame(() => {
          iframeRef.current?.focus();
          iframeRef.current?.contentWindow?.focus?.();
        });
      } catch {}
    }, [maskActive, pointerLocked]);

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
