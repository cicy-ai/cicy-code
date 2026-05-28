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
  // Agent picker handshake: wslInstaller awaits onPickAgents() and we
  // stash the resolver here. WslSetupBanner reads pickAgents, renders
  // the checkbox UI, and calls resolve() on click. Null when no pick
  // is pending.
  const [pickAgents, setPickAgents] = useState(null);

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


  // Docker model: WSL state is irrelevant (on Windows, Docker Desktop manages
  // WSL2 itself). Always clear it so no WSL-specific banner ("WSL not
  // supported / not installed") ever shows — sidecar.state (from dockerStatus)
  // drives the whole flow now.
  const checkWsl = useCallback(async () => {
    setWsl(null);
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
    // Web / cloud mode (no Electron RPC) — nothing local to manage.
    if (apiMode !== "electron" || typeof window.electronRPC !== "function") {
      setSidecar({ state: "uptodate", info: null, progress: null, error: null });
      setInstallSteps([]);
      setInstallLog([]);
      return;
    }
    // Docker model: determine state from the renderer via `docker` over
    // exec_shell (no main-process sidecar API — fixes ship via the CF Worker).
    try {
      const { dockerStatus } = await import("./dockerInstaller.js");
      const st = await dockerStatus();
      // Docker CLI absent → first thing the user needs is Docker Desktop;
      // the install flow guides that download, so surface "install".
      // Also: image not here and no container yet → needs install.
      if (!st.dockerInstalled || (!st.imagePresent && !st.containerExists)) {
        setSidecar((s) => ({ ...s, state: "missing", info: null, running: false, error: null }));
        return;
      }
      // Installed. running = container up AND actually serving :8008, so the
      // "Open" button only shows when the workspace will really load. When the
      // daemon is down or the container is stopped, running:false → "Start"
      // (handleStart → ensureRunning, which also boots Docker Desktop).
      setSidecar((s) => ({
        ...s,
        state: st.updateAvailable ? "outdated" : "uptodate",
        info: { installedVersion: st.version, latest: st.target },
        running: !!(st.containerRunning && st.healthy),
        dockerDown: !st.dockerRunning,
        error: null,
      }));
      // Drop a stale partial install timeline (keep a completed one's "done").
      setInstallSteps((steps) => {
        if (!steps || steps.length === 0) return steps;
        return steps[steps.length - 1].phase === "done" ? steps : [];
      });
    } catch (e) {
      setSidecar((s) => ({ ...s, state: "error", error: e.message }));
    }
  }, []);

  // Click "启动" — spawn the daemon, then re-check status so the button
  // flips to "打开" once :8008 is reachable.
  const handleStart = useCallback(async () => {
    if (typeof window.electronRPC !== "function") return;
    setSidecar((s) => ({ ...s, starting: true }));
    try {
      // ensureRunning: boot Docker Desktop if needed → start/create the
      // container → wait for :8008. Idempotent. If the image isn't here yet
      // (needsInstall), fall through to the full install flow.
      const { ensureRunning } = await import("./dockerInstaller.js");
      const r = await ensureRunning({ onProgress: () => {} });
      if (r?.needsInstall) { await handleInstall(); return; }
      if (!r?.ok) pushToast(t("toast.start_failed", { error: "engine not ready" }), "error");
    } catch (e) {
      pushToast(t("toast.start_failed", { error: e.message }), "error");
    } finally {
      setSidecar((s) => ({ ...s, starting: false }));
      checkSidecar();
    }
  }, [checkSidecar, t]);

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
        // Local Docker backend: make sure the container is actually running
        // before reading its token / opening it. Handles the common cases —
        // post-reboot (container auto-restarts but maybe still booting),
        // stopped container, or Docker Desktop not started yet. No-op when
        // already healthy. If the image isn't installed yet, divert to install.
        if (b.id === "local") {
          try {
            const { ensureRunning } = await import("./dockerInstaller.js");
            const r = await ensureRunning({ onProgress: () => {} });
            if (r?.needsInstall) { setOpeningId(null); await handleInstall(); return; }
          } catch (e) {
            pushToast(t("toast.start_failed", { error: e.message }), "error");
            setOpeningId(null);
            return;
          }
        }
        try {
          // Obtain the access token — and we WILL obtain it. The token always
          // physically exists in the WSL runtime (~/cicy-ai/global.json, written
          // by cicy-code on first run); a miss only means the distro is asleep
          // or still booting, and the read itself wakes it. So we retry until we
          // have a tokened URL rather than ever opening a "paste your token"
          // login page. Two sources, both retried:
          //   1. resolveUrl (the desktop's own resolver, handles local+remote)
          //   2. direct read of global.json over `wsl` (local backend fallback;
          //      the wsl invocation also boots a stopped distro)
          let url = b.resolvedUrl || b.url;
          const hasToken = (u) => /[?&]token=/.test(u || "");
          for (let i = 0; i < 30 && !hasToken(url); i++) {
            if (window.cicy?.backends?.resolveUrl) {
              try { url = (await window.cicy.backends.resolveUrl(b.id)) || url; } catch (e) {}
            }
            if (!hasToken(url) && b.id === "local") {
              try {
                const rr = await window.electronRPC("exec_shell", {
                  command: 'docker exec cicy cat /home/cicy/cicy-ai/global.json 2>/dev/null',
                  timeout_ms: 9000,
                });
                let out = (rr?.content || []).map((c) => c.text).join("");
                try { out = JSON.parse(out).stdout || out; } catch (e) {}
                const tm = out.match(/"api_token"\s*:\s*"([^"]+)"/);
                if (tm) url = "http://127.0.0.1:8008/?token=" + tm[1];
              } catch (e) {}
            }
            if (hasToken(url)) break;
            // Tell the user we're waking the runtime rather than silently hanging.
            if (i === 0) pushToast("正在唤醒运行环境…", "info");
            await new Promise((r) => setTimeout(r, 2500));
          }
          if (!hasToken(url)) {
            // ~75s of retries and still nothing readable — the runtime is
            // genuinely broken (not just asleep). Surface it instead of opening
            // a dead login window.
            pushToast("无法获取访问令牌，请重启应用后重试", "error");
            return;
          }
          // For LOCAL ONLY: gate the open on /api/health. The local cicy-code
          // container can take minutes to bind :8008 on a cold first start
          // (cicy-mihomo download trails the API listener). Opening before
          // the server answers strands the window on a connection-error
          // / login page that no in-window reload can fix until the server
          // is up. A warm backend answers on the first probe (instant).
          //
          // Remote backends are skipped: they're just a cloud URL the user
          // typed in, no local supervisor to wake up. Also, the homepage
          // ships over https://desktop.cicy-ai.com so a renderer-side
          // fetch() against an http://… remote is blocked by mixed-content
          // (Failed to fetch), which would falsely fail the gate.
          if (b.id === "local") {
            const origin = (url.match(/^https?:\/\/[^/]+/) || [])[0];
            let apiUp = !origin;
            for (let i = 0; origin && i < 150 && !apiUp; i++) {
              try { apiUp = (await fetch(origin + "/api/health", { cache: "no-store" })).ok; } catch (e) {}
              if (apiUp) break;
              if (i === 0) pushToast("正在启动运行环境，请稍候…", "info");
              await new Promise((r) => setTimeout(r, 2000));
            }
            if (!apiUp) {
              pushToast("运行环境启动较慢，请稍后重新点击打开", "error");
              return;
            }
          }
          // Check if any window is already showing this backend. Compare by
          // origin+pathname only — the token in the query string rotates on
          // every resolveUrl call, so a full-string match would always miss.
          const wins = await fetchWindows();
          const targetBase = stripQuery(url);
          const existing = wins.find((w) => w.url && stripQuery(w.url) === targetBase);
          let targetWid = existing ? existing.id : null;
          if (targetWid == null) {
            const opened = await window.electronRPC("open_window", { url, reuseWindow: false });
            const openedTxt = (opened?.content || []).map((c) => c.text).join("");
            const m = openedTxt.match(/ID:\s*(\d+)/);
            targetWid = m ? parseInt(m[1], 10) : null;
          }
          // GUARANTEE the user lands authenticated — never on the "paste your
          // token" login page. The cicy-code SPA's "token from ?token=" auto-
          // login is racy on a brand-new window / just-started backend, but a
          // `webContents.loadURL` of the same tokened URL reliably consumes the
          // token (proven). We drive the retry from the RENDERER with short
          // RPC calls + real awaits — a single long-running main-process loop
          // proved unreliable (the RPC reply times out and the in-window loop
          // gets abandoned mid-flight, so loadURL never actually fires). Each
          // iteration: check localStorage.api_token; if absent, loadURL the
          // tokened URL. An already-authenticated live session passes the first
          // check and is never reloaded.
          if (targetWid != null) {
            try {
              await window.electronRPC("control_electron_BrowserWindow", {
                win_id: targetWid,
                code: "(win.isMinimized()&&win.restore(),win.show(),win.focus(),'shown')",
              });
            } catch (e) {}
            for (let i = 0; i < 12; i++) {
              let authed = false;
              try {
                const cr = await window.electronRPC("control_electron_BrowserWindow", {
                  win_id: targetWid,
                  code: "win.webContents.executeJavaScript(\"!!localStorage.getItem('api_token')\")",
                });
                authed = /true/.test((cr?.content || []).map((c) => c.text).join(""));
              } catch (e) {}
              if (authed) break;
              try {
                await window.electronRPC("control_electron_BrowserWindow", {
                  win_id: targetWid,
                  code: "win.webContents.loadURL(" + JSON.stringify(url) + ")",
                });
              } catch (e) {}
              await new Promise((r) => setTimeout(r, 2000));
            }
          }
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
    // Don't wipe the step timeline on retry — keep the completed (✓) steps
    // visible and let the re-run update each phase row in place, so a retry
    // visibly RESUMES from where it failed instead of starting blank. We only
    // clear the error flag on each surviving step so stale ✗ marks don't
    // linger; the installer's idempotency (cached download, existing distro,
    // cached network + agent pick) means resumed steps fly past instantly.
    setInstallSteps((steps) =>
      Array.isArray(steps)
        ? steps.map((s) => (s.status === "error" ? { ...s, status: "running", error: null } : s))
        : [],
    );

    // All platforms (Win/Mac/Linux): drive the install from the renderer via
    // electronRPC.exec_shell using Docker. Bypasses the main-process
    // sidecar:install handler, so install fixes ship via the CF Worker —
    // no .exe rebuild required. Docker is uniform across OSes (on Windows
    // Docker Desktop runs on WSL2/Hyper-V under the hood).
    {
      try {
        const { dockerInstall, canRunRendererInstall } = await import("./dockerInstaller.js");
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
                  const merged = (prev) => {
                    const base = {
                      phase: ev.phase,
                      message: ev.message || prev?.message || ev.phase,
                      at: Date.now(),
                      progress: ev.progress != null ? ev.progress : prev?.progress,
                      status: ev.status || prev?.status,
                      error: ev.status === "error" ? (ev.error || ev.message) : prev?.error,
                    };
                    // Preserve extra metadata across updates to the same phase —
                    // later emits overwrite message but we still want e.g.
                    // network (from detecting) + wslHomePath (from done).
                    for (const k of ["network","installDrive","installDir","isSSD","version","agents","wslHomePath"]) {
                      if (ev[k] != null) base[k] = ev[k];
                      else if (prev?.[k] != null) base[k] = prev[k];
                    }
                    return base;
                  };
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
          const r = await dockerInstall({
            onProgress: handleEvent,
            // dockerInstaller awaits this callback right before pulling/
            // running the container. We surface the agent picker UI via
            // state and resolve the promise once the user clicks "Start".
            onPickAgents: ({ defaults, available }) => new Promise((resolve) => {
              setPickAgents({
                defaults,
                available,
                resolve: (picked) => {
                  setPickAgents(null);
                  resolve(picked);
                },
              });
            }),
          });
          if (r?.ok) {
            setSidecar({ state: "uptodate", info: { installedVersion: r.version }, progress: null, error: null });
            pushToast(t("toast.installed", { version: r.version }), "success");
            const freshList = await loadBackends();
            await loadHealth(freshList);
            checkWsl();
            return;
          }
          throw new Error("install returned not ok");
        }
        // Fall through to ipc-based install if electronRPC missing
      } catch (e) {
        setSidecar((s) => ({ ...s, state: "error", error: e.message, progress: null }));
        // Clear any orphaned agent-pick promise — if the installer threw
        // while the user was still on the picker step, leaving pickAgents
        // populated would freeze the drawer in pick mode forever even
        // after the error timeline rendered.
        setPickAgents(null);
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
        const freshList = await loadBackends();
        await loadHealth(freshList);
        checkWsl();
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
    // Installed but daemon not reachable on :8008 — show a Start button
    // that spawns the daemon. Once it binds the port, the next poll flips
    // sidecar.running and we fall through to the default Open button.
    if (sidecar.state === "uptodate" && sidecar.running === false) {
      return { label: t("card.start"), icon: "play", onClick: handleStart, loading: !!sidecar.starting };
    }
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
          pickAgents={pickAgents}
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



function stripQuery(url) {
  try { const u = new URL(url); return (u.origin + u.pathname).toLowerCase(); } catch { return url.toLowerCase(); }
}

async function fetchWindows() {
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

