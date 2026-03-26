import { useState, useEffect, useRef } from 'react';
import { X, Save, Settings, Zap, Globe, Loader2, LogOut } from 'lucide-react';
import { EditPaneData } from '../EditPaneDialog';
import Select from '../ui/Select';
import apiService from '../../services/api';
import { useAuth } from '../../contexts/AuthContext';
import { useApp } from '../../contexts/AppContext';

const THEME_KEY = 'app_theme';
const themes = [
  { value: '', label: 'Default Dark' },
  { value: 'livestream', label: 'Livestream' },
];

function applyTheme(t: string) {
  document.documentElement.setAttribute('data-theme', t || '');
  localStorage.setItem(THEME_KEY, t);
}

const sections = [
  { id: 'general', label: 'General', icon: Settings },
  { id: 'agent', label: 'Agent', icon: Zap },
  { id: 'config', label: 'Config', icon: Settings },
  { id: 'global', label: 'Global', icon: Globe },
] as const;
type SectionId = typeof sections[number]['id'];

export default function SettingsFloat({ paneId, fullPaneId, agentDetail, onAgentDetailChange, onClose }: { paneId: string; fullPaneId: string; agentDetail: any; onAgentDetailChange: (d: any) => void; onClose: () => void }) {
  const { logout } = useAuth();
  const { globalVar, updateGlobalVar } = useApp();
  const [data, setData] = useState<EditPaneData>({ target: fullPaneId, title: paneId, ...agentDetail });
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [section, setSection] = useState<SectionId>('general');
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_KEY) || '');
  const [globalJson, setGlobalJson] = useState('');
  const [globalSaving, setGlobalSaving] = useState(false);
  const [globalSaved, setGlobalSaved] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);

  useEffect(() => { setGlobalJson(JSON.stringify(globalVar, null, 2)); }, [globalVar]);

  const initRef = useRef(false);
  useEffect(() => { if (agentDetail && !initRef.current) { initRef.current = true; setData(prev => ({ ...prev, ...agentDetail })); } }, [agentDetail]);

  const save = async () => {
    setSaving(true);
    try { await apiService.updatePane(paneId, data); onAgentDetailChange(data); setSaved(true); setTimeout(() => setSaved(false), 2000); } catch {}
    finally { setSaving(false); }
  };

  const saveGlobal = async () => {
    setGlobalSaving(true);
    try { await updateGlobalVar(JSON.parse(globalJson)); setGlobalSaved(true); setTimeout(() => setGlobalSaved(false), 2000); } catch {}
    finally { setGlobalSaving(false); }
  };

  const handleTheme = (v: string) => { setTheme(v); applyTheme(v); };
  const set = (patch: Partial<EditPaneData>) => setData(prev => ({ ...prev, ...patch }));

  return (
    <div className="fixed inset-0 z-[99999] flex items-center justify-center" onClick={onClose}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div className="relative w-[680px] max-w-[92vw] h-[70vh] bg-[#161618] rounded-2xl shadow-2xl border border-white/[0.08] flex overflow-hidden"
        onClick={e => e.stopPropagation()}>

        {/* Sidebar */}
        <nav className="w-[180px] bg-[#111113] border-r border-white/[0.06] flex flex-col shrink-0">
          <div className="px-4 py-4 border-b border-white/[0.06]">
            <h2 className="text-sm font-semibold text-white">Settings</h2>
            <p className="text-[11px] text-zinc-500 font-mono mt-0.5 truncate">{paneId}</p>
          </div>
          <div className="flex-1 py-2 px-2 space-y-0.5 overflow-y-auto">
            {sections.map(s => {
              const Icon = s.icon;
              const active = section === s.id;
              return (
                <button key={s.id} onClick={() => setSection(s.id)}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-left text-[13px] transition-all cursor-pointer ${
                    active ? 'bg-white/[0.08] text-white' : 'text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.03]'
                  }`}>
                  <Icon className="w-4 h-4 shrink-0" />
                  <span>{s.label}</span>
                </button>
              );
            })}
          </div>
          {/* Save */}
          <div className="p-3 border-t border-white/[0.06]">
            <button onClick={logout}
              className="w-full flex items-center justify-center gap-2 py-2 rounded-lg text-sm font-medium transition-all cursor-pointer text-zinc-500 hover:text-red-400 hover:bg-red-500/10">
              <LogOut className="w-4 h-4" /> Logout
            </button>
          </div>
        </nav>

        {/* Content */}
        <div className="flex-1 flex flex-col min-w-0">
          <div className="flex items-center justify-between px-6 py-4 border-b border-white/[0.06] shrink-0">
            <div>
              <h3 className="text-[15px] font-semibold text-white">{sections.find(s => s.id === section)?.label}</h3>
              <p className="text-[11px] text-zinc-600 mt-0.5">
                {section === 'general' && 'Basic pane configuration'}
                {section === 'agent' && 'AI agent behavior and model'}
                {section === 'config' && 'Proxy and connectivity'}
                {section === 'global' && 'Shared settings across all agents'}
                {section === 'telegram' && 'Notification integration'}
              </p>
            </div>
            <button onClick={onClose} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors cursor-pointer">
              <X className="w-4 h-4" />
            </button>
          </div>

          <div ref={bodyRef} className="flex-1 overflow-y-auto px-6 py-5">
            {section === 'general' && (
              <div className="space-y-5">
                <Field label="Title">
                  <Input value={data.title} onChange={v => set({ title: v })} placeholder="Pane title" />
                </Field>
                <Field label="Workspace" mono>
                  <Input value={data.workspace || ''} onChange={v => set({ workspace: v })} placeholder="/home/user/project" mono />
                </Field>
                <Toggle label="Auto-start" desc="Restore on server restart" checked={data.active !== false} onChange={v => set({ active: v })} />
                <Field label="Init Script" desc="sleep:N waits, key:X sends key">
                  <Textarea value={data.init_script || ''} onChange={v => set({ init_script: v })} rows={3} mono placeholder={"pwd\n# sleep:2\n# key:t"} />
                </Field>
              </div>
            )}

            {section === 'agent' && (
              <div className="space-y-5">
                <Field label="Agent Type">
                  <div className="flex flex-wrap gap-2">
                    {[
                      { value: '', label: 'None', icon: null },
                      { value: 'kiro-cli', label: 'Kiro CLI', icon: '/assets/logos/kiro.png' },
                      { value: 'openai', label: 'OpenAI', icon: '/assets/logos/openai.svg' },
                      { value: 'claude', label: 'Claude', icon: '/assets/logos/claude-symbol.svg' },
                      { value: 'gemini', label: 'Gemini', icon: '/assets/logos/gemini.svg' },
                      { value: 'opencode', label: 'OpenCode', icon: '/assets/logos/opencode.svg' },
                    ].map(option => (
                      <button
                        key={option.value}
                        onClick={() => set({ agent_type: option.value })}
                        className={`flex items-center gap-2 px-3 py-2 rounded-lg border transition-all cursor-pointer ${
                          data.agent_type === option.value
                            ? 'bg-blue-500/20 border-blue-500/40 text-blue-300'
                            : 'bg-white/[0.03] border-white/[0.08] text-zinc-400 hover:bg-white/[0.06] hover:border-white/[0.12]'
                        }`}
                      >
                        {option.icon ? (
                          <div className="w-5 h-5 bg-zinc-400 rounded flex items-center justify-center">
                            <img src={option.icon} alt={option.label} className="w-4 h-4" />
                          </div>
                        ) : (
                          <div className="w-4 h-4 rounded border border-white/[0.2]" />
                        )}
                        <span className="text-sm">{option.label}</span>
                      </button>
                    ))}
                  </div>
                </Field>
                <Field label="Role">
                  <Select value={data.role || ''} onChange={v => set({ role: v })}
                    options={[{value:'',label:'None'},{value:'master',label:'Master'},{value:'worker',label:'Worker'}]} />
                </Field>
                <Field label="Agent Duty">
                  <Textarea value={data.agent_duty || ''} onChange={v => set({ agent_duty: v })} rows={5} placeholder="Describe agent's role and responsibilities..." />
                </Field>
              </div>
            )}

            {section === 'config' && (
              <div className="space-y-5">
                <Field label="Config (JSON)">
                  <Textarea value={data.config || '{}'} onChange={v => set({ config: v })} rows={8} mono placeholder='{"proxy": {"enable": true, "url": "http://..."}}' />
                </Field>
                <pre className="text-[11px] text-zinc-600 bg-white/[0.02] border border-white/[0.06] rounded-lg p-3 font-mono overflow-x-auto">{`{
  "proxy": {
    "enable": true,
    "url": "http://w-20001:x@127.0.0.1:8003"
  }
}`}</pre>
              </div>
            )}

            {section === 'global' && (
              <div className="space-y-5">
                <Field label="Global Settings (JSON)" desc="Shared across all agents">
                  <Textarea value={globalJson} onChange={setGlobalJson} rows={16} mono placeholder="{}" />
                </Field>
              </div>
            )}

            {section === 'telegram' && (
              <div className="space-y-5">
                <Toggle label="Enable Telegram" desc="Send notifications via Telegram bot" checked={!!data.tg_enable} onChange={v => set({ tg_enable: v })} />
                {data.tg_enable && (<>
                  <Field label="Bot Token">
                    <Input value={data.tg_token || ''} onChange={v => set({ tg_token: v })} mono placeholder="1234567890:ABCdef..." />
                  </Field>
                  <Field label="Chat ID">
                    <Input value={data.tg_chat_id || ''} onChange={v => set({ tg_chat_id: v })} mono placeholder="-1001234567890" />
                  </Field>
                </>)}
              </div>
            )}
          </div>
          <div className="px-6 py-3 border-t border-white/[0.06] flex justify-end shrink-0">
            {section === 'global' ? (
              <button onClick={saveGlobal} disabled={globalSaving}
                className={`flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all cursor-pointer ${
                  globalSaved ? 'bg-emerald-500/20 text-emerald-400' : 'bg-white/[0.06] text-zinc-300 hover:bg-white/[0.1]'
                } disabled:opacity-50`}>
                {globalSaving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
                {globalSaving ? 'Saving...' : globalSaved ? 'Saved!' : 'Save'}
              </button>
            ) : (
              <button onClick={save} disabled={saving}
                className={`flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all cursor-pointer ${
                  saved ? 'bg-emerald-500/20 text-emerald-400' : 'bg-white/[0.06] text-zinc-300 hover:bg-white/[0.1]'
                } disabled:opacity-50`}>
                {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
                {saving ? 'Saving...' : saved ? 'Saved!' : 'Save'}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

/* ── Primitives ── */

function Field({ label, desc, mono, children }: { label: string; desc?: string; mono?: boolean; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-[13px] text-zinc-300 mb-1.5 font-medium">{label}</label>
      {children}
      {desc && <p className="text-[11px] text-zinc-600 mt-1">{desc}</p>}
    </div>
  );
}

function Input({ value, onChange, placeholder, mono }: { value: string; onChange: (v: string) => void; placeholder?: string; mono?: boolean }) {
  return (
    <input type="text" value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder}
      className={`w-full bg-white/[0.03] border border-white/[0.08] text-zinc-200 text-sm rounded-lg px-3 py-2 focus:outline-none focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20 placeholder:text-zinc-700 transition-all ${mono ? 'font-mono' : ''}`} />
  );
}

function Textarea({ value, onChange, rows = 3, placeholder, mono }: { value: string; onChange: (v: string) => void; rows?: number; placeholder?: string; mono?: boolean }) {
  return (
    <textarea value={value} onChange={e => onChange(e.target.value)} rows={rows} placeholder={placeholder}
      className={`w-full bg-white/[0.03] border border-white/[0.08] text-zinc-200 text-sm rounded-lg px-3 py-2 focus:outline-none focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20 placeholder:text-zinc-700 resize-none transition-all ${mono ? 'font-mono' : ''}`} />
  );
}

function Toggle({ label, desc, checked, onChange }: { label: string; desc?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center justify-between py-1">
      <div>
        <p className="text-[13px] text-zinc-300 font-medium">{label}</p>
        {desc && <p className="text-[11px] text-zinc-600 mt-0.5">{desc}</p>}
      </div>
      <button onClick={() => onChange(!checked)}
        className={`relative w-11 h-6 rounded-full transition-colors cursor-pointer ${checked ? 'bg-blue-600' : 'bg-white/[0.08]'}`}>
        <div className={`absolute top-1 w-4 h-4 bg-white rounded-full shadow-md transition-transform ${checked ? 'translate-x-[22px]' : 'translate-x-1'}`} />
      </button>
    </div>
  );
}
