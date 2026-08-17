// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useEffect, useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import Markdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import { markdownComponents } from './Markdown';
import {
  isPatchText,
  parseToolInput,
  toolEditDiff,
  toolHeadline,
  toolBodyContent,
  cleanToolResult,
  formatToolResult,
  formatToolArg,
  shortenToolPath,
  normalizeToolForDisplay,
} from '../lib/toolFormat';

// Remembers each tool card's expand/collapse state across remounts, keyed by a
// unique per-card id. Module-scoped, so it outlives every unmount and grows one
// entry per card the user ever toggles — unbounded over a long session. Cap it
// LRU; an evicted (old) card just falls back to its default collapsed state when
// it remounts, which is harmless.
const toolCardOpenState = new Map<string, boolean>();
const TOOL_CARD_OPEN_STATE_MAX = 500;

function setToolCardOpen(toolId: string, open: boolean) {
  toolCardOpenState.delete(toolId); // re-insert at the tail for LRU ordering
  toolCardOpenState.set(toolId, open);
  let excess = toolCardOpenState.size - TOOL_CARD_OPEN_STATE_MAX;
  if (excess > 0) {
    for (const k of toolCardOpenState.keys()) {
      if (excess-- <= 0) break;
      toolCardOpenState.delete(k);
    }
  }
}

export function renderPatchLine(line: string, index: number) {
  const commonClass = 'px-3 py-0.5 font-mono text-sm leading-relaxed whitespace-pre';
  if (line.startsWith('*** Begin Patch') || line.startsWith('*** End Patch')) {
    return null;
  }
  if (line.startsWith('+')) {
    return <div key={index} data-id={`current-history-view-patch-line-add-${index}`} className={`${commonClass} bg-emerald-500/[0.08] text-emerald-300/85`}>{line}</div>;
  }
  if (line.startsWith('-')) {
    return <div key={index} data-id={`current-history-view-patch-line-remove-${index}`} className={`${commonClass} bg-red-500/[0.08] text-red-300/85`}>{line}</div>;
  }
  if (line.startsWith('@@')) {
    return null;
  }
  if (line.startsWith('*** Update File:')) {
    return <div key={index} data-id={`current-history-view-patch-line-update-${index}`} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line.replace('*** Update File:', 'Update:')}</div>;
  }
  if (line.startsWith('*** ')) {
    return <div key={index} data-id={`current-history-view-patch-line-marker-${index}`} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line}</div>;
  }
  return <div key={index} data-id={`current-history-view-patch-line-context-${index}`} className={`${commonClass} text-zinc-400/80`}>{line}</div>;
}

export function ShellCommandBlock({ text }: { text: string }) {
  const content = String(text || '').trim();
  if (!content) {
    return null;
  }
  return (
    <div data-id="current-history-view-shell-command" className="chat-markdown current-history-markdown">
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={markdownComponents}>
        {`~~~bash\n${content}\n~~~`}
      </Markdown>
    </div>
  );
}

// memo:WS 直推下 live 尾巴每个 delta 都重渲染一次,工具卡的 body 解析(input/diff/
// result 格式化)不便宜;WS 追加路径只换最后一个生长中的 step 对象,其余 step 的
// tool 引用不变 → memo 直接命中,只有真变的卡才重算。
export const ToolCard = memo(function ToolCard({ tool, toolId, running }: { tool: any; toolId: string; running?: boolean }) {
  const { t } = useTranslation('chat');
  const [open, setOpen] = useState(() => toolCardOpenState.get(toolId) ?? false);
  const normalizedTool = normalizeToolForDisplay(tool);
  const effectiveTool = normalizedTool || tool;
  const toolName = String(effectiveTool?.name || '').trim();
  const isError = tool?.isError === true;
  const input = parseToolInput(effectiveTool);
  const editDiff = toolEditDiff(effectiveTool);
  const hasDiff = !!editDiff && !!(editDiff.old || editDiff.new);
  const patchArg = String(effectiveTool?.arg || '');
  const showPatchArg = open && patchArg && isPatchText(patchArg);
  // 用户关心的:是哪个文件 / 什么命令 / 什么 pattern —— 永远显示在头部
  const headline = toolHeadline(effectiveTool);
  const command = input ? String(input.command ?? input.cmd ?? '').trim() : '';
  const bodyContent = toolBodyContent(effectiveTool);
  const displayResult = cleanToolResult(formatToolResult(effectiveTool));
  // For a short single-line command the headline already shows it in full, so
  // repeating it in the expanded body is pure duplication ("rm -rf rt/r2" twice).
  // BUT only suppress it when the body has something ELSE to show (a result,
  // diff, or content) — otherwise expanding would render an empty body and the
  // card looks like the click did nothing. So: redundant only if there's a body.
  const hasOtherBody = !!displayResult || hasDiff || !!bodyContent || showPatchArg;
  const commandRedundant = hasOtherBody && !!command && !command.includes('\n') && command.length <= 80 && shortenToolPath(command) === headline;
  // 兜底参数:只在没有 command / content / diff / patch 这些专门渲染时,才平铺剩余参数(如 Grep/Glob/Task)
  const genericArg = (!command && !bodyContent && !hasDiff && !showPatchArg) ? formatToolArg(effectiveTool) : '';
  const historyId = Number(tool?.history_id || 0);
  const stepIndex = Number(tool?.step_index || 0);
  const toolIndex = Number(tool?.tool_index || 0);

  useEffect(() => {
    setOpen(toolCardOpenState.get(toolId) ?? false);
  }, [toolId]);

  const toggleOpen = () => {
    setOpen((value) => {
      const next = !value;
      setToolCardOpen(toolId, next);
      return next;
    });
  };

  // 大段文字一律横向滚动、不换行(whitespace-pre,绝不 break-all)
  const scrollBlock = 'mx-2 mb-2 max-h-[280px] overflow-auto rounded bg-black/[0.18] px-2.5 py-1.5 font-mono text-xs leading-relaxed whitespace-pre';

  return (
    <div
      data-id="current-history-tool-card"
      data-tool-id={toolId}
      data-history-id={historyId > 0 ? String(historyId) : ''}
      data-step-index={String(stepIndex)}
      data-tool-index={String(toolIndex)}
      data-tool-name={toolName || 'tool'}
      data-open={open ? 'true' : 'false'}
      data-tool-state={isError ? 'error' : running ? 'running' : 'done'}
      className={`overflow-hidden rounded-lg border ${isError ? 'border-red-400/[0.18] bg-red-950/[0.14]' : 'border-emerald-300/[0.08] bg-emerald-950/[0.12]'}`}
    >
      <div
        data-id="current-history-tool-toggle"
        className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-zinc-500 hover:bg-white/[0.025] hover:text-zinc-400"
        onClick={toggleOpen}
      >
        {isError ? (
          <span data-id="current-history-tool-toggle-status" className="shrink-0 text-xs text-red-400/90">✗</span>
        ) : running ? (
          <span data-id="current-history-tool-toggle-status" className="shrink-0 text-xs text-amber-300/80 animate-pulse">●</span>
        ) : (
          <span data-id="current-history-tool-toggle-status" className="shrink-0 text-xs text-emerald-400/70">✓</span>
        )}
        <span data-id="current-history-tool-toggle-name" className="shrink-0 rounded border border-white/[0.04] bg-white/[0.035] px-1.5 py-0.5 text-xs text-zinc-300">{toolName || 'tool'}</span>
        {headline ? (
          <span data-id="current-history-tool-toggle-arg-preview" className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-400/90" title={headline}>{headline}</span>
        ) : <span data-id="current-history-tool-toggle-spacer" className="flex-1" />}
        <span data-id="current-history-tool-toggle-expand" className="shrink-0 text-zinc-600" aria-label={open ? t('collapse') : t('expand')}>
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        </span>
      </div>
      {open ? (
        <>
          {showPatchArg ? (
            <div data-id="current-history-tool-arg" className="border-t border-white/[0.04] overflow-x-auto overflow-y-hidden">
              <div data-id="current-history-tool-arg-patch" className="min-w-max">
                {patchArg.split('\n').map((line: string, index: number) => renderPatchLine(line, index))}
              </div>
            </div>
          ) : (command && !commandRedundant) ? (
            <div data-id="current-history-tool-arg" className="border-t border-white/[0.04] px-3 py-1.5">
              <ShellCommandBlock text={command} />
            </div>
          ) : bodyContent ? (
            <pre data-id="current-history-tool-arg" className={`${scrollBlock} mt-2 text-zinc-400/85`}>{bodyContent}</pre>
          ) : genericArg ? (
            <pre data-id="current-history-tool-arg" className={`${scrollBlock} mt-2 text-zinc-400/85`}>{shortenToolPath(genericArg)}</pre>
          ) : null}
          {hasDiff && editDiff ? (
            <div data-id="current-history-tool-result" className="mx-2 mb-2 max-h-[300px] overflow-auto rounded border border-white/[0.06] font-mono text-xs">
              {editDiff.old ? editDiff.old.split('\n').map((line: string, index: number) => <div key={`old-${index}`} data-id={`current-history-tool-diff-old-${index}`} className="bg-red-500/[0.08] px-2 leading-relaxed whitespace-pre text-red-400/80">- {line}</div>) : null}
              {editDiff.new ? editDiff.new.split('\n').map((line: string, index: number) => <div key={`new-${index}`} data-id={`current-history-tool-diff-new-${index}`} className="bg-emerald-500/[0.08] px-2 leading-relaxed whitespace-pre text-emerald-400/80">+ {line}</div>) : null}
            </div>
          ) : displayResult ? (
            <pre data-id="current-history-tool-result" className={`${scrollBlock} ${isError ? 'bg-red-500/[0.06] text-red-300/80' : 'bg-emerald-500/[0.04] text-emerald-300/70'}`}>{displayResult}</pre>
          ) : null}
        </>
      ) : null}
    </div>
  );
});
