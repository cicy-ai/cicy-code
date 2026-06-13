// Mobile-device helpers + screenshot column, used by BrowserWindowsPanel's
// Android / iOS backend tabs (peer to Electron / Chrome). The phones plug into a
// connected cicy-desktop machine; every action runs a shell command ON that
// machine via the chat-WS sync bridge (same transport BrowserWindowsPanel uses
// for Electron/Chrome):
//   POST /api/chat/push { client_id, type:'desktop_event',
//                         data:{type:'rpc_call', tool:'exec_shell', args:{command}}, wait_ack:true }
// so `adb` / libimobiledevice run where the phones are. This is the UI twin of
// the `agent-mobile` skill.
import { useCallback, useEffect, useState } from 'react';
import { Smartphone, Loader2, Camera, AlertCircle, Send, Home, ChevronLeft, X } from 'lucide-react';
import apiService from '../../services/api';
import { cn } from '../../lib/utils';

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

// Screenshot → base64 → data URL for <img>. Default: JPEG @ 50% quality (≈half
// size); lossless = full-resolution PNG. Conversion via macOS built-in `sips`.
async function capturePhone(clientId: string, sel: MobileSel, lossless = false): Promise<string> {
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
    : `sips -Z 1200 -s format jpeg -s formatOptions 50 ${SRC} --out ${OUT}`;
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

// ── middle column: one phone's screenshot (peer of BrowserWindowsColumn) ──────
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
  const [lossless, setLossless] = useState(false); // default 50% JPEG; toggle = lossless PNG

  const capture = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setShot(await capturePhone(sel.clientId, sel, lossless));
    } catch (e: any) {
      setError(e?.message || '截图失败');
    } finally {
      setLoading(false);
    }
  }, [sel, lossless]);

  useEffect(() => { capture(); }, [capture]);

  const sendKey = useCallback(async (keycode: string) => {
    try { await deviceShell(sel.clientId, `adb -s '${sel.id}' shell input keyevent ${keycode}`); }
    catch { /* best-effort */ }
    setTimeout(capture, 350);
  }, [sel, capture]);

  return (
    <div data-id="mobile-device-column" className="h-full flex flex-col bg-[#0A0A0A]">
      <div data-id="mobile-device-column-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center px-3 gap-2 bg-[#0e0e0e] shrink-0">
        <Smartphone className="w-3.5 h-3.5 text-zinc-500" />
        <span className="text-xs font-medium text-zinc-300 flex-1 min-w-0 truncate">{sel.label}</span>
        <button
          data-id="mobile-device-quality"
          onClick={() => setLossless((v) => !v)}
          title={lossless ? '无损 PNG(点击切回 50% 压缩)' : '50% 压缩(点击切到无损 PNG)'}
          className={cn(
            'px-1.5 py-0.5 rounded text-[10px] font-medium transition-colors cursor-pointer shrink-0',
            lossless ? 'text-emerald-300 bg-emerald-500/10' : 'text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.06]',
          )}
        >
          {lossless ? '无损' : '50%'}
        </button>
        <button
          data-id="mobile-device-capture"
          onClick={capture}
          title="刷新截图"
          className="p-1 rounded text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors cursor-pointer"
        >
          {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Camera className="w-3.5 h-3.5" />}
        </button>
        <button
          data-id="mobile-device-send"
          onClick={() => onSendToAgent(buildMobileAgentPrompt(sel))}
          title="发送给 agent 操控"
          className="p-1 rounded text-zinc-500 hover:text-blue-300 hover:bg-white/[0.06] transition-colors cursor-pointer"
        >
          <Send className="w-3.5 h-3.5" />
        </button>
        <button
          data-id="mobile-device-close"
          onClick={onClose}
          className="p-1 rounded text-zinc-600 hover:text-zinc-300 transition-colors cursor-pointer"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>

      <div data-id="mobile-device-preview" className="flex-1 overflow-auto flex items-start justify-center p-3">
        {error ? (
          <div className="flex flex-col items-center gap-2 text-center mt-8">
            <AlertCircle className="w-6 h-6 text-amber-400/90" />
            <div className="text-xs text-zinc-500 max-w-[240px]">{error}</div>
          </div>
        ) : shot ? (
          <img data-id="mobile-device-shot" src={shot} alt={sel.label} className="max-w-full rounded border border-[var(--vsc-border)]" />
        ) : loading ? (
          <Loader2 className="w-5 h-5 text-zinc-600 animate-spin mt-8" />
        ) : null}
      </div>

      {/* Android-only quick keys (iOS has no input control in v1) */}
      {sel.platform === 'android' ? (
        <div data-id="mobile-device-keys" className="border-t border-[var(--vsc-border)] flex items-center justify-center gap-2 py-2 shrink-0">
          <button
            data-id="mobile-device-key-back"
            onClick={() => sendKey('KEYCODE_BACK')}
            title="返回"
            className="px-3 py-1 rounded text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.06] transition-colors cursor-pointer"
          >
            <ChevronLeft className="w-4 h-4" />
          </button>
          <button
            data-id="mobile-device-key-home"
            onClick={() => sendKey('KEYCODE_HOME')}
            title="主屏"
            className="px-3 py-1 rounded text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.06] transition-colors cursor-pointer"
          >
            <Home className="w-4 h-4" />
          </button>
        </div>
      ) : null}
    </div>
  );
}
