// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { cn } from './utils';

// Global model display helper.
//
// Raw model ids (claude-opus-4-8, deepseek-v3.1-2025xxxx, gpt-5.5-preview, …)
// are long and noisy in tables/headers. `shortModel()` collapses them to a
// friendly label; `<ModelTag>` renders that label as a small color-coded chip
// (one hue per model family) with the full raw id on hover. Use this anywhere a
// model name is shown so the styling/short-naming stays consistent.

export type ModelFamily =
  | 'opus' | 'sonnet' | 'haiku' | 'fable'
  | 'gpt' | 'o' | 'deepseek' | 'gemini' | 'qwen' | 'kimi' | 'grok' | 'glm'
  | 'llama' | 'mistral' | 'other';

// Explicit short-name overrides win over the heuristics below. Add edge cases
// here (key = normalized id, value = label).
const MODEL_ALIASES: Record<string, string> = {};

// lowercase, strip provider path prefix (anthropic/, openai/, …) and trailing
// date/build suffixes (-20250101, @20250101, :latest, -preview, -exp).
function normalize(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/^[a-z0-9.+-]+\//, '')
    .replace(/[-@:](\d{6,8}|latest|preview|exp|beta)$/, '');
}

// isChatModel reports whether a model id can drive a chat completion — i.e.
// whether it belongs in a chat model picker. Speech-to-text (whisper) and
// text-to-speech (orpheus / *-tts) models can't chat, so they're excluded.
// Used to filter provider model lists in the chat model pickers (NOT in the
// provider dashboard or stt routing, which must still see every model).
export function isChatModel(raw?: string): boolean {
  const m = String(raw || '').toLowerCase();
  if (!m) return false;
  if (m.includes('whisper')) return false;            // STT (Groq whisper-large-v3*, openai whisper-1)
  if (m.includes('orpheus')) return false;            // TTS (canopylabs/orpheus-*)
  if (/(^|[/_-])tts([/_-]|$)/.test(m)) return false;  // generic text-to-speech ids
  return true;
}

export function modelFamily(raw?: string): ModelFamily {
  if (!raw || !raw.trim()) return 'other';
  const m = normalize(raw);
  if (/opus/.test(m)) return 'opus';
  if (/sonnet/.test(m)) return 'sonnet';
  if (/haiku/.test(m)) return 'haiku';
  if (/fable/.test(m)) return 'fable';
  if (/^deepseek|^ds-/.test(m)) return 'deepseek';
  if (/^o\d/.test(m)) return 'o';
  if (/^gpt/.test(m)) return 'gpt';
  if (/^gemini/.test(m)) return 'gemini';
  if (/^qwen/.test(m)) return 'qwen';
  if (/^kimi|^moonshot/.test(m)) return 'kimi';
  if (/^grok/.test(m)) return 'grok';
  if (/^glm|^chatglm/.test(m)) return 'glm';
  if (/^llama|^codellama/.test(m)) return 'llama';
  if (/^mistral|^mixtral/.test(m)) return 'mistral';
  return 'other';
}

export function shortModel(raw?: string): string {
  if (!raw || !raw.trim()) return '—';
  const orig = raw.trim();
  const m = normalize(orig);
  if (MODEL_ALIASES[m]) return MODEL_ALIASES[m];
  // claude new style: claude-opus-4-8 → opus-4.8
  let c = m.match(/^claude-(opus|sonnet|haiku|fable)-(\d+)-(\d+)/);
  if (c) return `${c[1]}-${c[2]}.${c[3]}`;
  // claude old style: claude-3-5-sonnet → sonnet-3.5
  c = m.match(/^claude-(\d+)-(\d+)-(opus|sonnet|haiku|fable)/);
  if (c) return `${c[3]}-${c[1]}.${c[2]}`;
  // claude-<family>-<n> → family-n  (e.g. claude-opus-4 → opus-4, claude-fable-5 → fable-5)
  c = m.match(/^claude-(opus|sonnet|haiku|fable)-(\d+)/);
  if (c) return `${c[1]}-${c[2]}`;
  // deepseek-* → ds-*
  if (m.startsWith('deepseek')) return m.replace(/^deepseek-?/, 'ds-') || 'ds';
  // already short-ish providers — keep the normalized id
  if (/^(gpt|o\d|qwen|gemini|kimi|grok|glm|llama|mistral|mixtral)/.test(m)) return m;
  // long unknown id: keep the last two dash segments
  if (m.length > 16) {
    const parts = m.split('-');
    if (parts.length > 2) return parts.slice(-2).join('-');
  }
  return m;
}

// Dark-theme chip styles per family — SOLID color background + white text
// (per product decision 2026-06-05). Dark -800/-900 hues at 60% so the chips
// sit quietly on the dark UI. Literal class strings so Tailwind's JIT keeps them.
const FAMILY_STYLE: Record<ModelFamily, string> = {
  opus:     'bg-violet-900/60 text-white/75',
  sonnet:   'bg-sky-900/60 text-white/75',
  haiku:    'bg-teal-900/60 text-white/75',
  fable:    'bg-pink-900/60 text-white/75',
  gpt:      'bg-emerald-900/60 text-white/75',
  o:        'bg-lime-900/60 text-white/75',
  deepseek: 'bg-indigo-900/60 text-white/75',
  gemini:   'bg-blue-900/60 text-white/75',
  qwen:     'bg-amber-900/60 text-white/75',
  kimi:     'bg-fuchsia-900/60 text-white/75',
  grok:     'bg-zinc-800/60 text-white/75',
  glm:      'bg-cyan-900/60 text-white/75',
  llama:    'bg-orange-900/60 text-white/75',
  mistral:  'bg-rose-900/60 text-white/75',
  other:    'bg-zinc-800/60 text-white/75',
};

// Family-colored chip classes for a raw model id (use when you need the classes
// without the component, e.g. on an existing span).
export function modelTagClass(raw?: string): string {
  return FAMILY_STYLE[modelFamily(raw)];
}

export function ModelTag({
  model,
  className,
  title,
}: {
  model?: string;
  className?: string;
  title?: string;
}) {
  return (
    <span
      data-id="ModelTag"
      title={title ?? (model || '')}
      className={cn(
        'inline-block max-w-[160px] truncate rounded px-1 text-[10px] font-medium leading-4 align-middle',
        modelTagClass(model),
        className,
      )}
    >
      {shortModel(model)}
    </span>
  );
}
