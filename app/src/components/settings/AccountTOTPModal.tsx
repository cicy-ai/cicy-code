import { Copy, Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AppModal } from "../ui/Modal";

export type TOTPValue = { capture: string; expiresAt: number };

export default function AccountTOTPModal({ name, value, loading, onClose }: { name: string | null; value?: TOTPValue; loading: boolean; onClose: () => void }) {
  const { t } = useTranslation("workspace");
  const [clock, setClock] = useState(Date.now());
  useEffect(() => { const timer = window.setInterval(() => setClock(Date.now()), 1000); return () => window.clearInterval(timer); }, []);
  return <AppModal open={name !== null} title={t("github2FAModalTitle", { name: name || "" })} onClose={onClose} maxWidth="400px"><section data-id="account-totp-modal" className="py-3 text-center">{loading ? <Loader2 className="mx-auto h-6 w-6 animate-spin text-amber-300" /> : value ? <><button data-id="account-totp-copy" type="button" onClick={() => void navigator.clipboard.writeText(value.capture)} className="group mx-auto flex items-center gap-3 rounded-xl border border-amber-500/20 bg-amber-500/[0.08] px-5 py-4 font-mono text-3xl tracking-[0.28em] text-amber-200">{value.capture}<Copy className="h-4 w-4 tracking-normal text-amber-500 group-hover:text-amber-300" /></button><div data-id="account-totp-countdown" className="mt-3 text-[12px] text-zinc-500">{t("github2FAExpiresIn", { seconds: Math.max(0, Math.ceil((value.expiresAt - clock) / 1000)) })}</div></> : <div className="text-[12px] text-rose-300">{t("github2FAGenerateFailed")}</div>}</section></AppModal>;
}
