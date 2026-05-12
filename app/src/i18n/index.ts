import i18n from 'i18next';
import {initReactI18next} from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

import enCommon from './locales/en/common.json';
import enCreateAgent from './locales/en/createAgent.json';
import enEditPane from './locales/en/editPane.json';
import enLogin from './locales/en/login.json';
import enSettings from './locales/en/settings.json';
import zhCommon from './locales/zh-CN/common.json';
import zhCreateAgent from './locales/zh-CN/createAgent.json';
import zhEditPane from './locales/zh-CN/editPane.json';
import zhLogin from './locales/zh-CN/login.json';
import zhSettings from './locales/zh-CN/settings.json';

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
  },
  'zh-CN': {
    common: zhCommon,
    login: zhLogin,
    settings: zhSettings,
    createAgent: zhCreateAgent,
    editPane: zhEditPane,
  },
};

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    ns: ['common', 'login', 'settings', 'createAgent', 'editPane'],
    defaultNS: 'common',
    fallbackLng: 'en',
    supportedLngs: [...SUPPORTED_LNGS],
    nonExplicitSupportedLngs: true,
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
