import { Terminal, WebTTY } from "./webtty";
import { applyMonoFontVar, monoFontStack } from "./font";

interface StorageHelper {
    get(key: string, defaultValue: any): any;
    set(key: string, value: any): void;
}

interface LoadingOverlayController {
    show(message?: string): void;
    hide(): void;
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
  padding-top: 28px !important;
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
  width: 36px;
  height: 36px;
  border: 3px solid rgba(255,255,255,0.1);
  border-top-color: rgba(99,102,241,0.7);
  border-radius: 50%;
  animation: cp-spin 0.8s linear infinite;
  margin-bottom: 18px;
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
  background: rgba(34,37,46,0.97);
  border: 1px solid rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.92);
  font-size: 11px;
  line-height: 1.2;
  white-space: nowrap;
  box-shadow: 0 10px 30px rgba(0,0,0,0.45);
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
  background: rgba(34,37,46,0.97);
  border-right: 1px solid rgba(255,255,255,0.08);
  border-bottom: 1px solid rgba(255,255,255,0.08);
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
  min-width: 164px;
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
  height: 28px;
  background: rgba(30,30,30,0.95);
  border-bottom: 1px solid rgba(255,255,255,0.06);
  font-family: var(--cp-mono-font);
  padding: 0 4px;
  gap: 0;
}
#fixed-top-action {
  position: fixed;
  top: 0;
  right: 4px;
  z-index: 9999;
  display: flex;
  flex-direction: row;
  align-items: center;
  height: 28px;
  gap: 2px;
  font-family: var(--cp-mono-font);
}
.fta-sep {
  width: 1px;
  height: 14px;
  background: rgba(255,255,255,0.1);
  margin: 0 4px;
}
.fta-btn {
  background: none;
  border: none;
  border-radius: 4px;
  color: rgba(255,255,255,0.4);
  font-size: 12px;
  padding: 3px 6px;
  cursor: pointer;
  transition: all .15s;
  font-family: var(--cp-mono-font);
  line-height: 1;
}
.fta-btn:hover { color: rgba(255,255,255,0.9); background: rgba(255,255,255,0.08); }#cp-win-tabs {
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
.fta-btn:hover { color: rgba(255,255,255,0.9); background: rgba(255,255,255,0.08); }
#cp-win-restart {
  width: 30px;
  min-width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
}
#cp-win-restart.restarting { color: rgba(59,130,246,0.8); animation: cp-spin .8s linear infinite; }
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
#cp-send {
  position: absolute;
  right: 12px;
  bottom: 12px;
  width: 26px;
  height: 26px;
  border-radius: 8px;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(99,102,241,0.15);
  color: rgba(99,102,241,0.7);
  transition: all .15s;
}
#cp-send:hover { background: rgba(99,102,241,0.3); color: rgba(129,140,248,1); }
#cp-send:active { transform: scale(0.92); }
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
`;
    document.head.appendChild(style);
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
    fixedTop.innerHTML =
        '<button id="cp-win-add" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="新加tmux window">+</button>' +
        '<button id="cp-win-restart" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="重启tmux">↻</button>' +
        '<span class="fta-sep"></span>' +
        '<button id="cp-sigint" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Ctrl+C">^C</button>' +
        '<button id="cp-esc-key" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Esc">⎋</button>' +
        '<button id="cp-backspace-key" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Backspace">⌫</button>' +
        '<button id="cp-up-key" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Up">↑</button>' +
        '<button id="cp-down-key" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Down">↓</button>' +
        '<button id="cp-enter-key" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="Enter">↵</button>' +
        '<span class="fta-sep"></span>' +
        '<button id="cp-reload" class="fta-btn cp-tooltip-host cp-tooltip-right cp-tooltip-bottom" data-tooltip="刷新页面" onclick="location.reload()">⟳</button>' +
        '<button id="cp-more" class="fta-btn" title="More" style="display:none">⋯</button>' +
        '<div id="cp-more-menu" class="cp-drop">' +
            '<select id="cp-model" title="Model">' +
                '<option value="">Model</option>' +
                '<option value="claude-opus-4.6">opus-4.6</option>' +
                '<option value="claude-opus-4.5">opus-4.5</option>' +
                '<option value="claude-sonnet-4.5">sonnet-4.5</option>' +
                '<option value="claude-sonnet-4">sonnet-4</option>' +
                '<option value="claude-haiku-4.5">haiku-4.5</option>' +
                '<option value="deepseek-3.2">deepseek-3.2</option>' +
                '<option value="minimax-m2.1">minimax-m2.1</option>' +
                '<option value="qwen3-coder-next">qwen3-coder</option>' +
            "</select>" +
            '<button id="cp-compact" class="cp-drop-item">Compact</button>' +
        "</div>";

    document.body.appendChild(winFloat);
    document.body.appendChild(fixedTop);

    var input = (document.getElementById("cp-input") || document.createElement("textarea")) as HTMLTextAreaElement;
    var sendBtn = (document.getElementById("cp-send") || document.createElement("button")) as HTMLButtonElement;
    var sigintBtn = document.getElementById("cp-sigint") as HTMLButtonElement;
    var escKeyBtn = document.getElementById("cp-esc-key") as HTMLButtonElement;
    var backspaceKeyBtn = document.getElementById("cp-backspace-key") as HTMLButtonElement;
    var upKeyBtn = document.getElementById("cp-up-key") as HTMLButtonElement;
    var downKeyBtn = document.getElementById("cp-down-key") as HTMLButtonElement;
    var enterKeyBtn = document.getElementById("cp-enter-key") as HTMLButtonElement;
    var enterBtn = (document.getElementById("cp-enter") || document.createElement("button")) as HTMLButtonElement;
    var modelSel = document.getElementById("cp-model") as HTMLSelectElement;
    var moreBtn = document.getElementById("cp-more") as HTMLButtonElement;
    var moreMenu = document.getElementById("cp-more-menu") as HTMLElement;
    var winTabs = document.getElementById("cp-win-tabs") as HTMLElement;
    var collapseBtn = (document.getElementById("cp-collapse") || document.createElement("button")) as HTMLButtonElement;
    var restartBtn = document.getElementById("cp-win-restart") as HTMLButtonElement;
    var addWindowBtn = document.getElementById("cp-win-add") as HTMLButtonElement;

    var historyKey = "cicy_hist_" + paneId;
    var draftKey = "cicy_draft_" + paneId;
    var clientTraceKey = "cicy_ttyd_trace_" + paneId;
    var history = storage.get(historyKey, []) as string[];
    var historyIndex = -1;
    var tempDraft = "";
    var enterToSend = storage.get("cicy_enter_to_send", true) as boolean;
    var sigintDefaultTooltip = sigintBtn.getAttribute("data-tooltip") || "发送 Ctrl+C Event";
    var sigintConfirmTooltip = "再点一次确认发送 Ctrl+C\n可能会终止当前操作\n如果不想操作，继续等待即可";

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
        enterBtn.setAttribute("data-tooltip", enterToSend ? "发送Prompt方式:Enter" : "发送Prompt方式:Shift+Enter");
    }

    function addHistory(command: string): void {
        history = [command].concat(history.filter(function(item: string): boolean {
            return item !== command;
        })).slice(0, 50);
        storage.set(historyKey, history);
    }

    function doSend(value?: string): void {
        var command = value !== undefined ? value : input.value;
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
            moreMenu.classList.remove("open");
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
            var close = win.index === "0" ? "" : '<span class="cp-wdel" data-idx="' + win.index + '" data-tooltip="关闭tmux window">✕</span>';
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

    input.value = storage.get(draftKey, "");
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

    escKeyBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        if (!sendInput("\x1b")) {
            flashButton(escKeyBtn);
        }
    });

    backspaceKeyBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        if (!sendInput("\x7f")) {
            flashButton(backspaceKeyBtn);
        }
    });

    upKeyBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        if (!sendInput("\x1b[A")) {
            flashButton(upKeyBtn);
        }
    });

    downKeyBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        if (!sendInput("\x1b[B")) {
            flashButton(downKeyBtn);
        }
    });

    enterKeyBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        sendTmuxKey("Enter").catch(function(): void {
            flashButton(enterKeyBtn);
        });
    });

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
                    flashButton(enterKeyBtn);
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
        storage.set(draftKey, input.value);
    });
    sendBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        doSend();
    });

    twiceConfirm(document.getElementById("cp-compact") as HTMLButtonElement, function(): void {
        sendLine("/model auto");
        setTimeout(function(): void {
            sendLine("/model claude-sonnet-4");
        }, 400);
        setTimeout(function(): void {
            sendLine("/compact --truncate-large-messages true --max-message-length 500");
        }, 1200);
    });

    modelSel.addEventListener("change", function(): void {
        if (!modelSel.value) {
            return;
        }
        sendLine("/model " + modelSel.value);
        modelSel.value = "";
        moreMenu.classList.remove("open");
    });

    document.body.appendChild(moreMenu);
    moreBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        var isOpen = moreMenu.classList.contains("open");
        moreMenu.classList.remove("open");
        if (!isOpen) {
            var rect = moreBtn.getBoundingClientRect();
            moreMenu.style.position = "fixed";
            moreMenu.style.top = rect.top + "px";
            moreMenu.style.left = (rect.right + 4) + "px";
            moreMenu.style.right = "auto";
            moreMenu.classList.add("open");
        }
    });
    document.addEventListener("click", function(event: MouseEvent): void {
        var target = event.target as Node;
        if (!moreMenu.contains(target) && target !== moreBtn) {
            moreMenu.classList.remove("open");
        }
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
            if (closeButton.dataset.confirm) {
                hideFixedTooltip();
                apiFetch("DELETE", "", { session: paneId, index: closeButton.dataset.idx }).then(loadWindows);
            } else {
                var confirmButton = closeButton;
                confirmButton.dataset.confirm = "1";
                confirmButton.textContent = "?";
                confirmButton.setAttribute("data-tooltip", "再点一次确认关闭tmux window");
                confirmButton.classList.add("cp-confirm");
                showFixedTooltip(confirmButton, "再点一次确认关闭tmux window");
                setTimeout(function(): void {
                    delete confirmButton.dataset.confirm;
                    confirmButton.textContent = "✕";
                    confirmButton.setAttribute("data-tooltip", "关闭tmux window");
                    confirmButton.classList.remove("cp-confirm");
                    hideFixedTooltip();
                }, 2000);
            }
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

    var sigintPending = false;
    var sigintTimer = 0;
    function resetSigintConfirm(): void {
        sigintPending = false;
        clearTimeout(sigintTimer);
        sigintBtn.textContent = "^C";
        sigintBtn.style.color = "";
        sigintBtn.setAttribute("data-tooltip", sigintDefaultTooltip);
        sigintBtn.classList.remove("cp-tooltip-force");
        sigintBtn.classList.remove("cp-tooltip-multiline");
    }
    sigintBtn.addEventListener("click", function(event: MouseEvent): void {
        event.preventDefault();
        event.stopPropagation();
        if (!sigintPending) {
            sigintPending = true;
            sigintBtn.textContent = "⚠";
            sigintBtn.style.color = "rgba(239,68,68,0.9)";
            sigintBtn.setAttribute("data-tooltip", sigintConfirmTooltip);
            sigintBtn.classList.add("cp-tooltip-force");
            sigintBtn.classList.add("cp-tooltip-multiline");
            sigintTimer = window.setTimeout(function(): void {
                resetSigintConfirm();
            }, 2000);
            return;
        }

        resetSigintConfirm();
        if (!sendInput("\x03")) {
            flashButton(sigintBtn);
        }
    });

    var restartPending = false;
    var restartTimer = 0;
    restartBtn.addEventListener("click", function(event: MouseEvent): void {
        event.stopPropagation();
        if (!restartPending) {
            restartPending = true;
            restartBtn.textContent = "⚠";
            restartBtn.style.color = "rgba(239,68,68,0.9)";
            restartTimer = window.setTimeout(function(): void {
                restartPending = false;
                restartBtn.textContent = "↻";
                restartBtn.style.color = "";
            }, 2000);
            return;
        }

        clearTimeout(restartTimer);
        restartPending = false;
        restartBtn.textContent = "↻";
        restartBtn.style.color = "";
        if (!webtty.isConnectionOpen()) {
            flashButton(restartBtn);
            return;
        }
        restartBtn.classList.add("restarting");
        loading.show("Restarting...");
        webtty.requestAPI("POST", "/api/tmux/panes/" + paneId + "/restart", undefined, apiHeaders).catch(function(): void {
            restartBtn.classList.remove("restarting");
        });
        setTimeout(function(): void {
            restartBtn.classList.remove("restarting");
        }, 30000);
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
    micButton.title = "Voice Mode";
    micButton.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path><path d="M19 10v2a7 7 0 0 1-14 0v-2"></path><line x1="12" x2="12" y1="19" y2="22"></line></svg>';
    fixedTop.insertBefore(micButton, moreBtn);

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

    webtty.onConnectionStateChange(function(isOpen: boolean): void {
        if (isOpen) {
            hadOpen = true;
            restartBtn.classList.remove("restarting");
            loading.hide();
            loadWindows();
        } else if (hadOpen) {
            loading.show();
        }
    });
}
