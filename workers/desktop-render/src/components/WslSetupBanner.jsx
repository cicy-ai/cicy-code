import { useState, useEffect } from "react";
import Button from "./Button.jsx";
import Icon from "./Icon.jsx";
import { useT } from "../i18n";
import "./WslSetupBanner.css";

// Friendly display metadata for each agent CLI. Mapped from the bare
// npm name to a short human label + one-sentence description. The list
// stays in sync with wslInstaller.js's AVAILABLE_AGENTS — adding a new
// agent there needs an entry here too.
const AGENT_META = {
  claude:   { name: "Claude",   desc: "Anthropic Claude Code CLI（最常用）" },
  codex:    { name: "Codex",    desc: "OpenAI Codex CLI" },
  opencode: { name: "OpenCode", desc: "开源多模型 CLI（本地推理友好）" },
};

// Map well-known error tags from wslInstaller into localized messages.
// Anything we don't recognize is rendered as-is, so devs still see the
// raw shell stderr while users get human-readable text for known cases.
function localizeStepError(raw, t) {
  if (!raw) return "";
  let m = String(raw).match(/^RELEASE_NOT_READY:(.+)$/);
  if (m) return t("wsl.error.release_not_ready", { version: m[1] });
  m = String(raw).match(/^LOW_DISK_SPACE:(.+):(.+)$/);
  if (m) return t("wsl.error.low_disk_space", { free: m[1], required: m[2] });
  return raw;
}

// Open Windows Settings → Storage. Uses the `ms-settings:` URI scheme,
// which any Windows shell handles. We try Electron's shell.openExternal
// first (the clean path), falling back to exec_shell `start` for older
// preload bridges where shell isn't exposed.
async function openStorageSettings() {
  if (window.cicy?.shell?.openExternal) {
    try { await window.cicy.shell.openExternal("ms-settings:storagesense"); return; } catch {}
  }
  if (window.electronRPC) {
    try { await window.electronRPC("exec_shell", { command: "start ms-settings:storagesense" }); } catch {}
  }
}

// Match an error string to a targeted recovery action ("Open Storage
// Settings", "Open Docs", etc.). Returning null falls through to a
// generic retry — every step in wslInstaller is idempotent so re-running
// the whole flow is always safe.
function errorRecoveryAction(raw) {
  if (!raw) return null;
  if (/^LOW_DISK_SPACE/.test(raw)) {
    return { label: "打开 Windows 储存设置", onClick: openStorageSettings, icon: "folder" };
  }
  return null;
}

export default function WslSetupBanner({ wsl, onRecheck, recheckLoading, onInstall, progress, progressLog, progressSteps, onDismiss, pickAgents }) {
  const t = useT();
  const [installing, setInstalling] = useState(false);
  const [installResult, setInstallResult] = useState(null);
  const [showRawLog, setShowRawLog] = useState(false);
  // Agent picker — only consulted when installer reaches phase=picking-agents.
  // `pickAgents` is an object the parent App.jsx fills with { defaults,
  // available, resolve } when wslInstaller awaits onPickAgents(). Without
  // it the picker section is invisible. Default selection mirrors the
  // installer's: claude only (single most-used CLI).
  const [picked, setPicked] = useState(() => new Set((pickAgents?.defaults || ["claude"])));

  // Sync local picked set whenever a new pick request arrives. The
  // parent rotates `pickAgents` (new identity → new request) when the
  // installer hits picking-agents, even if the defaults stay the same
  // across runs. Without this useEffect, re-running install on a fresh
  // distro would silently keep stale checkboxes from a previous run.
  useEffect(() => {
    if (pickAgents?.defaults) setPicked(new Set(pickAgents.defaults));
  }, [pickAgents]);

  if (!wsl) return null;
  // wsl.exe itself missing — link user to official docs, nothing to run.
  if (wsl.supported === false) {
    return (
      <>
        <div className="wsl-drawer-backdrop" />
        <div className="wsl-banner is-error">
          <div className="wsl-banner__head">
            <div className="wsl-banner__icon"><Icon name="warn" size={18} /></div>
            <div className="wsl-banner__text">
              <div className="wsl-banner__title">{t("wsl.no_exe_title")}</div>
              <div className="wsl-banner__subtitle">{t("wsl.no_exe_subtitle")}</div>
            </div>
            <Button variant="ghost" loading={recheckLoading} onClick={onRecheck}>{t("wsl.recheck")}</Button>
          </div>
          <Button variant="primary" onClick={() => window.cicy?.shell?.openExternal("https://aka.ms/wsl-install")}>
            {t("wsl.docs")} ↗
          </Button>
        </div>
      </>
    );
  }
  // Anatomy of "this install is finished":
  //   - we're not actively running (installing=false)
  //   - the timeline has at least one step
  //   - the last step is the explicit "done" phase
  const lastStep = progressSteps && progressSteps[progressSteps.length - 1];
  const installFinished = !installing && lastStep && lastStep.phase === "done";
  // wsl install path metadata, surfaced on the done step. We use it to
  // render a "Open WSL Files" button so the user has a one-click route
  // into their WSL home from Windows File Explorer.
  const wslHomePath = lastStep && lastStep.wslHomePath;
  const installDir = lastStep && lastStep.installDir;

  const openWslFiles = async () => {
    if (!wslHomePath) return;
    if (window.electronRPC) {
      // explorer.exe accepts UNC paths directly. Use `start` so the
      // shell does the path resolution (otherwise CreateProcess wants
      // a fully-resolved file:// URI).
      try { await window.electronRPC("exec_shell", { command: `explorer.exe "${wslHomePath}"` }); }
      catch {}
    } else if (window.cicy?.shell?.openExternal) {
      try { await window.cicy.shell.openExternal(`file:///${wslHomePath.replace(/\\\\/g, "/")}`); } catch {}
    }
  };

  // wsl is ready, but if an install (cicy-code into Ubuntu, etc.) is
  // still running OR has steps to display OR is awaiting an agent pick,
  // keep the drawer alive so the user can watch the timeline / pick.
  // Once everything is idle and steps are cleared, the drawer hides
  // itself.
  const awaitingPick = !!(pickAgents && typeof pickAgents.resolve === "function");
  const hasActiveInstall = installing || awaitingPick || (progressSteps && progressSteps.length > 0);
  if (wsl.installed && wsl.hasDistro && !hasActiveInstall) return null;

  const cmd = "wsl --install -d Ubuntu";
  const title    = wsl.installed ? t("wsl.title_no_distro") : t("wsl.title_install");
  const subtitle = t("wsl.subtitle");

  const copy = async () => {
    try { await navigator.clipboard.writeText(cmd); } catch {}
  };

  const oneClick = async () => {
    // Delegate to App.jsx handleInstall: that path drives wslInstaller.js
    // (renderer-side) so the BackendCard progress log surfaces every shell
    // command. Falls back to the legacy main-process IPC if no parent
    // handler was wired.
    if (typeof onInstall === "function") {
      setInstalling(true);
      setInstallResult(null);
      try { await onInstall(); }
      catch (e) { setInstallResult({ ok: false, error: e.message }); }
      finally { setInstalling(false); onRecheck?.(); }
      return;
    }
    if (!window.cicy?.sidecar?.installWsl) return;
    setInstalling(true);
    setInstallResult(null);
    try {
      const r = await window.cicy.sidecar.installWsl();
      setInstallResult(r);
      // Trigger re-detect even on failure (so error banner refreshes).
      onRecheck?.();
    } catch (e) {
      setInstallResult({ ok: false, error: e.message });
    } finally {
      setInstalling(false);
    }
  };

  // When WSL+distro are already set up, the banner is only shown because
  // an install/upgrade is in progress. Don't show the "please install WSL"
  // tip — the user already has WSL; we're just updating cicy-code.
  const isUpgradeMode = wsl.installed && wsl.hasDistro;

  // Agent picker is the gating step right before cicy-code starts.
  // Parent supplies pickAgents={ defaults, available, resolve } when
  // wslInstaller.js awaits onPickAgents(). The user picks their CLIs,
  // hits "Start", and we resolve the promise to unblock the install.
  const pickerVisible = !!(pickAgents && typeof pickAgents.resolve === "function");
  const togglePick = (key) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };
  const confirmPick = () => {
    // Empty selection would make cicy-code start with no agents — guard
    // it client-side too even though wslInstaller.js falls back to
    // claude. Users hitting "Start" with nothing checked clearly didn't
    // mean to ship a brain-dead cicy-code, so default to claude.
    const out = picked.size > 0 ? [...picked] : ["claude"];
    pickAgents.resolve(out);
  };

  return (
    <>
      <div className="wsl-drawer-backdrop" />
      <div className="wsl-banner">
        <div className="wsl-banner__head">
        <div className="wsl-banner__icon"><Icon name="warn" size={18} /></div>
        <div className="wsl-banner__text">
          <div className="wsl-banner__title">{installFinished ? "安装完成" : (isUpgradeMode ? t("wsl.title_upgrade") : title)}</div>
          {!isUpgradeMode && (
            <div className="wsl-banner__subtitle">
              {installFinished
                ? (installDir
                    ? `✨ 准备就绪！点击下方「本地团队」开始使用 · 文件位置：${installDir}`
                    : `✨ 准备就绪！点击下方「本地团队」开始使用`)
                : subtitle}
            </div>
          )}
        </div>
        {/* Header CTA: shown only when the user has something useful to do.
            - installFinished: "open files" + close
            - installing / awaitingPick: nothing (timeline shows progress)
            - idle (initial / failed / interrupted): recheck button so the
              user can re-detect WSL after fixing it manually
            「重新检查」 during an active install is noise — the installer
            already knows, and clicking it can race the in-flight check.
            We also surface it when a reload left a stale half-finished
            timeline: idleWithStaleTimeline reveals it again. */}
        {installFinished ? (
          <div style={{ display: "flex", gap: 8 }}>
            {wslHomePath && (
              <Button variant="ghost" onClick={openWslFiles} title={wslHomePath}>
                <Icon name="folder" size={14} /> 打开 WSL 文件
              </Button>
            )}
            <Button variant="primary" onClick={onDismiss}>
              <Icon name="close" size={14} /> 关闭
            </Button>
          </div>
        ) : (!installing && !awaitingPick) ? (
          <Button variant="ghost" loading={recheckLoading} onClick={onRecheck}>{t("wsl.recheck")}</Button>
        ) : null}
      </div>

      {/* Install CTA — hidden while an install is actively running or
          the agent picker is open. Visible in three cases:
            1. fresh state (no timeline yet)
            2. timeline exists but ended in error (retry)
            3. timeline exists but stalled mid-flight (reload during
               install) — last step status is neither "done" nor "error";
               treat as interrupted and let the user re-trigger.
          Without case 3 a reload mid-install would leave the user with
          no way to continue except clearing the timeline manually. */}
      {(() => {
        if (installFinished || installing || awaitingPick) return null;
        const last = progressSteps && progressSteps[progressSteps.length - 1];
        const hasError = last && last.status === "error";
        const interrupted = last && last.status !== "done" && last.status !== "error";
        const showInstall = !last || hasError || interrupted;
        if (!showInstall) return null;
        return (
          <div className="wsl-banner__actions">
            <Button variant="primary" loading={installing} onClick={oneClick}>
              <Icon name="download" size={14} /> {hasError || interrupted ? "重试安装" : t("wsl.install_now")}
            </Button>
            <span className="wsl-banner__hint">{t("wsl.uac_hint")}</span>
          </div>
        );
      })()}

      {pickerVisible && (
        <div className="wsl-banner__pick">
          <div className="wsl-banner__pick-title">选择 AI 助手</div>
          <div className="wsl-banner__pick-hint">
            勾选要启用的 CLI，首次创建对应 agent 时自动安装。可多选，建议至少保留 Claude。
          </div>
          <div className="wsl-banner__pick-options">
            {(pickAgents.available || ["claude", "codex", "opencode"]).map((k) => {
              const meta = AGENT_META[k] || { name: k, desc: "" };
              const checked = picked.has(k);
              return (
                <label
                  key={k}
                  className={`wsl-banner__pick-row ${checked ? "is-checked" : ""}`}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => togglePick(k)}
                  />
                  <span className="wsl-banner__pick-row-text">
                    <span className="wsl-banner__pick-row-name">{meta.name}</span>
                    <span className="wsl-banner__pick-row-desc">{meta.desc}</span>
                  </span>
                </label>
              );
            })}
          </div>
          <Button
            className="wsl-banner__pick-confirm"
            variant="primary"
            disabled={picked.size === 0}
            onClick={confirmPick}
          >
            启动 cicy-code（{picked.size > 0 ? [...picked].join(", ") : "claude"}）
          </Button>
        </div>
      )}

      {/* Prominent top-level failure banner.
          Surfaces the FIRST failed step + its raw stderr at the top so
          the user doesn't have to scan the timeline to find the red ✗.
          Sources merged from two places: the installResult error (whole-
          install throw, including pre-step setup errors) AND the first
          step in progressSteps that carries status=error. */}
      {(() => {
        const failedStep = (progressSteps || []).find((s) => s.status === "error");
        const rawErr = installResult && !installResult.ok
          ? (installResult.error || installResult.stderr)
          : (failedStep && (failedStep.error || failedStep.message));
        if (!rawErr) return null;
        const recovery = errorRecoveryAction(rawErr);
        return (
          <div className="wsl-banner__failure">
            <div className="wsl-banner__failure-head">
              <Icon name="warn" size={14} />
              <span>{t("wsl.install_failed")}</span>
              {failedStep && (
                <span className="wsl-banner__failure-step">· {phaseLabel(failedStep.phase, t)}</span>
              )}
            </div>
            <div className="wsl-banner__failure-msg">
              {localizeStepError(rawErr, t) || "unknown"}
            </div>
            <div className="wsl-banner__failure-actions">
              {recovery && (
                <Button variant="primary" onClick={recovery.onClick}>
                  {recovery.icon && <Icon name={recovery.icon} size={14} />} {recovery.label}
                </Button>
              )}
              <Button variant={recovery ? "ghost" : "primary"} loading={installing} onClick={oneClick}>
                <Icon name="refresh" size={14} /> 重试安装
              </Button>
            </div>
          </div>
        );
      })()}

      {/* Live install progress + scrolling shell log. progress is the
          phase/message object emitted by wslInstaller.js; progressLog is
          the accumulated `$ cmd` + stdout summary lines. Visible only
          while a parent install is in flight. */}
      {(installing || progress || (progressSteps && progressSteps.length > 0) || (progressLog && progressLog.length > 0)) && (
        <div className="wsl-banner__progress">
          {/* Step timeline — high-signal, user-readable. Each phase shows
              up once with its latest message; finished steps stay visible.
              The currently-running step is highlighted; completed steps
              get a checkmark; the failed step gets a ✗ and a retry button
              that re-runs the whole installer (every step is idempotent
              so already-done work is skipped). */}
          {progressSteps && progressSteps.length > 0 && (
            <ol className="wsl-banner__steps">
              {progressSteps.map((s, i) => {
                // Step state is driven primarily by explicit status emitted
                // from wslInstaller.js (running / done / error). Falls back
                // to "implicit done" for steps that don't carry an explicit
                // status when a later step has appeared — keeps the timeline
                // visually consistent without forcing every phase emit to
                // include a status field.
                const isLast = i === progressSteps.length - 1;
                const explicitDone   = s.status === "done"  || s.phase === "done";
                const explicitError  = s.status === "error";
                const explicitRun    = s.status === "running";
                const failed = explicitError;
                // If a later step exists and this one carries no status,
                // treat it as completed.
                const implicitDone = !isLast && !s.status && !failed;
                const isDone = (explicitDone || implicitDone) && !failed;
                const active = !failed && !isDone && (explicitRun || (isLast && installing));
                const label = phaseLabel(s.phase, t);
                const detail = phaseDetail(s, t);
                return (
                  <li
                    key={`${s.phase}-${i}`}
                    className={`wsl-banner__step ${active ? "is-active" : ""} ${isDone ? "is-done" : ""} ${failed ? "is-error" : ""}`}
                  >
                    <span className="wsl-banner__step-icon" aria-hidden="true">
                      {failed ? "✗" : isDone ? "✓" : active ? "•" : "·"}
                    </span>
                    <span className="wsl-banner__step-text">
                      <span className="wsl-banner__step-label">{label}</span>
                      {detail && <span className="wsl-banner__step-detail"> {detail}</span>}
                      {typeof s.progress === "number" && active && (
                        <span className="wsl-banner__step-pct"> {Math.round(s.progress * 100)}%</span>
                      )}
                      {failed && s.error && (
                        <span className="wsl-banner__step-error">{localizeStepError(s.error, t)}</span>
                      )}
                    </span>
                    {failed && !installing && (() => {
                      const recovery = errorRecoveryAction(s.error || s.message);
                      return (
                        <div style={{ display: "flex", gap: 6 }}>
                          {recovery && (
                            <button
                              type="button"
                              className="wsl-banner__step-retry"
                              onClick={recovery.onClick}
                              disabled={installing}
                            >
                              {recovery.label}
                            </button>
                          )}
                          <button
                            type="button"
                            className="wsl-banner__step-retry"
                            onClick={oneClick}
                            disabled={installing}
                          >
                            重试
                          </button>
                        </div>
                      );
                    })()}
                  </li>
                );
              })}
            </ol>
          )}

          {/* Indeterminate bar at the bottom while running — gives a
              live "something is happening" cue without numerics. */}
          {installing && (
            <div className="wsl-banner__progress-bar is-indeterminate">
              <div className="wsl-banner__progress-fill" style={{ width: "30%" }} />
            </div>
          )}

          {/* Raw shell log — hidden by default. The toggle is intentionally
              tiny + dim; users see the step timeline above, this is for
              when something goes wrong and the developer wants to inspect
              every shell call. */}
          {progressLog && progressLog.length > 0 && (
            <div className="wsl-banner__rawlog">
              <button
                type="button"
                className="wsl-banner__rawlog-toggle"
                onClick={() => setShowRawLog((v) => !v)}
                title="Developer-only shell log"
              >
                {showRawLog ? t("wsl.hide_log") : t("wsl.show_log")}
              </button>
              {showRawLog && (
                <pre className="wsl-banner__log" ref={(el) => {
                  if (el) el.scrollTop = el.scrollHeight;
                }}>
                  {progressLog.join("\n")}
                </pre>
              )}
            </div>
          )}
        </div>
      )}
      </div>
    </>
  );
}


// ── phase → human-readable label/detail ───────────────────────────────
// The wslInstaller.js raw emit messages are written for developers
// (English, often verbose). We map them to short Chinese labels here so
// the user sees a clean step list. `t` is reserved for future i18n —
// for now Chinese is hard-coded since the timeline is windows-install
// specific and only ever runs in zh-CN UI.
function phaseLabel(phase /*, t */) {
  switch (phase) {
    case "init":                 return "准备";
    case "detecting":            return "检测网络与磁盘";
    case "checking":             return "查询最新版本";
    case "downloading":          return "下载主程序";
    case "checking-wsl":         return "检查 Linux 子系统";
    case "installing-wsl":       return "安装 Linux 子系统";
    case "waiting-distro":       return "等待 Linux 子系统启动";
    case "configuring-apt":      return "配置软件源";
    case "installing-cicy-code": return "部署到 Linux";
    case "installing-deps":      return "下载运行依赖";
    case "picking-agents":       return "选择 AI 助手";
    case "starting":             return "启动 cicy-code";
    case "mounting-files":       return "桌面快捷方式";
    case "done":                 return "完成";
    default:                     return phase || "处理中";
  }
}

// Optional sub-detail extracted from the raw message — version, sizes,
// "cn" / "global" network tag, etc. Falls back to the raw message when
// nothing structured is recognized.
function phaseDetail(step /*, t */) {
  const m = step.message || "";
  if (step.phase === "detecting") {
    const nm = m.match(/Network:\s*(\S+)/);
    if (nm) {
      // Region-neutral labels — the original "国内/海外" wording assumes
      // the user is in China. Just say which probe succeeded; the user
      // can read either the English tag or the parenthetical hint.
      if (nm[1] === "cn")      return "China (mirrors)";
      if (nm[1] === "global")  return "Global (direct)";
      if (nm[1] === "unknown") return "Unknown (will try mirrors)";
      return nm[1];
    }
    // Pick-drive emit: surface the chosen disk + headroom.
    const dm = m.match(/Install drive:\s*([A-Z]):\s*\(([\d.]+)\s*GB free.*?(\bSSD\b|\bHDD\b|\bsystem\b)?/);
    if (dm) {
      const tag = dm[3] === "SSD" ? "SSD" : dm[3] === "HDD" ? "HDD" : "";
      return `安装到 ${dm[1]}: 盘 (剩余 ${dm[2]} GB${tag ? ", " + tag : ""})`;
    }
    return null;
  }
  if (step.phase === "checking") {
    const vm = m.match(/Latest:\s*v?([\d.]+)/);
    return vm ? `v${vm[1]}` : null;
  }
  if (step.phase === "downloading") {
    const um = m.match(/Using cached\s+([\d.]+\s*MB)/i);
    if (um) return `已缓存 ${um[1]}`;
    const dm = m.match(/Downloaded\s+([\d.]+\s*MB)/i);
    if (dm) return `已下载 ${dm[1]}`;
    return null; // mid-download — only the bar / percentage matters
  }
  if (step.phase === "checking-wsl") {
    const um = m.match(/Using distro:\s*(\S+)/);
    if (um) return um[1];
    return null;
  }
  if (step.phase === "installing-wsl") {
    const dlm = m.match(/Downloading Ubuntu rootfs.*?([\d.]+)\s*MB/i);
    if (dlm) return `下载中 ${dlm[1]} / ~350 MB`;
    if (/Importing|wsl --import/i.test(m)) return "导入 Ubuntu 镜像…";
    return "需要管理员权限，约 5–10 分钟";
  }
  if (step.phase === "waiting-distro") {
    return "首次启动约需一分钟，请稍候…";
  }
  if (step.phase === "installing-cicy-code") {
    const vm = m.match(/v([\d.]+)/);
    return vm ? `v${vm[1]}` : null;
  }
  if (step.phase === "picking-agents") {
    if (Array.isArray(step.agents) && step.agents.length > 0) {
      const friendly = step.agents.map((a) => ({ claude: "Claude", codex: "Codex", opencode: "OpenCode" }[a] || a)).join(", ");
      return friendly;
    }
    if (/Waiting/i.test(m)) return "请在右侧选择";
    return null;
  }
  if (step.phase === "starting") {
    const am = m.match(/--agents=(\S+)/);
    if (am) {
      const friendly = am[1].split(",").map((a) => ({ claude: "Claude", codex: "Codex", opencode: "OpenCode" }[a] || a)).join(", ");
      return friendly;
    }
    return null;
  }
  if (step.phase === "installing-deps") {
    if (/ready|Apt packages ready/i.test(m)) return "完成";
    if (/Warning/i.test(m)) return "部分失败，cicy-code 会自动补装";
    return "解压工具 + 中文字体";
  }
  if (step.phase === "done") {
    const vm = m.match(/v([\d.]+)/);
    return vm ? `v${vm[1]}` : null;
  }
  return null;
}
