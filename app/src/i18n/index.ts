import i18n from 'i18next';
import {initReactI18next} from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

import enAgentChat from './locales/en/agentChat.json';
import enAgentInspector from './locales/en/agentInspector.json';
import enAgentProviderRequest from './locales/en/agentProviderRequest.json';
import enAgentTypeDesc from './locales/en/agentTypeDesc.json';
import enApiSwitch from './locales/en/apiSwitch.json';
import enDesktop from './locales/en/desktop.json';
import enDevPanel from './locales/en/devPanel.json';
import enAudit from './locales/en/audit.json';
import enProvider from './locales/en/provider.json';
import enIm from './locales/en/im.json';
import enTerminal from './locales/en/terminal.json';
import enProvision from './locales/en/provision.json';
import enTeamPanel from './locales/en/teamPanel.json';
import enChat from './locales/en/chat.json';
import enCommon from './locales/en/common.json';
import enCreateAgent from './locales/en/createAgent.json';
import enEditPane from './locales/en/editPane.json';
import enLayout from './locales/en/layout.json';
import enLogin from './locales/en/login.json';
import enSettings from './locales/en/settings.json';
import enUi from './locales/en/ui.json';
import enWorkspace from './locales/en/workspace.json';
import enWslInstall from './locales/en/wslInstall.json';
import enSpeedUp from './locales/en/speedUp.json';
import enTodoPanel from './locales/en/todoPanel.json';
import zhAgentChat from './locales/zh-CN/agentChat.json';
import zhAgentInspector from './locales/zh-CN/agentInspector.json';
import zhAgentProviderRequest from './locales/zh-CN/agentProviderRequest.json';
import zhAgentTypeDesc from './locales/zh-CN/agentTypeDesc.json';
import zhApiSwitch from './locales/zh-CN/apiSwitch.json';
import zhDesktop from './locales/zh-CN/desktop.json';
import zhDevPanel from './locales/zh-CN/devPanel.json';
import zhAudit from './locales/zh-CN/audit.json';
import zhProvider from './locales/zh-CN/provider.json';
import zhIm from './locales/zh-CN/im.json';
import zhTerminal from './locales/zh-CN/terminal.json';
import zhProvision from './locales/zh-CN/provision.json';
import zhTeamPanel from './locales/zh-CN/teamPanel.json';
import zhChat from './locales/zh-CN/chat.json';
import zhCommon from './locales/zh-CN/common.json';
import zhCreateAgent from './locales/zh-CN/createAgent.json';
import zhEditPane from './locales/zh-CN/editPane.json';
import zhLayout from './locales/zh-CN/layout.json';
import zhLogin from './locales/zh-CN/login.json';
import zhSettings from './locales/zh-CN/settings.json';
import zhUi from './locales/zh-CN/ui.json';
import zhWorkspace from './locales/zh-CN/workspace.json';
import zhWslInstall from './locales/zh-CN/wslInstall.json';
import zhSpeedUp from './locales/zh-CN/speedUp.json';
import zhTodoPanel from './locales/zh-CN/todoPanel.json';
import frAgentChat from './locales/fr/agentChat.json';
import frAgentInspector from './locales/fr/agentInspector.json';
import frAgentProviderRequest from './locales/fr/agentProviderRequest.json';
import frAgentTypeDesc from './locales/fr/agentTypeDesc.json';
import frApiSwitch from './locales/fr/apiSwitch.json';
import frAudit from './locales/fr/audit.json';
import frChat from './locales/fr/chat.json';
import frCommon from './locales/fr/common.json';
import frCreateAgent from './locales/fr/createAgent.json';
import frDesktop from './locales/fr/desktop.json';
import frDevPanel from './locales/fr/devPanel.json';
import frEditPane from './locales/fr/editPane.json';
import frLayout from './locales/fr/layout.json';
import frLogin from './locales/fr/login.json';
import frProvider from './locales/fr/provider.json';
import frIm from './locales/fr/im.json';
import frProvision from './locales/fr/provision.json';
import frSettings from './locales/fr/settings.json';
import frTeamPanel from './locales/fr/teamPanel.json';
import frTerminal from './locales/fr/terminal.json';
import frUi from './locales/fr/ui.json';
import frWorkspace from './locales/fr/workspace.json';
import frWslInstall from './locales/fr/wslInstall.json';
import frSpeedUp from './locales/fr/speedUp.json';
import frTodoPanel from './locales/fr/todoPanel.json';
import jaAgentChat from './locales/ja/agentChat.json';
import jaAgentInspector from './locales/ja/agentInspector.json';
import jaAgentProviderRequest from './locales/ja/agentProviderRequest.json';
import jaAgentTypeDesc from './locales/ja/agentTypeDesc.json';
import jaApiSwitch from './locales/ja/apiSwitch.json';
import jaAudit from './locales/ja/audit.json';
import jaChat from './locales/ja/chat.json';
import jaCommon from './locales/ja/common.json';
import jaCreateAgent from './locales/ja/createAgent.json';
import jaDesktop from './locales/ja/desktop.json';
import jaDevPanel from './locales/ja/devPanel.json';
import jaEditPane from './locales/ja/editPane.json';
import jaLayout from './locales/ja/layout.json';
import jaLogin from './locales/ja/login.json';
import jaProvider from './locales/ja/provider.json';
import jaIm from './locales/ja/im.json';
import jaProvision from './locales/ja/provision.json';
import jaSettings from './locales/ja/settings.json';
import jaTeamPanel from './locales/ja/teamPanel.json';
import jaTerminal from './locales/ja/terminal.json';
import jaUi from './locales/ja/ui.json';
import jaWorkspace from './locales/ja/workspace.json';
import jaWslInstall from './locales/ja/wslInstall.json';
import jaSpeedUp from './locales/ja/speedUp.json';
import jaTodoPanel from './locales/ja/todoPanel.json';

const STORAGE_KEY = 'cicy.lang';

/**
 * Languages we ship full translation bundles for. Adding a new code here
 * requires a matching `locales/<code>/<ns>.json` for every namespace.
 */
export const TRANSLATED_LNGS = ['en', 'zh-CN', 'fr', 'ja'] as const;

/**
 * Languages offered in the in-app picker. The picker uses Intl.DisplayNames
 * to render each entry in its native form, so adding a code here is a
 * one-line change. Codes that are NOT in TRANSLATED_LNGS still switch
 * i18n.language and <html lang>, but every t() call falls back to English
 * (fallbackLng below).
 */
const SUPPORTED_LNGS = [
  // East Asia
  'en', 'zh-CN', 'ja', 'ko',
  // SE Asia
  'vi', 'th', 'id', 'ms', 'tl', 'my', 'km', 'lo',
  // South Asia
  'hi', 'bn', 'ta', 'te', 'ml', 'kn', 'mr', 'gu', 'pa', 'ur', 'ne', 'si',
  // Western Europe
  'es', 'es-MX', 'pt', 'pt-BR', 'fr', 'fr-CA', 'de', 'it', 'nl', 'sv',
  'da', 'no', 'fi', 'is', 'ga', 'cy', 'eu', 'ca', 'gl', 'lb', 'fo',
  // Central / Eastern Europe
  'pl', 'cs', 'sk', 'hu', 'ro', 'bg', 'hr', 'sr', 'sl', 'mk', 'sq',
  'lt', 'lv', 'et', 'mt', 'el',
  // East Slavic
  'ru', 'uk', 'be',
  // Middle East
  'fa', 'he', 'tr', 'az', 'ku',
  // Central Asia
  'kk', 'ky', 'uz', 'tg', 'mn',
  // Caucasus
  'hy', 'ka',
  // Africa
  'sw', 'am', 'ha', 'yo', 'ig', 'zu', 'xh', 'af', 'so', 'rw', 'om', 'sn',
] as const;

const resources = {
  en: {
    common: enCommon,
    login: enLogin,
    settings: enSettings,
    createAgent: enCreateAgent,
    editPane: enEditPane,
    workspace: enWorkspace,
    ui: enUi,
    layout: enLayout,
    chat: enChat,
    agentInspector: enAgentInspector,
    agentProviderRequest: enAgentProviderRequest,
    agentChat: enAgentChat,
    agentTypeDesc: enAgentTypeDesc,
    apiSwitch: enApiSwitch,
    desktop: enDesktop,
    devPanel: enDevPanel,
    provision: enProvision,
    teamPanel: enTeamPanel,
    audit: enAudit,
    provider: enProvider,
    im: enIm,
    terminal: enTerminal,
    wslInstall: enWslInstall,
    speedUp: enSpeedUp,
    todoPanel: enTodoPanel,
  },
  'zh-CN': {
    common: zhCommon,
    login: zhLogin,
    settings: zhSettings,
    createAgent: zhCreateAgent,
    editPane: zhEditPane,
    workspace: zhWorkspace,
    ui: zhUi,
    layout: zhLayout,
    chat: zhChat,
    agentInspector: zhAgentInspector,
    agentProviderRequest: zhAgentProviderRequest,
    agentChat: zhAgentChat,
    agentTypeDesc: zhAgentTypeDesc,
    apiSwitch: zhApiSwitch,
    desktop: zhDesktop,
    devPanel: zhDevPanel,
    provision: zhProvision,
    teamPanel: zhTeamPanel,
    audit: zhAudit,
    provider: zhProvider,
    im: zhIm,
    terminal: zhTerminal,
    wslInstall: zhWslInstall,
    speedUp: zhSpeedUp,
    todoPanel: zhTodoPanel,
  },
  fr: {
    agentChat: frAgentChat,
    agentInspector: frAgentInspector,
    agentProviderRequest: frAgentProviderRequest,
    agentTypeDesc: frAgentTypeDesc,
    apiSwitch: frApiSwitch,
    audit: frAudit,
    chat: frChat,
    common: frCommon,
    createAgent: frCreateAgent,
    desktop: frDesktop,
    devPanel: frDevPanel,
    editPane: frEditPane,
    layout: frLayout,
    login: frLogin,
    provider: frProvider,
    im: frIm,
    provision: frProvision,
    settings: frSettings,
    teamPanel: frTeamPanel,
    terminal: frTerminal,
    ui: frUi,
    workspace: frWorkspace,
    wslInstall: frWslInstall,
    speedUp: frSpeedUp,
    todoPanel: frTodoPanel,
  },
  ja: {
    agentChat: jaAgentChat,
    agentInspector: jaAgentInspector,
    agentProviderRequest: jaAgentProviderRequest,
    agentTypeDesc: jaAgentTypeDesc,
    apiSwitch: jaApiSwitch,
    audit: jaAudit,
    chat: jaChat,
    common: jaCommon,
    createAgent: jaCreateAgent,
    desktop: jaDesktop,
    devPanel: jaDevPanel,
    editPane: jaEditPane,
    layout: jaLayout,
    login: jaLogin,
    provider: jaProvider,
    im: jaIm,
    provision: jaProvision,
    settings: jaSettings,
    teamPanel: jaTeamPanel,
    terminal: jaTerminal,
    ui: jaUi,
    workspace: jaWorkspace,
    wslInstall: jaWslInstall,
    speedUp: jaSpeedUp,
    todoPanel: jaTodoPanel,
  },
};

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    ns: ['common', 'login', 'settings', 'createAgent', 'editPane', 'workspace', 'ui', 'layout', 'chat', 'agentInspector', 'agentProviderRequest', 'agentChat', 'agentTypeDesc', 'apiSwitch', 'desktop', 'devPanel', 'provision', 'teamPanel', 'audit', 'provider', 'im', 'terminal', 'wslInstall', 'speedUp', 'todoPanel'],
    defaultNS: 'common',
    fallbackLng: {
      'zh': ['zh-CN', 'en'],
      default: ['en'],
    },
    supportedLngs: [...SUPPORTED_LNGS],
    load: 'currentOnly',
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: STORAGE_KEY,
      caches: ['localStorage'],
    },
    interpolation: {escapeValue: false},
    returnNull: false,
    debug: false,
  })
  .then(() => {
    if (typeof document !== 'undefined') {
      const active = i18n.resolvedLanguage ?? i18n.language;
      document.documentElement.lang = active;
      document.documentElement.dir = directionFor(active);
    }
  });

const RTL_PREFIXES = ['ar', 'fa', 'he', 'ur', 'ps', 'ku', 'sd', 'ckb', 'dv', 'yi'];
function directionFor(code: string | undefined | null): 'rtl' | 'ltr' {
  const c = String(code || '').toLowerCase();
  return RTL_PREFIXES.some((p) => c === p || c.startsWith(p + '-')) ? 'rtl' : 'ltr';
}

i18n.on('languageChanged', (lng) => {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = lng;
    document.documentElement.dir = directionFor(lng);
  }
});

if (typeof window !== 'undefined' && import.meta.env.DEV) {
  (window as unknown as {i18n: typeof i18n}).i18n = i18n;
}

export default i18n;
