import Button from "./Button.jsx";
import Icon from "./Icon.jsx";
import "./SidecarBanner.css";

/**
 * <SidecarBanner state="..." info={...} progress={...} onInstall onCancel />
 *
 * Full-width banner shown when:
 *   missing    — cicy-code not installed yet (first-run blocker)
 *   installing — install / upgrade in progress
 *   error      — last install attempt failed
 *
 * In other states (uptodate, outdated, checking) the banner is null;
 * "outdated" surfaces inside the BackendCard's menu instead.
 */
export default function SidecarBanner({ state, info, progress, error, onInstall, onCancel }) {
  if (state !== "missing" && state !== "installing" && state !== "error") return null;

  const isInstalling = state === "installing";
  const isError      = state === "error";

  let title, subtitle, actions;

  if (state === "missing") {
    title = "Install cicy-code to get started";
    subtitle = info?.latest
      ? `Latest v${info.latest} · ${info.network === "cn" ? "China mirror" : "GitHub direct"}`
      : "Required for the local engine";
    actions = <Button variant="primary" icon={<Icon name="download" />} onClick={onInstall}>Install</Button>;
  } else if (isInstalling) {
    const p = progress || {};
    title = p.message || "Installing…";
    const pct = typeof p.progress === "number" ? Math.round(p.progress * 100) : null;
    subtitle = p.phase === "downloading" && p.total
      ? `${formatBytes(p.received)} / ${formatBytes(p.total)}${p.version ? ` · v${p.version}` : ""}`
      : (p.version ? `v${p.version}` : p.phase || "");
    actions = <Button variant="ghost" onClick={onCancel}>Cancel</Button>;
    return (
      <div className="sidecar-banner is-installing">
        <div className="sidecar-banner__head">
          <div className="sidecar-banner__icon"><Icon name="download" size={18} /></div>
          <div className="sidecar-banner__text">
            <div className="sidecar-banner__title">{title}</div>
            <div className="sidecar-banner__subtitle">{subtitle}</div>
          </div>
          <div className="sidecar-banner__actions">{actions}</div>
        </div>
        <div className={`sidecar-banner__bar ${pct == null ? "is-indeterminate" : ""}`}>
          <div className="sidecar-banner__bar-fill" style={{ width: pct == null ? "30%" : `${pct}%` }} />
        </div>
        <div className="sidecar-banner__bar-meta">
          <span>{p.phase || ""}</span>
          <span>{pct == null ? "" : `${pct}%`}</span>
        </div>
      </div>
    );
  } else if (isError) {
    title = "Install failed";
    subtitle = error || "Unknown error";
    actions = <Button variant="primary" onClick={onInstall}>Retry</Button>;
  }

  return (
    <div className={`sidecar-banner ${isError ? "is-error" : ""}`}>
      <div className="sidecar-banner__head">
        <div className="sidecar-banner__icon">
          <Icon name={isError ? "warn" : "download"} size={18} />
        </div>
        <div className="sidecar-banner__text">
          <div className="sidecar-banner__title">{title}</div>
          <div className="sidecar-banner__subtitle">{subtitle}</div>
        </div>
        <div className="sidecar-banner__actions">{actions}</div>
      </div>
    </div>
  );
}

function formatBytes(n) {
  if (n == null) return "?";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}
