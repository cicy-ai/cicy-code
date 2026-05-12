import i18n from 'i18next';
import {initReactI18next} from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

import enChat from './locales/en/chat.json';
import enCommon from './locales/en/common.json';
import enCreateAgent from './locales/en/createAgent.json';
import enEditPane from './locales/en/editPane.json';
import enLayout from './locales/en/layout.json';
import enLogin from './locales/en/login.json';
import enSettings from './locales/en/settings.json';
import enUi from './locales/en/ui.json';
import enWorkspace from './locales/en/workspace.json';
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

export const SUPPORTED_LNGS = ['en', 'zh-CN'] as const;
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
  },
};

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    ns: ['common', 'login', 'settings', 'createAgent', 'editPane', 'workspace', 'ui', 'layout', 'chat'],
    defaultNS: 'common',
    fallbackLng: 'en',
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
