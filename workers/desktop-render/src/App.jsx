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


  const checkWsl = useCallback(async () => {
    if (apiMode !== "electron" || !window.cicy?.sidecar?.wslStatus) {
      setWsl({ supported: false });
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

  // ── Actions ──
  const handleOpen = async (b) => {
    setOpeningId(b.id);
    try {
      const url = b.resolvedUrl || b.url;
      if (window.electronRPC && url) {
        try {
          const wins = await fetchWindows();
          const targetOrigin = new URL(url).origin;
          const existing = wins.find((w) => { try { return w.url && new URL(w.url).origin === targetOrigin; } catch { return false; } });
          if (existing) {
            // Reload with the fresh URL (which carries the latest token)
            // before focusing — the existing window may be on the login
            // page or have a stale session.
            try {
              await window.electronRPC("load_url", { win_id: existing.id, url });
            } catch {}
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

  const localProgress = sidecar.state === "installing" ? sidecar.progress : null;

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
        </div>
      </header>

      <main className="app__main">
        {/* Windows-only WSL setup card — surfaces above the cards. */}
        <WslSetupBanner wsl={wsl} onRecheck={checkWsl} recheckLoading={wslChecking} />

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
