// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState, useEffect, useCallback } from 'react';
import { createPortal } from 'react-dom';
import i18n from '../../i18n';
import { X, FileText, FileJson, FileCode, Loader2, Send, ExternalLink } from 'lucide-react';
import apiService from '../../services/api';

// ForkConfirmModal — fork ("分身") no longer fires-and-forgets the inherit
// prompt. Clicking 分身 opens this preview first: it shows the exact context the
// fork would inherit (source current.json + reply.json, the regenerated summary,
// token use, compression ratio), lets the user open any of the three files in
// the editor, and exposes a large EDITABLE prompt. The fork pane is created —
// and the (possibly edited) prompt sent — only when the user clicks Send.
//
// It is ANCHORED over the source agent's stack card (not a centered fullscreen
// modal) and has NO backdrop mask: the canvas card sits on the left, so leaving
// the rest of the screen interactive lets the user open a history file in the
// editor drawer and keep editing the prompt side-by-side.

interface ForkFile {
  path: string;
  content: string;
  size: number;
  truncated: boolean;
}

interface ForkPreview {
  source_pane_id: string;
  source_short: string;
  agent_type: string;
  workspace: string;
  files: { current_json: ForkFile; reply_json: ForkFile; summary: ForkFile };
  token_use: Record<string, number | string>;
  summary_tokens_est: number;
  compression: { ratio: number; token_ratio: number; original_bytes: number; summary_bytes: number };
  default_prompt: string;
  // Both language templates of the inherit prompt; the 中/EN toggle swaps the
  // textarea between them without a round-trip.
  default_prompts?: { en: string; zh: string };
}

type PromptLang = 'en' | 'zh';

interface ForkConfirmModalProps {
  sourcePaneId: string;
  masterPaneId: string;
  projectId?: number | string;
  onClose: () => void;
  onForked: () => void;
  // Opens a file (workspace-relative path) in the source agent's file editor.
  onOpenAgentFile: (paneId: string, relPath: string) => void;
}

interface AnchorRect {
  top: number;
  left: number;
  width: number;
  height: number;
}

function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}

function fmtTokens(n: unknown): string {
  const v = typeof n === 'number' ? n : Number(n);
  if (!Number.isFinite(v)) return '—';
  return v.toLocaleString('en-US');
}

const t = (k: string, o?: Record<string, unknown>) => i18n.t(k, { ns: 'teamPanel', ...o });

export default function ForkConfirmModal({ sourcePaneId, masterPaneId, projectId, onClose, onForked, onOpenAgentFile }: ForkConfirmModalProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<ForkPreview | null>(null);
  const [prompt, setPrompt] = useState('');
  // Inherit-prompt language; defaults to the UI language. Switching replaces
  // the textarea with that language's template (it's a template picker, so
  // manual edits are discarded on switch).
  const [promptLang, setPromptLang] = useState<PromptLang>(() => (i18n.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en');
  const [sending, setSending] = useState(false);
  const [anchor, setAnchor] = useState<AnchorRect | null>(null);

  const defaultPromptFor = useCallback((p: ForkPreview | null, lang: PromptLang): string => {
    if (!p) return '';
    return String(p.default_prompts?.[lang] || p.default_prompt || '');
  }, []);

  const switchLang = useCallback(
    (lang: PromptLang) => {
      setPromptLang(lang);
      setPrompt(defaultPromptFor(preview, lang));
    },
    [preview, defaultPromptFor],
  );

  // Anchor over the source agent's stack card (data-id="agent-stack-card-<id>").
  // Only the ACTIVE card is rendered (others are display:none → zero rect), so
  // when the source card is hidden we anchor to the visible stack area instead
  // of falling back to a fullscreen-looking centered box. TeamPanel activates
  // the source on open, so the real card rect is normally available.
  const rectOf = (sel: string): AnchorRect | null => {
    const el = document.querySelector<HTMLElement>(sel);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    if (r.width < 40 || r.height < 40) return null;
    return { top: r.top, left: r.left, width: r.width, height: r.height };
  };
  const computeAnchor = useCallback(
    (): AnchorRect | null => rectOf(`[data-id="agent-stack-card-${sourcePaneId}"]`) || rectOf('[data-id="agent-stack"]'),
    [sourcePaneId],
  );

  // Keep the modal glued to the source card via a rAF loop: opening a history
  // file pops the editor drawer, which shrinks the canvas and moves/resizes the
  // card — a plain resize listener wouldn't catch that layout shift. Only push a
  // new anchor when the rect actually changes, so this doesn't churn renders.
  useEffect(() => {
    let raf = 0;
    let prev = '';
    const tick = () => {
      const a = computeAnchor();
      const key = a ? `${Math.round(a.top)},${Math.round(a.left)},${Math.round(a.width)},${Math.round(a.height)}` : 'none';
      if (key !== prev) {
        prev = key;
        setAnchor(a);
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, [computeAnchor]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    apiService
      .forkPreview({ source_pane_id: sourcePaneId })
      .then(({ data }) => {
        if (!alive) return;
        const p = data as ForkPreview;
        setPreview(p);
        setPrompt(String(p.default_prompts?.[promptLang] || p.default_prompt || ''));
      })
      .catch(() => alive && setError(t('forkPreviewFailed') as string))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [sourcePaneId]);

  // ESC closes (unless sending).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !sending) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, sending]);

  // Absolute path → workspace-relative so the source agent's FilesView resolves
  // it (the FS API forbids absolute paths; it roots on the workspace).
  const relPath = useCallback(
    (abs: string): string => {
      const ws = preview?.workspace?.replace(/\/+$/, '') || '';
      if (ws && abs.startsWith(ws + '/')) return abs.slice(ws.length + 1);
      return abs;
    },
    [preview],
  );

  const openFile = useCallback(
    (abs: string) => {
      if (!abs || !preview) return;
      onOpenAgentFile(preview.source_pane_id, relPath(abs));
    },
    [preview, relPath, onOpenAgentFile],
  );

  const send = useCallback(async () => {
    if (sending || !preview) return;
    setSending(true);
    try {
      const { data } = await apiService.forkPane({
        source_pane_id: preview.source_pane_id,
        master_pane_id: masterPaneId,
        project_id: projectId,
        prompt,
      });
      if (data?.pane_id) {
        onForked();
        onClose();
      } else {
        setError(t('toastForkFailed') as string);
        setSending(false);
      }
    } catch {
      setError(t('toastForkFailed') as string);
      setSending(false);
    }
  }, [sending, preview, masterPaneId, projectId, prompt, onForked, onClose]);

  const fileRow = (label: string, icon: React.ReactNode, f?: ForkFile) => {
    if (!f) return null;
    return (
      <button
        data-id={`fork-confirm-file-${label}`}
        type="button"
        onClick={() => openFile(f.path)}
        disabled={!f.path}
        className="group flex w-full items-center gap-2.5 rounded-lg border border-white/[0.06] bg-white/[0.02] px-2.5 py-1.5 text-left transition hover:border-blue-400/40 hover:bg-blue-400/[0.06] disabled:cursor-not-allowed disabled:opacity-40"
      >
        <span className="shrink-0 text-zinc-400 group-hover:text-blue-300">{icon}</span>
        <span className="min-w-0 flex-1">
          {/* Show the ABSOLUTE path — the fork prompt references these files by
              absolute path, so the row must match what the agent will read.
              (openFile still converts to workspace-relative for the FS API.) */}
          <span className="block truncate text-xs font-mono text-zinc-200">{f.path || '—'}</span>
          <span className="block text-[11px] text-zinc-500">
            {fmtBytes(f.size)}
            {f.truncated ? ` · ${t('forkTruncated')}` : ''}
          </span>
        </span>
        <ExternalLink className="h-3.5 w-3.5 shrink-0 text-zinc-600 group-hover:text-blue-300" />
      </button>
    );
  };

  const tu = preview?.token_use || {};
  const comp = preview?.compression;
  const ratioPct = comp ? (comp.ratio * 100).toFixed(1) : '—';

  // Positioned over the source card; centered fallback when unanchored.
  const cardStyle: React.CSSProperties = anchor
    ? { position: 'fixed', top: anchor.top, left: anchor.left, width: anchor.width, height: anchor.height }
    : { position: 'fixed', top: '50%', left: '50%', width: 'min(680px, 92vw)', height: 'min(80vh, 760px)', transform: 'translate(-50%, -50%)' };

  return createPortal(
    <div
      data-id="fork-confirm-card"
      onClick={(e) => e.stopPropagation()}
      className="z-[10000] flex flex-col overflow-hidden rounded-2xl border border-white/[0.12] bg-[#141416] shadow-2xl"
      style={cardStyle}
    >
      {/* header */}
      <div data-id="fork-confirm-header" className="flex shrink-0 items-center justify-between border-b border-white/[0.06] px-4 py-2.5">
        <div className="min-w-0">
          <div className="text-sm font-semibold text-zinc-100">{t('forkPreviewTitle')}</div>
          <div className="mt-0.5 truncate text-xs text-zinc-500">
            {t('forkPreviewSubtitle')}
            {preview ? <span className="ml-2 font-mono text-zinc-600">{preview.source_short}</span> : null}
          </div>
        </div>
        <button
          data-id="fork-confirm-close"
          type="button"
          onClick={() => !sending && onClose()}
          className="shrink-0 rounded-lg p-1.5 text-zinc-500 transition hover:bg-white/[0.06] hover:text-zinc-200"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* body — flex column; the prompt textarea grows to fill remaining height */}
      <div data-id="fork-confirm-body" className="flex min-h-0 flex-1 flex-col gap-3 px-4 py-3">
        {loading ? (
          <div className="flex flex-1 items-center justify-center gap-2 text-sm text-zinc-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            {t('forkLoadingPreview')}
          </div>
        ) : error && !preview ? (
          <div className="flex flex-1 items-center justify-center text-sm text-red-400">{error}</div>
        ) : preview ? (
          <>
            {/* files */}
            <div className="shrink-0 space-y-1.5" data-id="fork-confirm-files">
              {fileRow('current_json', <FileJson className="h-4 w-4" />, preview.files.current_json)}
              {fileRow('reply_json', <FileCode className="h-4 w-4" />, preview.files.reply_json)}
              {fileRow('summary', <FileText className="h-4 w-4" />, preview.files.summary)}
            </div>

            {/* token use + compression */}
            <div className="grid shrink-0 grid-cols-2 gap-2.5" data-id="fork-confirm-stats">
              <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-2.5">
                <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-zinc-500">{t('forkTokenUse')}</div>
                <dl className="space-y-0.5 text-xs">
                  <div className="flex justify-between"><dt className="text-zinc-500">input</dt><dd className="font-mono text-zinc-200">{fmtTokens(tu.input_tokens)}</dd></div>
                  <div className="flex justify-between"><dt className="text-zinc-500">output</dt><dd className="font-mono text-zinc-200">{fmtTokens(tu.output_tokens)}</dd></div>
                  <div className="flex justify-between"><dt className="text-zinc-500">cache w/r</dt><dd className="font-mono text-zinc-200">{fmtTokens(tu.cache_creation_input_tokens)} / {fmtTokens(tu.cache_read_input_tokens)}</dd></div>
                  <div className="flex justify-between border-t border-white/[0.06] pt-0.5"><dt className="text-zinc-400">total</dt><dd className="font-mono text-zinc-100">{fmtTokens(tu.total_tokens)}</dd></div>
                  {tu.cost_credit != null ? <div className="flex justify-between"><dt className="text-zinc-500">cost</dt><dd className="font-mono text-zinc-200">{String(tu.cost_credit)}</dd></div> : null}
                  {tu.model ? <div className="flex justify-between"><dt className="text-zinc-500">model</dt><dd className="truncate font-mono text-zinc-300">{String(tu.model)}</dd></div> : null}
                </dl>
              </div>
              <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-2.5">
                <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-zinc-500">{t('forkCompression')}</div>
                <div className="text-2xl font-semibold text-blue-300">{ratioPct}<span className="text-base text-zinc-500">%</span></div>
                <div className="mt-1 text-xs text-zinc-500">
                  {fmtBytes(comp?.summary_bytes || 0)} / {fmtBytes(comp?.original_bytes || 0)}
                </div>
                <div className="mt-1.5 text-[11px] text-zinc-600">
                  {t('forkSummaryTokensEst')}: <span className="font-mono text-zinc-400">~{fmtTokens(preview.summary_tokens_est)}</span>
                </div>
              </div>
            </div>

            {/* prompt — grows to fill the rest of the card */}
            <div data-id="fork-confirm-prompt" className="flex min-h-0 flex-1 flex-col">
              <div className="mb-1.5 flex shrink-0 items-center justify-between">
                <label className="text-[11px] font-semibold uppercase tracking-wide text-zinc-500">{t('forkPromptLabel')}</label>
                {/* 中/EN template picker — switching swaps the textarea to that
                    language's handover template (manual edits are replaced). */}
                <div data-id="fork-confirm-prompt-lang" className="flex items-center gap-0.5 rounded-md border border-white/[0.08] p-0.5">
                  {(['zh', 'en'] as PromptLang[]).map((lang) => (
                    <button
                      key={lang}
                      type="button"
                      data-id={`fork-confirm-prompt-lang-${lang}`}
                      onClick={() => switchLang(lang)}
                      className={`rounded px-2 py-0.5 text-[11px] transition ${
                        promptLang === lang ? 'bg-blue-500/20 text-blue-300' : 'text-zinc-500 hover:text-zinc-300'
                      }`}
                    >
                      {lang === 'zh' ? '中文' : 'EN'}
                    </button>
                  ))}
                </div>
              </div>
              <textarea
                data-id="fork-confirm-prompt-textarea"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                spellCheck={false}
                className="min-h-[160px] w-full flex-1 resize-none rounded-lg border border-white/[0.08] bg-[#0d0d0f] px-3 py-2 font-mono text-xs leading-5 text-zinc-200 outline-none focus:border-blue-400/50"
              />
            </div>
          </>
        ) : null}
      </div>

      {/* footer */}
      <div data-id="fork-confirm-footer" className="flex shrink-0 items-center justify-between gap-3 border-t border-white/[0.06] px-4 py-2.5">
        <div className="min-w-0 truncate text-xs text-red-400">{error && preview ? error : ''}</div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            data-id="fork-confirm-cancel"
            type="button"
            onClick={() => !sending && onClose()}
            className="rounded-lg px-3.5 py-1.5 text-xs text-zinc-400 transition hover:bg-white/[0.06] hover:text-zinc-200"
          >
            {t('forkCancel')}
          </button>
          <button
            data-id="fork-confirm-send"
            type="button"
            onClick={send}
            disabled={sending || loading || !preview || !prompt.trim()}
            className="flex items-center gap-1.5 rounded-lg bg-blue-500 px-3.5 py-1.5 text-xs font-medium text-white transition hover:bg-blue-400 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {sending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
            {sending ? t('forkSending') : t('forkSendPrompt')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
