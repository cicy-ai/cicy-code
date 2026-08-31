// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// 桌面 (Desktop) tab — the selected machine's screen.
//
// What changed vs. the old DesktopSnapshotView:
//   • 实时刷新 — the screen can now stream. Pick an interval (1/2/5/10/30s) and the
//     view keeps capturing on its own. Previously the ONLY way to see a change was
//     to click 立即截图 again, which made "watch what the agent is doing" unusable.
//   • 画质 — the capture resolution/JPEG quality is now a user choice (流畅 → 超清)
//     instead of a hard-coded 600px @ q60, which was too coarse to read text on the
//     remote screen and too heavy for a 1s live loop on a slow link.
//   • Flicker-free swaps — the new frame is decoded off-screen and only swapped in
//     once it is ready, so the live loop never blinks white between frames.
//   • Honest status — capture latency, frame age and failure state are on screen,
//     rather than a bare timestamp that gave no clue the device had stopped
//     answering.
//
// Loop shape: a self-scheduling timeout (NOT setInterval) — one capture round-trip
// can take longer than the interval, and setInterval would stack requests on a slow
// device until it drowned. The loop also parks itself when the tab is hidden and
// stops after repeated failures instead of hammering a disconnected machine.
import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Camera, Send, RefreshCw, Monitor, MonitorOff, AlertCircle, X,
  Play, Pause, Gauge, Maximize2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../../services/api';
import { cn } from '../../../lib/utils';
import { Btn, Chip, Dot, ErrorStrip, HeaderBar, IconBtn, Menu, StateBlock, Toolbar } from './ui';

interface SnapItem { name: string; ts: number }

// ── user preferences (persisted per browser, not per device) ─────────────────
export interface QualityPreset { id: string; maxWidth: number; quality: number }
const QUALITY_PRESETS: QualityPreset[] = [
  { id: 'low', maxWidth: 480, quality: 45 },
  { id: 'medium', maxWidth: 720, quality: 65 },
  { id: 'high', maxWidth: 1280, quality: 80 },
  { id: 'ultra', maxWidth: 1920, quality: 92 },
];
const INTERVALS = [1000, 2000, 5000, 10000, 30000];

const LS_QUALITY = 'cicy.desktop.quality';
const LS_INTERVAL = 'cicy.desktop.interval';
const LS_LIVE = 'cicy.desktop.live';

function readLS(key: string, fallback: string): string {
  try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
}
function writeLS(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* private mode — preference just won't persist */ }
}

// Stop the loop after this many consecutive failures rather than hammering a
// device that has gone away; the user gets an explicit "resume" affordance.
const MAX_CONSECUTIVE_ERRORS = 3;

export default function DesktopScreenView({ clientId, onSendToAgent, deviceLabel }: {
  clientId: string;
  onSendToAgent?: (text: string) => void;
  /** Shown in the header when this view owns a wide pane rather than the list column. */
  deviceLabel?: string;
}) {
  const { t } = useTranslation('layout');

  const [latest, setLatest] = useState<SnapItem | null>(null);
  const [imgSrc, setImgSrc] = useState('');          // only ever set to a DECODED frame
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [lightbox, setLightbox] = useState(false);
  const [latencyMs, setLatencyMs] = useState<number | null>(null);
  const [ageTick, setAgeTick] = useState(0);         // drives the "N 秒前" readout
  const [errCount, setErrCount] = useState(0);       // render-visible mirror of errorCountRef

  const [live, setLive] = useState(() => readLS(LS_LIVE, '0') === '1');
  const [intervalMs, setIntervalMs] = useState(() => {
    const n = parseInt(readLS(LS_INTERVAL, '2000'), 10);
    return INTERVALS.includes(n) ? n : 2000;
  });
  const [qualityId, setQualityId] = useState(() => {
    const id = readLS(LS_QUALITY, 'medium');
    return QUALITY_PRESETS.some((p) => p.id === id) ? id : 'medium';
  });
  const preset = QUALITY_PRESETS.find((p) => p.id === qualityId) || QUALITY_PRESETS[1];

  // Refs the loop reads, so changing a preference does not tear down and restart
  // the loop mid-capture (which would drop the in-flight frame).
  const presetRef = useRef(preset);
  presetRef.current = preset;
  const liveRef = useRef(live);
  liveRef.current = live;
  const intervalRef = useRef(intervalMs);
  intervalRef.current = intervalMs;
  const errorCountRef = useRef(0);
  const runningRef = useRef(false);   // a capture is in flight
  const mountedRef = useRef(true);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Set TRUE on mount, not just false on unmount: React StrictMode mounts,
  // unmounts and remounts in dev, so a flag that is only ever cleared stays
  // false for the live component and silently swallows every setState — the
  // view then hangs on its loading skeleton with no frame and no error.
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // Decode the frame off-screen, then swap. A plain `src` change would blank the
  // <img> while the new bytes load — at a 1s interval that reads as a strobe.
  const swapImage = useCallback((url: string) => new Promise<void>((resolve) => {
    const img = new Image();
    img.onload = () => { if (mountedRef.current) setImgSrc(url); resolve(); };
    img.onerror = () => resolve();
    img.src = url;
  }), []);

  const frameUrl = useCallback(
    (s: SnapItem) => `${apiService.desktopSnapshotImageUrl(clientId, s.name)}&_=${s.ts}`,
    [clientId],
  );

  /** One capture round-trip: ask the device for a fresh frame, then show it. */
  const captureOnce = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!clientId || runningRef.current) return;
    runningRef.current = true;
    if (!opts.silent) setBusy(true);
    const started = Date.now();
    try {
      const p = presetRef.current;
      const resp = await apiService.desktopSnapshotNow(clientId, { maxWidth: p.maxWidth, quality: p.quality });
      const name: string = resp?.data?.name || '';
      const ts: number = resp?.data?.ts || Date.now();
      if (!name) throw new Error(t('bwSnapErrEmpty', { defaultValue: '设备没有返回画面' }));
      const item = { name, ts };
      await swapImage(frameUrl(item));
      if (!mountedRef.current) return;
      setLatest(item);
      setLatencyMs(Date.now() - started);
      setError('');
      errorCountRef.current = 0;
      setErrCount(0);
    } catch (e: any) {
      if (!mountedRef.current) return;
      errorCountRef.current += 1;
      setErrCount(errorCountRef.current);
      setError(e?.response?.data?.error || e?.response?.data || e?.message || String(e));
      // Repeated failures mean the device is gone, not that this frame was unlucky.
      if (errorCountRef.current >= MAX_CONSECUTIVE_ERRORS && liveRef.current) setLive(false);
    } finally {
      runningRef.current = false;
      if (mountedRef.current && !opts.silent) setBusy(false);
    }
  }, [clientId, frameUrl, swapImage, t]);

  /** Load whatever frame is already on disk — instant first paint, no device round-trip. */
  const loadCached = useCallback(async () => {
    if (!clientId) { setLatest(null); setImgSrc(''); return; }
    try {
      const resp = await apiService.getDesktopSnapshots(clientId);
      const list: SnapItem[] = Array.isArray(resp?.data?.items) ? resp.data.items : [];
      const first = list[0];
      if (!first) return;
      await swapImage(frameUrl(first));
      if (mountedRef.current) setLatest(first);
    } catch { /* no cached frame — the empty state covers it */ }
  }, [clientId, frameUrl, swapImage]);

  // Device change → reset everything, show the cached frame immediately.
  useEffect(() => {
    setLatest(null); setImgSrc(''); setError(''); setLatencyMs(null);
    errorCountRef.current = 0;
    setErrCount(0);
    void loadCached();
  }, [loadCached]);

  // ── the live loop ──────────────────────────────────────────────────────────
  useEffect(() => {
    if (!live || !clientId) return;
    let cancelled = false;

    const schedule = (delay: number) => {
      if (cancelled) return;
      timerRef.current = setTimeout(run, delay);
    };
    const run = async () => {
      if (cancelled) return;
      // Parked tab: keep the loop alive but do no work — a background panel does
      // not need frames, and the device shouldn't pay for one.
      if (document.hidden) { schedule(1000); return; }
      await captureOnce({ silent: true });
      if (!liveRef.current) return;    // an error may have switched live off
      schedule(intervalRef.current);
    };

    run();
    const onVisible = () => { if (!document.hidden && liveRef.current && !runningRef.current) { /* next tick picks up */ } };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      if (timerRef.current) clearTimeout(timerRef.current);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [live, clientId, captureOnce]);

  // Keep the "N 秒前" readout honest while idle.
  useEffect(() => {
    const id = setInterval(() => setAgeTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  // ESC closes the lightbox (which keeps live-updating while open).
  useEffect(() => {
    if (!lightbox) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setLightbox(false); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [lightbox]);

  const toggleLive = () => {
    const next = !live;
    errorCountRef.current = 0;
    setErrCount(0);
    setLive(next);
    writeLS(LS_LIVE, next ? '1' : '0');
  };
  const pickInterval = (ms: number) => { setIntervalMs(ms); writeLS(LS_INTERVAL, String(ms)); };
  const pickQuality = (id: string) => {
    setQualityId(id);
    writeLS(LS_QUALITY, id);
    // Re-capture at the new quality right away — otherwise the user changes the
    // setting and stares at the old blurry frame wondering if it took effect.
    void captureOnce({ silent: true });
  };

  const fmtAge = (ms: number) => {
    void ageTick;                                   // re-render dependency
    const s = Math.max(0, Math.round(ms / 1000));
    if (s < 2) return t('bwSnapJustNow', { defaultValue: '刚刚' });
    if (s < 60) return t('bwSnapSecsAgo', { count: s, defaultValue: `${s} 秒前` });
    const m = Math.floor(s / 60);
    if (m < 60) return t('bwSnapMinsAgo', { count: m, defaultValue: `${m} 分钟前` });
    return t('bwSnapHoursAgo', { count: Math.floor(m / 60), defaultValue: `${Math.floor(m / 60)} 小时前` });
  };
  const intervalLabel = (ms: number) => (ms >= 1000 ? `${ms / 1000}s` : `${ms}ms`);
  const qualityLabel = (id: string) => t(`bwSnapQuality_${id}`, {
    defaultValue: { low: '流畅', medium: '标准', high: '高清', ultra: '超清' }[id] || id,
  });

  if (!clientId) {
    return (
      <StateBlock
        dataId="desktop-screen-no-device"
        icon={<MonitorOff className="h-5 w-5" />}
        title={t('bwNoDevices')}
        hint={t('bwSnapNoDeviceHint', { defaultValue: '先在上方选择一台已连接的机器。' })}
      />
    );
  }

  const stale = live && latest ? Date.now() - latest.ts > intervalMs * 3 : false;

  return (
    <div data-id="desktop-screen-view" className="flex h-full min-h-0 flex-col bg-[var(--dev-bg)]">
      <HeaderBar
        icon={<Monitor className="h-3.5 w-3.5" />}
        title={deviceLabel || t('bwTabDesktop')}
      >
        {live && (
          <Chip tone="live" dataId="desktop-screen-live-badge">
            <Dot tone="live" pulse />
            {t('bwSnapLive', { defaultValue: '实时' })}
          </Chip>
        )}
        {stale && !error && (
          <Chip tone="warn" title={t('bwSnapStaleHint', { defaultValue: '画面比刷新间隔旧，设备可能变慢了' })}>
            {t('bwSnapStale', { defaultValue: '滞后' })}
          </Chip>
        )}
        {/* One-shot actions live in the header; the toolbar below keeps ONLY the
            three streaming controls, so neither row wraps in a narrow column. */}
        <IconBtn
          dataId="desktop-screen-capture"
          icon={<Camera className="h-3.5 w-3.5" />}
          busy={busy}
          onClick={() => void captureOnce()}
          title={t('bwSnapNow')}
        />
        {onSendToAgent && (
          <IconBtn
            dataId="desktop-screen-send-agent"
            icon={<Send className="h-3.5 w-3.5" />}
            tone="accent"
            onClick={() => onSendToAgent(t('bwPromptDesktop', { clientId, c: `agent-desktop --client ${clientId}` }))}
            title={t('bwSendToAgent')}
          />
        )}
        <IconBtn
          dataId="desktop-screen-reload"
          icon={<RefreshCw className="h-3.5 w-3.5" />}
          onClick={() => void loadCached()}
          title={t('bwSnapReloadCached', { defaultValue: '重新载入已有画面' })}
        />
      </HeaderBar>

      <Toolbar>
        {/* 实时刷新 — the headline control, so it leads and carries the state colour. */}
        <Btn
          dataId="desktop-screen-live-toggle"
          variant={live ? 'accent' : 'solid'}
          icon={live ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
          onClick={toggleLive}
          title={live
            ? t('bwSnapLiveStop', { defaultValue: '停止实时刷新' })
            : t('bwSnapLiveStart', { defaultValue: '开启实时刷新' })}
        >
          {live ? t('bwSnapLiveOn', { defaultValue: '实时中' }) : t('bwSnapLiveOff', { defaultValue: '实时' })}
        </Btn>

        <Menu
          dataId="desktop-screen-interval"
          tone={live ? 'solid' : 'ghost'}
          label={intervalLabel(intervalMs)}
          value={intervalMs}
          title={t('bwSnapIntervalTitle', { defaultValue: '刷新间隔' })}
          options={INTERVALS.map((ms) => ({
            value: ms,
            label: intervalLabel(ms),
            hint: ms <= 2000
              ? t('bwSnapIntervalFast', { defaultValue: '流量大' })
              : ms >= 30000 ? t('bwSnapIntervalSlow', { defaultValue: '省流量' }) : '',
          }))}
          onChange={pickInterval}
        />

        <Menu
          dataId="desktop-screen-quality"
          icon={<Gauge className="h-3.5 w-3.5" />}
          label={qualityLabel(qualityId)}
          value={qualityId}
          title={t('bwSnapQualityTitle', { defaultValue: '截屏质量' })}
          options={QUALITY_PRESETS.map((p) => ({
            value: p.id,
            label: qualityLabel(p.id),
            hint: `${p.maxWidth}px · q${p.quality}`,
          }))}
          onChange={pickQuality}
        />
      </Toolbar>

      {/* Status on its own full-width line. It used to be the header subtitle,
          where three action buttons squeezed it down to "No fr…" in a narrow
          pane — a status readout you cannot read is worse than none. */}
      <div
        data-id="desktop-screen-status"
        className="flex shrink-0 items-center gap-1.5 border-b border-[var(--dev-border)] px-2.5 py-1 text-[10px] text-[var(--dev-text-3)]"
      >
        <span className="truncate">
          {latest
            ? [
                fmtAge(Date.now() - latest.ts),
                `${preset.maxWidth}px · q${preset.quality}`,
                latencyMs != null ? `${latencyMs}ms` : '',
              ].filter(Boolean).join(' · ')
            : t('bwSnapNoFrame', { defaultValue: '尚无画面' })}
        </span>
      </div>

      {error && (
        <ErrorStrip
          dataId="desktop-screen-error"
          icon={<AlertCircle className="h-3.5 w-3.5" />}
          action={
            <Btn variant="ghost" onClick={() => { errorCountRef.current = 0; setErrCount(0); void captureOnce(); }}>
              {t('bwSnapRetry', { defaultValue: '重试' })}
            </Btn>
          }
        >
          {error}
          {errCount >= MAX_CONSECUTIVE_ERRORS
            ? ` · ${t('bwSnapLiveStopped', { defaultValue: '连续失败，已暂停实时刷新' })}`
            : ''}
        </ErrorStrip>
      )}

      {!imgSrc ? (
        busy ? (
          <div className="p-2.5">
            <div
              data-id="desktop-screen-loading"
              className="aspect-video animate-pulse rounded-xl bg-[var(--dev-surface-2)]"
            />
          </div>
        ) : (
          <StateBlock
            dataId="desktop-screen-empty"
            icon={<Monitor className="h-5 w-5" />}
            title={t('bwSnapEmpty')}
            hint={t('bwSnapEmptyHintNew', { defaultValue: '点「相机」抓一张，或开启实时刷新持续观看这台机器的屏幕。' })}
            action={
              <Btn variant="accent" icon={<Camera className="h-3.5 w-3.5" />} busy={busy} onClick={() => void captureOnce()}>
                {t('bwSnapNow')}
              </Btn>
            }
          />
        )
      ) : (
        <div className="min-h-0 flex-1 overflow-auto p-2.5">
          <button
            type="button"
            data-id="desktop-screen-frame"
            onClick={() => setLightbox(true)}
            className="group relative block w-full cursor-zoom-in overflow-hidden rounded-xl border border-[var(--dev-border)] bg-[var(--dev-surface-2)]"
            title={t('bwSnapZoom', { defaultValue: '点击放大' })}
          >
            <img src={imgSrc} alt="desktop" className="block w-full" />
            {/* Capture-in-flight is shown as a hairline on top of the LAST good
                frame — never by clearing the image, which is what made the old
                view flash between shots. */}
            <span
              className={cn(
                'absolute inset-x-0 top-0 h-0.5 origin-left bg-[var(--dev-accent)] transition-opacity',
                busy || (live && runningRef.current) ? 'animate-pulse opacity-100' : 'opacity-0',
              )}
            />
            <span className="pointer-events-none absolute bottom-1.5 right-1.5 rounded-md bg-black/55 p-1 text-white opacity-0 transition-opacity group-hover:opacity-100">
              <Maximize2 className="h-3.5 w-3.5" />
            </span>
          </button>
        </div>
      )}

      {lightbox && imgSrc && createPortal(
        <div
          data-id="desktop-screen-lightbox"
          className="fixed inset-0 z-[300] flex items-center justify-center bg-black/85 p-6 backdrop-blur-sm"
          onClick={() => setLightbox(false)}
        >
          <img src={imgSrc} alt="desktop" className="max-h-full max-w-full rounded-lg shadow-2xl" onClick={(e) => e.stopPropagation()} />
          {live && (
            <span className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-full bg-black/60 px-3 py-1 text-[11px] font-medium text-white">
              <Dot tone="live" pulse /> {t('bwSnapLive', { defaultValue: '实时' })} · {intervalLabel(intervalMs)}
            </span>
          )}
          <button
            type="button"
            data-id="desktop-screen-lightbox-close"
            className="absolute right-4 top-4 cursor-pointer rounded-lg bg-white/10 p-2 text-white hover:bg-white/20"
            onClick={() => setLightbox(false)}
          >
            <X className="h-5 w-5" />
          </button>
        </div>,
        document.body,
      )}
    </div>
  );
}
