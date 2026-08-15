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
    expect(css).toContain('[data-id="skill-md-table"]');
    expect(css).toContain('[data-id="skill-md-code-block"]');
    expect(css).toContain('html[data-theme="light"] .cm-editor');
    expect(css).toContain('.cm-cicy-search-field');
    expect(css).toContain(':is(input, textarea, select)[class*="bg-black"]');
    expect(css).toContain('[class~="bg-zinc-900/50"]');
    expect(css).toContain('[class*=" bg-[#0"]');
    expect(css).toContain('[class~="bg-black/30"]:not([class*="fixed"][class*="inset-0"])');
    expect(css).toContain('[class*="divide-white"] > :not(:last-child)');
    expect(css).toContain('[class~="hover:bg-zinc-800/60"]:hover');
    expect(css).toContain('[class*="text-amber-2"]');
    expect(css).toContain('[class*="bg-amber-900"]');
    expect(css).toContain('[class*="bg-sky-800"]');
    expect(css).toContain('[class*="bg-red-950"]');
    expect(css).toContain('[class*="bg-emerald-950"]');
    expect(css).toContain('[class*="text-blue-3"]');
    expect(css).toContain('[class*="text-violet-3"]');
    expect(css).toContain('[data-id="ModelTag"]');
    expect(css).toContain('[data-id="chatgpt-icon"]');
    expect(css).toContain('[data-id="agent-usage-analysis-breakdown"] .fill-zinc-100');
    expect(css).toContain('[data-id="team-context-ring"]');
    expect(css).toContain('[class~="text-zinc-400/85"]');
    expect(css).toContain('html[data-theme="light"] .inspector-markdown pre');
    expect(css).toContain('html[data-theme="light"] ::-webkit-scrollbar-thumb');
    expect(css).toContain('html[data-theme="light"] .markdown-body .hljs');
    expect(css).toContain('.hljs-template-variable');
    expect(css).toContain('rgba(63,63,70,0.14)');
  });
});
