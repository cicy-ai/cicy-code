import React, { useMemo, useState, useEffect } from 'react';
import { EditPaneData } from './EditPaneDialog';
import { Loader2 } from 'lucide-react';
import { useApp } from '../contexts/AppContext';
import Select from './ui/Select';

const THEME_KEY = 'app_theme';
const themes = [
  { value: '', label: '默认（VS Code 深色）' },
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

const tabs = ['常规', '智能体', '网络'] as const;
type Tab = typeof tabs[number];

export const SettingsView: React.FC<SettingsViewProps> = ({ pane, onChange, onSave, isSaving = false }) => {
  const { agentTypeOptions } = useApp();
  const [tab, setTab] = useState<Tab>('常规');
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_KEY) || '');
  const selectAgentTypeOptions = useMemo(() => {
    const options = agentTypeOptions.map((option) => ({ value: option.value, label: option.label, sub: option.description }));
    const currentValue = String(pane.agent_type || '').trim();
    if (currentValue && !options.some((option) => option.value === currentValue)) {
      options.unshift({ value: currentValue, label: currentValue, sub: '当前值（不在可选列表中）' });
    }
    return options;
  }, [agentTypeOptions, pane.agent_type]);

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
        {tab === '常规' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">主题</label>
            <Select value={theme}
              onChange={handleThemeChange}
              options={themes.map(t => ({ value: t.value, label: t.label }))}
              placeholder="选择主题"
            />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">标题</label>
            <input type="text" value={pane.title}
              onChange={e => onChange({ ...pane, title: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder="输入窗格标题" />
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">自动启动</p>
              <p className="text-xs text-vsc-text-muted">服务重启后自动恢复</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.active !== false ? 'bg-green-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({ ...pane, active: pane.active === false ? true : false })}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.active !== false ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">工作目录</label>
            <input type="text" value={pane.workspace || ''}
              onChange={e => onChange({ ...pane, workspace: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder="/home/user/project" />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">初始化脚本</label>
            <textarea value={pane.init_script || ''}
              onChange={e => onChange({ ...pane, init_script: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={4} placeholder={"pwd\n# sleep:2\n# key:t"} />
            <p className="text-xs text-vsc-text-muted mt-1">sleep:N 表示等待 N 秒，key:X 表示发送按键</p>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">智能体类型</label>
            <Select value={pane.agent_type || ''}
              onChange={v => onChange({ ...pane, agent_type: v })}
              options={selectAgentTypeOptions}
              searchable
            />
          </div>
          {/* <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">Role</label>
            <Select value={pane.role || ''}
              onChange={v => onChange({ ...pane, role: v })}
              options={[{value:'',label:'无'},{value:'master',label:'master'},{value:'worker',label:'worker'}]}
            />
          </div> */}
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">启动时允许所有操作</p>
              <p className="text-xs text-vsc-text-muted">Codex/Claude 追加危险参数</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.allow_all_actions ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({ ...pane, allow_all_actions: !pane.allow_all_actions })}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.allow_all_actions ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">使用官方认证</p>
              <p className="text-xs text-vsc-text-muted">开启后不注入本地 gateway auth</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.use_official_auth ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({ ...pane, use_official_auth: !pane.use_official_auth })}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.use_official_auth ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">启用代理</p>
              <p className="text-xs text-vsc-text-muted">启动前执行 cicy_proxy_on，并检查 mihome 规则</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.use_proxy ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({ ...pane, use_proxy: !pane.use_proxy })}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.use_proxy ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          {pane.use_proxy && (
            <>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">代理密码</label>
                <input type="text" value={pane.proxy?.password || ''}
                  onChange={e => onChange({ ...pane, proxy: { ...(pane.proxy || {}), password: e.target.value } })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                  placeholder="留空时优先 api_token，不存在时回退 user==pass" />
              </div>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">mihome 规则</label>
                <input type="text" value={pane.proxy?.rule || ''}
                  onChange={e => onChange({ ...pane, proxy: { ...(pane.proxy || {}), rule: e.target.value } })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                  placeholder="IN-USER,w-10001,proxy-a" />
              </div>
            </>
          )}
          <div style={{"display":"none"}}>
            <label className="block text-xs text-vsc-text-secondary mb-1">智能体职责</label>
            <textarea value={pane.agent_duty || ''}
              onChange={e => onChange({ ...pane, agent_duty: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              style={{paddingRight: '44px'}}
              rows={6} placeholder="描述智能体的角色与职责..." />
          </div>
        </>)}

        {tab === '网络' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">配置（JSON）</label>
            <textarea value={pane.config || '{}'}
              onChange={e => onChange({ ...pane, config: e.target.value })}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={12} placeholder='{"projects": ["/home/user/project"]}' />
            <div className="text-xs text-vsc-text-muted mt-2 space-y-1">
              <p className="font-medium text-vsc-text-secondary">示例：</p>
              <pre className="bg-vsc-bg-secondary border border-vsc-border rounded p-2 overflow-x-auto">{`{
  "projects": [
    "/home/user/project-a",
    "/home/user/project-b"
  ]
}`}</pre>
            </div>
          </div>
        </>)}
      </div>

      <div className="p-4 border-t border-vsc-border flex-shrink-0">
        <button onClick={onSave} disabled={isSaving}
          className="w-full bg-vsc-button hover:bg-vsc-button-hover disabled:bg-vsc-border disabled:cursor-not-allowed text-white text-sm font-medium py-2 rounded transition-colors flex items-center justify-center gap-2">
          {isSaving && <Loader2 size={16} className="animate-spin" />}
          {isSaving ? '保存中...' : '保存更改'}
        </button>
      </div>
    </div>
  );
};
