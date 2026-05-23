import { useState } from "react";
import Button from "./Button.jsx";
import Icon from "./Icon.jsx";
import { useT } from "../i18n";
import "./WslSetupBanner.css";

export default function WslSetupBanner({ wsl, onRecheck, recheckLoading }) {
  const t = useT();
  const [installing, setInstalling] = useState(false);
  const [installResult, setInstallResult] = useState(null);

  if (!wsl || !wsl.supported) return null;
  if (wsl.installed && wsl.hasDistro) return null;

  const cmd = "wsl --install -d Ubuntu";
  const title    = wsl.installed ? t("wsl.title_no_distro") : t("wsl.title_install");
  const subtitle = t("wsl.subtitle");

  const copy = async () => {
    try { await navigator.clipboard.writeText(cmd); } catch {}
  };

  const oneClick = async () => {
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

  return (
    <div className="wsl-banner">
      <div className="wsl-banner__head">
        <div className="wsl-banner__icon"><Icon name="warn" size={18} /></div>
        <div className="wsl-banner__text">
          <div className="wsl-banner__title">{title}</div>
          <div className="wsl-banner__subtitle">{subtitle}</div>
        </div>
        <Button variant="ghost" loading={recheckLoading} onClick={onRecheck}>{t("wsl.recheck")}</Button>
      </div>

      <div className="wsl-banner__actions">
        <Button variant="primary" loading={installing} onClick={oneClick}>
          <Icon name="download" size={14} /> {t("wsl.install_now")}
        </Button>
        <span className="wsl-banner__hint">{t("wsl.uac_hint")}</span>
      </div>

      <div className="wsl-banner__cmd">
        <code>{cmd}</code>
        <Button variant="ghost" size="sm" onClick={copy}>{t("wsl.copy")}</Button>
      </div>

      {installResult && !installResult.ok && (
        <div className="wsl-banner__error">
          {t("wsl.install_failed")}: {installResult.error || installResult.stderr || "unknown"}
        </div>
      )}

      <a className="wsl-banner__link" href="https://learn.microsoft.com/windows/wsl/install" target="_blank" rel="noreferrer">
        {t("wsl.docs")}
      </a>
    </div>
  );
}
