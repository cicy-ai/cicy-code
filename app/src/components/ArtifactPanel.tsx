import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, ArrowRight, RotateCw, ExternalLink, X, Monitor, Tablet, Smartphone, Package, ArrowUp } from 'lucide-react';
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
const URL_KEY = 'cicy_artifact_url';

// The last-opened artifact URL is persisted so it survives reloads/remounts.
function loadStoredUrl(): string {
  try {
    const v = JSON.parse(localStorage.getItem(URL_KEY)!);
    return typeof v === 'string' && v ? v : BLANK;
  } catch { return BLANK; }
}
function storeUrl(u: string) {
  try {
    if (!u || u === BLANK) localStorage.removeItem(URL_KEY);
    else localStorage.setItem(URL_KEY, JSON.stringify(u));
  } catch { /* ignore */ }
}

// Preview viewport presets. `web` fills the host; the others constrain the
// frame to a device-sized box so the loaded page renders at that viewport
// (responsive sites pick up their mobile/tablet layout). Persisted so the
// chosen mode survives reloads/remounts.
type PreviewMode = 'web' | 'portal' | 'mobile';
const PREVIEW_KEY = 'cicy_artifact_preview';
const PREVIEW_DIMS: Record<PreviewMode, { w: number; h: number } | null> = {
  web: null,
  portal: { w: 768, h: 1024 },
  mobile: { w: 390, h: 844 },
};
function loadPreview(): PreviewMode {
  try {
    const v = JSON.parse(localStorage.getItem(PREVIEW_KEY)!);
    return v === 'mobile' || v === 'portal' ? v : 'web';
  } catch { return 'web'; }
}

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
  const [url, setUrl] = useState<string>(loadStoredUrl);
  const [inputUrl, setInputUrl] = useState<string>(() => { const u = loadStoredUrl(); return u === BLANK ? '' : u; });
  const [iframeKey, setIframeKey] = useState(0);
  const [preview, setPreviewState] = useState<PreviewMode>(loadPreview);

  const elRef = useRef<any>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const urlRef = useRef<string>(BLANK);
  urlRef.current = url;
  const previewRef = useRef<PreviewMode>(preview);
  previewRef.current = preview;

  const load = useCallback((raw: string) => {
    const next = normalizeUrl(raw);
    setUrl(next);
    setInputUrl(next === BLANK ? '' : next);
    storeUrl(next);
    // Electron <webview> picks up the src change; for the iframe we also bump
    // the key so re-loading the *same* url forces a fresh mount.
    if (!electron) setIframeKey((k) => k + 1);
  }, [electron]);

  // Persisted preview-mode setter.
  const applyPreview = useCallback((m: PreviewMode) => {
    setPreviewState(m);
    try { localStorage.setItem(PREVIEW_KEY, JSON.stringify(m)); } catch { /* ignore */ }
  }, []);

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
      setPreview: (m: string) => { if (m === 'web' || m === 'portal' || m === 'mobile') applyPreview(m); },
      getPreview: () => previewRef.current,
    };
    registerArtifactController(controller);
    return () => unregisterArtifactController(controller);
  }, [electron, load, requestActivate, applyPreview]);

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
  const focusUrlInput = () => { requestActivate(); inputRef.current?.focus(); };

  const hasUrl = url && url !== BLANK;
  const dim = PREVIEW_DIMS[preview];

  // The frame element — kept identical across preview modes so switching mode
  // only resizes its container (no remount → no reload of the loaded page).
  const frameEl = electron
    ? React.createElement('webview', {
        'data-id': 'artifact-webview',
        id: ARTIFACT_WEBVIEW_ID,
        ref: (node: any) => { elRef.current = node; },
        src: url,
        allowpopups: 'true',
        style: { display: 'flex', width: '100%', height: '100%' },
        className: 'h-full w-full border-0 bg-white',
      })
    : (
      <WebFrame
        key={iframeKey}
        ref={(node: HTMLIFrameElement | null) => { elRef.current = node; }}
        src={url}
        title="artifact"
        className="h-full w-full border-0 bg-white"
      />
    );

  const previewModes: [PreviewMode, typeof Monitor, string, string][] = [
    ['web', Monitor, t('artifactPreviewWeb', 'Web'), 'Web'],
    ['portal', Tablet, t('artifactPreviewPortal', 'Portal'), 'Portal'],
    ['mobile', Smartphone, t('artifactPreviewMobile', 'Mobile'), 'Mobile'],
  ];

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
            ref={inputRef}
            value={inputUrl}
            onChange={(e) => setInputUrl(e.target.value)}
            placeholder={t('artifactUrlPlaceholder', 'Enter a URL to open in the artifact frame')}
            spellCheck={false}
            className="h-6 w-full rounded border border-[var(--vsc-border)] bg-black/20 px-2 text-xs text-zinc-200 outline-none focus:border-zinc-500"
          />
        </form>
        <div data-id="artifact-preview-modes" className="flex shrink-0 items-center gap-0.5 rounded border border-[var(--vsc-border)] p-0.5">
          {previewModes.map(([m, Icon, label]) => (
            <button
              key={m}
              data-id={`artifact-preview-${m}`}
              type="button"
              onClick={() => applyPreview(m)}
              aria-pressed={preview === m}
              title={label}
              className={'rounded p-1 transition-colors ' + (preview === m ? 'bg-white/[0.15] text-zinc-100' : 'text-zinc-400 hover:bg-white/10 hover:text-zinc-200')}
            >
              <Icon className="h-3.5 w-3.5" />
            </button>
          ))}
        </div>
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
        <div
          data-id="artifact-frame-stage"
          className={'absolute inset-0 ' + (dim ? 'flex items-start justify-center overflow-auto bg-[#0c0d10] p-3' : '')}
        >
          {hasUrl && (
            <div
              data-id="artifact-frame-viewport"
              className={dim ? 'shrink-0 overflow-hidden rounded-lg border border-[var(--vsc-border)] bg-white shadow-lg' : 'h-full w-full'}
              style={dim ? { width: dim.w, height: `min(${dim.h}px, 100%)`, maxWidth: '100%' } : undefined}
            >
              {frameEl}
            </div>
          )}
        </div>
        {!hasUrl && (
          <div data-id="artifact-empty" className="absolute inset-0 z-20 flex items-center justify-center bg-[radial-gradient(circle_at_top,rgba(59,130,246,0.08),transparent_45%),var(--vsc-bg)] p-6">
            {/* A faux browser window standing in for where the artifact will
                render — no real <webview>/<iframe> is mounted until a URL is set. */}
            <div data-id="artifact-empty-mock" className="flex w-[min(560px,94%)] flex-col overflow-hidden rounded-xl border border-[var(--vsc-border)] bg-[#0e0f13] shadow-2xl">
              <div data-id="artifact-empty-mock-bar" className="flex items-center gap-2 border-b border-[var(--vsc-border)] bg-white/[0.03] px-3 py-2">
                <span data-id="artifact-empty-mock-dots" className="flex shrink-0 items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="h-2.5 w-2.5 rounded-full bg-[#febc2e]" />
                  <span className="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
                </span>
                <button
                  data-id="artifact-empty-mock-address"
                  type="button"
                  onClick={focusUrlInput}
                  className="ml-2 flex-1 truncate rounded-md border border-[var(--vsc-border)] bg-black/30 px-2.5 py-1 text-left text-[11px] text-zinc-500 transition-colors hover:border-zinc-500 hover:text-zinc-300"
                >
                  {t('artifactUrlPlaceholder', 'Enter a URL to open in the artifact frame')}
                </button>
              </div>
              <div data-id="artifact-empty-mock-body" className="flex flex-col items-center justify-center gap-4 px-6 py-12 text-center">
                <div data-id="artifact-empty-icon" className="flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--vsc-border)] bg-white/[0.03] text-zinc-500 shadow-inner">
                  <Package className="h-8 w-8" />
                </div>
                <div data-id="artifact-empty-text" className="flex flex-col items-center gap-1.5">
                  <div data-id="artifact-empty-title" className="text-sm font-medium text-zinc-200">
                    {t('artifactEmptyTitle', 'Artifact preview')}
                  </div>
                  <div data-id="artifact-empty-desc" className="max-w-[22rem] text-xs leading-relaxed text-zinc-500">
                    {t('artifactEmpty', 'No artifact open. Enter a URL above, or let an agent open one via the artifact skill.')}
                  </div>
                </div>
                <button
                  data-id="artifact-empty-cta"
                  type="button"
                  onClick={focusUrlInput}
                  className="mt-1 inline-flex items-center gap-1.5 rounded-md border border-[var(--vsc-border)] bg-white/[0.03] px-3 py-1.5 text-xs font-medium text-zinc-300 transition-colors hover:border-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100"
                >
                  <ArrowUp className="h-3.5 w-3.5" />
                  {t('artifactEmptyCta', 'Enter a URL to open')}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
