import React, {useMemo, useState} from 'react';
import {useTranslation} from 'react-i18next';
import {EditPaneData} from './EditPaneDialog';
import {Loader2} from 'lucide-react';
import {useApp} from '../contexts/AppContext';
import Select from './ui/Select';
import i18n, {SUPPORTED_LNGS} from '../i18n';

const THEME_KEY = 'app_theme';
const THEME_VALUES = [
  {value: '', labelKey: 'themeDefault'},
  {value: 'livestream', labelKey: 'themeLivestream'},
] as const;

function applyTheme(theme: string) {
  document.documentElement.setAttribute('data-theme', theme || '');
  localStorage.setItem(THEME_KEY, theme);
}

// Restore the saved theme as early as the module is evaluated.
const savedTheme = localStorage.getItem(THEME_KEY) || '';
if (savedTheme) document.documentElement.setAttribute('data-theme', savedTheme);

interface SettingsViewProps {
  pane: EditPaneData;
  onChange: (pane: EditPaneData) => void;
  onSave: () => void;
  isSaving?: boolean;
}

const TABS = [
  {id: 'general', labelKey: 'tabGeneral'},
  {id: 'agent', labelKey: 'tabAgent'},
  {id: 'network', labelKey: 'tabNetwork'},
] as const;
type TabId = (typeof TABS)[number]['id'];

export const SettingsView: React.FC<SettingsViewProps> = ({pane, onChange, onSave, isSaving = false}) => {
  const {t} = useTranslation('settings');
  const {t: tc} = useTranslation('common');
  const {agentTypeOptions} = useApp();
  const [tab, setTab] = useState<TabId>('general');
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_KEY) || '');
  const [lang, setLang] = useState<string>(() => i18n.resolvedLanguage ?? i18n.language ?? 'en');

  const themeOptions = useMemo(
    () => THEME_VALUES.map((o) => ({value: o.value, label: t(o.labelKey)})),
    [t],
  );

  const languageOptions = useMemo(
    () =>
      SUPPORTED_LNGS.map((code) => ({
        value: code,
        label: code === 'zh-CN' ? tc('languageChinese') : tc('languageEnglish'),
      })),
    [tc],
  );

  const selectAgentTypeOptions = useMemo(() => {
    const options = agentTypeOptions.map((option) => ({value: option.value, label: option.label, sub: option.description}));
    const currentValue = String(pane.agent_type || '').trim();
    if (currentValue && !options.some((option) => option.value === currentValue)) {
      options.unshift({value: currentValue, label: currentValue, sub: t('agentTypeCurrentNotInList')});
    }
    return options;
  }, [agentTypeOptions, pane.agent_type, t]);

  const handleThemeChange = (value: string) => {
    setTheme(value);
    applyTheme(value);
  };

  const handleLanguageChange = (value: string) => {
    setLang(value);
    void i18n.changeLanguage(value);
  };

  return (
    <div className="flex flex-col h-full bg-vsc-bg">
      <div className="flex border-b border-vsc-border px-4 flex-shrink-0">
        {TABS.map((entry) => (
          <button
            key={entry.id}
            onClick={() => setTab(entry.id)}
            className={`px-3 py-2 text-xs font-medium border-b-2 transition-colors ${tab === entry.id ? 'border-vsc-accent text-vsc-link' : 'border-transparent text-vsc-text-muted hover:text-vsc-text'}`}
          >
            {t(entry.labelKey)}
          </button>
        ))}
      </div>

      <div className="p-4 space-y-3 overflow-y-auto flex-1">
        {tab === 'general' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{tc('language')}</label>
            <Select
              value={lang}
              onChange={handleLanguageChange}
              options={languageOptions}
            />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('themeLabel')}</label>
            <Select value={theme}
              onChange={handleThemeChange}
              options={themeOptions}
              placeholder={t('themePlaceholder')}
            />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('titleLabel')}</label>
            <input type="text" value={pane.title}
              onChange={e => onChange({...pane, title: e.target.value})}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder={t('titlePlaceholder')} />
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">{t('autoStartTitle')}</p>
              <p className="text-xs text-vsc-text-muted">{t('autoStartHint')}</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.active !== false ? 'bg-green-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({...pane, active: pane.active === false ? true : false})}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.active !== false ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('workspaceLabel')}</label>
            <input type="text" value={pane.workspace || ''}
              onChange={e => onChange({...pane, workspace: e.target.value})}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
              placeholder="/home/user/project" />
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('initScriptLabel')}</label>
            <textarea value={pane.init_script || ''}
              onChange={e => onChange({...pane, init_script: e.target.value})}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={4} placeholder={'pwd\n# sleep:2\n# key:t'} />
            <p className="text-xs text-vsc-text-muted mt-1">{t('initScriptHint')}</p>
          </div>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('agentTypeLabel')}</label>
            <Select value={pane.agent_type || ''}
              onChange={v => onChange({...pane, agent_type: v})}
              options={selectAgentTypeOptions}
              searchable
            />
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">{t('allowAllActionsTitle')}</p>
              <p className="text-xs text-vsc-text-muted">{t('allowAllActionsHint')}</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.allow_all_actions ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({...pane, allow_all_actions: !pane.allow_all_actions})}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.allow_all_actions ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">{t('officialAuthTitle')}</p>
              <p className="text-xs text-vsc-text-muted">{t('officialAuthHint')}</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.use_official_auth ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({...pane, use_official_auth: !pane.use_official_auth})}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.use_official_auth ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-vsc-text">{t('proxyToggleTitle')}</p>
              <p className="text-xs text-vsc-text-muted">{t('proxyToggleHint')}</p>
            </div>
            <div className={`relative w-10 h-5 rounded-full cursor-pointer transition-colors ${pane.use_proxy ? 'bg-orange-600' : 'bg-vsc-bg-active'}`}
              onClick={() => onChange({...pane, use_proxy: !pane.use_proxy})}>
              <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${pane.use_proxy ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
          </div>
          {pane.use_proxy && (
            <>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">{t('proxyPasswordLabel')}</label>
                <input type="text" value={pane.proxy?.password || ''}
                  onChange={e => onChange({...pane, proxy: {...(pane.proxy || {}), password: e.target.value}})}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                  placeholder={t('proxyPasswordPlaceholder')} />
              </div>
              <div>
                <label className="block text-xs text-vsc-text-secondary mb-1">{t('proxyRuleLabel')}</label>
                <input type="text" value={pane.proxy?.rule || ''}
                  onChange={e => onChange({...pane, proxy: {...(pane.proxy || {}), rule: e.target.value}})}
                  className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent"
                  placeholder={t('proxyRulePlaceholder')} />
              </div>
            </>
          )}
          <div style={{display: 'none'}}>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('agentDutyLabel')}</label>
            <textarea value={pane.agent_duty || ''}
              onChange={e => onChange({...pane, agent_duty: e.target.value})}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              style={{paddingRight: '44px'}}
              rows={6} placeholder={t('agentDutyPlaceholder')} />
          </div>
        </>)}

        {tab === 'network' && (<>
          <div>
            <label className="block text-xs text-vsc-text-secondary mb-1">{t('networkConfigLabel')}</label>
            <textarea value={pane.config || '{}'}
              onChange={e => onChange({...pane, config: e.target.value})}
              className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-2.5 py-1.5 focus:outline-none focus:border-vsc-accent resize-none"
              rows={12} placeholder='{"projects": ["/home/user/project"]}' />
            <div className="text-xs text-vsc-text-muted mt-2 space-y-1">
              <p className="font-medium text-vsc-text-secondary">{t('networkExampleTitle')}</p>
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
          {isSaving ? t('saving') : t('saveButton')}
        </button>
      </div>
    </div>
  );
};
