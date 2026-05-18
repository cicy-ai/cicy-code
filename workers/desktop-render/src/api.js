// Backend bridge. In Electron the contextBridge from
// src/backends/homepage-preload.js exposes window.cicy with real IPC.
// In a plain browser tab (Vite dev), no such bridge exists — fall back to
// a mock that pulls Local health from the same origin's /api/health
// (assumes cicy-code is at the same host:port that Vite is served from
// OR at a fixed dev URL configured below) and keeps Cloud backends in
// localStorage so the UI is fully exercisable for layout iteration.

const MODE = (typeof window !== "undefined" && window.cicy) ? "electron" : "browser";

// In browser mode, default to the cicy-code on the same host:8008.
// Override with ?cicy=http://other:8008 in the URL bar if needed.
const params = new URLSearchParams(typeof location !== "undefined" ? location.search : "");
const DEV_LOCAL_BASE = params.get("cicy")
  || (typeof location !== "undefined" ? `${location.protocol}//${location.hostname}:8008` : "http://127.0.0.1:8008");
const LS_KEY = "cicy-desktop-mock-backends";
const LS_LOCAL = {
  id: "local",
  name: "Local (dev)",
  kind: "local",
  url: "",
  addedAt: new Date(0).toISOString(),
};

function lsLoad() {
  try { return JSON.parse(localStorage.getItem(LS_KEY) || "[]"); } catch { return []; }
}
function lsSave(list) { localStorage.setItem(LS_KEY, JSON.stringify(list)); }

async function probeUrl(url, token) {
  if (!url) return { ok: false, error: "no url" };
  try {
    const u = new URL(url);
    u.pathname = "/api/health";
    u.search = "";
    const started = Date.now();
    const r = await fetch(u.toString(), {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    const latencyMs = Date.now() - started;
    if (!r.ok) return { ok: false, statusCode: r.status, latencyMs };
    const data = await r.json();
    return { ok: true, statusCode: r.status, latencyMs, ...data };
  } catch (e) { return { ok: false, error: e.message }; }
}

const browserApi = {
  mode: "browser",
  backends: {
    async list() {
      const cloud = lsLoad();
      return [LS_LOCAL, ...cloud].map(b => ({
        ...b,
        resolvedUrl: b.kind === "local"
          ? `${DEV_LOCAL_BASE}/`
          : (b.url + (b.token ? `${b.url.includes("?") ? "&" : "?"}token=${encodeURIComponent(b.token)}` : "")),
      }));
    },
    async add(input) {
      const list = lsLoad();
      const b = {
        id: crypto.randomUUID(),
        name: input.name || "Unnamed",
        kind: "manual",
        url: input.url,
        token: input.token || undefined,
        addedAt: new Date().toISOString(),
      };
      list.push(b);
      lsSave(list);
      return b;
    },
    async remove(id) {
      if (id === "local") return false;
      const list = lsLoad().filter(b => b.id !== id);
      lsSave(list);
      return true;
    },
    async probe(input) {
      return probeUrl(input.url, input.token);
    },
    async open(id) {
      const list = await browserApi.backends.list();
      const b = list.find(x => x.id === id);
      if (!b) return { ok: false, error: "not found" };
      window.open(b.resolvedUrl, "_blank");
      return { ok: true, windowId: -1 };
    },
    async health(id) {
      if (id === "local") return probeUrl(DEV_LOCAL_BASE, null);
      const list = lsLoad();
      const b = list.find(x => x.id === id);
      if (!b) return { ok: false, error: "not found" };
      return probeUrl(b.url, b.token);
    },
    async healthAll() {
      const list = await browserApi.backends.list();
      const out = await Promise.all(list.map(async b => ({
        id: b.id,
        health: b.kind === "local"
          ? await probeUrl(DEV_LOCAL_BASE, null)
          : await probeUrl(b.url, b.token),
      })));
      return out;
    },
    async restartSidecar() {
      return { ok: false, error: "restartSidecar only works inside Electron" };
    },
  },
  windows: {
    async list() {
      // In browser mode we just remember windows we opened this session.
      return [];
    },
    async focus() { return false; },
  },
  updates: {
    async check() {
      const health = await probeUrl(DEV_LOCAL_BASE, null);
      const currentVersion = health.ok ? health.version : null;
      try {
        const r = await fetch("https://api.github.com/repos/cicy-ai/cicy-code/releases/latest", {
          headers: { "Accept": "application/vnd.github+json" },
        });
        if (!r.ok) throw new Error(`GitHub ${r.status}`);
        const release = await r.json();
        const latestVersion = String(release.tag_name || "").replace(/^v/, "");
        const hasUpdate = !!currentVersion && cmpSemver(latestVersion, currentVersion) > 0;
        return { hasUpdate, currentVersion, latestVersion, releaseUrl: release.html_url };
      } catch (e) {
        throw new Error(`update check failed: ${e.message}`);
      }
    },
    async openReleasePage(url) {
      if (url) window.open(url, "_blank");
      return true;
    },
  },
  clipboard: {
    async write(text) {
      try { await navigator.clipboard.writeText(String(text || "")); return true; }
      catch { return false; }
    },
  },
  system: {
    // Browser-dev stub for the Local-card prereq probe. Honors
    // ?platform=win|mac|linux + ?prereq=ok|missing + ?installed=true|false
    // + ?running=true|false in the URL so we can preview every state
    // matrix without an actual host check.
    async checkPrereq() {
      const ua = (navigator.userAgent || "").toLowerCase();
      const auto = ua.includes("windows") ? "windows"
        : ua.includes("mac")               ? "darwin"
        :                                    "linux";
      const platform = params.get("platform") === "win" ? "windows"
        : params.get("platform") === "mac" ? "darwin"
        : params.get("platform") === "linux" ? "linux"
        : auto;
      const force = params.get("prereq");
      if (platform === "windows") {
        const ok = force === "missing" ? false : true;
        return {
          platform, kind: "docker",
          ok,
          required: "Docker Desktop",
          version: ok ? "27.3.1" : null,
          installUrl: "https://www.docker.com/products/docker-desktop/",
        };
      }
      const ok = force === "missing" ? false : true;
      return {
        platform, kind: "node",
        ok,
        required: "Node.js 22+",
        version: ok ? "v22.11.0" : "v18.19.0",
        installUrl: "https://nodejs.org/en/download",
      };
    },
    async checkCicyCodeInstalled() {
      // Same URL-param override for browser-dev preview.
      const v = params.get("installed");
      const installed = v == null ? true : v === "true";
      return { installed, version: installed ? "2.0.1" : null };
    },
    async localIPs() {
      // In a plain browser tab we can't read the host's IPs without WebRTC
      // tricks (which most browsers now block). Return empty — Electron
      // path provides the real impl via systemOps.localIPs.
      return [];
    },
  },
};

function cmpSemver(a, b) {
  const parse = s => String(s || "").replace(/^v/, "").split("-")[0].split(".").map(n => parseInt(n, 10) || 0);
  const A = parse(a), B = parse(b);
  for (let i = 0; i < Math.max(A.length, B.length); i++) {
    const x = A[i] || 0, y = B[i] || 0;
    if (x !== y) return x > y ? 1 : -1;
  }
  return 0;
}

// Hybrid bridge: each namespace prefers the Electron preload (when present)
// and falls back to the browser stub otherwise. Lets us add new namespaces
// like `system` to browserApi independently of the homepage-preload upgrade.
const eApi = (typeof window !== "undefined" && window.cicy) ? window.cicy : null;
// Browser stub for cicycode.* — Electron preload owns the real impl via
// exec_shell. In browser dev we just toast "not available" so the UI
// still renders without crashing.
const browserCicycode = {
  install:   async () => ({ ok: false, error: "cicycode.install only works inside Electron" }),
  upgrade:   async () => ({ ok: false, error: "cicycode.upgrade only works inside Electron" }),
  start:     async () => ({ ok: false, error: "cicycode.start only works inside Electron" }),
  stop:      async () => ({ ok: false, error: "cicycode.stop only works inside Electron" }),
  uninstall: async () => ({ ok: false, error: "cicycode.uninstall only works inside Electron" }),
};

// system + cicycode commands live entirely in Vite (cicycode-ops.js) so
// the install / start / stop / WSL / npm strings are HMR-able. The Electron
// preload's namespaces of the same name are now bypassed for cicycode +
// system (preload's stale Docker variants are no longer used).
import { systemOps, cicycodeOps } from "./cicycode-ops.js";

export const api = {
  backends:  (eApi && eApi.backends)  || browserApi.backends,
  windows:   (eApi && eApi.windows)   || browserApi.windows,
  updates:   (eApi && eApi.updates)   || browserApi.updates,
  clipboard: (eApi && eApi.clipboard) || browserApi.clipboard,
  shell:     (eApi && eApi.shell)     || { openExternal: (u) => { if (u) window.open(u, "_blank"); return true; } },
  app:       (eApi && eApi.app)       || { quit: () => { window.close(); return true; } },
  tos:       (eApi && eApi.tos)       || browserTos(),
  // Always Vite-side — drops the preload's stale Docker commands.
  system:    eApi ? systemOps : browserApi.system,
  cicycode:  eApi ? cicycodeOps : browserCicycode,
};

function browserTos() {
  const KEY = "cicy-tos-accepted";
  return {
    get:    async () => ({ version: localStorage.getItem(KEY), acceptedAt: null }),
    accept: async (version) => { localStorage.setItem(KEY, version); return { ok: true }; },
  };
}
export const apiMode = eApi ? "electron" : "browser";
export const devLocalBase = DEV_LOCAL_BASE;
