import { useState, useEffect } from 'react';
import { X, Plus, Trash2, Copy, Key, Loader2, Check } from 'lucide-react';
import apiService from '../../services/api';

const PERMS = ['ttyd_read', 'prompt', 'api_full', 'tmux_send', 'edit', 'restart', 'capture'];

export default function TokenDialog({ onClose }: { onClose: () => void }) {
  const [tokens, setTokens] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [note, setNote] = useState('');
  const [perms, setPerms] = useState<string[]>(['ttyd_read', 'prompt']);
  const [newToken, setNewToken] = useState('');
  const [copied, setCopied] = useState<string | null>(null);

  const load = async () => {
    try { const { data } = await apiService.getTokens(); setTokens(data.tokens || []); } catch {}
    finally { setLoading(false); }
  };
  useEffect(() => { load(); }, []);

  const create = async () => {
    if (!note.trim()) return;
    setCreating(true);
    try {
      const { data } = await apiService.createToken({ note: note.trim(), perms });
      setNewToken(data.token);
      setNote(''); setPerms(['ttyd_read', 'prompt']);
      load();
    } catch {} finally { setCreating(false); }
  };

  const remove = async (id: number) => {
    try { await apiService.deleteToken(id); setTokens(prev => prev.filter(t => t.id !== id)); } catch {}
  };

  const copy = (text: string, id?: string) => {
    const fallbackCopy = (text: string) => {
      const ta = document.createElement('textarea');
      ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
      document.body.appendChild(ta); ta.select(); document.execCommand('copy');
      document.body.removeChild(ta);
      setCopied(id || 'new'); setTimeout(() => setCopied(null), 1500);
    };
    
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(() => {
        setCopied(id || 'new'); setTimeout(() => setCopied(null), 1500);
      }).catch(() => fallbackCopy(text));
    } else fallbackCopy(text);
  };

  return (
    <div className="fixed inset-0 z-[99999] flex items-center justify-center" onClick={onClose}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div className="relative w-[600px] max-w-[92vw] max-h-[80vh] bg-[#161618] rounded-2xl shadow-2xl border border-white/[0.08] flex flex-col overflow-hidden"
        onClick={e => e.stopPropagation()}>

        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06] shrink-0">
          <div className="flex items-center gap-2">
            <Key className="w-4 h-4 text-zinc-400" />
            <h2 className="text-[15px] font-semibold text-white">API Tokens</h2>
          </div>
          <button onClick={onClose} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors cursor-pointer">
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Create */}
        <div className="px-5 py-4 border-b border-white/[0.06] space-y-3 shrink-0">
          <div className="flex gap-2">
            <input value={note} onChange={e => setNote(e.target.value)} placeholder="Token note..."
              className="flex-1 bg-white/[0.03] border border-white/[0.08] text-zinc-200 text-sm rounded-lg px-3 py-2 focus:outline-none focus:border-blue-500/40 placeholder:text-zinc-700" />
            <button onClick={create} disabled={creating || !note.trim()}
              className="flex items-center gap-1.5 px-3 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium disabled:opacity-40 cursor-pointer transition-colors">
              {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
              Create
            </button>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {PERMS.map(p => (
              <button key={p} onClick={() => setPerms(prev => prev.includes(p) ? prev.filter(x => x !== p) : [...prev, p])}
                className={`px-2 py-1 rounded text-[11px] font-mono transition-colors cursor-pointer ${
                  perms.includes(p) ? 'bg-blue-500/20 text-blue-300 border border-blue-500/30' : 'bg-white/[0.03] text-zinc-600 border border-white/[0.06] hover:text-zinc-400'
                }`}>{p}</button>
            ))}
          </div>
          {newToken && (
            <div className="flex items-center gap-2 bg-emerald-500/10 border border-emerald-500/20 rounded-lg px-3 py-2">
              <code className="text-xs text-emerald-300 font-mono flex-1 truncate">{newToken}</code>
              <button onClick={() => { copy(newToken); setNewToken(''); }} className="p-1 text-emerald-400 hover:text-emerald-300 cursor-pointer">
                {copied === 'new' ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
              </button>
            </div>
          )}
        </div>

        {/* List */}
        <div className="flex-1 overflow-y-auto px-5 py-3">
          {loading ? (
            <div className="flex items-center justify-center py-8"><Loader2 className="w-5 h-5 animate-spin text-zinc-600" /></div>
          ) : tokens.length === 0 ? (
            <p className="text-center text-zinc-600 text-sm py-8">No tokens</p>
          ) : (
            <div className="space-y-2">
              {tokens.map((t: any) => (
                <div key={t.id} className="flex items-center gap-3 bg-white/[0.02] border border-white/[0.06] rounded-lg px-3 py-2.5 group">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-zinc-300 font-medium">{t.note || 'Untitled'}</span>
                      <span className="text-[10px] text-zinc-600 font-mono">#{t.id}</span>
                    </div>
                    <div className="flex items-center gap-2 mt-1">
                      <code className="text-[11px] text-zinc-600 font-mono truncate max-w-[200px]">{t.token_prefix}</code>
                      <span className="text-[10px] text-zinc-700">prefix only</span>
                    </div>
                    <div className="flex flex-wrap gap-1 mt-1.5">
                      {(t.perms || '').split(',').filter(Boolean).map((p: string) => (
                        <span key={p} className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-white/[0.04] text-zinc-500">{p}</span>
                      ))}
                    </div>
                  </div>
                  <span className="text-[10px] text-zinc-700 shrink-0">{t.created_at?.slice(0, 10)}</span>
                  <button onClick={() => remove(t.id)}
                    className="p-1.5 rounded text-zinc-700 hover:text-red-400 hover:bg-red-500/10 transition-colors cursor-pointer opacity-0 group-hover:opacity-100">
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
