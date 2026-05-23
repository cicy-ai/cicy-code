// Minimal i18n: locale auto-detect + ?lang= override + LocalStorage persistence.
// Usage:  import { t, useT, setLocale, locale } from "./i18n";
//
// Locales: zh-CN (default), en, ja, fr. Add a new one by dropping a JSON in
// this dir and registering it in LOCALES below.

import zhCN from "./zh-CN.json";
import en   from "./en.json";
import ja   from "./ja.json";
import fr   from "./fr.json";
import { useEffect, useState } from "react";

const LOCALES = { "zh-CN": zhCN, en, ja, fr };
const FALLBACK = "zh-CN";

function detectLocale() {
  // Priority: ?lang=X → localStorage → navigator → fallback (zh-CN)
  // Note: zh-CN is the product's primary language; we only switch to en/ja/fr
  // when explicitly requested via URL, storage, or a clear navigator hint.
  try {
    const url = new URL(window.location.href);
    const q = url.searchParams.get("lang");
    if (q && LOCALES[q]) return q;
  } catch {}
  try {
    const stored = localStorage.getItem("cicy.locale");
    if (stored && LOCALES[stored]) return stored;
  } catch {}
  const nav = (navigator.language || "").toLowerCase();
  if (nav.startsWith("zh")) return "zh-CN";
  if (nav.startsWith("ja")) return "ja";
  if (nav.startsWith("fr")) return "fr";
  // Default: zh-CN. English requires explicit opt-in via ?lang=en.
  return FALLBACK;
}

let _locale = detectLocale();
const _listeners = new Set();

export function locale() { return _locale; }

export function setLocale(next) {
  if (!LOCALES[next]) return;
  _locale = next;
  try { localStorage.setItem("cicy.locale", next); } catch {}
  _listeners.forEach((fn) => fn(next));
}

/** Translate a key. Supports {var} interpolation. */
export function t(key, vars) {
  const dict = LOCALES[_locale] || LOCALES[FALLBACK];
  let s = dict[key];
  if (s == null) s = LOCALES[FALLBACK][key] || key;
  if (vars) for (const k in vars) s = s.replace(new RegExp(`\\{${k}\\}`, "g"), vars[k]);
  return s;
}

/** Hook that re-renders when locale changes. */
export function useT() {
  const [, force] = useState(0);
  useEffect(() => {
    const fn = () => force((n) => n + 1);
    _listeners.add(fn);
    return () => _listeners.delete(fn);
  }, []);
  return t;
}

export const AVAILABLE = Object.keys(LOCALES);
