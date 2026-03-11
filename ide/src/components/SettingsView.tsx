import React, { useState, useEffect } from 'react';
import { EditPaneData } from './EditPaneDialog';
import { Loader2 } from 'lucide-react';
import { useApp } from '../contexts/AppContext';

const THEME_KEY = 'app_theme';
const themes = [
  { value: '', label: 'Default (VS Code Dark)' },
  { value: 'livestream', label: 'Livestream' },
] as const;

function applyTheme(theme: string) {
  document.documentElement.setAttribute('data-theme', theme || '');
  localStorage.setItem(THEME_KEY, theme);
}

// 页面加载时恢复主题
const savedTheme = localStorage.getItem(THEME_KEY) || '';
if (savedTheme) document.documentElement.setAttribute('data-theme', savedTheme);

interface SettingsViewProps {
  pane: EditPaneData;
  onChange: (pane: EditPaneData) => void;
  onSave: () => void;
  isSaving?: boolean;
}

const tabs = ['General', 'Config', 'Telegram'] as const;
type Tab = typeof tabs[number];

export const SettingsView: React.FC<SettingsViewProps> = ({ pane, onChange, onSave, isSaving = false }) => {
  const [tab, setTab] = useState<Tab>('General');
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_KEY) || '');

  const handleThemeChange = (value: string) => {
    setTheme(value);
    applyTheme(value);
  };

  return (
    <div className="flex flex-col h-full bg-vsc-bg">
      <div className="flex border-b border-vsc-border px-4 flex-shrink-0">
        {tabs.map(t => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs font-medium border-b-2 transition-colors ${tab === t ? 'border-vsc-accent text-vsc-link' : 'border-transparent text-vsc-text-muted hover:text-vsc-text'}`}
          >{t}</button>
        ))}
      </div>

      <div className="p-4 space-y-3 overflow-y-auto flex-1">
        {tab === 'General' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Theme</label>
            <select value={theme}
              onChange={e => handleThemeChange(e.target.value)}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent">
              {themes.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Title</label>
            <input type="text" value={pane.title}
              onChange={e => onChange({ ...pane, title: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder="Enter pane title" />
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">Auto-start</p>
              <p className="text-xs text-vsc-text-muted">Auto restore on server restart</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.active !== false ? 'bg-green-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({ ...pane, active: pane.active === false ? true : false })}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.active !== false ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Workspace</label>
            <input type="text" value={pane.workspace || ''}
              onChange={e => onChange({ ...pane, workspace: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder="/home/user/project" />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Init Script</label>
            <textarea value={pane.init_script || ''}
              onChange={e => onChange({ ...pane, init_script: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={4} placeholder={"pwd\n# sleep:2\n# key:t"} />
            <p className="text-xs text-vsc-text-muted mt-1">sleep:N waits Ns, key:X sends key</p>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Agent Type</label>
            <select value={pane.agent_type || ''}
              onChange={e => onChange({ ...pane, agent_type: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent">
              <option value="">无</option>
              <option value="kiro-cli">kiro-cli</option>
              <option value="opencode">opencode</option>
              <option value="gemini">gemini</option>
              <option value="claude">claude</option>
              <option value="codex">codex</option>
              <option value="copilot">copilot</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Role</label>
            <select value={pane.role || ''}
              onChange={e => onChange({ ...pane, role: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent">
              <option value="">无</option>
              <option value="master">master</option>
              <option value="worker">worker</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Default Model</label>
            <select value={pane.default_model || ''}
              onChange={e => onChange({ ...pane, default_model: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent">
              <option value="">无</option>
              <option value="claude-opus-4.6">opus-4.6</option>
              <option value="claude-opus-4.5">opus-4.5</option>
              <option value="claude-sonnet-4.5">sonnet-4.5</option>
              <option value="claude-sonnet-4">sonnet-4</option>
              <option value="claude-haiku-4.5">haiku-4.5</option>
              <option value="deepseek-3.2">deepseek-3.2</option>
              <option value="minimax-m2.1">minimax-m2.1</option>
              <option value="qwen3-coder-next">qwen3-coder</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Trust Level</label>
            <select value={pane.trust_level || ''}
              onChange={e => onChange({ ...pane, trust_level: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent">
              <option value="">无</option>
              <option value="trust-all">trust-all</option>
              <option value="ask">ask</option>
              <option value="deny">deny</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Agent Duty</label>
            <textarea value={pane.agent_duty || ''}
              onChange={e => onChange({ ...pane, agent_duty: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              style={{paddingRight: '44px'}}
              rows={6} placeholder="Describe agent's role and responsibilities..." />
          </div>
        </>)}

        {tab === 'Config' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Config (JSON)</label>
            <textarea value={pane.config || '{}'}
              onChange={e => onChange({ ...pane, config: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={12} placeholder='{"proxy": {"enable": true}}' />
            <div className="text-xs text-vsc-text-muted mt-2 space-y-1">
              <p className="font-medium text-vsc-text-secondary">Example:</p>
              <pre className="bg-vsc-bg-secondary border border-vsc-border rounded p-2 overflow-x-auto">{`{
  "proxy": {
    "enable": true,
    "url": "https://proxy.example.com"
  }
}`}</pre>
            </div>
          </div>
        </>)}

        {tab === 'Telegram' && (
          <div className="space-y-3">
            <div>
              <label className="block text-xs font-medium text-vsc-text mb-1">Enable Telegram</label>
              <input type="checkbox" checked={pane.tg_enable || false}
                onChange={(e) => onChange({ ...pane, tg_enable: e.target.checked })}
                className="w-4 h-4" />
            </div>
            <div>
              <label className="block text-xs font-medium text-vsc-text mb-1">Bot Token</label>
              <input type="text" value={pane.tg_token || ''} 
                onChange={(e) => onChange({ ...pane, tg_token: e.target.value })}
                className="w-full bg-vsc-bg-secondary text-vsc-text text-xs px-2 py-1.5 rounded border border-vsc-border focus:border-vsc-accent focus:outline-none" 
                placeholder="1234567890:ABCdefGHIjklMNOpqrsTUVwxyz" />
            </div>
            <div>
              <label className="block text-xs font-medium text-vsc-text mb-1">Chat ID</label>
              <input type="text" value={pane.tg_chat_id || ''} 
                onChange={(e) => onChange({ ...pane, tg_chat_id: e.target.value })}
                className="w-full bg-vsc-bg-secondary text-vsc-text text-xs px-2 py-1.5 rounded border border-vsc-border focus:border-vsc-accent focus:outline-none" 
                placeholder="-1001234567890" />
            </div>
          </div>
        )}
      </div>

      <div className="p-4 border-t border-vsc-border flex-shrink-0">
        <button onClick={onSave} disabled={isSaving}
          className="w-full bg-vsc-button hover:bg-vsc-button-hover disabled:bg-vsc-border disabled:cursor-not-allowed text-white text-sm font-medium py-2 rounded transition-colors flex items-center justify-center gap-2">
          {isSaving && <Loader2 size={16} className="animate-spin" />}
          {isSaving ? 'Saving...' : 'Save Changes'}
        </button>
      </div>
    </div>
  );
};
