import React, { useState } from 'react';
import { X, RefreshCw } from 'lucide-react';

export interface EditPaneData {
  target: string;
  title: string;
  agent_duty?: string;
  agent_type?: string;
  workspace?: string;
  active?: boolean;
  init_script?: string;
  proxy?: string;
  tg_enable?: boolean;
  tg_token?: string;
  tg_chat_id?: string;
  url?: string;
  private_mode?: boolean;
  allowed_users?: string;
  proxy_enable?: boolean;
  config?: string;
  ttyd_preview?: string;
  role?: string;
  default_model?: string;
  trust_level?: string;
}

interface EditPaneDialogProps {
  open: boolean;
  pane: EditPaneData | null;
  mode?: 'simple' | 'full';
  onChange: (pane: EditPaneData) => void;
  onClose: () => void;
  onSave: () => void;
  onRestart?: () => void;
  onDelete?: () => void;
}

const tabs = ['General', 'Agent', 'Network', 'Access'] as const;
type Tab = typeof tabs[number];

export const EditPaneDialog: React.FC<EditPaneDialogProps> = ({
  open, pane, mode = 'simple', onChange, onClose, onSave, onRestart, onDelete,
}) => {
  const [tab, setTab] = useState<Tab>('General');
  if (!open || !pane) return null;
  const isFull = mode === 'full';

  return (
    <div className="fixed inset-0 bg-vsc-bg flex flex-col" style={{zIndex:999999999}} onClick={onClose}>
      <div className="bg-vsc-bg w-full h-full flex flex-col" onClick={e => e.stopPropagation()}>
        {/* 头部 */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-vsc-border flex-shrink-0">
          <div>
            <h3 className="text-sm font-semibold text-white">Edit Pane</h3>
            <p className="text-xs text-vsc-text-muted font-mono">{pane.target}</p>
          </div>
          <button onClick={onClose} className="p-1 rounded text-vsc-text-secondary hover:text-vsc-text"><X size={16} /></button>
        </div>

        {/* Tabs */}
        {isFull && (
          <div className="flex border-b border-vsc-border px-4 flex-shrink-0">
            {tabs.map(t => (
              <button key={t} onClick={() => setTab(t)}
                className={`px-3 py-2 text-xs font-medium border-b-2 transition-colors ${tab === t ? 'border-vsc-accent text-vsc-link' : 'border-transparent text-vsc-text-muted hover:text-vsc-text'}`}
              >{t}</button>
            ))}
          </div>
        )}

        {/* 内容 */}
        <div className="p-4 space-y-3 overflow-y-auto flex-1">
          {/* General - 始终显示 */}
          {(!isFull || tab === 'General') && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">Title</label>
              <input type="text" value={pane.title}
                onChange={e => onChange({ ...pane, title: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                placeholder="Enter pane title" autoFocus={!isFull} />
            </div>
            {isFull && (<>
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
            </>)}
          </>)}

          {/* Agent */}
          {isFull && tab === 'Agent' && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">Agent Duty</label>
              <textarea value={pane.agent_duty || ''}
                onChange={e => onChange({ ...pane, agent_duty: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
                rows={6} placeholder="Describe the agent's duty for this pane" />
            </div>
          </>)}

          {/* Network */}
          {isFull && tab === 'Network' && (<>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-vsc-text">HTTP Proxy</p>
                <p className="text-xs text-vsc-text-muted">Enable and set proxy URL</p>
              </div>
              <div className={`relative w-8 h-4 rounded-full cursor-pointer transition-colors ${pane.proxy_enable ? 'bg-cyan-600' : 'bg-vsc-bg-active'}`}
                onClick={() => onChange({ ...pane, proxy_enable: !pane.proxy_enable })}>
                <div className={`absolute top-0.5 w-3 h-3 bg-white rounded-full shadow transition-transform ${pane.proxy_enable ? 'translate-x-4' : 'translate-x-0.5'}`} />
              </div>
            </div>
            <div className={pane.proxy_enable ? '' : 'opacity-40 pointer-events-none'}>
              <input type="text" value={pane.proxy || ''}
                onChange={e => onChange({ ...pane, proxy: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                placeholder="http://proxy:8080" />
            </div>
            <div className="pt-3 border-t border-vsc-border-subtle">
              <div className="flex items-center justify-between mb-2">
                <span className="text-xs text-vsc-text-secondary">Telegram Notifications</span>
                <div className={`relative w-8 h-4 rounded-full cursor-pointer transition-colors ${pane.tg_enable ? 'bg-purple-600' : 'bg-vsc-bg-active'}`}
                  onClick={() => onChange({ ...pane, tg_enable: !pane.tg_enable })}>
                  <div className={`absolute top-0.5 w-3 h-3 bg-white rounded-full shadow transition-transform ${pane.tg_enable ? 'translate-x-4' : 'translate-x-0.5'}`} />
                </div>
              </div>
              <div className={`space-y-2 ${pane.tg_enable ? '' : 'opacity-40 pointer-events-none'}`}>
                <input type="text" value={pane.tg_token || ''}
                  onChange={e => onChange({ ...pane, tg_token: e.target.value })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                  placeholder="Bot Token" />
                <input type="text" value={pane.tg_chat_id || ''}
                  onChange={e => onChange({ ...pane, tg_chat_id: e.target.value })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                  placeholder="Chat ID" />
              </div>
            </div>
          </>)}

          {/* Access */}
          {isFull && tab === 'Access' && (<>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-vsc-text">Private Mode</p>
                <p className="text-xs text-vsc-text-muted">Restrict access to allowed users</p>
              </div>
              <div className={`relative w-8 h-4 rounded-full cursor-pointer transition-colors ${pane.private_mode ? 'bg-yellow-600' : 'bg-vsc-bg-active'}`}
                onClick={() => onChange({ ...pane, private_mode: !pane.private_mode })}>
                <div className={`absolute top-0.5 w-3 h-3 bg-white rounded-full shadow transition-transform ${pane.private_mode ? 'translate-x-4' : 'translate-x-0.5'}`} />
              </div>
            </div>
            <div className={pane.private_mode ? '' : 'opacity-40 pointer-events-none'}>
              <label className="block text-xs text-vsc-text-secondary mb-1">Allowed Users</label>
              <input type="text" value={pane.allowed_users || ''}
                onChange={e => onChange({ ...pane, allowed_users: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                placeholder="user1, user2" />
            </div>
          </>)}
        </div>

        {/* 底部按钮 */}
        <div className="flex gap-2 px-4 py-3 border-t border-vsc-border flex-shrink-0">
          {onRestart && (
            <button onClick={() => { if (confirm(`Restart ${pane.target}?`)) onRestart(); }}
              className="px-3 py-2 bg-orange-600 text-white rounded text-sm hover:bg-orange-500 transition-colors flex items-center gap-1">
              <RefreshCw size={14} /> Restart
            </button>
          )}
          {onDelete && (
            <button onClick={() => { if (confirm(`Delete ${pane.target}?`)) { onDelete(); onClose(); } }}
              className="px-3 py-2 bg-red-600 text-white rounded text-sm hover:bg-red-500 transition-colors flex items-center gap-1">
              <X size={14} /> Delete
            </button>
          )}
          <div className="flex-1" />
          <button onClick={onClose} className="px-4 py-2 bg-vsc-bg-secondary text-vsc-text rounded text-sm hover:bg-vsc-bg-active transition-colors">Cancel</button>
          <button onClick={onSave} className="px-4 py-2 bg-vsc-button text-white rounded text-sm hover:bg-vsc-button-hover transition-colors">Save</button>
        </div>
      </div>
    </div>
  );
};
