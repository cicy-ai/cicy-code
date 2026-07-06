// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useContext } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { OpenUrlContext } from '../contexts';
import { isExternalUrl, assetAbsPathToURL } from '../lib/misc';

export function MarkdownLink({ href, children, ...props }: any) {
  const requestOpenUrl = useContext(OpenUrlContext);
  const url = String(href || '').trim();
  return (
    <a
      {...props}
      data-id="current-history-md-link"
      href={url || undefined}
      className="text-sky-400/90 underline decoration-sky-400/30 underline-offset-2 hover:text-sky-300 hover:decoration-sky-300/60"
      onClick={(e) => {
        if (!url) return;
        e.preventDefault();
        e.stopPropagation();
        if (isExternalUrl(url)) {
          requestOpenUrl?.(url);
        } else {
          // path-like link → open in the workspace editor (FilesView listens)
          window.dispatchEvent(new CustomEvent('cicy:open-file', { detail: { path: url } }));
        }
      }}
    >
      {children}
    </a>
  );
}

export function MarkdownImg({ src, alt, ...props }: any) {
  return <img {...props} data-id="current-history-md-img" src={assetAbsPathToURL(String(src || ''))} alt={alt || ''} />;
}

export const markdownComponents = { a: MarkdownLink, img: MarkdownImg } as const;

// react-markdown 没有内置 memo,每次渲染整篇重新 parse;且 remarkPlugins={[remarkGfm]}
// 内联数组每次都是新引用,就算外面包 memo 也会失效。流式期 live 尾巴每个 tick 重渲染,
// 不 memo 的话所有已完成段落 + thinking 全部跟着整篇重 parse。这里用稳定的 plugins
// 引用 + memo:只有文本真变的那个块才重 parse。
export const REMARK_PLUGINS = [remarkGfm];
export const MarkdownBlock = memo(function MarkdownBlock({ text }: { text: string }) {
  return <Markdown remarkPlugins={REMARK_PLUGINS} components={markdownComponents}>{text}</Markdown>;
});

// Confirm-before-leaving modal for external URLs. Opening goes to a NEW window.
export function LinkConfirmModal({ url, onClose }: { url: string; onClose: () => void }) {
  return (
    <div
      data-id="current-history-link-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4"
      onClick={onClose}
    >
      <div
        data-id="current-history-link-modal-box"
        className="w-full max-w-md overflow-hidden rounded-xl border border-white/[0.08] bg-[#16161a] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div data-id="current-history-link-modal-title" className="border-b border-white/[0.06] px-4 py-3 text-sm font-medium text-zinc-200">打开外部链接?</div>
        <div data-id="current-history-link-modal-url" className="break-all px-4 py-3 font-mono text-xs leading-relaxed text-sky-300/80">{url}</div>
        <div data-id="current-history-link-modal-actions" className="flex justify-end gap-2 border-t border-white/[0.06] px-4 py-3">
          <button
            type="button"
            data-id="current-history-link-modal-cancel"
            onClick={onClose}
            className="rounded-md border border-white/[0.08] px-3 py-1.5 text-xs text-zinc-400 transition-colors hover:bg-white/[0.04] hover:text-zinc-200"
          >取消</button>
          <button
            type="button"
            data-id="current-history-link-modal-open"
            onClick={() => { window.open(url, '_blank', 'noopener,noreferrer'); onClose(); }}
            className="rounded-md bg-sky-500/90 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-sky-500"
          >在新窗口打开</button>
        </div>
      </div>
    </div>
  );
}
