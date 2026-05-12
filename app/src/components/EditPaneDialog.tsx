import React, {useState} from 'react';
import {useTranslation} from 'react-i18next';
import {X, RefreshCw} from 'lucide-react';
import {useDialog} from '../contexts/DialogContext';

export interface EditPaneData {
  target: string;
  title: string;
  agent_duty?: string;
  agent_type?: string;
  allow_all_actions?: boolean;
  use_official_auth?: boolean;
  use_proxy?: boolean;
  proxy?: {
    password?: string;
    rule?: string;
  } | null;
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
  runtime_ai?: {
    provider_name?: string;
    provider_protocol?: string;
    model?: string;
  } | null;
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

const TABS = [
  {id: 'general', labelKey: 'tabGeneral'},
  {id: 'agent', labelKey: 'tabAgent'},
  {id: 'network', labelKey: 'tabNetwork'},
] as const;
type TabId = (typeof TABS)[number]['id'];

export const EditPaneDialog: React.FC<EditPaneDialogProps> = ({
  open, pane, mode = 'simple', onChange, onClose, onSave, onRestart, onDelete,
}) => {
  const {t} = useTranslation('editPane');
  const {t: ts} = useTranslation('settings');
  const {t: tc} = useTranslation('common');
  const [tab, setTab] = useState<TabId>('general');
  const {confirm} = useDialog();
  if (!open || !pane) return null;
  const isFull = mode === 'full';

  return (
    <div className="fixed inset-0 flex cursor-pointer flex-col bg-vsc-bg" style={{zIndex: 999999999}} onClick={onClose}>
      <div className="flex h-full w-full cursor-default flex-col bg-vsc-bg" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-vsc-border flex-shrink-0">
          <div>
            <h3 className="text-sm font-semibold text-white">{t('title')}</h3>
            <p className="text-xs text-vsc-text-muted font-mono">{pane.target}</p>
          </div>
          <button onClick={onClose} className="p-1 rounded text-vsc-text-secondary hover:text-vsc-text"><X size={16} /></button>
        </div>

        {isFull && (
          <div className="flex border-b border-vsc-border px-4 flex-shrink-0">
            {TABS.map(entry => (
              <button key={entry.id} onClick={() => setTab(entry.id)}
                className={`px-3 py-2 text-xs font-medium border-b-2 transition-colors ${tab === entry.id ? 'border-vsc-accent text-vsc-link' : 'border-transparent text-vsc-text-muted hover:text-vsc-text'}`}
              >{ts(entry.labelKey)}</button>
            ))}
          </div>
        )}

        <div className="p-4 space-y-3 overflow-y-auto flex-1">
          {(!isFull || tab === 'general') && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">{ts('titleLabel')}</label>
              <input type="text" value={pane.title}
                onChange={e => onChange({...pane, title: e.target.value})}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                placeholder={ts('titlePlaceholder')} autoFocus={!isFull} />
            </div>
            {isFull && (<>
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm text-vsc-text">{ts('autoStartTitle')}</p>
                  <p className="text-xs text-vsc-text-muted">{ts('autoStartHint')}</p>
                </div>
                <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.active !== false ? 'bg-green-600' : 'bg-vsc-bg-active'}`}
                  onClick={() => onChange({...pane, active: pane.active === false ? true : false})}>
                  <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.active !== false ? 'translate-x-5' : 'translate-x-0.5'}`} />
                </div>
              </div>
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm text-vsc-text">{ts('officialAuthTitle')}</p>
                  <p className="text-xs text-vsc-text-muted">{ts('officialAuthHint')}</p>
                </div>
                <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.use_official_auth ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
                  onClick={() => onChange({...pane, use_official_auth: !pane.use_official_auth})}>
                  <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.use_official_auth ? 'translate-x-5' : 'translate-x-0.5'}`} />
                </div>
              </div>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">{ts('workspaceLabel')}</label>
                <input type="text" value={pane.workspace || ''}
                  onChange={e => onChange({...pane, workspace: e.target.value})}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                  placeholder="/home/user/project" />
              </div>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">{ts('initScriptLabel')}</label>
                <textarea value={pane.init_script || ''}
                  onChange={e => onChange({...pane, init_script: e.target.value})}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
                  rows={4} placeholder={'pwd\n# sleep:2\n# key:t'} />
                <p className="text-xs text-vsc-text-muted mt-1">{t('initScriptHintShort')}</p>
              </div>
            </>)}
          </>)}

          {isFull && tab === 'agent' && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">{t('agentDutyLabel')}</label>
              <textarea value={pane.agent_duty || ''}
                onChange={e => onChange({...pane, agent_duty: e.target.value})}
                className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
                rows={6} placeholder={t('agentDutyPlaceholder')} />
            </div>
          </>)}

          {isFull && tab === 'network' && (<>
            <div>
              <label className="block text-xs text-vsc-text-secondary mb-1">{ts('networkConfigLabel')}</label>
              <textarea value={pane.config || '{}'}
                onChange={e => onChange({...pane, config: e.target.value})}
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
        </div>

        <div className="flex gap-2 px-4 py-3 border-t border-vsc-border flex-shrink-0">
          {onRestart && (
            <button onClick={() => confirm(t('confirmRestart', {target: pane.target}), () => onRestart())}
              className="px-3 py-2 bg-orange-600 text-white rounded text-sm hover:bg-orange-500 transition-colors flex items-center gap-1">
              <RefreshCw size={14} /> {t('restart')}
            </button>
          )}
          {onDelete && (
            <button onClick={() => confirm(t('confirmDelete', {target: pane.target}), () => { onDelete(); onClose(); })}
              className="px-3 py-2 bg-red-600 text-white rounded text-sm hover:bg-red-500 transition-colors flex items-center gap-1">
              <X size={14} /> {t('delete')}
            </button>
          )}
          <div className="flex-1" />
          <button onClick={onClose} className="px-4 py-2 bg-vsc-bg-secondary text-vsc-text rounded text-sm hover:bg-vsc-bg-active transition-colors">{tc('cancel')}</button>
          <button onClick={onSave} className="px-4 py-2 bg-vsc-button text-white rounded text-sm hover:bg-vsc-button-hover transition-colors">{tc('save')}</button>
        </div>
      </div>
    </div>
  );
};
