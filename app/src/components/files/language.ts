import type { Extension } from '@codemirror/state';
import { javascript } from '@codemirror/lang-javascript';
import { python } from '@codemirror/lang-python';
import { go } from '@codemirror/lang-go';
import { markdown } from '@codemirror/lang-markdown';
import { json } from '@codemirror/lang-json';
import { css } from '@codemirror/lang-css';
import { html } from '@codemirror/lang-html';
import { xml } from '@codemirror/lang-xml';
import { yaml } from '@codemirror/lang-yaml';
import { rust } from '@codemirror/lang-rust';
import { cpp } from '@codemirror/lang-cpp';
import { java } from '@codemirror/lang-java';

// Maps a file path to a CodeMirror language extension.
// Returns an empty array (no language) when no match is found; the editor
// falls back to plain text rendering.
export function languageForPath(path: string): Extension[] {
  const lower = path.toLowerCase();
  const dot = lower.lastIndexOf('.');
  const ext = dot >= 0 ? lower.slice(dot + 1) : '';
  const base = lower.slice(lower.lastIndexOf('/') + 1);

  switch (ext) {
    case 'js':
    case 'mjs':
    case 'cjs':
      return [javascript()];
    case 'jsx':
      return [javascript({ jsx: true })];
    case 'ts':
      return [javascript({ typescript: true })];
    case 'tsx':
      return [javascript({ typescript: true, jsx: true })];
    case 'py':
    case 'pyi':
      return [python()];
    case 'go':
      return [go()];
    case 'md':
    case 'markdown':
    case 'mdx':
      return [markdown()];
    case 'json':
    case 'jsonl':
    case 'jsonc':
      return [json()];
    case 'css':
    case 'scss':
    case 'less':
      return [css()];
    case 'html':
    case 'htm':
    case 'svg':
    case 'vue':
      return [html()];
    case 'xml':
    case 'plist':
      return [xml()];
    case 'yml':
    case 'yaml':
    case 'toml': // toml lacks a dedicated pkg; yaml is a passable approximation
      return [yaml()];
    case 'rs':
      return [rust()];
    case 'c':
    case 'cc':
    case 'cpp':
    case 'cxx':
    case 'h':
    case 'hpp':
    case 'hxx':
      return [cpp()];
    case 'java':
    case 'kt':
    case 'kts':
      return [java()];
    case 'sh':
    case 'bash':
    case 'zsh':
    case 'fish':
      return []; // shell lang pkg is heavyweight; skip for MVP
    case 'sql':
      return [];
    default:
      break;
  }

  // dotfiles and well-known names without extensions
  switch (base) {
    case 'dockerfile':
    case 'makefile':
    case '.gitignore':
    case '.dockerignore':
    case '.npmignore':
      return [];
    default:
      return [];
  }
}
