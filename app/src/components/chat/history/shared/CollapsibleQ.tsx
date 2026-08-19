// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useContext, useState } from 'react';
import { User, Copy, Check, ChevronUp } from 'lucide-react';
import { QAlignContext } from '../contexts';
import { cn } from '../../../../lib/utils';
import { splitLeadingHarnessBlocks, parseEnvironmentContext } from '../lib/normalizeItem';
import { MarkdownBlock } from './Markdown';
import { SystemNoticeCard, EnvironmentContextCard } from './notices';

// 用户轮的左侧头像(居左布局时用):一个用户 icon 的圆形头像,与 assistant 头像同尺寸/
// 顶对齐,使问答两列头像对齐成一条线。
export function UserTurnAvatar({ className }: { className?: string } = {}) {
  return (
    <div data-id="current-history-user-avatar" className={cn('mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-sky-500/15 text-sky-300', className)}>
      <User className="h-4 w-4" />
    </div>
  );
}

export function CollapsibleQ({ text, bare = false, open = false, onSetOpen }: { text: string; bare?: boolean; open?: boolean; onSetOpen?: (v: boolean) => void }) {
  const qAlign = useContext(QAlignContext);
  const qJustify = qAlign === 'left' ? 'justify-start' : 'justify-end';
  const qTail = qAlign === 'left' ? 'rounded-bl-sm' : 'rounded-br-sm';
  const [qCopied, setQCopied] = useState(false);
  const copyQ = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const value = String(text || '');
    const done = () => { setQCopied(true); window.setTimeout(() => setQCopied(false), 1200); };
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(value).then(done).catch(() => {
        try { const ta = document.createElement('textarea'); ta.value = value; ta.style.position = 'fixed'; ta.style.opacity = '0'; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta); done(); } catch { /* ignore */ }
      });
    } else {
      try { const ta = document.createElement('textarea'); ta.value = value; ta.style.position = 'fixed'; ta.style.opacity = '0'; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta); done(); } catch { /* ignore */ }
    }
  }, [text]);
  // Peel leading harness blocks (system-reminder / command echoes) into a small
  // collapsed fold, then render the real question below. Recurse on the rest so
  // the existing env-context / xml-block / bubble logic runs on the clean text.
  // `bare` (prompts-only): drop the harness fold entirely — show ONLY the clean
  // question, never a folded system card.
  const { blocks: harnessBlocks, remaining: afterHarness } = splitLeadingHarnessBlocks(text);
  if (harnessBlocks.length) {
    if (bare) {
      return afterHarness ? <CollapsibleQ text={afterHarness} bare /> : null;
    }
    return (
      <div data-id="current-history-view-q-harness" className="mb-2.5 flex flex-col gap-1.5">
        <SystemNoticeCard text={harnessBlocks.join('\n\n')} />
        {afterHarness ? <CollapsibleQ text={afterHarness} /> : null}
      </div>
    );
  }
  const environmentContext = parseEnvironmentContext(text);
  let remaining = text;
  const xmlBlocks: string[] = [];
  while (/^<[\w-]+>[\s\S]*?<\/[\w-]+>/.test(remaining)) {
    const match = remaining.match(/^<[\w-]+>[\s\S]*?<\/[\w-]+>/);
    if (!match) break;
    xmlBlocks.push(match[0]);
    remaining = remaining.slice(match[0].length).trim();
  }
  if (xmlBlocks.length) {
    return (
      <div data-id="current-history-view-q-xml" className={`mb-2.5 flex ${qJustify}`}>
        <div data-id="current-history-view-q-xml-wrap" className="max-w-[95%] flex flex-col gap-2">
          <pre data-id="current-history-view-q-xml-block" className="overflow-x-auto rounded-lg border border-sky-300/[0.12] bg-black/[0.25] px-3 py-2 font-mono text-xs leading-relaxed text-sky-100/70 whitespace-pre-wrap">{xmlBlocks.join('\n')}</pre>
          {remaining ? (
            <div data-id="current-history-view-q-xml-trailing" className={`overflow-hidden rounded-2xl ${qTail} border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]`}>
              <MarkdownBlock text={remaining} />
            </div>
          ) : null}
        </div>
      </div>
    );
  }
  // Copy button + (only when the answer is expanded) a collapse chevron, grouped.
  // Shown on hover beside the question bubble. Collapse lives here so the bubble
  // itself is expand-only.
  const qControls = (
    <div className="mb-0.5 flex shrink-0 items-center gap-0.5">
      <button type="button" data-id="current-history-view-q-copy" onClick={copyQ} title="复制" aria-label="复制"
        className="inline-flex h-6 w-6 items-center justify-center rounded-md text-sky-200/50 opacity-0 transition-opacity hover:text-sky-100 group-hover:opacity-100">
        {qCopied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
      {open && onSetOpen ? (
        <button type="button" data-id="current-history-view-q-collapse" onClick={(e) => { e.stopPropagation(); onSetOpen(false); }} title="收起回复" aria-label="收起回复"
          className="inline-flex h-6 w-6 items-center justify-center rounded-md text-sky-200/50 opacity-0 transition-opacity hover:text-sky-100 group-hover:opacity-100">
          <ChevronUp className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  );
  return (
    <div data-id="current-history-view-q" className={`group mb-2.5 flex items-end gap-1 ${qJustify}`}>
      {environmentContext ? (
        <EnvironmentContextCard context={environmentContext} />
      ) : (
        <>
          {/* Copy button OUTSIDE the bubble so it never overlaps the text. Sits on
              the bubble's left for right-aligned questions, right for left-aligned. */}
          {qAlign !== 'left' && qControls}
          {/* Isolate pointer/click from the parent card so selecting text in the
              bubble never bubbles up to a parent handler (which was toggling history). */}
          <div
            data-id="current-history-view-q-body"
            onClick={(e) => { e.stopPropagation(); if (!open && onSetOpen) onSetOpen(true); }}
            onMouseDown={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
            className={`max-w-[95%] select-text overflow-hidden rounded-2xl ${qTail} border border-[var(--chat-question-border)] bg-[var(--chat-question-bg)] px-3.5 py-2 text-base leading-relaxed text-zinc-200 shadow-[0_8px_24px_rgba(0,0,0,0.10)]`}
          >
            <MarkdownBlock text={String(text || '').replace(/^\-\n/, '')} />
          </div>
          {qAlign === 'left' && qControls}
        </>
      )}
    </div>
  );
}
