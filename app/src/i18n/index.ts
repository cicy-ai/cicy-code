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
import enAgentCanvas from './locales/en/agentCanvas.json';
import enAudit from './locales/en/audit.json';
import enIm from './locales/en/im.json';
import enProvider from './locales/en/provider.json';
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
import zhAgentChat from './locales/zh-CN/agentChat.json';
import zhAgentInspector from './locales/zh-CN/agentInspector.json';
import zhAgentProviderRequest from './locales/zh-CN/agentProviderRequest.json';
import zhAgentTypeDesc from './locales/zh-CN/agentTypeDesc.json';
import zhApiSwitch from './locales/zh-CN/apiSwitch.json';
import zhDesktop from './locales/zh-CN/desktop.json';
import zhDevPanel from './locales/zh-CN/devPanel.json';
import zhAgentCanvas from './locales/zh-CN/agentCanvas.json';
import zhAudit from './locales/zh-CN/audit.json';
import zhIm from './locales/zh-CN/im.json';
import zhProvider from './locales/zh-CN/provider.json';
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

export const STORAGE_KEY = 'cicy.lang';

/**
 * Languages we ship full translation bundles for. Adding a new code here
 * requires a matching `locales/<code>/<ns>.json` for every namespace.
 */
export const TRANSLATED_LNGS = ['en', 'zh-CN'] as const;
export type TranslatedLng = (typeof TRANSLATED_LNGS)[number];

/**
 * Languages offered in the in-app picker. The picker uses Intl.DisplayNames
 * to render each entry in its native form, so adding a code here is a
 * one-line change. Codes that are NOT in TRANSLATED_LNGS still switch
 * i18n.language and <html lang>, but every t() call falls back to English
 * (fallbackLng below).
 */
export const SUPPORTED_LNGS = [
  // East Asia
  'en', 'zh-CN', 'zh-TW', 'zh-HK', 'ja', 'ko',
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
  'ar', 'fa', 'he', 'tr', 'az', 'ku',
  // Central Asia
  'kk', 'ky', 'uz', 'tg', 'mn',
  // Caucasus
  'hy', 'ka',
  // Africa
  'sw', 'am', 'ha', 'yo', 'ig', 'zu', 'xh', 'af', 'so', 'rw', 'om', 'sn',
] as const;
export type SupportedLng = (typeof SUPPORTED_LNGS)[number];

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
    agentCanvas: enAgentCanvas,
    teamPanel: enTeamPanel,
    audit: enAudit,
    provider: enProvider,
    im: enIm,
    terminal: enTerminal,
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
    agentCanvas: zhAgentCanvas,
    teamPanel: zhTeamPanel,
    audit: zhAudit,
    provider: zhProvider,
    im: zhIm,
    terminal: zhTerminal,
  },
};

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    ns: ['common', 'login', 'settings', 'createAgent', 'editPane', 'workspace', 'ui', 'layout', 'chat', 'agentInspector', 'agentProviderRequest', 'agentChat', 'agentTypeDesc', 'apiSwitch', 'desktop', 'devPanel', 'provision', 'agentCanvas', 'teamPanel', 'audit', 'provider', 'im', 'terminal'],
    defaultNS: 'common',
    fallbackLng: {
      'zh-TW': ['zh-CN', 'en'],
      'zh-HK': ['zh-CN', 'en'],
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
      document.documentElement.lang = i18n.resolvedLanguage ?? i18n.language;
    }
  });

i18n.on('languageChanged', (lng) => {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = lng;
  }
});

if (typeof window !== 'undefined' && import.meta.env.DEV) {
  (window as unknown as {i18n: typeof i18n}).i18n = i18n;
}

export default i18n;
