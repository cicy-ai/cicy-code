import { useCallback, useEffect, useRef, useState } from "react";
import { api, apiMode } from "./api.js";
import Button from "./components/Button.jsx";
import Icon from "./components/Icon.jsx";
import BackendCard from "./components/BackendCard.jsx";
import SidecarBanner from "./components/SidecarBanner.jsx";
import WslSetupBanner from "./components/WslSetupBanner.jsx";
import BackendModal from "./components/BackendModal.jsx";
import Toast, { pushToast } from "./components/Toast.jsx";
import { useT } from "./i18n";
import "./App.css";

export default function App() {
  const t = useT();
  const [backends, setBackends]   = useState([]);
  const [healthMap, setHealthMap] = useState({});
  const [sidecar, setSidecar]     = useState({ state: "checking", info: null, progress: null, error: null });
  const [wsl, setWsl]             = useState(null);
  const [wslChecking, setWslChecking] = useState(false);
  const [openingId, setOpeningId] = useState(null);
  const [tab, setTab]               = useState("all"); // "all" | "local" | "cloud"
  const [modalState, setModalState] = useState({ open: false, mode: "add", backend: null });
  const [appVersion, setAppVersion] = useState(""); // cicy-desktop version, populated in electron mode
  const [installLog, setInstallLog] = useState(() => loadPersistedLog()); // accumulated lines from windowsInstall onProgress (kind:"log")
  const [installSteps, setInstallSteps] = useState(() => loadPersistedSteps()); // user-facing step timeline

  // Persist install log only — sidecar state always re-fetches fresh on
  // mount so we don't show a stale "uptodate" badge if the binary moved
  // or got broken between sessions.
  useEffect(() => {
    try {
      sessionStorage.setItem(LS_LOG_KEY, JSON.stringify({ at: Date.now(), lines: installLog }));
    } catch {}
  }, [installLog]);
  useEffect(() => {
    try {
      sessionStorage.setItem(LS_STEPS_KEY, JSON.stringify({ at: Date.now(), steps: installSteps }));
    } catch {}
  }, [installSteps]);


  const checkWsl = useCallback(async () => {
    const isWin = window.cicy?.platform === "win32";
    if (apiMode !== "electron" || !window.cicy?.sidecar?.wslStatus || !isWin) {
      setWsl(null);
      return;
    }
    setWslChecking(true);
    try {
      const s = await window.cicy.sidecar.wslStatus();
      setWsl(s);
    } catch (e) {
      setWsl({ supported: true, installed: false, error: e.message });
    } finally {
      setWslChecking(false);
    }
  }, []);

  // ── Loaders ──
  const loadBackends = useCallback(async () => {
    const list = await api.backends.list();
    setBackends(list);
    return list;
  }, []);

  const loadHealth = useCallback(async (list) => {
    const items = list || backends;
    if (!items.length) return;
    const results = await api.backends.healthAll();
    const next = {};
    for (const r of results) next[r.id] = r.health;
    setHealthMap(next);
  }, [backends]);

  const checkSidecar = useCallback(async () => {
    if (apiMode !== "electron" || !window.cicy?.sidecar) {
      setSidecar({ state: "uptodate", info: null, progress: null, error: null });
      // Stable state — drop any timeline left over from a previous run.
      setInstallSteps([]);
      setInstallLog([]);
      return;
    }
    try {
      const status = await window.cicy.sidecar.status();
      if (!status.userInstalled) {
        const check = await window.cicy.sidecar.checkLatest();
        if (!check.ok) {
          setSidecar((s) => ({ ...s, state: "error", error: check.error }));
        } else {
          setSidecar((s) => ({ ...s, state: "missing", info: check }));
        }
        return;
      }
      // Installed — also check if outdated (non-blocking, best-effort).
      setSidecar((s) => ({ ...s, state: "uptodate", info: { installedVersion: status.userVersion } }));
      // Don't auto-clear installSteps/installLog here — the timeline's
      // "✓ Installed v…" final entry is still useful info to surface
      // after the install finishes. The banner shows a Close button on
      // the done state which is the user's signal to dismiss it.
      // We do clear partial timelines (no done step) so a stale "still
      // running" banner doesn't survive a fresh page load.
      setInstallSteps((steps) => {
        if (!steps || steps.length === 0) return steps;
        return steps[steps.length - 1].phase === "done" ? steps : [];
      });
      try {
        const check = await window.cicy.sidecar.checkLatest();
        if (check.ok && check.latest && check.installedVersion !== check.latest) {
          setSidecar((s) => ({ ...s, state: "outdated", info: check }));
        }
      } catch {}
    } catch (e) {
      setSidecar((s) => ({ ...s, state: "error", error: e.message }));
    }
  }, []);

  // ── Init ──
  useEffect(() => {
    (async () => {
      await loadBackends();
      checkSidecar();
      checkWsl();

      // Pull cicy-desktop version (electron mode only) for the topbar badge.
      if (window.cicy?.app?.getVersion) {
        try {
          const v = await window.cicy.app.getVersion();
          if (v?.desktop) setAppVersion(`v${v.desktop}`);
        } catch {}
      }
    })();
  }, [loadBackends, checkSidecar, checkWsl]);

  // After backends load, kick off health.
  useEffect(() => {
    if (!backends.length) return;
    loadHealth(backends);
    const t = setInterval(() => loadHealth(backends), 8000);
    return () => clearInterval(t);
  }, [backends, loadHealth]);

  // Subscribe to sidecar progress events.
  useEffect(() => {
    if (apiMode !== "electron" || !window.cicy?.sidecar?.onProgress) return;
    return window.cicy.sidecar.onProgress((p) => {
      setSidecar((s) => {
        if (s.state !== "installing") return s;
        if (p.phase === "error")     return { ...s, state: "error", error: p.error || p.message, progress: null };
        if (p.phase === "cancelled") { checkSidecar(); return { ...s, state: "checking", progress: null }; }
        return { ...s, progress: p };
      });
    });
  }, [checkSidecar]);

  // ── Deep link: cicy://addTeam?title=&url=&token= ──
  useEffect(() => {
    if (apiMode !== "electron" || !window.cicy?.deeplink?.onAddTeam) return;
    return window.cicy.deeplink.onAddTeam(async ({ title, url, token }) => {
      if (!url) return;
      // De-duplicate by URL origin
      const list = await api.backends.list();
      const exists = list.some((b) => {
        try { return new URL(b.url || "").origin === new URL(url).origin; } catch { return false; }
      });
      if (exists) return;
      await api.backends.add({ name: title || url, url, token: token || undefined });
      await loadBackends();
    });
  }, [loadBackends]);

  // ── Actions ──
  const handleOpen = async (b) => {
    setOpeningId(b.id);
    try {
      if (window.electronRPC) {
        try {
          // Resolve fresh URL first (reads latest token from WSL global.json).
          let url = b.url;
          if (window.cicy?.backends?.resolveUrl) {
            url = (await window.cicy.backends.resolveUrl(b.id)) || b.resolvedUrl || b.url;
          } else {
            url = b.resolvedUrl || b.url;
          }
          // Check if any window is already showing this backend (same origin+path,
          // ignoring ?token= and other query params).
          const wins = await fetchWindows();
          const existing = wins.find((w) => w.url && w.url.toLowerCase() === url.toLowerCase());
          if (existing) {
            await window.electronRPC("control_electron_BrowserWindow", {
              win_id: existing.id,
              code: "(win.isMinimized() && win.restore(), win.show(), win.focus(), 'focused')",
            });
            pushToast(t("toast.opened", { name: b.name }));
            return;
          }
          await window.electronRPC("open_window", { url, reuseWindow: false });
          pushToast(t("toast.opened", { name: b.name }));
          return;
        } catch (e) {
          // fall through to api.backends.open
        }
      }
      const r = await api.backends.open(b.id);
      if (r?.ok) pushToast(t("toast.opened", { name: b.name }));
      else       pushToast(t("toast.check_failed", { error: r?.error || "unknown" }), "error");
    } finally {
      setOpeningId(null);
    }
  };

  const handleRemove = async (b) => {
    if (!confirm(t("confirm.remove", { name: b.name }))) return;
    await api.backends.remove(b.id);
    await loadBackends();
    pushToast(t("toast.removed", { name: b.name }));
  };

  const handleRestart = async () => {
    pushToast(t("toast.restarting"));
    const r = await api.backends.restartSidecar();
    if (r?.ok) {
      pushToast(t("toast.restarted", { pid: r.pid }), "success");
      setTimeout(() => loadHealth(backends), 1500);
    } else {
      pushToast(t("toast.restart_failed", { error: r?.error || "unknown" }), "error");
    }
  };

  const handleCheckUpdate = async () => {
    if (apiMode !== "electron" || !window.cicy?.sidecar) {
      pushToast(t("toast.install_only_electron"), "error");
      return;
    }
    pushToast(t("toast.checking"));
    const check = await window.cicy.sidecar.checkLatest();
    if (!check.ok) { pushToast(t("toast.check_failed", { error: check.error }), "error"); return; }
    if (check.installedVersion === check.latest) {
      setSidecar((s) => ({ ...s, state: "uptodate", info: check }));
      pushToast(t("toast.uptodate", { version: check.latest }), "success");
    } else {
      setSidecar((s) => ({ ...s, state: "outdated", info: check }));
      pushToast(t("toast.new_version", { version: check.latest }));
    }
  };

  const handleInstall = async () => {
    if (apiMode !== "electron" || !window.cicy?.sidecar) {
      pushToast(t("toast.install_only_electron"), "error");
      return;
    }

    setSidecar((s) => ({ ...s, state: "installing", progress: { phase: "init", message: "Preparing…" }, error: null }));
    setInstallLog([]);
    setInstallSteps([]);

    // Win32: drive the install entirely from the renderer using
    // electronRPC.exec_shell. This bypasses the main-process sidecar:install
    // handler in shipped versions of cicy-desktop, so we can fix wsl install
    // bugs by deploying the CF Worker — no .exe rebuild required.
    const isWin = (window.cicy?.platform === "win32") || (typeof navigator !== "undefined" && /Windows/.test(navigator.userAgent));
    if (isWin) {
      try {
        const { windowsInstall, canRunRendererInstall } = await import("./wslInstaller.js");
        if (canRunRendererInstall()) {
          const handleEvent = (ev) => {
            if (ev?.kind === "log") {
              // Append + cap at 300 lines so memory stays bounded on slow installs.
              setInstallLog((l) => {
                const next = [...l, ev.text];
                return next.length > 300 ? next.slice(next.length - 300) : next;
              });
            } else {
              setSidecar((s) => ({ ...s, progress: ev }));
              // Build a user-facing step timeline alongside the raw shell log.
              // Phase-keyed (not last-only) so concurrent phases — e.g. the
              // parallel `downloading` + `checking-wsl` — both update their
              // own row instead of overwriting each other.
              if (ev?.phase) {
                setInstallSteps((steps) => {
                  const idx = steps.findIndex((s) => s.phase === ev.phase);
                  const merged = (prev) => ({
                    phase: ev.phase,
                    message: ev.message || prev?.message || ev.phase,
                    at: Date.now(),
                    progress: ev.progress != null ? ev.progress : prev?.progress,
                    status: ev.status || prev?.status,
                    error: ev.status === "error" ? (ev.error || ev.message) : prev?.error,
                  });
                  if (idx >= 0) {
                    const next = [...steps];
                    next[idx] = merged(steps[idx]);
                    return next;
                  }
                  return [...steps, merged(null)];
                });
              }
            }
          };
          const r = await windowsInstall({ onProgress: handleEvent });
          if (r?.ok) {
            setSidecar({ state: "uptodate", info: { installedVersion: r.version }, progress: null, error: null });
            pushToast(t("toast.installed", { version: r.version }), "success");
            await loadHealth(backends);
            return;
          }
          throw new Error("install returned not ok");
        }
        // Fall through to ipc-based install if electronRPC missing
      } catch (e) {
        setSidecar((s) => ({ ...s, state: "error", error: e.message, progress: null }));
        // Mark the step that was running as failed so the timeline shows
        // a red ✗ next to it and surfaces a retry button. Subsequent
        // retries replay the whole windowsInstall — every step is
        // idempotent (size-checked downloads, "skip if already done"
        // installs) so resumption "feels" like only the failing step
        // re-runs even though we're calling the full sequence.
        setInstallSteps((steps) => {
          if (!steps.length) return steps;
          const last = steps[steps.length - 1];
          return [...steps.slice(0, -1), { ...last, status: "error", error: e.message }];
        });
        return;
      }
    }

    try {
      const r = await window.cicy.sidecar.install();
      if (r?.ok) {
        setSidecar({ state: "uptodate", info: { installedVersion: r.version }, progress: null, error: null });
        pushToast(t("toast.installed", { version: r.version }), "success");
        await loadHealth(backends);
      } else {
        setSidecar((s) => ({ ...s, state: "error", error: r?.error || "unknown", progress: null }));
      }
    } catch (e) {
      setSidecar((s) => ({ ...s, state: "error", error: e.message, progress: null }));
    }
  };

  const handleCancelInstall = async () => {
    if (window.cicy?.sidecar?.cancel) await window.cicy.sidecar.cancel();
  };

  // ── Derived ──
  const local = backends.filter((b) => b.kind === "local");
  const cloud = backends.filter((b) => b.kind !== "local");

  // Always show a Local card. If backends API hasn't returned one yet
  // (cicy-code missing/installing), synthesize a placeholder so the user
  // sees a single, unified install/open surface — no separate banner.
  const localCard = local[0] || {
    id: "local-placeholder",
    name: "Local",
    kind: "local",
    port: 8008,
  };

  // State-driven primary button + progress for the Local card.
  const localPrimary = (() => {
    if (sidecar.state === "missing")  return { label: t("card.install"), icon: "download", onClick: handleInstall };
    if (sidecar.state === "outdated") return { label: t("card.open"), icon: "open", onClick: () => handleOpen(localCard) };
    if (sidecar.state === "error")    return { label: t("card.retry"), icon: "refresh", onClick: handleInstall, variant: "primary" };
    return null; // fall through to default Open button
  })();

  // Banner above the cards owns the detailed step / log timeline. The
  // BackendCard only needs to know "install is happening" so it renders
  // a minimal placeholder ("安装中…") instead of a duplicate progress
  // panel. We intentionally strip the rich progress fields here.
  const localProgress = sidecar.state === "installing"
    ? { phase: "installing", minimal: true }
    : null;
  // Banner gets the full progress object — phase + message + progress
  // bar + step timeline live there.
  const bannerProgress = sidecar.state === "installing" ? sidecar.progress : null;

  const localMenu = (b) => {
    const items = [];
    items.push({ label: t("menu.edit"), icon: <Icon name="edit" />, onClick: () => setModalState({ open: true, mode: "edit", backend: b }) });
    items.push("divider");
    if (sidecar.state === "outdated" && sidecar.info?.latest) {
      items.push({
        label: t("menu.update_to", { version: sidecar.info.latest }),
        icon: <Icon name="download" />,
        badge: true,
        onClick: handleInstall,
      });
      items.push("divider");
    }
    items.push({ label: t("menu.restart"), icon: <Icon name="restart" />, onClick: handleRestart });
    items.push({ label: t("menu.check_update"), icon: <Icon name="refresh" />, onClick: handleCheckUpdate });
    return items;
  };

  const cloudMenu = (b) => [
    { label: t("menu.edit"),   icon: <Icon name="edit" />,  onClick: () => setModalState({ open: true, mode: "edit", backend: b }) },
    { label: t("menu.remove"), icon: <Icon name="trash" />, danger: true, onClick: () => handleRemove(b) },
  ];

  const localOpenDisabled = (b) => {
    if (sidecar.state === "missing" || sidecar.state === "installing") return true;
    const h = healthMap[b.id];
    return !!(h && h.ok === false);
  };

  return (
    <div className="app">
      <header className="app__topbar drag">
        <div className="app__brand">
          <span className="app__brand-mark" />
          CiCy Desktop
          {appVersion && (
            <span style={{ fontSize: 11, opacity: 0.5, marginLeft: 8, fontWeight: 400 }}>
              {appVersion}
            </span>
          )}
          {/* Worker build stamp — proves which CF Worker deploy is loaded.
              Format: <git_sha>·<MM-DD HH:mm UTC>. Click to copy full ISO time. */}
          <span
            style={{ fontSize: 10, opacity: 0.4, marginLeft: 6, fontWeight: 400, fontFamily: "ui-monospace, monospace", cursor: "pointer" }}
            title={`Worker build ${__WORKER_BUILD_TIME__} (${__WORKER_GIT_SHA__})`}
            onClick={() => {
              try { navigator.clipboard.writeText(`${__WORKER_GIT_SHA__} @ ${__WORKER_BUILD_TIME__}`); } catch {}
            }}
          >
            {__WORKER_GIT_SHA__}·{__WORKER_BUILD_TIME__.slice(5, 16).replace("T", " ")}
          </span>
        </div>
      </header>

      <main className="app__main">
        {/* Windows-only WSL setup card — surfaces above the cards. */}
        <WslSetupBanner
          wsl={wsl}
          onRecheck={checkWsl}
          recheckLoading={wslChecking}
          onInstall={handleInstall}
          progress={bannerProgress}
          progressLog={installLog}
          progressSteps={installSteps}
          onDismiss={() => { setInstallSteps([]); setInstallLog([]); }}
        />

        {/* Tabs: 全部 / 本地 / 云团队 */}
        <div className="app__tabs">
          {["all", "local", "cloud"].map((k) => (
            <button
              key={k}
              type="button"
              className={`app__tab ${tab === k ? "is-active" : ""}`}
              onClick={() => setTab(k)}
            >
              {t(`tab.${k}`)}
              <span className="app__tab-count">
                {k === "all" ? (1 + cloud.length) : k === "local" ? 1 : cloud.length}
              </span>
            </button>
          ))}
        </div>

        {/* Unified grid driven by tab filter */}
        <div className="app__grid">
          {(tab === "all" || tab === "local") && (
            <BackendCard
              backend={localCard}
              health={healthMap[localCard.id]}
              menuItems={localMenu(localCard)}
              onOpen={() => handleOpen(localCard)}
              openLoading={openingId === localCard.id}
              openDisabled={localOpenDisabled(localCard)}
              primaryButton={localPrimary}
              progress={localProgress}
              onCancel={handleCancelInstall}
            />
          )}
          {(tab === "all" || tab === "cloud") && cloud.map((b) => (
            <BackendCard
              key={b.id}
              backend={b}
              health={healthMap[b.id]}
              menuItems={cloudMenu(b)}
              onOpen={() => handleOpen(b)}
              openLoading={openingId === b.id}
            />
          ))}
          {(tab === "all" || tab === "cloud") && (
            <button
              type="button"
              className="add-card"
              onClick={() => setModalState({ open: true, mode: "add", backend: null })}
            >
              <span className="add-card__plus"><Icon name="add" size={20} /></span>
              <span className="add-card__label">{t("team.add")}</span>
            </button>
          )}
        </div>

        <BackendModal
          open={modalState.open}
          mode={modalState.mode}
          backend={modalState.backend}
          onClose={() => setModalState({ open: false, mode: "add", backend: null })}
          onSaved={() => loadBackends()}
        />
      </main>

      <Toast />
    </div>
  );
}



async function fetchWindows() {
  if (!window.electronRPC) return [];
  try {
    const res = await window.electronRPC("get_windows", {});
    const txt = (res.content || []).map((c) => c.text).join("");
    const parsed = JSON.parse(txt);
    return Array.isArray(parsed) ? parsed : [];
  } catch { return []; }
}


// ── persisted install state ─────────────────────────────────────────
// Survives page reloads (cf-deploy + auto-reload, F5, etc.) so the user
// doesn't lose visible install progress just because the page refreshed.
// Only the *display* survives — the running install promise itself is
// gone after reload, so a state of "installing" is rewritten to
// "interrupted" on rehydrate. Stored in sessionStorage so a fresh
// browser session starts clean.
const LS_LOG_KEY     = "cicy.installLog";
const LS_STEPS_KEY   = "cicy.installSteps";
const LS_SIDECAR_KEY = "cicy.sidecar";
const LS_MAX_AGE_MS  = 24 * 60 * 60 * 1000; // 24h — after that, treat as stale.

function loadPersistedLog() {
  try {
    const raw = sessionStorage.getItem(LS_LOG_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!parsed || (Date.now() - (parsed.at || 0)) > LS_MAX_AGE_MS) return [];
    return Array.isArray(parsed.lines) ? parsed.lines : [];
  } catch { return []; }
}

function loadPersistedSteps() {
  try {
    const raw = sessionStorage.getItem(LS_STEPS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!parsed || (Date.now() - (parsed.at || 0)) > LS_MAX_AGE_MS) return [];
    return Array.isArray(parsed.steps) ? parsed.steps : [];
  } catch { return []; }
}

function loadPersistedSidecar() {
  const fallback = { state: "checking", info: null, progress: null, error: null };
  try {
    const raw = sessionStorage.getItem(LS_SIDECAR_KEY);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw);
    if (!parsed || (Date.now() - (parsed.at || 0)) > LS_MAX_AGE_MS) return fallback;
    const v = parsed.value || fallback;
    // The promise that drove "installing" is dead after a reload. Surface
    // that to the UI as a distinct, non-blocking state so the install
    // button comes back and the user can retry / inspect the saved log.
    if (v.state === "installing") {
      return { ...v, state: "interrupted", error: "page reloaded mid-install" };
    }
    return v;
  } catch { return fallback; }
}
