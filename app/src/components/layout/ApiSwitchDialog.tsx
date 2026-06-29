import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Plus, Trash2, Check } from 'lucide-react';
import { getApiBase } from '../../config';
import api from '../../services/api';

const LS_PRESETS = 'cicy_api_presets';
const TOKEN_KEY = 'api_token';

type Preset = { label: string; value: string };

function parseValue(value: string): { url: string; token: string } {
  const idx = value.indexOf('?token=');
  if (idx === -1) return { url: value, token: '' };
  return { url: value.slice(0, idx), token: value.slice(idx + 7) };
}

function loadPresets(): Preset[] {
  try { return JSON.parse(localStorage.getItem(LS_PRESETS) || '[]'); }
  catch { return []; }
}

function savePresets(p: Preset[]) {
  localStorage.setItem(LS_PRESETS, JSON.stringify(p));
}

const DEFAULT_URL = import.meta.env.VITE_API_BASE || '';
const LOCAL_TOKEN_BACKUP = 'api_token_local';


export function ApiSwitchDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation('apiSwitch');
  const [presets, setPresets] = useState(loadPresets);
  const [current, setCurrent] = useState(getApiBase);
  const [newLabel, setNewLabel] = useState('');
  const [newUrl, setNewUrl] = useState('');
  const [copied, setCopied] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [testing, setTesting] = useState(false);

  function copy(text: string, key: string) {
    navigator.clipboard.writeText(text);
    setCopied(key);
    setTimeout(() => setCopied(null), 1000);
  }

  function select(value: string) {
    const { url, token } = parseValue(value);
    const isDefault = !url || url === DEFAULT_URL;
    if (isDefault) {
      // restore local token
      const local = localStorage.getItem(LOCAL_TOKEN_BACKUP);
      if (local) localStorage.setItem(TOKEN_KEY, local);
    } else {
      // backup local token before switching
      const cur = localStorage.getItem(TOKEN_KEY);
      if (cur) localStorage.setItem(LOCAL_TOKEN_BACKUP, cur);
      if (token) localStorage.setItem(TOKEN_KEY, token);
    }
    // apiBase 切换已下线:前端一律同域名直连(inferApiBase 不再读 localStorage),
    // 这里仅切换 token,不再改写 apiBase。
    setCurrent(isDefault ? DEFAULT_URL : url);
  }

  async function add() {
    if (!newUrl.trim()) { setError(t('errorUrlRequired')); return; }
    const { url: baseUrl, token } = parseValue(newUrl.trim());
    try { new URL(baseUrl); } catch { setError(t('errorUrlInvalid')); return; }
    if (!token) { setError(t('errorTokenRequired')); return; }

    setTesting(true); setError('');
    try {
      const { data } = await api.verifyTokenAt(baseUrl, token);
      if (!data.valid) { setError(t('errorTokenInvalid')); return; }
    } catch (e: any) {
      setError(t('errorConnect', { message: e.message || t('errorConnectUnknown') })); return;
    } finally { setTesting(false); }

    const label = newLabel.trim() || (() => { try { return new URL(baseUrl).hostname; } catch { return baseUrl; } })();
    // Auto-upgrade to https (non-localhost)
    const finalUrl = newUrl.trim().replace(/^http:\/\/(?!localhost|127\.)/, 'https://');
    const p = [...presets, { label, value: finalUrl }];
    setPresets(p); savePresets(p);
    setNewLabel(''); setNewUrl(''); setError('');
  }

  function remove(i: number) {
    const p = presets.filter((_, idx) => idx !== i);
    setPresets(p); savePresets(p);
  }

  const defaultVal = `${DEFAULT_URL}${localStorage.getItem(TOKEN_KEY) ? `?token=${localStorage.getItem(TOKEN_KEY)}` : ''}`;

  return (
    <div data-id="api-switch-dialog-overlay" className="fixed inset-0 z-50 flex cursor-pointer items-center justify-center bg-black/60" onClick={onClose}>
      <div data-id="api-switch-dialog" className="w-[460px] cursor-default rounded-xl border border-white/10 bg-[#1a1a1a] p-5 shadow-2xl" onClick={e => e.stopPropagation()}>
        <div data-id="api-switch-dialog-header" className="flex items-center justify-between mb-4">
          <button data-id="api-switch-dialog-close" onClick={onClose} className="ml-auto text-zinc-500 hover:text-zinc-300"><X className="w-4 h-4" /></button>
        </div>

        <div data-id="api-switch-dialog-presets" className="space-y-1 mb-4">
          {/* default cannot be removed */}
          <div data-id="api-switch-dialog-preset-default" className={`flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer group ${current === DEFAULT_URL ? 'bg-white/10' : 'hover:bg-white/5'}`}
            onClick={() => select(DEFAULT_URL)}>
            <Check className={`w-3.5 h-3.5 shrink-0 ${current === DEFAULT_URL ? 'text-emerald-400' : 'text-transparent'}`} />
            <span data-id="api-switch-dialog-preset-default-label" className="text-xs text-zinc-300 w-16 shrink-0">{t('labelDefault')}</span>
            <span data-id="api-switch-dialog-preset-default-value" className="text-xs text-zinc-500 font-mono truncate flex-1">{defaultVal || t('defaultEmpty')}</span>
            <button data-id="api-switch-dialog-preset-default-copy" onClick={e => { e.stopPropagation(); copy(defaultVal, 'default'); }}
              className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-zinc-300 text-[10px] shrink-0">
              {copied === 'default' ? t('copied') : t('copy')}
            </button>
          </div>

          {presets.map((p, i) => {
            const { url, token } = parseValue(p.value);
            return (
              <div key={i} data-id={`api-switch-dialog-preset-${i}`} className={`flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer group ${current === url ? 'bg-white/10' : 'hover:bg-white/5'}`}
                onClick={() => select(p.value)}>
                <Check className={`w-3.5 h-3.5 shrink-0 ${current === url ? 'text-emerald-400' : 'text-transparent'}`} />
                <span data-id={`api-switch-dialog-preset-${i}-label`} className="text-xs text-zinc-300 w-16 shrink-0">{p.label}</span>
                <span data-id={`api-switch-dialog-preset-${i}-value`} className="text-xs text-zinc-500 font-mono truncate flex-1">{url}</span>
                {token && <span data-id={`api-switch-dialog-preset-${i}-token`} className="text-[10px] text-zinc-600 shrink-0">🔑</span>}
                <button data-id={`api-switch-dialog-preset-${i}-copy`} onClick={e => { e.stopPropagation(); copy(p.value, `p${i}`); }}
                  className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-zinc-300 text-[10px] shrink-0">
                  {copied === `p${i}` ? t('copied') : t('copy')}
                </button>
                <button data-id={`api-switch-dialog-preset-${i}-remove`} onClick={e => { e.stopPropagation(); remove(i); }}
                  className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-red-400">
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
            );
          })}
        </div>

        <div data-id="api-switch-dialog-add" className="border-t border-white/5 pt-3 mb-4">
          <div data-id="api-switch-dialog-add-row" className="flex gap-2">
            <input data-id="api-switch-dialog-add-label-input" value={newLabel} onChange={e => setNewLabel(e.target.value)} placeholder={t('namePlaceholder')}
              className="w-20 bg-white/5 border border-white/10 rounded px-2 py-1.5 text-xs text-zinc-300 outline-none focus:border-white/20" />
            <input data-id="api-switch-dialog-add-url-input" value={newUrl} onChange={e => { setNewUrl(e.target.value); setError(''); }} placeholder="https://...?token=xxx"
              className={`flex-1 bg-white/5 border rounded px-2 py-1.5 text-xs text-zinc-300 font-mono outline-none focus:border-white/20 ${error ? 'border-red-500/60' : 'border-white/10'}`} />
            <button data-id="api-switch-dialog-add-submit" onClick={add} disabled={testing} className="px-3 bg-white/5 hover:bg-white/10 disabled:opacity-50 rounded text-zinc-300 text-xs flex items-center gap-1">
              <Plus className="w-3.5 h-3.5" /> {testing ? t('verifying') : t('add')}
            </button>
          </div>
          {error && <p data-id="api-switch-dialog-add-error" className="text-red-400 text-[11px] mt-1.5">{error}</p>}
        </div>

        <div data-id="api-switch-dialog-footer" className="flex justify-end">
          <button data-id="api-switch-dialog-apply" onClick={() => window.location.reload()}
            className="px-3 py-1.5 bg-emerald-600/80 hover:bg-emerald-600 text-white text-xs rounded-lg">
            {t('applyAndReload')}
          </button>
        </div>
      </div>
    </div>
  );
}
