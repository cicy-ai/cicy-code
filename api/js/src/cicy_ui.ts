import { Terminal, WebTTY, normalizeTerminalText } from "./webtty";
import { ttydT } from "./cicy_i18n";
import { applyMonoFontVar, monoFontStack } from "./font";

interface StorageHelper {
    get(key: string, defaultValue: any): any;
    set(key: string, value: any): void;
}

interface LoadingOverlayController {
    show(message?: string): void;
    hide(): void;
}

interface FilePasteDialogContent {
    element: HTMLElement;
    cleanup?: () => void;
}

function createStorage(): StorageHelper {
    return {
        get(key: string, defaultValue: any): any {
            try {
                var value = localStorage.getItem(key);
                return value !== null ? JSON.parse(value) : defaultValue;
            } catch (_error) {
                return defaultValue;
            }
        },
        set(key: string, value: any): void {
            localStorage.setItem(key, JSON.stringify(value));
        },
    };
}

function queryToken(): string {
    var match = location.search.match(/[?&]token=([^&]+)/);
    return match ? decodeURIComponent(match[1]) : "";
}

function queryPaneId(): string {
    var match = location.pathname.match(/\/ttyd\/([^\/]+)/);
    return match ? match[1] : "default";
}

function createAPIHeaders(token: string): { [key: string]: string } {
    var headers: { [key: string]: string } = {
        "Content-Type": "application/json",
    };
    if (token) {
        headers["Authorization"] = "Bearer " + token;
    }
    return headers;
}

function ensureTmuxSendSucceeded(payload: any): any {
    if (!payload || typeof payload !== "object") {
        return payload;
    }
    var errorText = "";
    if (typeof payload.error === "string" && payload.error.trim()) {
        errorText = payload.error.trim();
    } else if (typeof payload.detail === "string" && payload.detail.trim()) {
        errorText = payload.detail.trim();
    }
    if (errorText) {
        throw createTmuxSendError(errorText, payload);
    }
    if (payload.success === false) {
        throw createTmuxSendError("tmux send failed", payload);
    }
    return payload;
}

function createTmuxSendError(message: string, payload?: any, statusCode?: number, isNetworkError?: boolean): any {
    var error: any = new Error(message || "request failed");
    if (typeof statusCode === "number" && isFinite(statusCode)) {
        error.statusCode = statusCode;
    }
    error.isNetworkError = isNetworkError === true;
    if (payload && typeof payload === "object") {
        error.detail = typeof payload.detail === "string" ? payload.detail : "";
        error.paneUpdated = payload.pane_updated === true;
        if (typeof payload.restore_input === "boolean") {
            error.restoreInput = payload.restore_input;
        } else if (error.paneUpdated) {
            error.restoreInput = false;
        }
    }
    return error;
}

function formatPastedFileSize(size: number): string {
    if (size < 1024) {
        return String(size) + " B";
    }
    if (size < 1024 * 1024) {
        return (size / 1024).toFixed(size < 10 * 1024 ? 1 : 0) + " KB";
    }
    return (size / (1024 * 1024)).toFixed(size < 10 * 1024 * 1024 ? 1 : 0) + " MB";
}

function clipTracePreview(text: string, maxLen: number): string {
    var normalized = String(text || "").replace(/\s+/g, " ").trim();
    if (normalized.length <= maxLen) {
        return normalized;
    }
    return normalized.slice(0, maxLen);
}

function installStyles(): void {
    if (document.getElementById("cicy-ttyd-source-style") !== null) {
        return;
    }

    applyMonoFontVar(document);
    var style = document.createElement("style");
    style.id = "cicy-ttyd-source-style";
    style.textContent = `
:root {
  --cp-mono-font: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
html, body, #terminal, .terminal {
  height: 100% !important;
  width: 100% !important;
  padding: 0 !important;
  margin: 0 !important;
  box-sizing: border-box !important;
}
html {
  overflow: hidden !important;
}
body {
  margin: 8px !important;
  padding-top: 0 !important;
  padding-left: 8px !important;
}
.terminal {
  font-size: 13px !important;
  color: #b9adad !important;
  font-family: var(--cp-mono-font);
}
::-webkit-scrollbar { width: 4px; height: 4px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.15); border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.3); }
::-webkit-scrollbar-corner { background: transparent; }
.xterm-overlay { display: none !important; }
.xterm-reconnect-overlay div:not(.xterm-reconnect-spinner) { display: none !important; }
.xterm-reconnect-overlay button { display: block !important; }
.terminal .xterm-rows {
  color: #b9adad !important;
}
/* Hide the agent (codex) bottom status row that leaks the model name. The row
   has a distinctive span sequence: <colored model> <dim " · "> <colored cwd>.
   Match that structure via :has() (colored + dim + colored) so it's robust to
   the codex theme's exact colors, and so we hit exactly that line — it isn't
   the literal last child (xterm pads empty rows below it). visibility:hidden
   keeps the row's space so the layout doesn't shift. */
.terminal .xterm-rows > div:has(> [class*="xterm-fg-"] + .xterm-dim + [class*="xterm-fg-"]) {
  visibility: hidden !important;
}
#cp-loading-overlay {
  position: fixed;
  inset: 0;
  z-index: 99999;
  background: rgba(10,10,11,0.92);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  transition: opacity 0.3s;
}
#cp-loading-overlay.cp-fade-out { opacity: 0; pointer-events: none; }
#cp-loading-spinner {
  width: 32px;
  height: 32px;
  border: 2px solid rgba(0,122,204,0.3);
  border-top-color: #007acc;
  border-radius: 50%;
  animation: cp-spin 0.8s linear infinite;
  margin-bottom: 16px;
}
#cp-loading-text {
  color: rgba(255,255,255,0.5);
  font-size: 13px;
  font-family: var(--cp-mono-font);
  letter-spacing: 0.3px;
}
#cp {
  position: fixed;
  left: 8px;
  right: 8px;
  bottom: 8px;
  height: 104px;
  min-width: 0;
  background: rgba(16,16,20,0.88);
  border: 1px solid rgba(255,255,255,0.07);
  border-radius: 14px;
  z-index: 9999;
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
  font-family: var(--cp-mono-font);
  box-shadow: 0 8px 32px rgba(0,0,0,0.55), 0 0 0 0.5px rgba(255,255,255,0.08) inset;
  display: flex;
  flex-direction: column;
  transition: box-shadow .2s;
}
#cp:hover { box-shadow: 0 12px 48px rgba(0,0,0,0.65), 0 0 0 0.5px rgba(255,255,255,0.1) inset; }
#cp-bar {
  cursor: default;
  padding: 4px 10px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  user-select: none;
  min-height: 24px;
}
.cp-bar-l, .cp-bar-r { display: flex; align-items: center; gap: 4px; }
.cp-chip {
  background: rgba(255,255,255,0.06);
  border: none;
  color: rgba(255,255,255,0.6);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 7px;
  cursor: pointer;
  font-family: inherit;
  transition: all .12s;
  display: flex;
  align-items: center;
  gap: 3px;
  white-space: nowrap;
}
.cp-chip:hover { background: rgba(255,255,255,0.12); color: rgba(255,255,255,0.85); }
.cp-chip-dim { color: rgba(255,255,255,0.4); padding: 3px 6px; }
.cp-chip-dim:hover { color: rgba(255,255,255,0.7); }
#cp-sigint {
  width: 28px;
  min-width: 28px;
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}
#cp-enter-key {
  width: 28px;
  min-width: 28px;
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}
#cp-up-key,
#cp-down-key {
  width: 28px;
  min-width: 28px;
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}
#cp-esc-key {
  width: 32px;
  min-width: 32px;
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}
#cp-backspace-key {
  width: 32px;
  min-width: 32px;
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}
.cp-tooltip-host {
  position: relative;
}
.cp-tooltip-host::after {
  content: attr(data-tooltip);
  position: absolute;
  left: 50%;
  bottom: calc(100% + 8px);
  transform: translateX(-50%) translateY(4px);
  padding: 6px 8px;
  border-radius: 8px;
  /* Match cp-drop / cp-modal surface tone so tooltips read as part of the
     same dark chrome layer instead of a slightly-blue oddball. */
  background: rgba(22,22,26,0.97);
  border: 1px solid rgba(255,255,255,0.09);
  color: rgba(255,255,255,0.92);
  font-size: 11px;
  line-height: 1.2;
  white-space: nowrap;
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  box-shadow: 0 12px 40px rgba(0,0,0,0.6);
  pointer-events: none;
  opacity: 0;
  transition: opacity .12s ease, transform .12s ease;
}
.cp-tooltip-host::before {
  content: "";
  position: absolute;
  left: 50%;
  bottom: calc(100% + 4px);
  width: 8px;
  height: 8px;
  background: rgba(22,22,26,0.97);
  border-right: 1px solid rgba(255,255,255,0.09);
  border-bottom: 1px solid rgba(255,255,255,0.09);
  transform: translateX(-50%) rotate(45deg);
  pointer-events: none;
  opacity: 0;
  transition: opacity .12s ease;
}
.cp-tooltip-host:hover::after,
.cp-tooltip-host:hover::before,
.cp-tooltip-host:focus-visible::after,
.cp-tooltip-host:focus-visible::before {
  opacity: 1;
}
.cp-tooltip-host:hover::after,
.cp-tooltip-host:focus-visible::after {
  transform: translateX(-50%) translateY(0);
}
.cp-tooltip-host.cp-tooltip-left::after {
  left: 0;
  right: auto;
  transform: translateX(0) translateY(4px);
  transform-origin: left bottom;
  text-align: left;
}
.cp-tooltip-host.cp-tooltip-multiline::after {
  white-space: pre-line;
  min-width: 260px;
  max-width: 320px;
  padding: 8px 12px;
  line-height: 1.45;
}
.cp-tooltip-host.cp-tooltip-left::before {
  left: 6px;
  right: auto;
  transform: translateX(0) rotate(45deg);
}
.cp-tooltip-host.cp-tooltip-right::after {
  left: auto;
  right: 0;
  transform: translateX(0) translateY(4px);
  transform-origin: right bottom;
  text-align: left;
}
.cp-tooltip-host.cp-tooltip-right::before {
  left: auto;
  right: 6px;
  transform: translateX(0) rotate(45deg);
}
.cp-tooltip-host.cp-tooltip-left:hover::after,
.cp-tooltip-host.cp-tooltip-left:focus-visible::after {
  transform: translateX(0) translateY(0);
}
.cp-tooltip-host.cp-tooltip-right:hover::after,
.cp-tooltip-host.cp-tooltip-right:focus-visible::after {
  transform: translateX(0) translateY(0);
}
.cp-tooltip-host.cp-tooltip-force::after,
.cp-tooltip-host.cp-tooltip-force::before {
  opacity: 1;
}
.cp-tooltip-host.cp-tooltip-force::after {
  transform: translateX(-50%) translateY(0);
}
.cp-tooltip-host.cp-tooltip-left.cp-tooltip-force::after {
  transform: translateX(0) translateY(0);
}
.cp-tooltip-host.cp-tooltip-right.cp-tooltip-force::after {
  transform: translateX(0) translateY(0);
}
.cp-tooltip-host.cp-tooltip-bottom::after {
  bottom: auto;
  top: calc(100% + 8px);
  transform: translateX(-50%) translateY(-4px);
  transform-origin: center top;
}
.cp-tooltip-host.cp-tooltip-bottom::before {
  bottom: auto;
  top: calc(100% + 4px);
  transform: translateX(-50%) rotate(225deg);
}
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-right::after {
  left: auto;
  right: 0;
  transform: translateX(0) translateY(-4px);
  transform-origin: right top;
}
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-right::before {
  left: auto;
  right: 6px;
  transform: translateX(0) rotate(225deg);
}
.cp-tooltip-host.cp-tooltip-bottom:hover::after,
.cp-tooltip-host.cp-tooltip-bottom:focus-visible::after,
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-force::after {
  transform: translateX(-50%) translateY(0);
}
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-right:hover::after,
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-right:focus-visible::after,
.cp-tooltip-host.cp-tooltip-bottom.cp-tooltip-right.cp-tooltip-force::after {
  transform: translateX(0) translateY(0);
}
.cp-drop {
  display: none;
  position: absolute;
  top: calc(100% + 5px);
  background: rgba(22,22,26,0.97);
  border: 1px solid rgba(255,255,255,0.09);
  border-radius: 10px;
  padding: 5px;
  min-width: 170px;
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  box-shadow: 0 12px 40px rgba(0,0,0,0.6);
  z-index: 10000;
  flex-direction: column;
  gap: 2px;
}
.cp-drop.open { display: flex; }
#cp-win-float {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 9998;
  display: flex;
  align-items: center;
  height: 36px;
  background: rgba(30,30,30,0.95);
  border-bottom: 1px solid rgba(255,255,255,0.06);
  font-family: var(--cp-mono-font);
  padding: 0 4px;
  gap: 0;
  /* Auto-hide: fully off-screen by default. A JS-bound mousemove listener
     (see setupWinFloatAutoHide below) reveals the bar when the cursor is
     within 8px of the viewport top, or while it's hovering the bar itself.
     Body padding-top stays at 36px so the terminal layout doesn't shift. */
  transform: translateY(-36px);
  opacity: 0;
  transition: transform .18s ease, opacity .18s ease;
  pointer-events: none;
}
#cp-win-float.cp-win-float-show {
  transform: translateY(0);
  opacity: 1;
  pointer-events: auto;
}
#cp-win-float:hover,
#cp-win-float:focus-within {
  transform: translateY(0);
  opacity: 1;
  pointer-events: auto;
}
#fixed-top-action {
  /* No longer position: fixed — sits as the rightmost flex child of
     #cp-win-float so it auto-hides together with the bar. */
  margin-left: auto;
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-radius: 11px;
  background: rgba(22,22,28,0.5);
  border: 1px solid rgba(255,255,255,0.06);
  backdrop-filter: blur(16px) saturate(1.3);
  -webkit-backdrop-filter: blur(16px) saturate(1.3);
  box-shadow: 0 2px 12px rgba(0,0,0,0.3);
  font-family: var(--cp-mono-font);
  transition: opacity .18s ease;
}
.fta-sep {
  width: 1px;
  height: 14px;
  background: rgba(255,255,255,0.1);
  margin: 0 3px;
}
.fta-btn {
  width: 26px;
  height: 26px;
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  border-radius: 8px;
  color: rgba(255,255,255,0.5);
  font-size: 13px;
  line-height: 1;
  padding: 0;
  cursor: pointer;
  font-family: var(--cp-mono-font);
  transition: background .15s, color .15s, transform .1s;
}
.fta-btn:hover { color: #fff; background: rgba(255,255,255,0.1); }
.fta-btn:active { transform: scale(0.88); }
#cp-win-tabs {
  display: flex;
  align-items: center;
  height: 100%;
  flex: 1;
  min-width: 0;
  overflow-x: auto;
}
#cp-win-tabs::-webkit-scrollbar { display: none; }
.cp-wtab {
  position: relative;
  background: none;
  border: none;
  color: rgba(255,255,255,0.35);
  font-size: 12px;
  font-family: inherit;
  height: 100%;
  min-width: 100px;
  padding: 0 36px;
  cursor: pointer;
  transition: all .1s;
  white-space: nowrap;
  border-right: 1px solid rgba(255,255,255,0.04);
  overflow: visible;
}
.cp-wtab:hover { color: rgba(255,255,255,0.7); background: rgba(255,255,255,0.04); }
.cp-wtab.active { color: rgba(255,255,255,0.9); background: rgba(255,255,255,0.07); }
.cp-wtab .cp-wdel {
  position: absolute;
  top: 1px;
  right: 1px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: rgba(255,255,255,0.1);
  color: rgba(255,255,255,0.5);
  font-size: 11px;
  line-height: 18px;
  text-align: center;
  cursor: pointer;
  display: none;
  transition: all .1s;
  align-items: center;
  justify-content: center;
  overflow: visible;
  z-index: 3;
}
.cp-wtab:hover .cp-wdel { display: inline-flex; }
.cp-wtab .cp-wdel:hover { background: rgba(239,68,68,0.8); color: #fff; }
#cp-win-restart {
  width: 26px;
  min-width: 26px;
  height: 26px;
}
#cp-win-restart.restarting { color: rgba(0,122,204,0.85); pointer-events: none; }
#cp-more-menu { z-index: 10001; }
.cp-drop-item {
  background: none;
  border: none;
  color: rgba(255,255,255,0.6);
  font-size: 11px;
  padding: 6px 10px;
  border-radius: 7px;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
  transition: all .12s;
}
.cp-drop-item:hover { background: rgba(255,255,255,0.08); color: #fff; }
.cp-drop-danger:hover { background: rgba(239,68,68,0.15); color: #f87171; }
#cp-fixed-tooltip {
  position: fixed;
  left: 0;
  top: 0;
  z-index: 10002;
  padding: 6px 8px;
  border-radius: 8px;
  background: rgba(34,37,46,0.97);
  border: 1px solid rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.92);
  font-size: 11px;
  font-family: var(--cp-mono-font);
  line-height: 1.2;
  white-space: nowrap;
  box-shadow: 0 10px 30px rgba(0,0,0,0.45);
  pointer-events: none;
  opacity: 0;
  transform: translateY(-4px);
  transition: opacity .12s ease, transform .12s ease;
}
#cp-fixed-tooltip.open {
  opacity: 1;
  transform: translateY(0);
}
#cp-model {
  background: rgba(255,255,255,0.05);
  border: none;
  color: rgba(255,255,255,0.6);
  font-size: 11px;
  padding: 6px 10px;
  border-radius: 7px;
  cursor: pointer;
  font-family: var(--cp-mono-font);
  outline: none;
  width: 100%;
}
#cp-model:hover { background: rgba(255,255,255,0.1); }
.cp-win-wrap, .cp-bar-r { position: relative; }
#cp-body { padding: 0 8px 8px; height: 64px; display: none; position: relative; min-height: 0; box-sizing: border-box; }
#cp-input {
  width: 100%;
  background: rgba(255,255,255,0.03);
  border: 1px solid rgba(255,255,255,0.06);
  border-radius: 10px;
  color: #e4e4e7;
  font-size: 13px;
  font-family: var(--cp-mono-font) !important;
  font-variant-ligatures: none;
  font-feature-settings: "liga" 0, "calt" 0;
  letter-spacing: 0;
  padding: 8px 40px 8px 12px;
  resize: none;
  outline: none;
  box-sizing: border-box;
  flex: 1;
  min-height: 0;
  line-height: 1.35;
  transition: border-color .15s, background .15s;
}
#cp-input::placeholder { color: rgba(255,255,255,0.2); }
#cp-input:focus { border-color: rgba(99,102,241,0.4); background: rgba(255,255,255,0.04); }
/* ── bottom prompt bar ──────────────────────────────────────────────────
   A compact, single-row composer docked at the bottom. When open, the
   xterm viewport's bottom is pushed up by exactly this bar's footprint
   (height + bottom offset + gap) so nothing is ever covered. */
#cp-prompt {
  position: fixed;
  left: 8px;
  right: 8px;
  bottom: 8px;
  height: 56px;
  display: none;
  align-items: center;
  gap: 8px;
  padding: 0 8px 0 14px;
  box-sizing: border-box;
  background: rgba(20,20,26,0.94);
  border: 1px solid rgba(255,255,255,0.09);
  border-radius: 14px;
  z-index: 9998;
  backdrop-filter: blur(28px) saturate(1.4);
  -webkit-backdrop-filter: blur(28px) saturate(1.4);
  box-shadow: 0 6px 28px rgba(0,0,0,0.5), 0 0 0 0.5px rgba(255,255,255,0.06) inset;
}
#cp-prompt.open { display: flex; }
body.cp-prompt-open { padding-bottom: 74px !important; }

#cp-prompt #cp-input {
  flex: 1;
  min-width: 0;
  height: 32px;
  margin: 0;
  padding: 7px 10px;
  background: transparent;
  border: none;
  border-radius: 0;
  color: #e8e8ec;
  font-size: 13px;
  font-family: var(--cp-mono-font) !important;
  font-variant-ligatures: none;
  line-height: 1.3;
  resize: none;
  outline: none;
  box-sizing: border-box;
  scrollbar-width: thin;
  vertical-align: middle;
}
#cp-prompt #cp-input::placeholder { color: rgba(255,255,255,0.28); }
#cp-prompt #cp-input:focus { background: transparent; border: none; }

/* enter and send share the same 32px height so they read as a paired control */
#cp-prompt #cp-enter,
#cp-prompt #cp-send {
  flex: 0 0 auto;
  height: 32px;
  min-width: 32px;
  border: none;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-family: var(--cp-mono-font);
  line-height: 1;
  box-sizing: border-box;
  padding: 0;
}
#cp-prompt #cp-enter {
  position: static;
  padding: 0 9px;
  border-radius: 9px;
  background: rgba(255,255,255,0.06);
  color: rgba(255,255,255,0.55);
  font-size: 12px;
  transition: background .15s, color .15s;
}
#cp-prompt #cp-enter:hover { background: rgba(255,255,255,0.13); color: rgba(255,255,255,0.9); }

#cp-prompt #cp-send {
  position: relative;
  width: 32px;
  border-radius: 9px;
  background: rgba(99,102,241,0.92);
  color: #fff;
  font-size: 14px;
  transition: background .15s, transform .1s;
}
#cp-prompt #cp-send:hover { background: rgba(99,102,241,1); }
#cp-prompt #cp-send:active { transform: scale(0.9); }
#cp-prompt #cp-send.sending { pointer-events: none; color: transparent; background: rgba(0,122,204,0.7); }
#cp-prompt #cp-send.sending::after {
  content: "";
  position: absolute;
  width: 14px;
  height: 14px;
  border: 2px solid rgba(0,122,204,0.3);
  border-top-color: #007acc;
  border-radius: 50%;
  animation: cp-spin 0.8s linear infinite;
}

.fta-btn.active { color: rgba(165,180,252,1) !important; background: rgba(99,102,241,0.28); }
.fta-btn.active:hover { background: rgba(99,102,241,0.38); }
#cp.collapsed #cp-body, #cp.collapsed #cp-grip { display: none; }
#cp.collapsed { min-height: 0 !important; height: auto !important; }
#cp.collapsed #cp-bar { border-bottom: none; }
#cp-grip { display: none; }
#cp:hover #cp-grip { opacity: 0; }
#cp-grip:hover { opacity: 0 !important; }
#cp-grip::after {
  content: "";
  position: absolute;
  right: 4px;
  bottom: 4px;
  width: 6px;
  height: 6px;
  border-right: 1.5px solid rgba(255,255,255,0.4);
  border-bottom: 1.5px solid rgba(255,255,255,0.4);
}
.cp-confirm { animation: cp-pulse .5s ease-in-out infinite alternate; }
#cp-mic.active { background: rgba(220,38,38,0.2); color: #f87171; }
#cp-mic.active:hover { background: rgba(220,38,38,0.3); }
#cp-mic { display: none !important; }
#vm-overlay {
  display: none;
  position: fixed;
  inset: 0;
  z-index: 9998;
  pointer-events: none;
  font-family: var(--cp-mono-font);
  background: transparent;
  transition: background .3s;
}
#vm-overlay.open { display: block; pointer-events: auto; }
#vm-center {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  pointer-events: auto;
}
#vm-btn {
  width: 110px;
  height: 110px;
  border-radius: 50%;
  background: rgba(24,24,30,0.92);
  border: 2px solid rgba(255,255,255,0.1);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: all .2s;
  user-select: none;
  touch-action: none;
  position: relative;
  z-index: 2;
  box-shadow: 0 8px 32px rgba(0,0,0,0.5);
}
#vm-btn:hover { background: rgba(32,32,40,0.95); border-color: rgba(255,255,255,0.15); }
#vm-icon { color: rgba(255,255,255,0.5); transition: all .2s; }
#vm-overlay.rec #vm-btn {
  background: rgba(220,38,38,0.85);
  border-color: rgba(255,255,255,0.2);
  box-shadow: 0 0 60px rgba(220,38,38,0.4);
  transform: scale(1.05);
}
#vm-overlay.rec #vm-icon { color: #fff; transform: scale(1.15); }
#vm-ripple1, #vm-ripple2 {
  position: absolute;
  border-radius: 50%;
  pointer-events: none;
  opacity: 0;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
}
#vm-ripple1 { width: 150px; height: 150px; border: 2px solid rgba(220,38,38,0.3); }
#vm-ripple2 { width: 190px; height: 190px; border: 1.5px solid rgba(220,38,38,0.15); }
#vm-overlay.rec #vm-ripple1 { animation: vm-ping 1.5s cubic-bezier(0,0,0.2,1) infinite; }
#vm-overlay.rec #vm-ripple2 { animation: vm-ping 2s cubic-bezier(0,0,0.2,1) infinite .4s; }
#vm-hint, #vm-status { display: none !important; }
#vm-overlay.processing #vm-btn {
  background: rgba(99,102,241,0.3);
  border-color: rgba(99,102,241,0.3);
  box-shadow: 0 0 40px rgba(99,102,241,0.2);
}
#vm-overlay.processing #vm-icon { color: rgba(129,140,248,0.8); animation: vm-spin 1.5s linear infinite; }
#cp-paste-confirm-overlay {
  position: fixed;
  inset: 0;
  z-index: 10003;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  padding: 24px;
  background: rgba(0,0,0,0.55);
  box-sizing: border-box;
}
#cp-paste-confirm-modal {
  width: min(760px, 100%);
  margin-left: 0;
  max-height: min(80vh, 720px);
  border-radius: 14px;
  background: #111214;
  border: 1px solid rgba(255,255,255,0.08);
  box-shadow: 0 24px 80px rgba(0,0,0,0.45);
  padding: 18px;
  color: #f5f5f5;
  font-family: var(--cp-mono-font);
  display: flex;
  flex-direction: column;
  gap: 12px;
}
#cp-paste-confirm-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}
#cp-paste-confirm-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: rgba(255,255,255,0.72);
}
#cp-paste-confirm-body {
  min-height: 160px;
  max-height: min(52vh, 520px);
  overflow: auto;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(255,255,255,0.04);
  border: 1px solid rgba(255,255,255,0.06);
  color: #8bd5ff;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  width: 100%;
  resize: vertical;
  box-sizing: border-box;
  font-family: var(--cp-mono-font);
}
#cp-paste-confirm-actions {
  display: flex;
  justify-content: flex-start;
  gap: 10px;
}
.cp-paste-confirm-btn {
  appearance: none;
  border: none;
  border-radius: 10px;
  padding: 9px 14px;
  font-size: 13px;
  cursor: pointer;
  font-family: var(--cp-mono-font);
}
#cp-paste-confirm-cancel {
  background: rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.86);
}
#cp-paste-confirm-send {
  background: #2f6df6;
  color: #fff;
}
#cp-file-paste-overlay {
  position: fixed;
  inset: 0;
  z-index: 10004;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 28px;
  background: rgba(5,8,14,0.72);
  box-sizing: border-box;
}
#cp-file-paste-modal {
  width: min(820px, 100%);
  max-height: min(84vh, 780px);
  overflow: hidden;
  border-radius: 18px;
  background: linear-gradient(180deg, rgba(14,18,28,0.98), rgba(10,13,20,0.98));
  border: 1px solid rgba(140,170,255,0.18);
  box-shadow: 0 32px 90px rgba(0,0,0,0.55);
  color: #f5f7fb;
  font-family: var(--cp-mono-font);
}
#cp-file-paste-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 18px 20px 14px;
  border-bottom: 1px solid rgba(255,255,255,0.08);
}
#cp-file-paste-eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgba(143,190,255,0.92);
}
#cp-file-paste-eyebrow::before {
  content: "";
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: #5ea0ff;
  box-shadow: 0 0 16px rgba(94,160,255,0.8);
}
#cp-file-paste-title {
  margin: 10px 0 0;
  font-size: 20px;
  line-height: 1.2;
}
#cp-file-paste-desc {
  margin: 8px 0 0;
  color: rgba(255,255,255,0.72);
  font-size: 13px;
  line-height: 1.6;
}
#cp-file-paste-desc:empty {
  display: none;
}
#cp-file-paste-close {
  appearance: none;
  border: none;
  border-radius: 10px;
  width: 36px;
  height: 36px;
  background: rgba(255,255,255,0.06);
  color: rgba(255,255,255,0.82);
  font-size: 18px;
  cursor: pointer;
  flex: 0 0 auto;
}
#cp-file-paste-body {
  display: block;
  max-height: calc(min(84vh, 780px) - 160px);
}
#cp-file-paste-preview {
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  min-height: 280px;
  padding: 18px;
  overflow: auto;
  background: rgba(255,255,255,0.02);
}
#cp-file-paste-preview.image-only {
  min-height: 0;
}
#cp-file-paste-preview img {
  display: block;
  width: auto;
  height: auto;
  max-width: 100%;
  max-height: 100%;
  margin: 0;
  border-radius: 12px;
  background: rgba(255,255,255,0.03);
}
.cp-file-paste-label {
  margin: 0 0 10px;
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgba(143,190,255,0.78);
}
#cp-file-paste-meta {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.cp-file-paste-meta-row {
  padding: 10px 12px;
  border-radius: 12px;
  background: rgba(255,255,255,0.04);
  border: 1px solid rgba(255,255,255,0.05);
}
.cp-file-paste-meta-key {
  display: block;
  margin-bottom: 6px;
  font-size: 11px;
  color: rgba(255,255,255,0.48);
  text-transform: uppercase;
}
.cp-file-paste-meta-value {
  display: block;
  color: #f5f7fb;
  font-size: 13px;
  line-height: 1.5;
  word-break: break-word;
}
#cp-file-paste-list {
  margin: 14px 0 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.cp-file-paste-list-item {
  padding: 10px 12px;
  border-radius: 12px;
  background: rgba(255,255,255,0.04);
  border: 1px solid rgba(255,255,255,0.05);
}
.cp-file-paste-list-name {
  display: block;
  font-size: 13px;
  color: #f5f7fb;
  word-break: break-word;
}
.cp-file-paste-list-meta {
  display: block;
  margin-top: 6px;
  font-size: 12px;
  color: rgba(255,255,255,0.6);
}
#cp-file-paste-actions {
  display: flex;
  justify-content: flex-start;
  gap: 10px;
  padding: 16px 20px 20px;
  border-top: 1px solid rgba(255,255,255,0.08);
}
.cp-file-paste-btn {
  appearance: none;
  border: none;
  border-radius: 12px;
  padding: 10px 16px;
  font-size: 13px;
  cursor: pointer;
  font-family: var(--cp-mono-font);
}
#cp-file-paste-cancel {
  background: rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.86);
}
#cp-file-paste-send {
  background: linear-gradient(135deg, #3b82f6, #2563eb);
  color: #fff;
}
@media (max-width: 760px) {
  #cp-file-paste-overlay {
    padding: 14px;
  }
}
#cp.voice-mode {
  top: 8px !important;
  right: 8px !important;
  left: auto !important;
  width: auto !important;
  height: auto !important;
  min-width: 0 !important;
}
#cp.voice-mode #cp-body, #cp.voice-mode #cp-grip { display: none; }
#cp.voice-mode #cp-bar { border-bottom: none; }
@keyframes cp-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes cp-pulse { from { opacity: 1; } to { opacity: .4; } }
@keyframes vm-ping { 0% { transform: translate(-50%, -50%) scale(0.85); opacity: 1; } 100% { transform: translate(-50%, -50%) scale(1.3); opacity: 0; } }
@keyframes vm-spin { to { transform: rotate(360deg); } }
.cp-modal-overlay {
  position: fixed; inset: 0; z-index: 2147483600;
  background: rgba(0,0,0,0.55);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
  display: flex; align-items: center; justify-content: center;
  font-family: var(--cp-mono-font);
  animation: cp-fadein .12s ease-out;
}
@keyframes cp-fadein { from { opacity: 0; } to { opacity: 1; } }
.cp-modal-card {
  width: 100%; max-width: 360px; margin: 0 16px;
  background: #161618; border: 1px solid rgba(255,255,255,0.08);
  border-radius: 14px; overflow: hidden;
  box-shadow: 0 24px 64px rgba(0,0,0,0.6);
}
.cp-modal-body { padding: 18px 20px 6px; color: #d4d4d8; font-size: 13px; line-height: 1.5; }
.cp-modal-title { color: #fff; font-weight: 600; font-size: 14px; margin-bottom: 4px; }
.cp-modal-actions { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 16px 14px; }
.cp-modal-btn {
  height: 30px; padding: 0 12px; border-radius: 8px;
  border: none; font-size: 13px; cursor: pointer; font-family: inherit;
  white-space: nowrap;
  transition: background .12s ease, color .12s ease;
}
.cp-modal-btn-cancel { background: transparent; color: #a1a1aa; }
.cp-modal-btn-cancel:hover { background: rgba(255,255,255,0.05); color: #e4e4e7; }
.cp-modal-btn-ok { background: #fff; color: #0b0b0c; font-weight: 500; }
.cp-modal-btn-ok:hover { background: #e4e4e7; }
.cp-modal-btn-danger { background: rgba(239,68,68,0.18); color: #fca5a5; border: 1px solid rgba(239,68,68,0.3); }
.cp-modal-btn-danger:hover { background: rgba(239,68,68,0.3); color: #fecaca; }
`;
    document.head.appendChild(style);
}

// Vanilla-JS modal confirm. Mirrors the React useDialogs().confirm() pattern
// from app/src/components/ui/Modal.tsx so destructive actions across the
// whole app (including the ttyd-injected float bar) ask through one
// consistent dialog instead of inline "click again to confirm" hacks.
function cpModalConfirm(opts: { title?: string; body: string; confirmLabel?: string; cancelLabel?: string; danger?: boolean }): Promise<boolean> {
    return new Promise(function(resolve) {
        var overlay = document.createElement("div");
        overlay.className = "cp-modal-overlay";
        var card = document.createElement("div");
        card.className = "cp-modal-card";
        var bodyHtml = "";
        if (opts.title) bodyHtml += '<div class="cp-modal-title">' + escapeHtmlText(opts.title) + '</div>';
        bodyHtml += '<div>' + escapeHtmlText(opts.body) + '</div>';
        card.innerHTML =
            '<div class="cp-modal-body">' + bodyHtml + '</div>' +
            '<div class="cp-modal-actions">' +
                '<button type="button" class="cp-modal-btn cp-modal-btn-cancel">' + escapeHtmlText(opts.cancelLabel || ttydT("cancel")) + '</button>' +
                '<button type="button" class="cp-modal-btn ' + (opts.danger ? 'cp-modal-btn-danger' : 'cp-modal-btn-ok') + '">' + escapeHtmlText(opts.confirmLabel || ttydT("confirm")) + '</button>' +
            '</div>';
        overlay.appendChild(card);
        document.body.appendChild(overlay);

        var btns = card.querySelectorAll("button");
        var cancelBtn = btns[0] as HTMLButtonElement;
        var okBtn = btns[1] as HTMLButtonElement;
        var done = false;
        function finish(v: boolean): void {
            if (done) return;
            done = true;
            document.removeEventListener("keydown", onKey, true);
            overlay.remove();
            resolve(v);
        }
        function onKey(e: KeyboardEvent): void {
            if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finish(false); }
            else if (e.key === "Enter") { e.preventDefault(); finish(true); }
        }
        overlay.addEventListener("mousedown", function(e) {
            if (e.target === overlay) finish(false);
        });
        card.addEventListener("mousedown", function(e) { e.stopPropagation(); });
        cancelBtn.addEventListener("click", function() { finish(false); });
        okBtn.addEventListener("click", function() { finish(true); });
        document.addEventListener("keydown", onKey, true);
        setTimeout(function() { okBtn.focus(); }, 0);
    });
}

function escapeHtmlText(s: string): string {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function(c) {
        return c === "&" ? "&amp;"
            : c === "<" ? "&lt;"
            : c === ">" ? "&gt;"
            : c === "\"" ? "&quot;"
            : "&#39;";
    });
}

// Reveals #cp-win-float when the cursor enters the top of the viewport, and
// keeps it shown while the cursor stays within the bar's footprint (or any
// dropdown / menu it opens). Two thresholds, tuned for low strobe risk:
//
//   - Hidden state: only the top REVEAL_PX strip triggers a show. Kept
//     narrow so users don't accidentally pop the bar while reaching for
//     terminal content near the top.
//   - Shown state: the bar's full BAR_HEIGHT_PX plus a margin is the
//     "stay open" zone. Once visible, the user has to actually move past
//     the bar (not just inside it) before we start the hide timer.
//
// The hide is delayed by HIDE_DELAY_MS so a quick mouse-flick across the
// top doesn't strobe the bar in and out.
function setupWinFloatAutoHide(bar: HTMLElement): void {
    var REVEAL_PX = 12;        // top strip that triggers reveal when hidden
    var BAR_HEIGHT_PX = 36;    // matches #cp-win-float css height
    var KEEP_OPEN_PX = BAR_HEIGHT_PX + 12; // grace margin below bar
    var HIDE_DELAY_MS = 600;   // long enough to forgive jittery cursors
    var hideTimer: number | null = null;

    function reveal(): void {
        if (hideTimer !== null) {
            clearTimeout(hideTimer);
            hideTimer = null;
        }
        bar.classList.add("cp-win-float-show");
    }
    function scheduleHide(): void {
        if (hideTimer !== null) return;
        hideTimer = window.setTimeout(function() {
            hideTimer = null;
            // Final guard: don't hide while the user is still hovering
            // the bar (or one of its descendants, e.g. an open tab menu).
            if (!bar.matches(":hover") && !bar.matches(":focus-within")) {
                bar.classList.remove("cp-win-float-show");
            }
        }, HIDE_DELAY_MS);
    }

    document.addEventListener("mousemove", function(e) {
        var shown = bar.classList.contains("cp-win-float-show");
        // Threshold widens once visible so moving inside the bar's region
        // counts as "still hovering" even if the bar's :hover state hasn't
        // caught up on a fast mousemove.
        var keepShownThreshold = shown ? KEEP_OPEN_PX : REVEAL_PX;
        if (e.clientY < keepShownThreshold) {
            reveal();
            return;
        }
        if (shown && !bar.matches(":hover") && !bar.matches(":focus-within")) {
            scheduleHide();
        }
    });
    bar.addEventListener("mouseleave", scheduleHide);
}

function configureTerminal(term: Terminal): void {
    term.configure({
        scrollback: 5000,
        fontFamily: monoFontStack(),
    });

    setTimeout(function(): void {
        term.fit();
    }, 200);

    setTimeout(function() {
        window.dispatchEvent(new Event("resize"));
    }, 200);
    setTimeout(function() {
        window.dispatchEvent(new Event("resize"));
    }, 1000);
    setTimeout(function() {
        window.dispatchEvent(new Event("resize"));
    }, 3000);
}

function createLoadingOverlay(): LoadingOverlayController {
    var currentEl: HTMLElement | null = null;
    var timer = 0;

    return {
        show(message?: string): void {
            if (currentEl !== null) {
                return;
            }
            currentEl = document.createElement("div");
            currentEl.id = "cp-loading-overlay";
            currentEl.innerHTML = '<div id="cp-loading-spinner"></div>' + (message ? '<div id="cp-loading-text">' + message + "</div>" : "");
            document.body.appendChild(currentEl);
            timer = window.setTimeout(function() {
                if (currentEl !== null) {
                    currentEl.classList.add("cp-fade-out");
                }
            }, 60000);
        },
        hide(): void {
            if (timer) {
                clearTimeout(timer);
                timer = 0;
            }
            if (currentEl !== null) {
                var el = currentEl;
                currentEl = null;
                el.classList.add("cp-fade-out");
                setTimeout(function() {
                    if (el.parentNode !== null) {
                        el.parentNode.removeChild(el);
                    }
                }, 350);
            }
        },
    };
}

function flashButton(button: HTMLElement): void {
    button.style.background = "rgba(239,68,68,0.3)";
    setTimeout(function() {
        button.style.background = "";
    }, 600);
}

function blobToArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
    var PromiseCtor = (window as any).Promise;
    return new PromiseCtor(function(resolve: (value?: ArrayBuffer) => void, reject: (reason?: any) => void): void {
        var reader = new FileReader();
        reader.onload = function(): void {
            resolve(reader.result as ArrayBuffer);
        };
        reader.onerror = function(): void {
            reject(reader.error || new Error("failed to read blob"));
        };
        reader.readAsArrayBuffer(blob);
    });
}

function asciiToUint8Array(value: string): Uint8Array {
    var bytes = new Uint8Array(value.length);
    for (var i = 0; i < value.length; i++) {
        bytes[i] = value.charCodeAt(i) & 0xff;
    }
    return bytes;
}

function concatUint8Arrays(parts: Uint8Array[]): Uint8Array {
    var total = 0;
    parts.forEach(function(part: Uint8Array): void {
        total += part.length;
    });
    var result = new Uint8Array(total);
    var offset = 0;
    parts.forEach(function(part: Uint8Array): void {
        result.set(part, offset);
        offset += part.length;
    });
    return result;
}

function uint8ArrayToBase64(bytes: Uint8Array): string {
    var binary = "";
    for (var i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
}

function buildMultipartPayload(file: Blob, fileName: string, mimeType: string): Promise<{ bodyBase64: string; contentType: string; }> {
    return blobToArrayBuffer(file).then(function(buffer: ArrayBuffer): { bodyBase64: string; contentType: string; } {
        var boundary = "----cicy-ws-" + String(Date.now());
        var safeName = String(fileName || "file").replace(/[\r\n"]/g, "_");
        var header =
            "--" + boundary + "\r\n" +
            'Content-Disposition: form-data; name="file"; filename="' + safeName + '"\r\n' +
            "Content-Type: " + (mimeType || "application/octet-stream") + "\r\n\r\n";
        var footer = "\r\n--" + boundary + "--\r\n";
        var bodyBytes = concatUint8Arrays([
            asciiToUint8Array(header),
            new Uint8Array(buffer),
            asciiToUint8Array(footer),
        ]);
        return {
            bodyBase64: uint8ArrayToBase64(bodyBytes),
            contentType: "multipart/form-data; boundary=" + boundary,
        };
    });
}

function normalizePastedFiles(event: ClipboardEvent): File[] {
    var result: File[] = [];
    var seen: string[] = [];
    var items = event.clipboardData && event.clipboardData.items ? event.clipboardData.items : [];
    for (var i = 0; i < items.length; i++) {
        var item = items[i];
        if (!item || item.kind !== "file") {
            continue;
        }
        var file = item.getAsFile();
        if (!file) {
            continue;
        }
        var key = [file.name, file.type, String(file.size)].join("|");
        if (seen.indexOf(key) >= 0) {
            continue;
        }
        seen.push(key);
        result.push(file);
    }
    var files = event.clipboardData && event.clipboardData.files ? event.clipboardData.files : [];
    for (var j = 0; j < files.length; j++) {
        var extra = files[j];
        if (!extra) {
            continue;
        }
        var extraKey = [extra.name, extra.type, String(extra.size)].join("|");
        if (seen.indexOf(extraKey) >= 0) {
            continue;
        }
        seen.push(extraKey);
        result.push(extra);
    }
    return result;
}

function isEditableTextTarget(target: HTMLElement | null): target is HTMLInputElement | HTMLTextAreaElement {
    if (!target) {
        return false;
    }
    if (target instanceof HTMLTextAreaElement) {
        return !target.readOnly && !target.disabled;
    }
    if (target instanceof HTMLInputElement) {
        if (target.readOnly || target.disabled) {
            return false;
        }
        var type = String(target.type || "text").toLowerCase();
        return ["text", "search", "url", "tel", "password", "email"].indexOf(type) >= 0;
    }
    return false;
}

function isTerminalTarget(target: HTMLElement | null): boolean {
    if (!target) {
        return false;
    }
    if (target.id === "terminal") {
        return true;
    }
    if (target.classList && target.classList.contains("xterm-helper-textarea")) {
        return true;
    }
    return !!target.closest("#terminal");
}

function isPasteDialogTarget(target: HTMLElement | null): boolean {
    return !!(target && target.closest("#cp-paste-confirm-modal, #cp-file-paste-modal"));
}

function insertTextAtCursor(target: HTMLInputElement | HTMLTextAreaElement, text: string): void {
    var start = typeof target.selectionStart === "number" ? target.selectionStart : target.value.length;
    var end = typeof target.selectionEnd === "number" ? target.selectionEnd : start;
    if (typeof target.setRangeText === "function") {
        target.setRangeText(text, start, end, "end");
    } else {
        target.value = target.value.slice(0, start) + text + target.value.slice(end);
        var next = start + text.length;
        try {
            target.setSelectionRange(next, next);
        } catch (_error) {
        }
    }
    target.dispatchEvent(new Event("input", { bubbles: true }));
}

function uploadPastedFile(term: Terminal, webtty: WebTTY, paneId: string, apiHeaders: { [key: string]: string }, file: File): Promise<any> {
    return buildMultipartPayload(file, file.name || "file", file.type || "application/octet-stream").then(function(payload: { bodyBase64: string; contentType: string; }): Promise<any> {
        var uploadHeaders: { [key: string]: string } = {};
        if (apiHeaders.Authorization) {
            uploadHeaders.Authorization = apiHeaders.Authorization;
        }
        return webtty.requestAPI("POST", "/assets/files?pane=" + encodeURIComponent(paneId), undefined, uploadHeaders, payload.bodyBase64, payload.contentType);
    }).then(function(data: any): any {
        var asset = data && data.file ? data.file : null;
        if (!asset || (!asset.file_ref && !asset.url)) {
            throw new Error("upload failed");
        }
        var assetRef = String(asset.file_ref || "");
        if (!assetRef) {
            assetRef = "file://" + String(asset.url || "").replace(/^\/+/, "");
        } else if (assetRef.indexOf("file:///") === 0) {
            assetRef = "file://" + assetRef.slice(8);
        }
        if (asset && asset.is_image) {
            if (assetRef.indexOf("file://") === 0) {
                assetRef = "image://" + assetRef.slice(7);
            } else if (assetRef.indexOf("image://") !== 0) {
                assetRef = "image://" + assetRef.replace(/^image:\/\//, "").replace(/^\/+/, "");
            }
        }
        if (!webtty.sendInput(assetRef)) {
            throw new Error("send asset ref failed");
        }
        return asset;
    });
}

function buildSTTMultipartPayload(blob: Blob, mimeType: string): Promise<{ bodyBase64: string; contentType: string; }> {
    return blobToArrayBuffer(blob).then(function(buffer: ArrayBuffer): { bodyBase64: string; contentType: string; } {
        var boundary = "----cicy-ws-" + String(Date.now());
        var header =
            "--" + boundary + "\r\n" +
            'Content-Disposition: form-data; name="file"; filename="voice.webm"\r\n' +
            "Content-Type: " + mimeType + "\r\n\r\n";
        var engineField =
            "\r\n--" + boundary + "\r\n" +
            'Content-Disposition: form-data; name="engine"\r\n\r\n' +
            "google";
        var mimeField =
            "\r\n--" + boundary + "\r\n" +
            'Content-Disposition: form-data; name="mime"\r\n\r\n' +
            mimeType;
        var footer = "\r\n--" + boundary + "--\r\n";
        var bodyBytes = concatUint8Arrays([
            asciiToUint8Array(header),
            new Uint8Array(buffer),
            asciiToUint8Array(engineField),
            asciiToUint8Array(mimeField),
            asciiToUint8Array(footer),
        ]);

        return {
            bodyBase64: uint8ArrayToBase64(bodyBytes),
            contentType: "multipart/form-data; boundary=" + boundary,
        };
    });
}

export function mountCicyTTYUI(term: Terminal, webtty: WebTTY): void {
    installStyles();
    configureTerminal(term);

    var storage = createStorage();
    var paneId = queryPaneId();
    var token = queryToken();
    var apiHeaders = createAPIHeaders(token);
    var loading = createLoadingOverlay();
    var hadOpen = false;

    // panel removed - dummy element for legacy references
    var panel = document.createElement("div");

    var winFloat = document.createElement("div");
    winFloat.id = "cp-win-float";
    winFloat.innerHTML = '<div id="cp-win-tabs"></div>';

    var fixedTop = document.createElement("div");
    fixedTop.id = "fixed-top-action";
    // Lucide-style outline SVGs (24x24 viewBox @ 14px) — currentColor stroke
    // so the existing .fta-btn hover/focus state still controls the icon
    // tint without per-icon overrides.
    var svgKbd     = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="16" x="2" y="4" rx="2"/><path d="M6 8h.01M10 8h.01M14 8h.01M18 8h.01M8 12h.01M12 12h.01M16 12h.01M7 16h10"/></svg>';
    var svgPlus    = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>';
    var svgPlay    = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><polygon points="7 4 20 12 7 20 7 4"/></svg>';
    var svgUpdate  = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v13"/><path d="m6 10 6-6 6 6"/><path d="M5 21h14"/></svg>';
    var svgRestart = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>';
    var svgReload  = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>';

    // All buttons share the same tooltip stack: bottom-right anchored,
    // multiline (cp-tooltip-multiline = pre-line + min-width 164). Tooltips
    // that mention {agent} are filled in once we learn the pane's
    // agent_type (fetched right after fixedTop is appended). Until then they
    // render with the literal placeholder — replaced async below.
    var tipCls = 'fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom cp-tooltip-multiline';
    fixedTop.innerHTML =
        '<button id="cp-kbd" class="' + tipCls + '" data-tooltip="' + ttydT("tipPromptArea") + '">' + svgKbd + '</button>' +
        '<button id="cp-win-add" class="' + tipCls + '" data-tooltip="' + ttydT("tipAddCliWindow") + '">' + svgPlus + '</button>' +
        '<button id="cp-agent-launch" class="' + tipCls + '" data-tooltip="' + ttydT("tipLaunchAgent") + '">' + svgPlay + '</button>' +
        '<button id="cp-agent-update" class="' + tipCls + '" data-tooltip="' + ttydT("tipUpdateAgent") + '">' + svgUpdate + '</button>' +
        '<button id="cp-win-restart" class="' + tipCls + '" data-tooltip="' + ttydT("tipRestartAgent") + '">' + svgRestart + '</button>' +
        '<button id="cp-reload" class="' + tipCls + '" data-tooltip="' + ttydT("tipReloadPage") + '" onclick="location.reload()">' + svgReload + '</button>';

    // Action buttons live inside the floating bar (as its rightmost flex
     // child) so they slide in / out with the tab list — no longer a separate
     // fixed-positioned overlay above it.
    winFloat.appendChild(fixedTop);
    document.body.appendChild(winFloat);

    // Auto-hide trigger for the floating tab bar. The bar is fully hidden by
    // default; this listener reveals it whenever the cursor is within an 8px
    // strip at the top of the viewport, then schedules a delayed hide once
    // the cursor leaves both the trigger strip and the bar itself.
    setupWinFloatAutoHide(winFloat);

    var input = (document.getElementById("cp-input") || document.createElement("textarea")) as HTMLTextAreaElement;
    var sendBtn = (document.getElementById("cp-send") || document.createElement("button")) as HTMLButtonElement;
    var enterBtn = (document.getElementById("cp-enter") || document.createElement("button")) as HTMLButtonElement;
    var winTabs = document.getElementById("cp-win-tabs") as HTMLElement;
    var collapseBtn = (document.getElementById("cp-collapse") || document.createElement("button")) as HTMLButtonElement;
    var restartBtn = document.getElementById("cp-win-restart") as HTMLButtonElement;
    var launchBtn = document.getElementById("cp-agent-launch") as HTMLButtonElement;
    var updateBtn = document.getElementById("cp-agent-update") as HTMLButtonElement;
    var addWindowBtn = document.getElementById("cp-win-add") as HTMLButtonElement;
    var kbdBtn = document.getElementById("cp-kbd") as HTMLButtonElement;

    // Resolve this pane's agent_type once on init so the Launch/Update confirm
    // dialogs can show the concrete agent name ("启动 codex" / "Update claude").
    // The toolbar tooltips themselves are static ("启动 Agent" / "更新 Agent") —
    // no per-pane substitution there.
    var paneAgentType = "agent";
    webtty.requestAPI("GET", "/api/tmux/panes/" + paneId, undefined, apiHeaders)
        .then(function(resp: any) {
            var t = resp && resp.agent_type ? String(resp.agent_type).trim() : "";
            if (!t) return;
            paneAgentType = t;
        })
        .catch(function() { /* leave generic "agent" label for the dialogs */ });

    // Bottom prompt area: compose the whole line locally, send it in one HTTP
    // request — avoids the per-keystroke websocket round-trips that make the
    // raw terminal feel sluggish on slow links. Reuses the existing composer
    // wiring below (history, drafts, Enter/Shift+Enter, normalization).
    input.id = "cp-input";
    input.setAttribute("placeholder", ttydT("promptAreaPlaceholder"));
    sendBtn.id = "cp-send";
    sendBtn.textContent = "➤";
    enterBtn.id = "cp-enter";
    var promptArea = document.createElement("div");
    promptArea.id = "cp-prompt";
    promptArea.appendChild(input);
    promptArea.appendChild(enterBtn);
    promptArea.appendChild(sendBtn);
    document.body.appendChild(promptArea);

    var promptOpenKey = "cicy_prompt_open_" + paneId;
    function setPromptOpen(open: boolean): void {
        promptArea.classList.toggle("open", open);
        document.body.classList.toggle("cp-prompt-open", open);
        if (kbdBtn) {
            kbdBtn.classList.toggle("active", open);
        }
        storage.set(promptOpenKey, open);
        // the terminal box shrank/grew (body padding-bottom) — re-fit xterm
        window.dispatchEvent(new Event("resize"));
        if (open) {
            try {
                input.focus();
            } catch (_error) {
            }
        }
    }
    if (kbdBtn) {
        kbdBtn.addEventListener("click", function(): void {
            setPromptOpen(!promptArea.classList.contains("open"));
        });
    }
    setPromptOpen(storage.get(promptOpenKey, false) as boolean);

    var historyKey = "cicy_hist_" + paneId;
    var draftKey = "cicy_draft_" + paneId;
    var clientTraceKey = "cicy_ttyd_trace_" + paneId;
    var history = storage.get(historyKey, []) as string[];
    var historyIndex = -1;
    var tempDraft = "";
    var enterToSend = storage.get("cicy_enter_to_send", true) as boolean;

    function writeClientTrace(eventName: string, meta?: any): void {
        var entry: any = {
            event: eventName,
            pane_id: paneId,
            path: window.location.pathname,
            ts_client: new Date().toISOString(),
        };
        if (meta && typeof meta === "object") {
            Object.keys(meta).forEach(function(key: string): void {
                entry[key] = meta[key];
            });
        }
        try {
            var existing = storage.get(clientTraceKey, []) as any[];
            existing.push(entry);
            if (existing.length > 200) {
                existing = existing.slice(existing.length - 200);
            }
            storage.set(clientTraceKey, existing);
        } catch (_error) {
        }

        var fetchImpl = (window as any).fetch;
        if (typeof fetchImpl === "function") {
            fetchImpl("/api/tmux/client-trace", {
                method: "POST",
                headers: apiHeaders,
                body: JSON.stringify(entry),
                keepalive: true
            }).catch(function(): void {});
            return;
        }
        webtty.requestAPI("POST", "/api/tmux/client-trace", entry, apiHeaders).catch(function(): void {});
    }

    function sendInput(value: string): boolean {
        var ok = webtty.sendInput(value);
        if (!ok) {
            flashButton(sendBtn);
        }
        return ok;
    }

    function sendLine(value: string): boolean {
        var ok = webtty.sendLine(value);
        if (!ok) {
            flashButton(sendBtn);
        }
        return ok;
    }

    function sendHTTP(command: string): Promise<any> {
        if (!webtty.isConnectionOpen()) {
            writeClientTrace("cp-send-http-skipped-closed", {
                command_len: command.length,
                command_preview: clipTracePreview(command, 160),
            });
            flashButton(sendBtn);
            var PromiseCtor = (window as any).Promise;
            return PromiseCtor.resolve(null);
        }
        var fetchImpl = (window as any).fetch;
        writeClientTrace("cp-send-http-request", {
            command_len: command.length,
            command_preview: clipTracePreview(command, 160),
            has_fetch: typeof fetchImpl === "function",
        });
        if (typeof fetchImpl !== "function") {
            return webtty.requestAPI("POST", "/api/tmux/send", {
                pane_id: paneId,
                text: command
            }, apiHeaders).then(function(payload: any): any {
                writeClientTrace("cp-send-http-response", {
                    mode: "webtty-request-api",
                    success: payload && payload.success,
                    error: payload && payload.error,
                    detail: payload && payload.detail,
                });
                return ensureTmuxSendSucceeded(payload);
            });
        }
        return fetchImpl("/api/tmux/send", {
            method: "POST",
            headers: apiHeaders,
            body: JSON.stringify({
                pane_id: paneId,
                text: command
            })
        }).then(function(response: any): Promise<any> {
            if (!response || !response.ok) {
                var status = response ? response.status : 0;
                var statusText = response ? response.statusText : "";
                var contentType = response && response.headers && response.headers.get ? response.headers.get("content-type") : "";
                if (contentType && contentType.indexOf("application/json") >= 0 && response && response.json) {
                    return response.json().then(function(payload: any): any {
                        writeClientTrace("cp-send-http-bad-status", {
                            status: status,
                            status_text: statusText,
                            detail: payload && payload.detail,
                            pane_updated: payload && payload.pane_updated === true,
                            restore_input: payload && payload.restore_input,
                        });
                        throw createTmuxSendError((payload && payload.detail) || "request failed", payload, status);
                    });
                }
                writeClientTrace("cp-send-http-bad-status", {
                    status: status,
                    status_text: statusText,
                });
                throw createTmuxSendError("request failed", null, status);
            }
            var contentType = response.headers && response.headers.get ? response.headers.get("content-type") : "";
            if (contentType && contentType.indexOf("application/json") >= 0) {
                return response.json().then(function(payload: any): any {
                    writeClientTrace("cp-send-http-response", {
                        mode: "fetch-json",
                        success: payload && payload.success,
                        error: payload && payload.error,
                        detail: payload && payload.detail,
                    });
                    return ensureTmuxSendSucceeded(payload);
                });
            }
            return response.text().then(function(text: string): string {
                writeClientTrace("cp-send-http-response", {
                    mode: "fetch-text",
                    text_len: text.length,
                    text_preview: clipTracePreview(text, 160),
                });
                return text;
            });
        }).catch(function(error: any): any {
            if (error && (typeof error.statusCode === "number" || error.paneUpdated === true || error.restoreInput === false || error.isNetworkError === true)) {
                throw error;
            }
            throw createTmuxSendError(error && error.message ? error.message : "network request failed", null, 0, true);
        });
    }

    function sendTmuxKey(key: string): Promise<any> {
        if (!webtty.isConnectionOpen()) {
            writeClientTrace("cp-send-key-skipped-closed", { key: key });
            flashButton(sendBtn);
            var PromiseCtor = (window as any).Promise;
            return PromiseCtor.resolve(null);
        }
        var fetchImpl = (window as any).fetch;
        writeClientTrace("cp-send-key-request", { key: key, has_fetch: typeof fetchImpl === "function" });
        if (typeof fetchImpl !== "function") {
            return webtty.requestAPI("POST", "/api/tmux/send-keys", {
                win_id: paneId,
                keys: key
            }, apiHeaders);
        }
        return fetchImpl("/api/tmux/send-keys", {
            method: "POST",
            headers: apiHeaders,
            body: JSON.stringify({
                win_id: paneId,
                keys: key
            })
        }).then(function(response: any): Promise<any> {
            if (!response || !response.ok) {
                var status = response ? response.status : 0;
                var statusText = response ? response.statusText : "";
                throw createTmuxSendError("send key failed", { detail: statusText }, status);
            }
            var contentType = response.headers && response.headers.get ? response.headers.get("content-type") : "";
            if (contentType && contentType.indexOf("application/json") >= 0) {
                return response.json();
            }
            return response.text();
        }).catch(function(error: any): any {
            if (error && (typeof error.statusCode === "number" || error.isNetworkError === true)) {
                throw error;
            }
            throw createTmuxSendError(error && error.message ? error.message : "network request failed", null, 0, true);
        });
    }

    function updateEnterButton(): void {
        enterBtn.textContent = enterToSend ? "⏎" : "⇧⏎";
        enterBtn.setAttribute("data-tooltip", enterToSend ? ttydT("enterSendPromptEnter") : ttydT("enterSendPromptShiftEnter"));
    }

    function addHistory(command: string): void {
        history = [command].concat(history.filter(function(item: string): boolean {
            return item !== command;
        })).slice(0, 50);
        storage.set(historyKey, history);
    }

    function normalizePromptPunctuation(value: string): string {
        return normalizeTerminalText(value);
    }

    function syncNormalizedPromptValue(): void {
        var normalized = normalizePromptPunctuation(input.value);
        if (normalized === input.value) {
            storage.set(draftKey, input.value);
            return;
        }
        var start = input.selectionStart;
        var end = input.selectionEnd;
        input.value = normalized;
        try {
            input.setSelectionRange(start, end);
        } catch (_error) {
        }
        storage.set(draftKey, normalized);
    }

    function doSend(value?: string): void {
        var command = value !== undefined ? normalizePromptPunctuation(value) : normalizePromptPunctuation(input.value);
        if (!command || !command.trim()) {
            return;
        }
        writeClientTrace("cp-do-send", {
            command_len: command.length,
            command_preview: clipTracePreview(command, 160),
            enter_to_send: enterToSend,
        });
        addHistory(command);
        historyIndex = -1;
        tempDraft = "";
        var prev = input.value;
        input.value = "";
        storage.set(draftKey, "");
        sendBtn.classList.add("sending");
        sendHTTP(command).catch(function(error: any): void {
            var shouldRestore = !!(error && error.isNetworkError === true);
            writeClientTrace("cp-do-send-error", {
                message: error && error.message ? error.message : String(error || ""),
                status: error && typeof error.statusCode === "number" ? error.statusCode : 0,
                pane_updated: !!(error && error.paneUpdated === true),
                restore_input: shouldRestore,
                command_len: command.length,
                command_preview: clipTracePreview(command, 160),
            });
            if (shouldRestore) {
                input.value = prev;
                storage.set(draftKey, prev);
            }
            flashButton(sendBtn);
        }).finally(function(): void {
            sendBtn.classList.remove("sending");
        });
    }

    function twiceConfirm(button: HTMLButtonElement, action: () => void): void {
        var pending = false;
        var timer = 0;
        var original = button.textContent || "";
        button.addEventListener("click", function(event: MouseEvent): void {
            event.stopPropagation();
            if (!pending) {
                pending = true;
                button.textContent = "confirm?";
                button.classList.add("cp-confirm");
                timer = window.setTimeout(function(): void {
                    pending = false;
                    button.textContent = original;
                    button.classList.remove("cp-confirm");
                }, 2000);
                return;
            }

            clearTimeout(timer);
            pending = false;
            button.textContent = original;
            button.classList.remove("cp-confirm");
            action();
        });
    }

    function apiFetch(method: string, path: string, body?: object): Promise<any> {
        if (!webtty.isConnectionOpen()) {
            var PromiseCtor = (window as any).Promise;
            return PromiseCtor.resolve(null);
        }
        return webtty.requestAPI(method, "/api/tmux/windows" + path, body, apiHeaders);
    }

    var latestWindows: any[] = [];
    var optimisticActiveIndex = "";
    function renderWindowTabs(windows: any[]): void {
        latestWindows = windows.slice();
        var pendingExists = optimisticActiveIndex !== "" && windows.some(function(win: any): boolean {
            return String(win.index) === optimisticActiveIndex;
        });
        var serverActiveIndex = "";
        windows.some(function(win: any): boolean {
            if (win.active) {
                serverActiveIndex = String(win.index);
                return true;
            }
            return false;
        });
        if (optimisticActiveIndex !== "" && optimisticActiveIndex === serverActiveIndex) {
            optimisticActiveIndex = "";
            pendingExists = false;
        }
        var activeIndex = pendingExists ? optimisticActiveIndex : serverActiveIndex;
        winTabs.innerHTML = windows.map(function(win: any): string {
            var close = win.index === "0" ? "" : '<span class="cp-wdel" data-idx="' + win.index + '" data-tooltip="' + ttydT("closeCliWindow") + '">✕</span>';
            var active = String(win.index) === activeIndex ? " active" : "";
            return '<button class="cp-wtab' + active + '" data-idx="' + win.index + '">' + win.name + "." + win.index + close + "</button>";
        }).join("");
    }

    var windowsLoadPending = false;
    function loadWindows(): void {
        if (!webtty.isConnectionOpen() || windowsLoadPending) {
            return;
        }
        windowsLoadPending = true;
        apiFetch("GET", "?session=" + paneId).then(function(data: any): void {
            renderWindowTabs((data && data.windows) || []);
            windowsLoadPending = false;
        }).catch(function(): void {
            windowsLoadPending = false;
        });
    }

    input.value = normalizePromptPunctuation(storage.get(draftKey, ""));
    storage.set(draftKey, input.value);
    updateEnterButton();

    var fixedTooltip = document.createElement("div");
    fixedTooltip.id = "cp-fixed-tooltip";
    document.body.appendChild(fixedTooltip);

    function hideFixedTooltip(): void {
        fixedTooltip.classList.remove("open");
    }

    function showFixedTooltip(target: HTMLElement, text: string): void {
        if (!text) {
            hideFixedTooltip();
            return;
        }
        fixedTooltip.textContent = text;
        fixedTooltip.classList.add("open");
        var rect = target.getBoundingClientRect();
        var tipRect = fixedTooltip.getBoundingClientRect();
        var left = rect.right - tipRect.width;
        if (left < 8) {
            left = 8;
        }
        var top = rect.bottom + 10;
        if (left + tipRect.width > window.innerWidth - 8) {
            left = Math.max(8, window.innerWidth - tipRect.width - 8);
        }
        if (top + tipRect.height > window.innerHeight - 8) {
            top = Math.max(8, rect.top - tipRect.height - 10);
        }
        fixedTooltip.style.left = left + "px";
        fixedTooltip.style.top = top + "px";
    }

    enterBtn.addEventListener("click", function(): void {
        enterToSend = !enterToSend;
        storage.set("cicy_enter_to_send", enterToSend);
        updateEnterButton();
    });

    input.addEventListener("keydown", function(event: KeyboardEvent): void {
        if ((event as any).isComposing) {
            return;
        }
        if (event.key === "Enter" && !event.ctrlKey && !event.metaKey) {
            var shouldSend = enterToSend ? !event.shiftKey : event.shiftKey;
            writeClientTrace("cp-keydown-enter", {
                should_send: shouldSend,
                enter_to_send: enterToSend,
                shift_key: event.shiftKey,
                ctrl_key: event.ctrlKey,
                meta_key: event.metaKey,
                input_len: input.value.length,
                input_preview: clipTracePreview(input.value, 160),
            });
            if (!shouldSend) {
                return;
            }
            event.preventDefault();
            if (input.value.trim()) {
                doSend();
            } else {
                sendTmuxKey("Enter").catch(function(): void {
                    flashButton(sendBtn);
                });
            }
            return;
        }
        if (event.key === "ArrowUp" && input.value.substring(0, input.selectionStart).indexOf("\n") === -1) {
            if (history.length && historyIndex < history.length - 1) {
                event.preventDefault();
                if (historyIndex === -1) {
                    tempDraft = input.value;
                }
                historyIndex++;
                input.value = history[historyIndex];
            }
            return;
        }
        if (event.key === "ArrowDown" && historyIndex >= 0 && input.value.substring(input.selectionStart).indexOf("\n") === -1) {
            event.preventDefault();
            historyIndex--;
            input.value = historyIndex >= 0 ? history[historyIndex] : tempDraft;
        }
    });

    input.addEventListener("input", function(): void {
        syncNormalizedPromptValue();
    });
    sendBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        doSend();
    });

    collapseBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        panel.classList.toggle("collapsed");
        collapseBtn.textContent = panel.classList.contains("collapsed") ? "+" : "−";
    });

    loadWindows();
    setInterval(loadWindows, 5000);

    winTabs.addEventListener("click", function(event: MouseEvent): void {
        var target = event.target as Element;
        var closeButton = target.closest(".cp-wdel") as HTMLElement | null;
        if (closeButton !== null) {
            event.stopPropagation();
            hideFixedTooltip();
            var idx = closeButton.dataset.idx || "";
            cpModalConfirm({
                title: ttydT("closeCliWindow"),
                body: ttydT("windowConfirmDelete", { idx: idx }) || ("Close window " + idx + "?"),
                confirmLabel: ttydT("close"),
                danger: true,
            }).then(function(ok) {
                if (ok) apiFetch("DELETE", "", { session: paneId, index: idx }).then(loadWindows);
            });
            return;
        }

        var tab = target.closest(".cp-wtab") as HTMLElement | null;
        if (tab !== null && !tab.classList.contains("active")) {
            optimisticActiveIndex = String(tab.dataset.idx || "");
            if (latestWindows.length) {
                renderWindowTabs(latestWindows);
            } else {
                Array.prototype.forEach.call(winTabs.querySelectorAll(".cp-wtab"), function(node: Element): void {
                    node.classList.toggle("active", node === tab);
                });
            }
            apiFetch("PUT", "", { session: paneId, index: tab.dataset.idx }).then(function(): void {
                loadWindows();
            }).catch(function(): void {
                optimisticActiveIndex = "";
                loadWindows();
            });
        }
    });

    winTabs.addEventListener("mouseover", function(event: MouseEvent): void {
        var target = event.target as Element;
        var closeButton = target.closest(".cp-wdel") as HTMLElement | null;
        if (closeButton !== null) {
            showFixedTooltip(closeButton, closeButton.getAttribute("data-tooltip") || "");
        }
    });

    winTabs.addEventListener("mousemove", function(event: MouseEvent): void {
        var target = event.target as Element;
        var closeButton = target.closest(".cp-wdel") as HTMLElement | null;
        if (closeButton !== null) {
            showFixedTooltip(closeButton, closeButton.getAttribute("data-tooltip") || "");
        }
    });

    winTabs.addEventListener("mouseout", function(event: MouseEvent): void {
        var target = event.target as Element;
        var closeButton = target.closest(".cp-wdel") as HTMLElement | null;
        if (closeButton !== null) {
            var related = event.relatedTarget as Element | null;
            if (!related || !closeButton.contains(related)) {
                hideFixedTooltip();
            }
        }
    });

    addWindowBtn.addEventListener("click", function(): void {
        apiFetch("POST", "", { session: paneId }).then(loadWindows);
    });

    restartBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        cpModalConfirm({
            title: ttydT("restartPaneTitle"),
            body: ttydT("confirmRestartAgent"),
            confirmLabel: ttydT("actionRestart"),
        }).then(function(ok) {
            if (!ok) return;
            if (!webtty.isConnectionOpen()) {
                flashButton(restartBtn);
                return;
            }
            restartBtn.classList.add("restarting");
            loading.show(ttydT("restartingAgent"));
            webtty.requestAPI("POST", "/api/tmux/panes/" + paneId + "/restart", undefined, apiHeaders).catch(function(): void {
                restartBtn.classList.remove("restarting");
            });
            setTimeout(function(): void {
                restartBtn.classList.remove("restarting");
            }, 30000);
        });
    });

    // Re-source .cicy/boot.sh in the pane. Use case: user Ctrl+C'd out of
    // claude/codex; the shell is alive but the agent's env (gateway URLs,
    // settings.json) needs to be re-exported before the binary restarts.
    // Cheaper than the full pane restart above — no tmux respawn, no
    // scrollback loss.
    launchBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        cpModalConfirm({
            title: ttydT("launchAgentTitle", { agent: paneAgentType }),
            body: ttydT("confirmLaunchAgent", { agent: paneAgentType }),
            confirmLabel: ttydT("actionLaunch"),
        }).then(function(ok) {
            if (!ok) return;
            webtty.requestAPI("POST", "/api/tmux/panes/" + paneId + "/relaunch-agent", undefined, apiHeaders).catch(function(): void {
                flashButton(launchBtn);
            });
        });
    });

    // npm install -g <pkg>@latest — sends the install line straight into the
    // pane so the user sees the same live progress UX as boot.sh's
    // first-time install (driven by __cicy_require_command_live in
    // .cicy_tmux.conf). Update is the same command as install: npm
    // overwrites the existing prefix link, no separate "update" path needed.
    updateBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        cpModalConfirm({
            title: ttydT("updateAgentTitle", { agent: paneAgentType }),
            body: ttydT("confirmUpdateAgent", { agent: paneAgentType }),
            confirmLabel: ttydT("actionUpdate"),
            danger: true,
        }).then(function(ok) {
            if (!ok) return;
            // Pass the localized "update complete, restart {agent}" hint
            // from JS i18n so the server can echo it after npm install
            // succeeds — lands in the new tmux window's terminal, not as a
            // JS toast (the install runs async and we don't wait on it).
            var postHint = ttydT("updateCompleteRestartHint", { agent: paneAgentType });
            webtty.requestAPI("POST", "/api/tmux/panes/" + paneId + "/update-agent-cli", { post_install_hint: postHint }, apiHeaders).catch(function(): void {
                flashButton(updateBtn);
            });
        });
    });

    document.addEventListener("keydown", function(event: KeyboardEvent): void {
        if ((event.ctrlKey || event.metaKey) && event.key === "/") {
            event.preventDefault();
            input.focus();
            return;
        }
    });

    var micButton = document.createElement("button");
    micButton.id = "cp-mic";
    micButton.className = "fta-btn";
    micButton.title = ttydT("voiceMode");
    micButton.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path><path d="M19 10v2a7 7 0 0 1-14 0v-2"></path><line x1="12" x2="12" y1="19" y2="22"></line></svg>';
    document.body.appendChild(micButton);

    var voiceOverlay = document.createElement("div");
    voiceOverlay.id = "vm-overlay";
    voiceOverlay.innerHTML =
        '<div id="vm-center">' +
            '<div id="vm-ripple1"></div><div id="vm-ripple2"></div>' +
            '<div id="vm-btn">' +
                '<svg id="vm-icon" width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">' +
                    '<path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path>' +
                    '<path d="M19 10v2a7 7 0 0 1-14 0v-2"></path><line x1="12" x2="12" y1="19" y2="22"></line>' +
                "</svg>" +
            "</div>" +
            '<div id="vm-hint"></div>' +
            '<div id="vm-status"></div>' +
        "</div>";
    document.body.appendChild(voiceOverlay);

    var voiceModeActive = false;
    var voiceRecording = false;
    var voiceRecorder: any = null;
    var voiceStream: MediaStream | null = null;
    var voiceChunks: Blob[] = [];
    var voiceProcessing = false;
    var savedPanelStyle: { left: string; top: string; width: string; height: string; } | null = null;
    var voiceButton = document.getElementById("vm-btn") as HTMLElement;
    var voiceCenter = document.getElementById("vm-center") as HTMLElement;
    var voiceModeKey = "cicy_vm_active";
    var voicePositionKey = "cicy_vm_pos";
    var savedVoicePosition = storage.get(voicePositionKey, null) as { x: number; y: number; } | null;

    if (savedVoicePosition !== null) {
        voiceCenter.style.position = "fixed";
        voiceCenter.style.left = savedVoicePosition.x + "px";
        voiceCenter.style.top = savedVoicePosition.y + "px";
        voiceCenter.style.transform = "none";
    }

    function setVoiceOverlayClass(value: string): void {
        voiceOverlay.className = value;
    }

    function toggleVoiceMode(): void {
        voiceModeActive = !voiceModeActive;
        micButton.classList.toggle("active", voiceModeActive);
        voiceOverlay.classList.toggle("open", voiceModeActive);
        if (!voiceModeActive) {
            setVoiceOverlayClass("");
        }
        input.disabled = voiceModeActive;
        storage.set(voiceModeKey, voiceModeActive);
        if (voiceModeActive) {
            savedPanelStyle = {
                left: panel.style.left || "",
                top: panel.style.top || "",
                width: panel.style.width || "",
                height: panel.style.height || "",
            };
            panel.classList.add("voice-mode");
            if (navigator.mediaDevices && navigator.mediaDevices.getUserMedia) {
                navigator.mediaDevices.getUserMedia({ audio: true }).then(function(stream: MediaStream): void {
                    stream.getTracks().forEach(function(track: MediaStreamTrack): void {
                        track.stop();
                    });
                }).catch(function(): void {
                });
            }
            return;
        }

        if (voiceRecording) {
            stopVoiceRecording(false);
        }
        voiceProcessing = false;
        panel.classList.remove("voice-mode");
        if (savedPanelStyle !== null) {
            panel.style.left = savedPanelStyle.left;
            panel.style.top = savedPanelStyle.top;
            panel.style.width = savedPanelStyle.width;
            panel.style.height = savedPanelStyle.height;
        }
    }

    function startVoiceRecording(): void {
        if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
            return;
        }
        setVoiceOverlayClass("open rec");
        navigator.mediaDevices.getUserMedia({ audio: true }).then(function(stream: MediaStream): void {
            voiceStream = stream;
            voiceChunks = [];

            var mediaRecorderCtor = (window as any).MediaRecorder;
            if (!mediaRecorderCtor) {
                setVoiceOverlayClass("open");
                return;
            }

            var mime = mediaRecorderCtor.isTypeSupported("audio/ogg;codecs=opus") ? "audio/ogg;codecs=opus"
                : mediaRecorderCtor.isTypeSupported("audio/webm;codecs=opus") ? "audio/webm;codecs=opus" : "";
            voiceRecorder = mime ? new mediaRecorderCtor(stream, { mimeType: mime }) : new mediaRecorderCtor(stream);
            voiceRecorder.ondataavailable = function(event: any): void {
                if (event.data && event.data.size > 0) {
                    voiceChunks.push(event.data);
                }
            };
            voiceRecorder.start(200);
            voiceRecording = true;
        }).catch(function(): void {
            setVoiceOverlayClass("open");
        });
    }

    function stopVoiceRecording(shouldSend: boolean): void {
        if (!voiceRecording) {
            return;
        }
        voiceRecording = false;
        if (!voiceRecorder || voiceRecorder.state === "inactive") {
            if (voiceModeActive) {
                setVoiceOverlayClass("open");
            }
            return;
        }

        voiceRecorder.onstop = function(): void {
            if (voiceStream !== null) {
                voiceStream.getTracks().forEach(function(track: MediaStreamTrack): void {
                    track.stop();
                });
            }
            if (!voiceModeActive) {
                return;
            }
            if (!shouldSend || !voiceChunks.length) {
                setVoiceOverlayClass("open");
                return;
            }
            setVoiceOverlayClass("open processing");
            voiceProcessing = true;

            var blob = new Blob(voiceChunks, { type: voiceRecorder.mimeType || "audio/webm" });
            if (blob.size < 100) {
                setVoiceOverlayClass("open");
                voiceProcessing = false;
                return;
            }

            buildSTTMultipartPayload(blob, voiceRecorder.mimeType || "audio/webm").then(function(payload: { bodyBase64: string; contentType: string; }): Promise<any> {
                return webtty.requestAPI("POST", "/stt?token=" + encodeURIComponent(token), undefined, undefined, payload.bodyBase64, payload.contentType);
            }).then(function(data: any): void {
                if (data && data.text) {
                    doSend(data.text);
                }
                setVoiceOverlayClass("open");
                voiceProcessing = false;
            }).catch(function(): void {
                setVoiceOverlayClass("open");
                voiceProcessing = false;
            });
        };
        voiceRecorder.stop();
    }

    var voiceDragging = false;
    var voiceDragX = 0;
    var voiceDragY = 0;
    var voiceMoved = false;

    function getPointerPosition(event: MouseEvent | TouchEvent): { x: number; y: number; } {
        var anyEvent = event as any;
        if (anyEvent.touches && anyEvent.touches.length > 0) {
            return {
                x: anyEvent.touches[0].clientX,
                y: anyEvent.touches[0].clientY,
            };
        }
        return {
            x: (event as MouseEvent).clientX,
            y: (event as MouseEvent).clientY,
        };
    }

    function onVoicePointerDown(event: MouseEvent | TouchEvent): void {
        if (voiceProcessing) {
            return;
        }
        event.preventDefault();
        var point = getPointerPosition(event);
        voiceDragX = point.x;
        voiceDragY = point.y;
        voiceMoved = false;
        voiceDragging = true;
    }

    function onVoicePointerMove(event: MouseEvent | TouchEvent): void {
        if (!voiceDragging) {
            return;
        }
        var point = getPointerPosition(event);
        var dx = point.x - voiceDragX;
        var dy = point.y - voiceDragY;
        if (!voiceMoved && Math.abs(dx) + Math.abs(dy) > 10) {
            voiceMoved = true;
        }
        if (voiceMoved) {
            if (voiceRecording) {
                stopVoiceRecording(false);
            }
            var rect = voiceCenter.getBoundingClientRect();
            voiceCenter.style.position = "fixed";
            voiceCenter.style.left = (rect.left + dx) + "px";
            voiceCenter.style.top = (rect.top + dy) + "px";
            voiceCenter.style.transform = "none";
            voiceDragX = point.x;
            voiceDragY = point.y;
        }
    }

    function onVoicePointerUp(): void {
        if (!voiceDragging) {
            return;
        }
        var wasMoved = voiceMoved;
        voiceDragging = false;
        if (wasMoved) {
            var rect = voiceCenter.getBoundingClientRect();
            storage.set(voicePositionKey, { x: Math.round(rect.left), y: Math.round(rect.top) });
            return;
        }
        if (voiceRecording) {
            setTimeout(function(): void {
                stopVoiceRecording(true);
            }, 500);
        } else if (!voiceProcessing) {
            startVoiceRecording();
        }
    }

    micButton.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        toggleVoiceMode();
    });
    document.addEventListener("keydown", function(event: KeyboardEvent): void {
        if (event.key === "Escape" && voiceModeActive) {
            event.preventDefault();
            toggleVoiceMode();
        }
    });
    voiceButton.addEventListener("mousedown", onVoicePointerDown);
    voiceButton.addEventListener("touchstart", onVoicePointerDown as EventListener, { passive: false } as AddEventListenerOptions);
    window.addEventListener("mousemove", onVoicePointerMove as EventListener);
    window.addEventListener("touchmove", onVoicePointerMove as EventListener, { passive: false } as AddEventListenerOptions);
    window.addEventListener("mouseup", onVoicePointerUp);
    window.addEventListener("touchend", onVoicePointerUp);

    if (storage.get(voiceModeKey, false)) {
        requestAnimationFrame(function(): void {
            requestAnimationFrame(function(): void {
                toggleVoiceMode();
            });
        });
    }

    function buildFileMetaText(file: File): string {
        var meta: string[] = [];
        if (file.type) {
            meta.push(file.type);
        }
        meta.push(formatPastedFileSize(file.size || 0));
        return meta.join(" · ");
    }

    function createFileListPreview(files: File[]): FilePasteDialogContent {
        var wrapper = document.createElement("div");
        var meta = document.createElement("div");
        meta.id = "cp-file-paste-meta";

        var summaryRow = document.createElement("div");
        summaryRow.className = "cp-file-paste-meta-row";
        var summaryKey = document.createElement("span");
        summaryKey.className = "cp-file-paste-meta-key";
        summaryKey.textContent = ttydT("uploadContent");
        var summaryValue = document.createElement("span");
        summaryValue.className = "cp-file-paste-meta-value";
        summaryValue.textContent = files.length === 1 ? ttydT("singleFileCount") : ttydT("multiFileCount", { n: files.length });
        summaryRow.appendChild(summaryKey);
        summaryRow.appendChild(summaryValue);
        meta.appendChild(summaryRow);

        var list = document.createElement("ul");
        list.id = "cp-file-paste-list";
        files.forEach(function(file: File): void {
            var item = document.createElement("li");
            item.className = "cp-file-paste-list-item";
            var name = document.createElement("span");
            name.className = "cp-file-paste-list-name";
            name.textContent = file.name || "file";
            var details = document.createElement("span");
            details.className = "cp-file-paste-list-meta";
            details.textContent = buildFileMetaText(file);
            item.appendChild(name);
            item.appendChild(details);
            list.appendChild(item);
        });

        wrapper.appendChild(meta);
        wrapper.appendChild(list);
        return { element: wrapper };
    }

    function createImagePastePreview(file: File): FilePasteDialogContent {
        var wrapper = document.createElement("div");
        wrapper.style.display = "flex";
        wrapper.style.alignItems = "flex-start";
        wrapper.style.justifyContent = "flex-start";
        var img = document.createElement("img");
        var objectURL = URL.createObjectURL(file);
        img.src = objectURL;
        img.alt = file.name || "pasted image";
        wrapper.appendChild(img);
        return {
            element: wrapper,
            cleanup: function(): void {
                URL.revokeObjectURL(objectURL);
            },
        };
    }

    function createFileMetaPanel(files: File[]): HTMLElement {
        var panel = document.createElement("div");
        panel.id = "cp-file-paste-meta";

        function appendRow(label: string, value: string): void {
            var row = document.createElement("div");
            row.className = "cp-file-paste-meta-row";
            var key = document.createElement("span");
            key.className = "cp-file-paste-meta-key";
            key.textContent = label;
            var text = document.createElement("span");
            text.className = "cp-file-paste-meta-value";
            text.textContent = value;
            row.appendChild(key);
            row.appendChild(text);
            panel.appendChild(row);
        }

        if (files.length === 1) {
            appendRow(ttydT("fileName"), files[0].name || "file");
            appendRow(ttydT("fileType"), files[0].type || "unknown");
            appendRow(ttydT("fileSize"), formatPastedFileSize(files[0].size || 0));
        } else {
            appendRow(ttydT("fileCount"), String(files.length));
            appendRow(ttydT("totalSize"), formatPastedFileSize(files.reduce(function(sum: number, file: File): number {
                return sum + (file.size || 0);
            }, 0)));
        }
        return panel;
    }

    function openFilePasteDialog(files: File[]): void {
        if (!files.length) {
            return;
        }
        var existing = document.getElementById("cp-file-paste-overlay");
        if (existing && existing.parentNode) {
            existing.parentNode.removeChild(existing);
        }
        var previewContent = files.length === 1 && String(files[0].type || "").match(/^image\//)
            ? createImagePastePreview(files[0])
            : createFileListPreview(files);
        var overlay = document.createElement("div");
        overlay.id = "cp-file-paste-overlay";
        var modal = document.createElement("div");
        modal.id = "cp-file-paste-modal";
        var head = document.createElement("div");
        head.id = "cp-file-paste-head";
        var heading = document.createElement("div");
        var eyebrow = document.createElement("div");
        eyebrow.id = "cp-file-paste-eyebrow";
        eyebrow.textContent = files.length === 1 && String(files[0].type || "").match(/^image\//) ? ttydT("imagePasteEyebrow") : ttydT("filePasteEyebrow");
        var title = document.createElement("h3");
        title.id = "cp-file-paste-title";
        title.textContent = files.length === 1 && String(files[0].type || "").match(/^image\//) ? ttydT("sendPastedImage") : ttydT("sendPastedFiles");
        var desc = document.createElement("p");
        desc.id = "cp-file-paste-desc";
        desc.textContent = files.length === 1 && String(files[0].type || "").match(/^image\//)
            ? ""
            : ttydT("confirmUploadHint");
        heading.appendChild(eyebrow);
        heading.appendChild(title);
        heading.appendChild(desc);
        var closeBtn = document.createElement("button");
        closeBtn.id = "cp-file-paste-close";
        closeBtn.textContent = "×";
        head.appendChild(heading);
        head.appendChild(closeBtn);

        var body = document.createElement("div");
        body.id = "cp-file-paste-body";
        var preview = document.createElement("div");
        preview.id = "cp-file-paste-preview";
        preview.appendChild(previewContent.element);
        body.appendChild(preview);

        var actions = document.createElement("div");
        actions.id = "cp-file-paste-actions";
        var cancelBtn = document.createElement("button");
        cancelBtn.id = "cp-file-paste-cancel";
        cancelBtn.className = "cp-file-paste-btn";
        cancelBtn.textContent = ttydT("cancel");
        var sendBtn = document.createElement("button");
        sendBtn.id = "cp-file-paste-send";
        sendBtn.className = "cp-file-paste-btn";
        sendBtn.textContent = ttydT("send");
        actions.appendChild(cancelBtn);
        actions.appendChild(sendBtn);

        modal.appendChild(head);
        modal.appendChild(body);
        modal.appendChild(actions);
        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        var cleanup = previewContent.cleanup || null;
        function close(): void {
            document.removeEventListener("keydown", onKeyDown, true);
            if (cleanup) {
                cleanup();
                cleanup = null;
            }
            if (overlay.parentNode) {
                overlay.parentNode.removeChild(overlay);
            }
        }
        function onKeyDown(event: KeyboardEvent): void {
            if (event.key === "Escape") {
                event.preventDefault();
                close();
            }
        }
        overlay.addEventListener("click", function(event: MouseEvent): void {
            if (event.target === overlay) {
                close();
            }
        });
        closeBtn.addEventListener("click", close);
        cancelBtn.addEventListener("click", close);
        sendBtn.addEventListener("click", function(): void {
            close();
            uploadPastedFiles(files);
        });
        document.addEventListener("keydown", onKeyDown, true);
        sendBtn.focus();
    }

    function openPasteConfirmDialog(titleText: string, descriptionText: string, bodyValue: string, onSend: (bodyValue: string) => void): void {
        var existing = document.getElementById("cp-paste-confirm-overlay");
        if (existing && existing.parentNode) {
            existing.parentNode.removeChild(existing);
        }
        var overlay = document.createElement("div");
        overlay.id = "cp-paste-confirm-overlay";
        var modal = document.createElement("div");
        modal.id = "cp-paste-confirm-modal";
        var title = document.createElement("h3");
        title.id = "cp-paste-confirm-title";
        title.textContent = titleText;
        var desc = document.createElement("p");
        desc.id = "cp-paste-confirm-desc";
        desc.textContent = descriptionText;
        var body = document.createElement("textarea");
        body.id = "cp-paste-confirm-body";
        body.value = bodyValue || "";
        var actions = document.createElement("div");
        actions.id = "cp-paste-confirm-actions";
        var cancelBtn = document.createElement("button");
        cancelBtn.id = "cp-paste-confirm-cancel";
        cancelBtn.className = "cp-paste-confirm-btn";
        cancelBtn.textContent = ttydT("cancel");
        var sendBtnEl = document.createElement("button");
        sendBtnEl.id = "cp-paste-confirm-send";
        sendBtnEl.className = "cp-paste-confirm-btn";
        sendBtnEl.textContent = ttydT("send");
        actions.appendChild(cancelBtn);
        actions.appendChild(sendBtnEl);
        modal.appendChild(title);
        modal.appendChild(desc);
        modal.appendChild(body);
        modal.appendChild(actions);
        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        function close(): void {
            document.removeEventListener("keydown", onKeyDown, true);
            if (overlay.parentNode) {
                overlay.parentNode.removeChild(overlay);
            }
        }
        function onKeyDown(event: KeyboardEvent): void {
            if (event.key === "Escape") {
                event.preventDefault();
                close();
            }
        }
        overlay.addEventListener("click", function(event: MouseEvent): void {
            if (event.target === overlay) {
                close();
            }
        });
        cancelBtn.addEventListener("click", function(): void {
            close();
        });
        sendBtnEl.addEventListener("click", function(): void {
            var nextValue = body.value || "";
            close();
            onSend(nextValue);
        });
        document.addEventListener("keydown", onKeyDown, true);
        body.focus();
        try {
            body.setSelectionRange(body.value.length, body.value.length);
        } catch (_error) {
        }
    }

    function sendPastedText(command: string): void {
        if (!command) {
            return;
        }
        openPasteConfirmDialog(
            ttydT("sendPastedText"),
            ttydT("pastedTextDetected"),
            command,
            function(bodyValue: string): void {
                var finalCommand = normalizePromptPunctuation(bodyValue || "");
                if (!finalCommand) {
                    return;
                }
                writeClientTrace("cp-paste-http", {
                    command_len: finalCommand.length,
                    command_preview: clipTracePreview(finalCommand, 160),
                });
                sendHTTP(finalCommand).catch(function(): void {
                    flashButton(restartBtn);
                });
            }
        );
    }

    function sendPastedFiles(files: File[]): void {
        if (!files.length) {
            return;
        }
        openFilePasteDialog(files);
    }

    function uploadPastedFiles(files: File[]): void {
        if (!files.length) {
            return;
        }
        var PromiseCtor = (window as any).Promise || Promise;
        writeClientTrace("cp-paste-files", {
            count: files.length,
            names: files.map(function(file: File): string {
                return file.name || "file";
            }).join(","),
        });
        files.reduce(function(chain: Promise<any>, file: File): Promise<any> {
            return chain.then(function(): Promise<any> {
                return uploadPastedFile(term, webtty, paneId, apiHeaders, file);
            });
        }, PromiseCtor.resolve()).catch(function(): void {
            flashButton(restartBtn);
        });
    }

    window.addEventListener("paste", function(event: ClipboardEvent): void {
        var target = event.target as HTMLElement | null;
        if (isPasteDialogTarget(target)) {
            return;
        }
        var files = normalizePastedFiles(event);
        if (files.length) {
            event.preventDefault();
            event.stopPropagation();
            if ((event as any).stopImmediatePropagation) {
                (event as any).stopImmediatePropagation();
            }
            sendPastedFiles(files);
            return;
        }
        var rawText = (event.clipboardData && event.clipboardData.getData("text/plain")) || "";
        if (!rawText) {
            return;
        }
        var trimmedText = rawText.trim();
        if (!trimmedText) {
            return;
        }
        var text = normalizePromptPunctuation(trimmedText);
        var lineCount = text.split(/\r\n|\r|\n/).length;
        var shouldConfirm = lineCount > 1 || text.length > 100;
        if (!shouldConfirm) {
            if (isEditableTextTarget(target) && !isTerminalTarget(target)) {
                event.preventDefault();
                insertTextAtCursor(target, text);
            }
            return;
        }
        event.preventDefault();
        event.stopPropagation();
        if ((event as any).stopImmediatePropagation) {
            (event as any).stopImmediatePropagation();
        }
        sendPastedText(text);
    }, true);

    webtty.onConnectionStateChange(function(isOpen: boolean): void {
        if (isOpen) {
            hadOpen = true;
            restartBtn.classList.remove("restarting");
            loading.hide();
            loadWindows();
        }
    });
}
