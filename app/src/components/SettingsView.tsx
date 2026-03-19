import React, { useState, useEffect } from 'react';
import { EditPaneData } from './EditPaneDialog';
import { Loader2 } from 'lucide-react';
import { useApp } from '../contexts/AppContext';
import Select from './ui/Select';

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

const tabs = ['General', 'Agent', 'Network'] as const;
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
            <Select value={theme}
              onChange={handleThemeChange}
              options={themes.map(t => ({ value: t.value, label: t.label }))}
              placeholder="Select theme"
            />
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
            <Select value={pane.agent_type || ''}
              onChange={v => onChange({ ...pane, agent_type: v })}
              options={[{value:'',label:'无'},{value:'kiro-cli',label:'kiro-cli'},{value:'opencode',label:'opencode'},{value:'gemini',label:'gemini'},{value:'claude',label:'claude'},{value:'codex',label:'codex'},{value:'copilot',label:'copilot'}]}
              searchable
            />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Role</label>
            <Select value={pane.role || ''}
              onChange={v => onChange({ ...pane, role: v })}
              options={[{value:'',label:'无'},{value:'master',label:'master'},{value:'worker',label:'worker'}]}
            />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Default Model</label>
            <Select value={pane.default_model || ''}
              onChange={v => onChange({ ...pane, default_model: v })}
              options={[{value:'',label:'无'},{value:'claude-opus-4.6',label:'opus-4.6'},{value:'claude-opus-4.5',label:'opus-4.5'},{value:'claude-sonnet-4.5',label:'sonnet-4.5'},{value:'claude-sonnet-4',label:'sonnet-4'},{value:'claude-haiku-4.5',label:'haiku-4.5'},{value:'deepseek-3.2',label:'deepseek-3.2'},{value:'minimax-m2.1',label:'minimax-m2.1'},{value:'qwen3-coder-next',label:'qwen3-coder'}]}
              searchable
            />
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

        {tab === 'Network' && (<>
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
    "url": "http://w-20001:x@127.0.0.1:8003"
  }
}`}</pre>
            </div>
          </div>
        </>)}
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
