import { useState } from "react";
import Button from "./Button.jsx";
import Icon from "./Icon.jsx";
import { useT } from "../i18n";
import "./WslSetupBanner.css";

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

export default function WslSetupBanner({ wsl, onRecheck, recheckLoading, onInstall, progress, progressLog, progressSteps, onDismiss }) {
  const t = useT();
  const [installing, setInstalling] = useState(false);
  const [installResult, setInstallResult] = useState(null);
  const [showRawLog, setShowRawLog] = useState(false);

  if (!wsl) return null;
  // wsl.exe itself missing — link user to official docs, nothing to run.
  if (wsl.supported === false) {
    return (
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
  // still running OR has steps to display, keep the banner alive so the
  // user can watch the timeline. Once everything is idle and steps are
  // cleared, the banner hides itself.
  const hasActiveInstall = installing || (progressSteps && progressSteps.length > 0);
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

  return (
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
        ) : (
          <Button variant="ghost" loading={recheckLoading} onClick={onRecheck}>{t("wsl.recheck")}</Button>
        )}
      </div>

      {/* Hide the install button once we're done — the close button in
          the head is the only relevant action. */}
      {!installFinished && <div className="wsl-banner__actions">
        <Button variant="primary" loading={installing} onClick={oneClick}>
          <Icon name="download" size={14} /> {t("wsl.install_now")}
        </Button>
        <span className="wsl-banner__hint">{t("wsl.uac_hint")}</span>
      </div>}

      {installResult && !installResult.ok && (
        <div className="wsl-banner__error">
          {t("wsl.install_failed")}: {installResult.error || installResult.stderr || "unknown"}
        </div>
      )}

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
                    {failed && !installing && (
                      <button
                        type="button"
                        className="wsl-banner__step-retry"
                        onClick={oneClick}
                        disabled={installing}
                      >
                        重试
                      </button>
                    )}
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
    case "configuring-apt":      return "配置软件源";
    case "installing-cicy-code": return "部署到 Linux";
    case "installing-deps":      return "下载运行依赖";
    case "starting":             return "启动 cicy-code";
    case "installing-agents":    return "下载 AI 助手";
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
    return "需要管理员权限，约 5–10 分钟";
  }
  if (step.phase === "installing-cicy-code") {
    const vm = m.match(/v([\d.]+)/);
    return vm ? `v${vm[1]}` : null;
  }
  if (step.phase === "installing-agents") {
    // Per-agent messages emit `<name> ✓` or `<name>: …` — surface the
    // current agent name in Chinese so a novice doesn't see raw npm
    // package identifiers.
    const am = m.match(/Installing (\S+)/);
    if (am) {
      const friendly = { claude: "Claude", codex: "Codex", opencode: "OpenCode" }[am[1]] || am[1];
      return `正在下载 ${friendly}…`;
    }
    const tickMatch = m.match(/^(\S+)\s*✓/);
    if (tickMatch) {
      const friendly = { claude: "Claude", codex: "Codex", opencode: "OpenCode" }[tickMatch[1]] || tickMatch[1];
      return `${friendly} 已就绪`;
    }
    if (/ready/i.test(m)) return "Claude / Codex / OpenCode 全部就绪";
    if (/Warning/i.test(m)) return "部分失败，首次使用时会自动重试";
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
