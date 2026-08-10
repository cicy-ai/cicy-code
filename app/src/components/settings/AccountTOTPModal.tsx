import { Check, Copy, Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AppModal } from "../ui/Modal";

export type TOTPValue = { capture: string; expiresAt: number };

export default function AccountTOTPModal({ name, value, loading, onClose, onRefresh }: { name: string | null; value?: TOTPValue; loading: boolean; onClose: () => void; onRefresh: () => void }) {
  const { t } = useTranslation("workspace");
  const [clock, setClock] = useState(Date.now());
  const [copied, setCopied] = useState(false);
  const refreshRef = useRef(onRefresh);
  useEffect(() => { refreshRef.current = onRefresh; }, [onRefresh]);
  useEffect(() => { const timer = window.setInterval(() => setClock(Date.now()), 1000); return () => window.clearInterval(timer); }, []);
  useEffect(() => {
    if (!name || !value || loading) return;
    const timer = window.setTimeout(() => refreshRef.current(), Math.max(0, value.expiresAt - Date.now()) + 150);
    return () => window.clearTimeout(timer);
  }, [name, value?.expiresAt, loading]);
  useEffect(() => { setCopied(false); }, [name, value?.capture]);
  const copy = async () => {
    if (!value) return;
    await navigator.clipboard.writeText(value.capture);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  return <AppModal open={name !== null} title={t("github2FAModalTitle", { name: name || "" })} onClose={onClose} maxWidth="400px"><section data-id="account-totp-modal" className="py-3 text-center">{loading ? <Loader2 className="mx-auto h-6 w-6 animate-spin text-amber-300" /> : value ? <><button data-id="account-totp-copy" type="button" onClick={() => void copy()} className={`group mx-auto flex items-center gap-3 rounded-xl border px-5 py-4 font-mono text-3xl tracking-[0.28em] transition-colors ${copied ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-200" : "border-amber-500/20 bg-amber-500/[0.08] text-amber-200"}`}>{value.capture}{copied ? <Check className="h-4 w-4 tracking-normal text-emerald-400" /> : <Copy className="h-4 w-4 tracking-normal text-amber-500 group-hover:text-amber-300" />}</button><div data-id="account-totp-copy-feedback" className={`mt-2 h-4 text-[11px] transition-opacity ${copied ? "text-emerald-400 opacity-100" : "opacity-0"}`}>{t("copied", { defaultValue: "已复制" })}</div><div data-id="account-totp-countdown" className="mt-1 text-[12px] text-zinc-500">{t("github2FAExpiresIn", { seconds: Math.max(0, Math.ceil((value.expiresAt - clock) / 1000)) })}</div></> : <div className="text-[12px] text-rose-300">{t("github2FAGenerateFailed")}</div>}</section></AppModal>;
}
