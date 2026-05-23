import Card from "./Card.jsx";
import Button from "./Button.jsx";
import Menu from "./Menu.jsx";
import StatusChip, { dotKind } from "./StatusChip.jsx";
import Icon from "./Icon.jsx";
import { useT } from "../i18n";
import "./BackendCard.css";

/**
 * Square BackendCard — single tile in the grid. 1:1 aspect ratio.
 *
 * Visual layout (top-down):
 *   ┌──────────────────────┐
 *   │ ●           ⋯        │  meta row: status dot, kind icon, menu
 *   │                      │
 *   │ Local                │  name (large, bold)
 *   │ 127.0.0.1:8008       │  host (mono, dim)
 *   │ v2.0.7  ·  3h        │  version + uptime
 *   │                      │
 *   │ ┌──────────────────┐ │
 *   │ │  ↗   Open        │ │  primary action (full-width)
 *   │ └──────────────────┘ │
 *   └──────────────────────┘
 */
export default function BackendCard({
  backend, health, onOpen, menuItems = [],
  badge, openLoading = false, openDisabled = false,
  primaryButton, progress, onCancel,
}) {
  const t = useT();
  const isLocal = backend.kind === "local";
  const kind = dotKind(health);
  const ver = health && health.version ? `v${health.version}` : "";
  const port = backend.port || (isLocal ? "8008" : "");
  const host = isLocal
    ? `127.0.0.1${port ? `:${port}` : ""}`
    : (() => { try { return new URL(backend.url).host; } catch { return backend.url; } })();
  const uptime = health && health.uptime_sec ? formatUptime(health.uptime_sec) : "";

  const hasProgress = !!progress;
  const pct = hasProgress && typeof progress.progress === "number"
    ? Math.round(progress.progress * 100) : null;

  const accent = isLocal ? "var(--brand)" : "var(--accent-cloud)";

  return (
    <div className={`bcard ${isLocal ? "bcard--local" : "bcard--cloud"} ${kind === "up" ? "bcard--online" : ""}`}>
      <div className="bcard__accent" style={{ background: accent }} />

      <div className="bcard__top">
        <div className="bcard__status-pill">
          <span className={`bcard__dot bcard__dot--${kind}`} />
          <Icon name={isLocal ? "laptop" : "globe"} size={12} />
        </div>
        {menuItems.length > 0 && !hasProgress && (
          <Menu
            trigger={<Button variant="ghost" size="sm" className="btn--icon bcard__menu-trigger" icon={<Icon name="more" />} />}
            items={menuItems}
          />
        )}
      </div>

      <div className="bcard__body">
        <h3 className="bcard__name" title={backend.name}>{backend.name}</h3>
        {badge && <span className="bcard__badge">{badge}</span>}
        <div className="bcard__host selectable" title={host}>{host}</div>
        {(ver || uptime) && (
          <div className="bcard__meta">
            {ver && <span className="bcard__ver">{ver}</span>}
            {ver && uptime && <span className="bcard__sep">·</span>}
            {uptime && <span className="bcard__uptime">{t("card.uptime", { value: uptime })}</span>}
          </div>
        )}
      </div>

      {hasProgress ? (
        <div className="bcard__progress">
          <div className="bcard__progress-text">
            <span>{progress.message || t("card.installing")}</span>
            <span className="bcard__progress-pct">{pct == null ? "" : `${pct}%`}</span>
          </div>
          <div className={`bcard__progress-bar ${pct == null ? "is-indeterminate" : ""}`}>
            <div className="bcard__progress-fill" style={{ width: pct == null ? "30%" : `${pct}%` }} />
          </div>
          <div className="bcard__progress-meta">
            <span>
              {progress.received != null && progress.total
                ? `${formatBytes(progress.received)} / ${formatBytes(progress.total)}`
                : (progress.phase || "")}
            </span>
            {onCancel && (
              <button type="button" className="bcard__cancel" onClick={onCancel}>{t("card.cancel")}</button>
            )}
          </div>
        </div>
      ) : (
        <button
          type="button"
          className={`bcard__cta ${primaryButton?.variant === "ghost" ? "bcard__cta--ghost" : ""}`}
          disabled={primaryButton?.disabled ?? openDisabled}
          onClick={primaryButton?.onClick || onOpen}
        >
          {(primaryButton?.loading ?? openLoading)
            ? <span className="bcard__cta-spinner" />
            : <Icon name={primaryButton?.icon || "open"} size={14} />}
          <span>{primaryButton?.label || t("card.open")}</span>
        </button>
      )}
    </div>
  );
}

function formatUptime(sec) {
  if (sec < 60)    return `${sec}s`;
  if (sec < 3600)  return `${Math.floor(sec / 60)}m`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h`;
  return `${Math.floor(sec / 86400)}d`;
}

function formatBytes(n) {
  if (n == null) return "?";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}
