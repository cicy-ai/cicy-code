// artifactBridge.ts — agent-facing remote control for the 产物 (artifact) tab frame.
//
// The 产物 tab hosts a controllable page frame: an Electron <webview> when
// running inside cicy-desktop (window.cicy present), otherwise a plain
// <iframe>. Agents drive it from the chat-WS exec_js channel (see
// Workspace.tsx 'exec_js' handler) by calling the global window.cicyArtifact.*
// API installed here. The `artifact` skill is a thin CLI over that channel.
//
// Control routing (Electron):
//   - methods the <webview> DOM element already exposes (loadURL, reload,
//     getURL, executeJavaScript, insertCSS, capturePage, sendInputEvent, …)
//     are called directly on the element by call().
//   - webContents-only methods + full CDP are routed through the desktop
//     preload bridge window.cicy.artifact.{invoke,cdp}, which drives the
//     webview's webContents from the main process (capturePage/printToPDF come
//     back as base64 so the result survives JSON/WS).
//
// Events (console-message, navigation, the CDP event stream) are forwarded by
// the desktop side as CustomEvent('cicy-artifact-event', {detail}) and buffered
// here into a ring the agent drains with drainEvents() — exec_js is
// request/response, so there is no streaming channel.

export const ARTIFACT_WEBVIEW_ID = 'cicy-artifact-webview';
export const ARTIFACT_EVENT = 'cicy-artifact-event';

// One artifact frame is live at a time; the mounted ArtifactPanel registers its
// controller here and the global API delegates to it.
export interface ArtifactController {
  isElectron(): boolean;
  /** the live <webview> (Electron) or <iframe> (browser) element, or null. */
  getEl(): any | null;
  /** current url known to the panel (state), used when the element can't report it. */
  stateUrl(): string;
  /** activate the 产物 tab and load url. */
  open(url: string): void;
  /** load url without forcing the tab active. */
  setUrl(url: string): void;
  /** reload the frame. */
  reload(): void;
  /** blank the frame. */
  clear(): void;
}

let controller: ArtifactController | null = null;

// ── event ring buffer ────────────────────────────────────────────────────────
const EVENT_CAP = 1000;
const events: any[] = [];
let listenerInstalled = false;

function pushEvent(detail: any) {
  events.push(detail);
  if (events.length > EVENT_CAP) events.splice(0, events.length - EVENT_CAP);
}

function ensureEventListener() {
  if (listenerInstalled || typeof window === 'undefined') return;
  listenerInstalled = true;
  window.addEventListener(ARTIFACT_EVENT, (e: Event) => {
    pushEvent((e as CustomEvent).detail ?? {});
  });
}

// ── helpers ──────────────────────────────────────────────────────────────────
function requireController(): ArtifactController {
  if (!controller) throw new Error('artifact frame not mounted (open the 产物 tab once)');
  return controller;
}

function desktopBridge(): any | null {
  const cicy = (typeof window !== 'undefined' ? (window as any).cicy : null) || null;
  return cicy && cicy.artifact ? cicy.artifact : null;
}

async function awaitMaybe<T>(v: T | Promise<T>): Promise<T> {
  return v && typeof (v as any).then === 'function' ? await (v as any) : (v as T);
}

// ── public registration (called by ArtifactPanel) ────────────────────────────
export function registerArtifactController(c: ArtifactController) {
  controller = c;
  installArtifactBridge();
}

export function unregisterArtifactController(c: ArtifactController) {
  if (controller === c) controller = null;
}

// ── the global API ────────────────────────────────────────────────────────────
export function installArtifactBridge() {
  if (typeof window === 'undefined') return;
  ensureEventListener();
  if ((window as any).cicyArtifact) return; // install once; methods read live `controller`

  const api = {
    // — basics —
    open(url: string) { requireController().open(String(url)); return 'ok'; },
    setUrl(url: string) { requireController().setUrl(String(url)); return 'ok'; },
    load(url: string) { requireController().setUrl(String(url)); return 'ok'; },
    reload() { requireController().reload(); return 'ok'; },
    clear() { requireController().clear(); return 'ok'; },
    isElectron(): boolean { return !!controller && controller.isElectron(); },

    getUrl(): string {
      const c = requireController();
      const el = c.getEl();
      if (c.isElectron() && el && typeof el.getURL === 'function') {
        try { return el.getURL() || c.stateUrl(); } catch { /* fallthrough */ }
      }
      return c.stateUrl();
    },

    info() {
      const c = controller;
      return {
        mounted: !!c,
        electron: !!c && c.isElectron(),
        url: c ? (this as any).getUrl?.() ?? c.stateUrl() : '',
        hasBridge: !!desktopBridge(),
        hasCdp: !!(desktopBridge() && desktopBridge().cdp),
        bufferedEvents: events.length,
      };
    },

    // — run JS inside the inner artifact page —
    async execJs(code: string): Promise<any> {
      const c = requireController();
      const el = c.getEl();
      if (c.isElectron() && el && typeof el.executeJavaScript === 'function') {
        return await el.executeJavaScript(String(code), true);
      }
      const b = desktopBridge();
      if (b && b.invoke) return await b.invoke('executeJavaScript', [String(code), true]);
      // browser <iframe>: only same-origin is reachable.
      if (el && el.contentWindow) {
        try { return (el.contentWindow as any).eval(String(code)); }
        catch (e: any) { throw new Error('execJs into artifact iframe failed (cross-origin?): ' + (e?.message || e)); }
      }
      throw new Error('execJs unavailable (no electron webview / desktop bridge)');
    },

    // — full native control: any <webview> element method or webContents method —
    async call(method: string, ...args: any[]): Promise<any> {
      const c = requireController();
      const el = c.getEl();
      if (c.isElectron() && el && typeof el[method] === 'function') {
        return await awaitMaybe(el[method](...args));
      }
      const b = desktopBridge();
      if (b && b.invoke) return await b.invoke(method, args);
      throw new Error(`artifact.call: '${method}' unavailable (no native element method, no desktop bridge)`);
    },

    // force the main-process webContents path (e.g. for methods cicy-desktop
    // wants to fully own); capturePage/printToPDF already work natively below.
    async invoke(method: string, args: any[] = []): Promise<any> {
      const b = desktopBridge();
      if (!b || !b.invoke) throw new Error('artifact.invoke: desktop bridge (window.cicy.artifact) unavailable');
      return await b.invoke(method, args);
    },

    // screenshot → data URL. Native NativeImage in the trusted renderer
    // (nodeIntegration), else base64 from the desktop bridge.
    async capture(): Promise<string> {
      const c = requireController();
      const el = c.getEl();
      if (c.isElectron() && el && typeof el.capturePage === 'function') {
        const img = await el.capturePage();
        return img && typeof img.toDataURL === 'function' ? img.toDataURL() : String(img);
      }
      const b = desktopBridge();
      if (b && b.invoke) return await b.invoke('capturePage', []);
      throw new Error('capture unavailable (no electron webview / desktop bridge)');
    },

    // render to PDF → base64 string.
    async pdf(opts: any = {}): Promise<string> {
      const c = requireController();
      const el = c.getEl();
      if (c.isElectron() && el && typeof el.printToPDF === 'function') {
        const buf = await el.printToPDF(opts);
        // Buffer/Uint8Array in the trusted renderer (nodeIntegration:true).
        if (buf && typeof buf.toString === 'function') { try { return buf.toString('base64'); } catch { /* fallthrough */ } }
        try {
          let bin = '';
          const u8 = new Uint8Array(buf);
          for (let i = 0; i < u8.length; i++) bin += String.fromCharCode(u8[i]);
          return (window as any).btoa(bin);
        } catch (e: any) { throw new Error('pdf encode failed: ' + (e?.message || e)); }
      }
      const b = desktopBridge();
      if (b && b.invoke) return await b.invoke('printToPDF', [opts]);
      throw new Error('pdf unavailable (no electron webview / desktop bridge)');
    },

    // — CDP (Electron only, via debugger on the webContents) —
    async cdpAttach(protocolVersion?: string): Promise<any> {
      const b = desktopBridge();
      if (!b || !b.cdp) throw new Error('artifact.cdp: desktop bridge unavailable');
      return await b.cdp.attach(protocolVersion);
    },
    async cdpDetach(): Promise<any> {
      const b = desktopBridge();
      if (!b || !b.cdp) throw new Error('artifact.cdp: desktop bridge unavailable');
      return await b.cdp.detach();
    },
    cdpIsAttached(): boolean {
      const b = desktopBridge();
      try { return !!(b && b.cdp && b.cdp.isAttached && b.cdp.isAttached()); } catch { return false; }
    },
    async cdp(method: string, params: any = {}): Promise<any> {
      const b = desktopBridge();
      if (!b || !b.cdp) throw new Error('artifact.cdp: desktop bridge unavailable');
      return await b.cdp.send(method, params);
    },

    // — events / logs (console-message, navigation, CDP event stream) —
    drainEvents(max?: number): any[] {
      const n = typeof max === 'number' && max > 0 ? Math.min(max, events.length) : events.length;
      return events.splice(0, n);
    },
    peekEvents(max?: number): any[] {
      const n = typeof max === 'number' && max > 0 ? Math.min(max, events.length) : events.length;
      return events.slice(events.length - n);
    },
    clearEvents(): string { events.length = 0; return 'ok'; },
  };

  (window as any).cicyArtifact = api;
}
