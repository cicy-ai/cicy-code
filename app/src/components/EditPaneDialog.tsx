import React, { useState } from 'react';
import { X, RefreshCw } from 'lucide-react';
import { useDialog } from '../contexts/DialogContext';

export interface EditPaneData {
  target: string;
  title: string;
  agent_duty?: string;
  agent_type?: string;
  allow_all_actions?: boolean;
  workspace?: string;
  active?: boolean;
  init_script?: string;
  tg_enable?: boolean;
  tg_token?: string;
  tg_chat_id?: string;
  url?: string;
  config?: string;
  ttyd_preview?: string;
  role?: string;
  default_model?: string;
  runtime_ai_provider_name?: string;
  runtime_ai_provider_protocol?: string;
  runtime_ai_model?: string;
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

const tabs = ['常规', '智能体', '网络'] as const;
type Tab = typeof tabs[number];

export const EditPaneDialog: React.FC<EditPaneDialogProps> = ({
  open, pane, mode = 'simple', onChange, onClose, onSave, onRestart, onDelete,
}) => {
  const [tab, setTab] = useState<Tab>('常规');
  const { confirm } = useDialog();
  if (!open || !pane) return null;
  const isFull = mode === 'full';

  return (
    <div className="fixed inset-0 flex cursor-pointer flex-col bg-vsc-bg" style={{zIndex:999999999}} onClick={onClose}>
      <div className="flex h-full w-full cursor-default flex-col bg-vsc-bg" onClick={e => e.stopPropagation()}>
        {/* 头部 */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-vsc-border flex-shrink-0">
          <div>
            <h3 className="text-sm font-semibold text-white">编辑窗格</h3>
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
          {(!isFull || tab === '常规') && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">标题</label>
              <input type="text" value={pane.title}
                onChange={e => onChange({ ...pane, title: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                placeholder="输入窗格标题" autoFocus={!isFull} />
            </div>
            {isFull && (<>
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
                <p className="text-xs text-vsc-text-muted mt-1">sleep:N 等待 N 秒，key:X 发送按键</p>
              </div>
            </>)}
          </>)}

          {/* Agent */}
          {isFull && tab === '智能体' && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">智能体职责</label>
              <textarea value={pane.agent_duty || ''}
                onChange={e => onChange({ ...pane, agent_duty: e.target.value })}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
                rows={6} placeholder="描述这个窗格的智能体职责" />
            </div>
          </>)}

          {/* Network */}
          {isFull && tab === '网络' && (<>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">配置（JSON）</label>
                <textarea value={pane.config || '{}'}
                  onChange={e => onChange({ ...pane, config: e.target.value })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
                  rows={8} placeholder='{"projects": ["/home/user/project"]}' />
                <pre className="text-xs text-vsc-text-muted bg-vsc-bg-secondary border border-vsc-border rounded p-2 mt-2 overflow-x-auto">{`{
  "projects": [
    "/home/user/project-a",
    "/home/user/project-b"
  ]
}`}</pre>
              </div>
            </>)}

          {/* Telegram - hidden from tabs */}
          {isFull && tab === 'Telegram' as any && (<>
              <div className="flex items-center justify-between">
                <span className="text-xs text-vsc-text-secondary">Telegram 通知</span>
                <div className={`relative w-8 h-4 rounded-full cursor-pointer transition-colors ${pane.tg_enable ? 'bg-purple-600' : 'bg-vsc-bg-active'}`}
                  onClick={() => onChange({ ...pane, tg_enable: !pane.tg_enable })}>
                  <div className={`absolute top-0.5 w-3 h-3 bg-white rounded-full shadow transition-transform ${pane.tg_enable ? 'translate-x-4' : 'translate-x-0.5'}`} />
                </div>
              </div>
              <div className={`space-y-2 ${pane.tg_enable ? '' : 'opacity-40 pointer-events-none'}`}>
                <input type="text" value={pane.tg_token || ''}
                  onChange={e => onChange({ ...pane, tg_token: e.target.value })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                  placeholder="机器人令牌" />
                <input type="text" value={pane.tg_chat_id || ''}
                  onChange={e => onChange({ ...pane, tg_chat_id: e.target.value })}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2 py-1 focus:outline-none focus:border-vsc-accent"
                  placeholder="聊天 ID" />
              </div>
            </>)}
        </div>

        {/* 底部按钮 */}
        <div className="flex gap-2 px-4 py-3 border-t border-vsc-border flex-shrink-0">
          {onRestart && (
            <button onClick={() => confirm(`确认重启 ${pane.target}？`, () => onRestart())}
              className="px-3 py-2 bg-orange-600 text-white rounded text-sm hover:bg-orange-500 transition-colors flex items-center gap-1">
              <RefreshCw size={14} /> 重启
            </button>
          )}
          {onDelete && (
            <button onClick={() => confirm(`确认删除 ${pane.target}？`, () => { onDelete(); onClose(); })}
              className="px-3 py-2 bg-red-600 text-white rounded text-sm hover:bg-red-500 transition-colors flex items-center gap-1">
              <X size={14} /> 删除
            </button>
          )}
          <div className="flex-1" />
          <button onClick={onClose} className="px-4 py-2 bg-vsc-bg-secondary text-vsc-text rounded text-sm hover:bg-vsc-bg-active transition-colors">取消</button>
          <button onClick={onSave} className="px-4 py-2 bg-vsc-button text-white rounded text-sm hover:bg-vsc-button-hover transition-colors">保存</button>
        </div>
      </div>
    </div>
  );
};
