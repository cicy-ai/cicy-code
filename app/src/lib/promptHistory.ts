// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

const PREFIX = 'cicy_prompt_history:v1:';
const LIMIT = 200;

export const readPromptHistory = (paneId: string): string[] => {
  try {
    const value = JSON.parse(localStorage.getItem(`${PREFIX}${paneId}`) || '[]');
    return Array.isArray(value) ? value.filter((item) => typeof item === 'string' && item.trim()) : [];
  } catch { return []; }
};

export const appendPromptHistory = (paneId: string, value: string) => {
  const text = String(value || '').trim();
  if (!text) return;
  try { localStorage.setItem(`${PREFIX}${paneId}`, JSON.stringify([...readPromptHistory(paneId), text].slice(-LIMIT))); } catch {}
};

export const canNavigatePromptHistory = (element: HTMLTextAreaElement, direction: 'up' | 'down') => {
  if (element.selectionStart !== element.selectionEnd) return false;
  const value = element.value;
  const cursor = element.selectionStart;
  return direction === 'up'
    ? cursor <= (value.indexOf('\n') < 0 ? value.length : value.indexOf('\n'))
    : cursor > value.lastIndexOf('\n');
};
