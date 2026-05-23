import "./StatusChip.css";

/**
 * <StatusChip kind="up|warn|down|unknown" label="..." />
 *
 * The single source of truth for backend status presentation.
 * Avoids the row's previous "dot + colored text + icon" triple-redundancy.
 */
const COLORS = {
  up:      { bg: "var(--success-soft)", fg: "var(--success)", label: "Online" },
  warn:    { bg: "var(--warn-soft)",    fg: "var(--warn)",    label: "Degraded" },
  down:    { bg: "var(--danger-soft)",  fg: "var(--danger)",  label: "Offline" },
  unknown: { bg: "var(--bg-elev-2)",    fg: "var(--fg-3)",    label: "—" },
};

export default function StatusChip({ kind = "unknown", label }) {
  const c = COLORS[kind] || COLORS.unknown;
  return (
    <span className={`status-chip status-chip--${kind}`} style={{ background: c.bg, color: c.fg }}>
      <span className="status-chip__dot" />
      {label || c.label}
    </span>
  );
}

export function dotKind(health) {
  if (!health) return "unknown";
  if (health.ok && health.status === "ok") return "up";
  if (health.ok) return "warn";
  return "down";
}
