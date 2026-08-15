import { beforeEach, describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
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

  it('keeps the project shell, canvas and footer in the light theme contract', () => {
    const css = readFileSync('src/index.css', 'utf8');
    expect(css).toContain('[data-id="projects-panel"]');
    expect(css).toContain('[data-id="projects-list"]');
    expect(css).toContain('[data-id="projects-agent-header"]');
    expect(css).toContain('[data-id="project-infinite-canvas"]');
    expect(css).toContain('[data-id="project-canvas-footer"]');
    expect(css).toContain('[data-id="knowledge-graph-canvas"]');
    expect(css).toContain('[data-id="knowledge-graph-search"]');
    expect(css).toContain('html[data-theme="light"] .cm-editor');
    expect(css).toContain('rgba(63,63,70,0.14)');
  });
});
