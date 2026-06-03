import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, ArrowRight, RotateCw, ExternalLink, X } from 'lucide-react';
import { WebFrame } from './WebFrame';
import {
  ARTIFACT_WEBVIEW_ID,
  registerArtifactController,
  unregisterArtifactController,
  type ArtifactController,
} from '../lib/artifactBridge';

// ArtifactPanel — the 产物 tab body. Hosts a remotely controllable page frame:
// an Electron <webview> when running inside cicy-desktop (window.cicy present),
// otherwise a plain <iframe> (via WebFrame). The mounted panel registers an
// ArtifactController with artifactBridge so window.cicyArtifact.* can drive it
// from the agent's exec_js channel. See app/src/lib/artifactBridge.ts.

const BLANK = 'about:blank';

// cicy-desktop injects window.cicy into its trusted cicy-code windows; its
// presence is how the app already detects Electron (see MobileQRPopover).
function detectElectron(): boolean {
  return typeof (window as any).cicy !== 'undefined';
}

// Light normalization: a bare "example.com/x" becomes https. Anything with a
// scheme, a leading slash, or about:/data: is left untouched.
function normalizeUrl(raw: string): string {
  const s = (raw || '').trim();
  if (!s) return BLANK;
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(s) || s.startsWith('//') || s.startsWith('/')) return s;
  if (/^[^\s/]+\.[^\s/]+/.test(s)) return 'https://' + s;
  return s;
}

interface Props {
  active: boolean;
  requestActivate: () => void;
  className?: string;
}

export default function ArtifactPanel({ active, requestActivate, className }: Props) {
  const { t } = useTranslation('workspace');
  const [electron] = useState(detectElectron);
  const [url, setUrl] = useState<string>(BLANK);
  const [inputUrl, setInputUrl] = useState<string>('');
  const [iframeKey, setIframeKey] = useState(0);

  const elRef = useRef<any>(null);
  const urlRef = useRef<string>(BLANK);
  urlRef.current = url;

  const load = useCallback((raw: string) => {
    const next = normalizeUrl(raw);
    setUrl(next);
    setInputUrl(next === BLANK ? '' : next);
    // Electron <webview> picks up the src change; for the iframe we also bump
    // the key so re-loading the *same* url forces a fresh mount.
    if (!electron) setIframeKey((k) => k + 1);
  }, [electron]);

  // Register the controller for window.cicyArtifact while mounted.
  useEffect(() => {
    const controller: ArtifactController = {
      isElectron: () => electron,
      getEl: () => elRef.current,
      stateUrl: () => urlRef.current,
      open: (u: string) => { requestActivate(); load(u); },
      setUrl: (u: string) => load(u),
      reload: () => {
        const el = elRef.current;
        if (electron && el && typeof el.reload === 'function') { try { el.reload(); return; } catch { /* fallthrough */ } }
        setIframeKey((k) => k + 1);
      },
      clear: () => load(BLANK),
    };
    registerArtifactController(controller);
    return () => unregisterArtifactController(controller);
  }, [electron, load, requestActivate]);

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (inputUrl.trim()) load(inputUrl);
  };

  const reload = () => {
    const el = elRef.current;
    if (electron && el && typeof el.reload === 'function') { try { el.reload(); return; } catch { /* fallthrough */ } }
    setIframeKey((k) => k + 1);
  };
  const goBack = () => { const el = elRef.current; if (electron && el?.goBack) try { el.goBack(); } catch {} };
  const goForward = () => { const el = elRef.current; if (electron && el?.goForward) try { el.goForward(); } catch {} };
  const openExternal = () => { if (url && url !== BLANK) window.open(url, '_blank', 'noopener'); };

  const hasUrl = url && url !== BLANK;

  return (
    <div data-id="artifact-panel" className={'flex h-full w-full flex-col bg-vsc-bg ' + (className || '')}>
      <div
        data-id="artifact-toolbar"
        className="flex h-9 shrink-0 items-center gap-1 border-b border-[var(--vsc-border)] px-2"
      >
        {electron && (
          <>
            <button data-id="artifact-back" type="button" onClick={goBack} title={t('artifactBack', 'Back')}
              className="rounded p-1 text-zinc-400 hover:bg-white/10 hover:text-zinc-200">
              <ArrowLeft className="h-3.5 w-3.5" />
            </button>
            <button data-id="artifact-forward" type="button" onClick={goForward} title={t('artifactForward', 'Forward')}
              className="rounded p-1 text-zinc-400 hover:bg-white/10 hover:text-zinc-200">
              <ArrowRight className="h-3.5 w-3.5" />
            </button>
          </>
        )}
        <button data-id="artifact-reload" type="button" onClick={reload} title={t('artifactReload', 'Reload')}
          className="rounded p-1 text-zinc-400 hover:bg-white/10 hover:text-zinc-200">
          <RotateCw className="h-3.5 w-3.5" />
        </button>
        <form data-id="artifact-url-form" onSubmit={onSubmit} className="min-w-0 flex-1">
          <input
            data-id="artifact-url-input"
            type="text"
            value={inputUrl}
            onChange={(e) => setInputUrl(e.target.value)}
            placeholder={t('artifactUrlPlaceholder', 'Enter a URL to open in the artifact frame')}
            spellCheck={false}
            className="h-6 w-full rounded border border-[var(--vsc-border)] bg-black/20 px-2 text-xs text-zinc-200 outline-none focus:border-zinc-500"
          />
        </form>
        <button data-id="artifact-open-external" type="button" onClick={openExternal} disabled={!hasUrl}
          title={t('artifactOpenExternal', 'Open in browser')}
          className="rounded p-1 text-zinc-400 hover:bg-white/10 hover:text-zinc-200 disabled:opacity-30">
          <ExternalLink className="h-3.5 w-3.5" />
        </button>
        <button data-id="artifact-clear" type="button" onClick={() => load(BLANK)} disabled={!hasUrl}
          title={t('artifactClear', 'Clear')}
          className="rounded p-1 text-zinc-400 hover:bg-white/10 hover:text-zinc-200 disabled:opacity-30">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      <div data-id="artifact-frame-host" className="relative min-h-0 flex-1">
        {!hasUrl ? (
          <div data-id="artifact-empty" className="flex h-full w-full items-center justify-center px-6 text-center text-xs text-zinc-500">
            {t('artifactEmpty', 'No artifact open. Enter a URL above, or let an agent open one via the artifact skill.')}
          </div>
        ) : electron ? (
          React.createElement('webview', {
            'data-id': 'artifact-webview',
            id: ARTIFACT_WEBVIEW_ID,
            ref: (node: any) => { elRef.current = node; },
            src: url,
            allowpopups: 'true',
            // a bare <webview> is inline by default; make it fill the host.
            style: { display: active ? 'flex' : 'flex', width: '100%', height: '100%' },
            className: 'h-full w-full border-0 bg-white',
          })
        ) : (
          <WebFrame
            key={iframeKey}
            ref={(node: HTMLIFrameElement | null) => { elRef.current = node; }}
            src={url}
            title="artifact"
            className="h-full w-full border-0 bg-white"
          />
        )}
      </div>
    </div>
  );
}
