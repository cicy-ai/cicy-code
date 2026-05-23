import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import "./Menu.css";

/**
 * <Menu trigger={<button>⋯</button>} items={[
 *   { label: "Restart", icon: ..., onClick: () => {} },
 *   "divider",
 *   { label: "Update to v2.0.3", icon: ..., onClick: () => {}, badge: true },
 * ]} />
 *
 * Renders the menu in a portal (position: fixed) so parent overflow:hidden
 * never clips it. Closes on outside click + Escape.
 */
export default function Menu({ trigger, items, align = "right" }) {
  const [open, setOpen] = useState(false);
  const [pos, setPos]   = useState({ top: 0, left: 0 });
  const triggerRef = useRef(null);
  const menuRef    = useRef(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)
          && triggerRef.current && !triggerRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    const onKey = (e) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const toggle = (e) => {
    e.stopPropagation();
    if (open) { setOpen(false); return; }
    const r = triggerRef.current.getBoundingClientRect();
    // Initial position; refined after first paint when we know menu width.
    setPos({ top: r.bottom + 4, left: align === "right" ? r.right - 180 : r.left });
    setOpen(true);
  };

  // After mount, snap to actual width.
  useEffect(() => {
    if (!open || !menuRef.current || !triggerRef.current) return;
    const r = triggerRef.current.getBoundingClientRect();
    const w = menuRef.current.offsetWidth;
    setPos((p) => ({ ...p, left: align === "right" ? r.right - w : r.left }));
  }, [open, align]);

  return (
    <>
      <span ref={triggerRef} onClick={toggle} className="menu-trigger no-drag">
        {trigger}
      </span>
      {open && createPortal((
        <div
          ref={menuRef}
          className="menu"
          role="menu"
          style={{ top: pos.top, left: pos.left }}
        >
          {items.map((it, i) => {
            if (it === "divider") return <div key={i} className="menu-divider" />;
            return (
              <button
                key={i}
                role="menuitem"
                className={`menu-item ${it.disabled ? "is-disabled" : ""} ${it.danger ? "is-danger" : ""}`}
                disabled={it.disabled}
                onClick={(e) => {
                  e.stopPropagation();
                  setOpen(false);
                  it.onClick && it.onClick();
                }}
              >
                {it.icon && <span className="menu-item__icon">{it.icon}</span>}
                <span className="menu-item__label">{it.label}</span>
                {it.badge && <span className="menu-item__badge" />}
              </button>
            );
          })}
        </div>
      ), document.body)}
    </>
  );
}
