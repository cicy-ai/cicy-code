import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import Button from "./Button.jsx";
import Icon from "./Icon.jsx";
import { api } from "../api.js";
import { useT } from "../i18n";
import "./BackendModal.css";

/**
 * <BackendModal mode="add"|"edit" backend={...} open onClose onSaved />
 *
 * Single dialog for both adding and editing a cloud backend.
 *   - mode="add":   blank fields, "Probe & Add" button
 *   - mode="edit":  pre-filled, "Save" button (URL probe optional via test btn)
 *
 * Rendered via React portal so it floats above any overflow:hidden ancestor.
 */
export default function BackendModal({ mode = "add", backend, open, onClose, onSaved }) {
  const t = useT();
  const isEdit = mode === "edit";
  const isLocal = isEdit && backend?.kind === "local";

  const [name, setName]   = useState("");
  const [url, setUrl]     = useState("");
  const [token, setToken] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [status, setStatus] = useState({ kind: "idle", msg: "" });
  const [submitting, setSubmitting] = useState(false);

  // Reset / hydrate when opening or switching backend
  useEffect(() => {
    if (!open) return;
    setStatus({ kind: "idle", msg: "" });
    setShowToken(false);
    if (isEdit && backend) {
      setName(backend.name || "");
      setUrl(backend.url || "");
      setToken(backend.token || "");
    } else {
      setName(""); setUrl(""); setToken("");
    }
  }, [open, isEdit, backend]);

  // Esc closes
  useEffect(() => {
    if (!open) return;
    const onKey = (e) => { if (e.key === "Escape") onClose && onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const submit = async () => {
    // Local edit path: only the display name. URL/token are managed by the
    // app (window-manager.buildLocalUrl reads global.json live). No probe.
    if (isLocal) {
      setSubmitting(true);
      try {
        const saved = await api.backends.update({ id: backend.id, name: name.trim() || backend.name });
        onSaved && onSaved(saved);
        onClose && onClose();
      } catch (e) {
        setStatus({ kind: "err", msg: e.message });
      } finally {
        setSubmitting(false);
      }
      return;
    }

    if (!url.trim()) { setStatus({ kind: "err", msg: t("modal.url_required") }); return; }
    setSubmitting(true);
    setStatus({ kind: "probing", msg: t("modal.probing") });
    try {
      const probe = await api.backends.probe({ url: url.trim(), token: token.trim() });
      if (!probe.ok) {
        setStatus({ kind: "err", msg: probe.error || `HTTP ${probe.statusCode || "?"}` });
        return;
      }
      const finalName = name.trim() || (() => { try { return new URL(url).host; } catch { return "Cloud"; } })();
      let saved;
      if (isEdit) {
        saved = await api.backends.update({
          id: backend.id,
          name: finalName,
          url: url.trim(),
          token: token.trim() || "",
        });
      } else {
        saved = await api.backends.add({
          name: finalName,
          url: url.trim(),
          token: token.trim() || undefined,
        });
      }
      onSaved && onSaved(saved);
      onClose && onClose();
    } catch (e) {
      setStatus({ kind: "err", msg: e.message });
    } finally {
      setSubmitting(false);
    }
  };

  const onBackdrop = (e) => {
    if (e.target === e.currentTarget) onClose && onClose();
  };

  return createPortal(
    <div className="modal-backdrop" onClick={onBackdrop}>
      <div className="modal" role="dialog" aria-modal="true">
        <header className="modal__head">
          <h3 className="modal__title">{
            isLocal ? t("modal.edit_local_title")
            : isEdit ? t("modal.edit_title")
            : t("modal.add_title")
          }</h3>
          <button className="modal__close" onClick={onClose} aria-label="Close">
            <Icon name="close" />
          </button>
        </header>

        <div className="modal__body">
          <label className="modal__field">
            <span className="modal__label">{t("modal.name")}</span>
            <input
              className="modal__input"
              type="text"
              placeholder={t("modal.name_placeholder")}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus={isLocal || isEdit}
            />
          </label>

          <label className="modal__field">
            <span className="modal__label">
              {t("modal.url")} {!isLocal && <span className="modal__required">*</span>}
              {isLocal && <span className="modal__locked">· {t("modal.locked_local")}</span>}
            </span>
            <input
              className="modal__input"
              type="url"
              placeholder="https://cicy.example.com:8008"
              value={isLocal ? "http://127.0.0.1:8008" : url}
              onChange={(e) => setUrl(e.target.value)}
              readOnly={isLocal}
              autoFocus={!isEdit && !isLocal}
            />
          </label>

          <label className="modal__field">
            <span className="modal__label">
              {t("modal.token")}
              {isLocal && <span className="modal__locked">· {t("modal.token_local_hint")}</span>}
            </span>
            <div className="modal__token-wrap">
              <input
                className="modal__input modal__token-input"
                type={showToken ? "text" : "password"}
                placeholder={isLocal ? "—" : t("modal.token_placeholder")}
                value={isLocal ? "" : token}
                onChange={(e) => setToken(e.target.value)}
                readOnly={isLocal}
                autoComplete="off"
                spellCheck={false}
              />
              {!isLocal && (
                <button
                  type="button"
                  className="modal__token-toggle"
                  onClick={() => setShowToken((v) => !v)}
                  aria-label={showToken ? t("modal.hide_token") : t("modal.show_token")}
                  title={showToken ? t("modal.hide_token") : t("modal.show_token")}
                >
                  <Icon name={showToken ? "eye-off" : "eye"} size={16} />
                </button>
              )}
            </div>
          </label>

          {status.msg && (
            <div className={`modal__status modal__status--${status.kind}`}>
              {status.msg}
            </div>
          )}
        </div>

        <footer className="modal__foot">
          <Button variant="ghost" onClick={onClose}>{t("modal.cancel")}</Button>
          <Button variant="primary" loading={submitting} onClick={submit}>
            {isEdit ? t("modal.save") : t("modal.probe_add")}
          </Button>
        </footer>
      </div>
    </div>,
    document.body
  );
}
