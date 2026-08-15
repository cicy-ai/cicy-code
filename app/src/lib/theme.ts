// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export type CicyTheme = 'light' | 'dark';

const THEME_KEY = 'cicy_theme';

export function getCicyTheme(): CicyTheme {
  return localStorage.getItem(THEME_KEY) === 'light' ? 'light' : 'dark';
}

export function applyCicyTheme(theme: CicyTheme) {
  document.documentElement.dataset.theme = theme;
  document.documentElement.classList.toggle('dark', theme === 'dark');
  document.documentElement.style.colorScheme = theme;
}

export function setCicyTheme(theme: CicyTheme) {
  localStorage.setItem(THEME_KEY, theme);
  applyCicyTheme(theme);
  window.dispatchEvent(new CustomEvent('cicy-theme-change', { detail: { theme } }));
}
