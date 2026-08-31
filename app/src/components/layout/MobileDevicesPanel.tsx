// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Mobile-device helpers + screenshot column, used by BrowserWindowsPanel's
// Android / iOS backend tabs (peer to Electron / Chrome). The phones plug into a
// connected cicy-desktop machine; every action runs a shell command ON that
// machine via the chat-WS sync bridge (same transport BrowserWindowsPanel uses
// for Electron/Chrome):
//   POST /api/chat/push { client_id, type:'desktop_event',
//                         data:{type:'rpc_call', tool:'exec_shell', args:{command}}, wait_ack:true }
// so `adb` / libimobiledevice run where the phones are. This is the UI twin of
// the `agent-mobile` skill.
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Smartphone, Loader2, Camera, AlertCircle, Send, Home, ChevronLeft, X,
  Play, Pause, Gauge, RefreshCw,
} from 'lucide-react';
import apiService from '../../services/api';
import { Btn, Chip, Dot, ErrorStrip, HeaderBar, IconBtn, Menu, StateBlock, Toolbar } from './devices/ui';

export type MobilePlatform = 'android' | 'ios';
export interface MobileDevice { id: string; platform: MobilePlatform; model: string; status: string }
export interface MobileSel { clientId: string; platform: MobilePlatform; id: string; label: string }

// ── transport (same shape as BrowserWindowsPanel.deviceShell) ────────────────
async function devicePushRaw(clientId: string, tool: string, args: Record<string, any> = {}): Promise<string> {
  let resp: any;
  try {
    resp = await apiService.chatPush({
      client_id: clientId,
      type: 'desktop_event',
      data: { type: 'rpc_call', tool, args },
      wait_ack: true,
      timeout_ms: 45000,
    });
  } catch (e: any) {
    const status = e?.response?.status;
    if (status === 404) throw new Error('设备已断开');
    if (status === 504) throw new Error('设备无响应(超时)');
    throw new Error(e?.response?.data || e?.message || '请求失败');
  }
  const inner = resp?.data?.data || {};
  if (inner.error) throw new Error(inner.error);
  const result = inner.result;
  return typeof result === 'string' ? result : (result == null ? '' : JSON.stringify(result));
}
// exec_shell returns JSON { stdout, stderr, exitCode } (string or object); return stdout.
async function deviceShell(clientId: string, command: string): Promise<string> {
  const raw = (await devicePushRaw(clientId, 'exec_shell', { command })).trim();
  let r: any = raw;
  if (typeof r === 'string') { try { r = JSON.parse(r); } catch { return raw.replace(/\\n/g, '\n'); } }
  if (r && r.error) throw new Error(r.error);
  return String(r?.stdout ?? '').replace(/\\n/g, '\n');
}

// One round-trip: adb device table + iOS udid→name. Mirrors agent-mobile listDevices().
export async function listPhones(clientId: string): Promise<MobileDevice[]> {
  const script =
    'echo "===ADB==="; adb devices -l 2>/dev/null; ' +
    'echo "===IOS==="; for u in $(idevice_id -l 2>/dev/null); do ' +
    'n=$(ideviceinfo -u "$u" -k DeviceName 2>/dev/null); echo "${u}|${n}"; done';
  const out = await deviceShell(clientId, script);
  const devices: MobileDevice[] = [];
  let section = '';
  for (const raw of out.split('\n')) {
    const line = raw.trim();
    if (line === '===ADB===') { section = 'adb'; continue; }
    if (line === '===IOS===') { section = 'ios'; continue; }
    if (!line) continue;
    if (section === 'adb') {
      if (/^list of devices/i.test(line)) continue;
      const m = line.match(/^(\S+)\s+(device|unauthorized|offline)\b(.*)$/);
      if (!m) continue;
      const model = (m[3].match(/model:(\S+)/) || [])[1] || '';
      devices.push({ id: m[1], platform: 'android', model: model.replace(/_/g, ' '), status: m[2] });
    } else if (section === 'ios') {
      const [udid, name] = line.split('|');
      if (udid) devices.push({ id: udid, platform: 'ios', model: (name || '').trim(), status: 'device' });
    }
  }
  return devices;
}

// Screenshot → base64 → data URL for <img>.
//
// The old signature was a single `lossless` boolean surfaced as a cryptic
// "50% / 无损" chip — two extremes with nothing usable in between, and no way to
// trade resolution for latency when driving a phone over a slow link. It now
// takes the same 流畅/标准/高清/无损 preset ladder the 桌面 tab uses.
export interface PhoneQuality { id: string; longEdge: number; jpeg: number; lossless?: boolean }
export const PHONE_QUALITY: PhoneQuality[] = [
  { id: 'low', longEdge: 720, jpeg: 40 },
  { id: 'medium', longEdge: 1200, jpeg: 55 },
  { id: 'high', longEdge: 1600, jpeg: 80 },
  { id: 'lossless', longEdge: 0, jpeg: 0, lossless: true },
];

async function capturePhone(clientId: string, sel: MobileSel, q: PhoneQuality): Promise<string> {
  const lossless = !!q.lossless;
  const SRC = '/tmp/cicy-ui-mobile-shot.png';
  const ext = lossless ? 'png' : 'jpg';
  const OUT = `/tmp/cicy-ui-mobile-shot.out.${ext}`;
  const cap = sel.platform === 'android'
    ? `adb -s '${sel.id}' exec-out screencap -p > ${SRC}`
    // iOS needs the Developer Disk Image mounted for screenshotr — best-effort
    // auto-mount the matching Xcode DDI first (no-op when already mounted).
    : `MAJ=$(ideviceinfo -u '${sel.id}' -k ProductVersion 2>/dev/null | cut -d. -f1); ` +
      `DS=/Applications/Xcode.app/Contents/Developer/Platforms/iPhoneOS.platform/DeviceSupport; ` +
      `IMG=$(ls -d "$DS"/$MAJ.* 2>/dev/null | sort -V | tail -1); ` +
      `[ -n "$IMG" ] && ideviceimagemounter -u '${sel.id}' "$IMG/DeveloperDiskImage.dmg" "$IMG/DeveloperDiskImage.dmg.signature" >/dev/null 2>&1; ` +
      `idevicescreenshot -u '${sel.id}' ${SRC} >/dev/null 2>&1`;
  const conv = lossless
    ? `sips -s format png ${SRC} --out ${OUT}`
    : `sips -Z ${q.longEdge} -s format jpeg -s formatOptions ${q.jpeg} ${SRC} --out ${OUT}`;
  const b64 = (await deviceShell(clientId, `${cap} && ${conv} >/dev/null 2>&1 && base64 < ${OUT}`))
    .replace(/\\n/g, '').replace(/\s+/g, '');
  if (!b64) throw new Error('截图失败');
  return `data:image/${lossless ? 'png' : 'jpeg'};base64,${b64}`;
}

// Prompt handed to the agent so it drives the phone with the agent-mobile skill.
export function buildMobileAgentPrompt(sel: MobileSel): string {
  const kind = sel.platform === 'android' ? 'Android' : 'iOS';
  const extra = sel.platform === 'android'
    ? '可用 `agent-mobile tap/swipe/text/key/exec` 操作界面。'
    : 'iOS 仅支持 screenshot/info/applist/install。';
  return [
    `请用 \`agent-mobile\` skill 操控这台 ${kind} 设备(id \`${sel.id}\`,桌面客户端 \`${sel.clientId}\`)。`,
    `先 \`agent-mobile screenshot ${sel.id}\` 看屏幕,${extra}`,
    '当前任务:',
  ].join('\n');
}

// ── middle column: one phone's screen (peer of BrowserWindowsColumn) ─────────
// Same interaction model as the 桌面 tab, deliberately: live refresh with a
// chosen interval, a quality ladder, flicker-free frame swaps, and a state block
// instead of a bare red sentence when the phone stops answering.
const MOBILE_INTERVALS = [1000, 2000, 5000, 10000];
const LS_M_QUALITY = 'cicy.mobile.quality';
const LS_M_INTERVAL = 'cicy.mobile.interval';
const LS_M_LIVE = 'cicy.mobile.live';
const MOBILE_MAX_ERRORS = 3;

function readLS(key: string, fallback: string): string {
  try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
}
function writeLS(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* private mode */ }
}

export function MobileDeviceColumn({
  sel,
  onClose,
  onSendToAgent,
}: {
  sel: MobileSel;
  onClose: () => void;
  onSendToAgent: (text: string) => void;
}) {
  const [shot, setShot] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastTs, setLastTs] = useState<number | null>(null);
  const [, setTick] = useState(0);

  const [live, setLive] = useState(() => readLS(LS_M_LIVE, '0') === '1');
  const [intervalMs, setIntervalMs] = useState(() => {
    const n = parseInt(readLS(LS_M_INTERVAL, '2000'), 10);
    return MOBILE_INTERVALS.includes(n) ? n : 2000;
  });
  const [qualityId, setQualityId] = useState(() => {
    const id = readLS(LS_M_QUALITY, 'medium');
    return PHONE_QUALITY.some((q) => q.id === id) ? id : 'medium';
  });
  const quality = PHONE_QUALITY.find((q) => q.id === qualityId) || PHONE_QUALITY[1];

  const qualityRef = useRef(quality); qualityRef.current = quality;
  const liveRef = useRef(live); liveRef.current = live;
  const intervalRef = useRef(intervalMs); intervalRef.current = intervalMs;
  const runningRef = useRef(false);
  const errorsRef = useRef(0);
  const mountedRef = useRef(true);
  // true on mount as well as false on unmount — see DesktopScreenView: a
  // clear-only flag stays false after StrictMode's remount and eats setState.
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // Decode before swapping so the live loop never blanks the frame between shots.
  const swap = useCallback((dataUrl: string) => new Promise<void>((resolve) => {
    const img = new Image();
    img.onload = () => { if (mountedRef.current) setShot(dataUrl); resolve(); };
    img.onerror = () => { if (mountedRef.current) setShot(dataUrl); resolve(); };
    img.src = dataUrl;
  }), []);

  const capture = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (runningRef.current) return;
    runningRef.current = true;
    if (!opts.silent) setLoading(true);
    try {
      const url = await capturePhone(sel.clientId, sel, qualityRef.current);
      await swap(url);
      if (!mountedRef.current) return;
      setLastTs(Date.now());
      setError(null);
      errorsRef.current = 0;
    } catch (e: any) {
      if (!mountedRef.current) return;
      errorsRef.current += 1;
      setError(e?.message || '截图失败');
      if (errorsRef.current >= MOBILE_MAX_ERRORS && liveRef.current) setLive(false);
    } finally {
      runningRef.current = false;
      if (mountedRef.current && !opts.silent) setLoading(false);
    }
  }, [sel, swap]);

  // First paint on device change.
  useEffect(() => { setShot(null); setError(null); setLastTs(null); errorsRef.current = 0; void capture(); }, [capture]);

  // Self-scheduling loop (see DesktopScreenView for why not setInterval).
  useEffect(() => {
    if (!live) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const run = async () => {
      if (cancelled) return;
      if (document.hidden) { timer = setTimeout(run, 1000); return; }
      await capture({ silent: true });
      if (cancelled || !liveRef.current) return;
      timer = setTimeout(run, intervalRef.current);
    };
    run();
    return () => { cancelled = true; if (timer) clearTimeout(timer); };
  }, [live, capture]);

  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  const sendKey = useCallback(async (keycode: string) => {
    try { await deviceShell(sel.clientId, `adb -s '${sel.id}' shell input keyevent ${keycode}`); }
    catch { /* best-effort */ }
    setTimeout(() => void capture({ silent: true }), 350);
  }, [sel, capture]);

  const toggleLive = () => {
    const next = !live;
    errorsRef.current = 0;
    setLive(next);
    writeLS(LS_M_LIVE, next ? '1' : '0');
  };
  const qualityLabel = (id: string) => ({ low: '流畅', medium: '标准', high: '高清', lossless: '无损' } as Record<string, string>)[id] || id;
  const age = lastTs ? Math.max(0, Math.round((Date.now() - lastTs) / 1000)) : null;

  return (
    <div data-id="mobile-device-column" className="flex h-full min-h-0 flex-col bg-[var(--dev-bg)]">
      <HeaderBar
        icon={<Smartphone className="h-3.5 w-3.5" />}
        title={sel.label}
        subtitle={age == null ? sel.id : `${age < 2 ? '刚刚' : `${age} 秒前`} · ${qualityLabel(qualityId)}`}
      >
        {live && (
          <Chip tone="live" dataId="mobile-device-live-badge">
            <Dot tone="live" pulse />实时
          </Chip>
        )}
        <IconBtn
          dataId="mobile-device-send"
          icon={<Send className="h-3.5 w-3.5" />}
          tone="accent"
          title="发送给 agent 操控"
          onClick={() => onSendToAgent(buildMobileAgentPrompt(sel))}
        />
        <IconBtn dataId="mobile-device-close" icon={<X className="h-3.5 w-3.5" />} title="关闭" onClick={onClose} />
      </HeaderBar>

      <Toolbar>
        <Btn
          dataId="mobile-device-live-toggle"
          variant={live ? 'accent' : 'solid'}
          icon={live ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
          onClick={toggleLive}
          title={live ? '停止实时刷新' : '开启实时刷新'}
        >
          {live ? '实时中' : '实时'}
        </Btn>
        <Menu
          dataId="mobile-device-interval"
          tone={live ? 'solid' : 'ghost'}
          label={`${intervalMs / 1000}s`}
          value={intervalMs}
          title="刷新间隔"
          options={MOBILE_INTERVALS.map((ms) => ({ value: ms, label: `${ms / 1000}s` }))}
          onChange={(ms) => { setIntervalMs(ms); writeLS(LS_M_INTERVAL, String(ms)); }}
        />
        <Menu
          dataId="mobile-device-quality"
          icon={<Gauge className="h-3.5 w-3.5" />}
          label={qualityLabel(qualityId)}
          value={qualityId}
          title="截屏质量"
          options={PHONE_QUALITY.map((q) => ({
            value: q.id,
            label: qualityLabel(q.id),
            hint: q.lossless ? 'PNG' : `${q.longEdge}px · q${q.jpeg}`,
          }))}
          onChange={(id) => { setQualityId(id); writeLS(LS_M_QUALITY, id); void capture({ silent: true }); }}
        />
        <div className="flex-1" />
        <IconBtn dataId="mobile-device-capture" icon={<Camera className="h-3.5 w-3.5" />} busy={loading} onClick={() => void capture()} title="重新截图" />
      </Toolbar>

      {error && shot && (
        <ErrorStrip
          icon={<AlertCircle className="h-3.5 w-3.5" />}
          action={<Btn variant="ghost" onClick={() => { errorsRef.current = 0; void capture(); }}>重试</Btn>}
        >
          {error}{errorsRef.current >= MOBILE_MAX_ERRORS ? ' · 连续失败，已暂停实时刷新' : ''}
        </ErrorStrip>
      )}

      <div data-id="mobile-device-preview" className="flex min-h-0 flex-1 items-start justify-center overflow-auto p-3">
        {error && !shot ? (
          <StateBlock
            tone="warn"
            icon={<AlertCircle className="h-5 w-5" />}
            title="截图失败"
            hint={error}
            action={<Btn variant="solid" icon={<RefreshCw className="h-3.5 w-3.5" />} onClick={() => { errorsRef.current = 0; void capture(); }}>重试</Btn>}
          />
        ) : shot ? (
          <div className="relative">
            <img
              data-id="mobile-device-shot"
              src={shot}
              alt={sel.label}
              className="max-w-full rounded-lg border border-[var(--dev-border)]"
            />
            {loading || (live && runningRef.current) ? (
              <span className="absolute inset-x-0 top-0 h-0.5 animate-pulse rounded-t-lg bg-[var(--dev-accent)]" />
            ) : null}
          </div>
        ) : loading ? (
          <Loader2 className="mt-8 h-5 w-5 animate-spin text-[var(--dev-text-3)]" />
        ) : null}
      </div>

      {/* Android-only quick keys (iOS has no input control in v1) */}
      {sel.platform === 'android' ? (
        <div
          data-id="mobile-device-keys"
          className="flex shrink-0 items-center justify-center gap-1 border-t border-[var(--dev-border)] py-1.5"
        >
          <IconBtn dataId="mobile-device-key-back" icon={<ChevronLeft className="h-4 w-4" />} title="返回" onClick={() => void sendKey('KEYCODE_BACK')} />
          <IconBtn dataId="mobile-device-key-home" icon={<Home className="h-4 w-4" />} title="主屏" onClick={() => void sendKey('KEYCODE_HOME')} />
        </div>
      ) : null}
    </div>
  );
}
