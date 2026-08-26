import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import zh from '../../i18n/locales/zh-CN/workspace.json';
import en from '../../i18n/locales/en/workspace.json';

// A locale string whose placeholder name does not match the call site renders
// the raw "{{placeholder}}" to the user, which is exactly what shipped once.
// This pins every account-matrix string to the arguments its panel passes.
const PANELS = [
  'src/components/settings/NpmAccountsPanel.tsx',
  'src/components/settings/DockerAccountsPanel.tsx',
  'src/components/settings/AliyunAccountsPanel.tsx',
];
const PREFIXES = ['npm', 'docker', 'aliyun'];

function callSiteArgs(source: string, key: string): Set<string> | null {
  // Matches t("key", { a: …, b: … }) and collects the top-level argument names.
  const start = source.indexOf(`t("${key}"`);
  if (start < 0) return null;
  const open = source.indexOf('{', start);
  if (open < 0) return null;
  let depth = 0;
  let end = open;
  for (; end < source.length; end += 1) {
    if (source[end] === '{') depth += 1;
    else if (source[end] === '}') {
      depth -= 1;
      if (depth === 0) break;
    }
  }
  const body = source.slice(open + 1, end);
  const args = new Set<string>();
  let nesting = 0;
  body.split('\n').join(' ').split(',').forEach((chunk) => {
    const balanced = nesting === 0;
    nesting += (chunk.match(/[{([`]/g) || []).length - (chunk.match(/[})\]`]/g) || []).length;
    if (!balanced) return;
    const name = chunk.split(':')[0].trim();
    if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) args.add(name);
  });
  return args;
}

describe('account matrix i18n', () => {
  const sources = PANELS.map((path) => readFileSync(path, 'utf8')).join('\n');
  const keys = Object.keys(zh).filter((key) => PREFIXES.some((prefix) => key.startsWith(prefix)));

  it('covers every matrix key in both locales', () => {
    expect(keys.length).toBeGreaterThan(20);
    keys.forEach((key) => expect(en, `missing en key ${key}`).toHaveProperty(key));
  });

  it('interpolates only placeholders the panels actually pass', () => {
    keys.forEach((key) => {
      [zh, en].forEach((locale) => {
        const value = String((locale as unknown as Record<string, unknown>)[key] ?? '');
        const placeholders = [...value.matchAll(/\{\{(\w+)\}\}/g)].map((match) => match[1]);
        if (!placeholders.length) return;
        const args = callSiteArgs(sources, key);
        if (!args) return; // key rendered by the shared shell, not a panel
        placeholders.forEach((placeholder) => {
          expect(args, `${key}: "${value}" needs {{${placeholder}}} but the panel passes ${[...args].join(', ')}`).toContain(placeholder);
        });
      });
    });
  });
});
