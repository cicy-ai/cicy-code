import { beforeEach, describe, expect, it, vi } from 'vitest';
import { applyCicyTheme, getCicyTheme, setCicyTheme } from './theme';

describe('global theme', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
    document.documentElement.className = '';
  });

  it('defaults to dark and applies the document contract', () => {
    expect(getCicyTheme()).toBe('dark');
    applyCicyTheme('dark');
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    expect(document.documentElement).toHaveClass('dark');
  });

  it('persists light and notifies mounted surfaces immediately', () => {
    const listener = vi.fn();
    window.addEventListener('cicy-theme-change', listener);
    setCicyTheme('light');
    expect(localStorage.getItem('cicy_theme')).toBe('light');
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(document.documentElement).not.toHaveClass('dark');
    expect(listener).toHaveBeenCalledOnce();
    window.removeEventListener('cicy-theme-change', listener);
  });
});
