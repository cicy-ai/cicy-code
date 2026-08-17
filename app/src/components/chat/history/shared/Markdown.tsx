// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useContext, useState, useEffect } from 'react';
import type { MouseEvent } from 'react';
import { createPortal } from 'react-dom';
import { Download, FileText } from 'lucide-react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { OpenUrlContext } from '../contexts';
import { isExternalUrl, assetAbsPathToURL } from '../lib/misc';

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'heic', 'heif']);
const AUDIO_EXTENSIONS = new Set(['mp3', 'wav', 'ogg', 'opus', 'm4a', 'aac', 'flac']);
const VIDEO_EXTENSIONS = new Set(['mp4', 'mov', 'm4v', 'avi', 'mkv', 'webm', 'ogv']);

function attachmentInfo(href: string) {
  const source = String(href || '').trim();
  const url = assetAbsPathToURL(source);
  const isAttachment = source.includes('/cicy-ai/assets/') || url.startsWith('/assets/files/');
  const cleanPath = source.split(/[?#]/, 1)[0];
  const filename = decodeURIComponent(cleanPath.split('/').pop() || 'attachment');
  const extension = filename.includes('.') ? filename.split('.').pop()!.toLowerCase() : '';
  const media = IMAGE_EXTENSIONS.has(extension)
    ? 'image'
    : AUDIO_EXTENSIONS.has(extension)
      ? 'audio'
      : VIDEO_EXTENSIONS.has(extension)
        ? 'video'
        : 'file';
  return { source, url, filename, media, isAttachment };
}

function AttachmentLink({ href, children }: { href: string; children: any }) {
  const info = attachmentInfo(href);
  const openFile = (event: MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    window.dispatchEvent(new CustomEvent('cicy:open-file', { detail: { path: info.source } }));
  };
  if (info.media === 'image') return (
    <span data-id="current-history-attachment" className="my-2 block w-fit max-w-full overflow-hidden rounded-lg">
      <span data-id="current-history-attachment-image" className="block">
        <MarkdownImg src={info.source} alt={info.filename} reserveSpace />
      </span>
    </span>
  );
  return (
    <span data-id="current-history-attachment" className="my-2 block max-w-2xl overflow-hidden rounded-xl border border-white/[0.07] bg-white/[0.025]">
      {info.media === 'audio' ? (
        <span data-id="current-history-attachment-audio" className="block px-3 pt-3">
          <audio data-id="current-history-attachment-audio-player" src={info.url} controls preload="metadata" className="w-full" />
        </span>
      ) : null}
      {info.media === 'video' ? (
        <span data-id="current-history-attachment-video" className="block px-2 pt-2">
          <video data-id="current-history-attachment-video-player" src={info.url} controls preload="metadata" className="aspect-video max-h-96 w-full cursor-zoom-in rounded-lg bg-black object-contain" onClick={(event) => { event.stopPropagation(); void event.currentTarget.requestFullscreen?.(); }} />
        </span>
      ) : null}
      <span data-id="current-history-attachment-actions" className="flex min-w-0 items-center gap-2 px-3 py-2">
        <FileText data-id="current-history-attachment-file-icon" className="h-4 w-4 shrink-0 text-zinc-500" />
        <button
          type="button"
          data-id="current-history-attachment-open"
          onClick={openFile}
          className="min-w-0 flex-1 truncate text-left text-sm text-sky-400/90 underline decoration-sky-400/30 underline-offset-2 hover:text-sky-300"
          title={info.source}
        >
          {children || info.filename}
        </button>
        <a
          data-id="current-history-attachment-download"
          href={info.url}
          download={info.filename}
          onClick={(event) => event.stopPropagation()}
          className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.04] px-2.5 text-xs text-zinc-300 hover:bg-white/[0.08] hover:text-white"
          title="下载"
        >
          <Download className="h-3.5 w-3.5" /> 下载
        </a>
      </span>
    </span>
  );
}

export function MarkdownLink({ href, children, ...props }: any) {
  const requestOpenUrl = useContext(OpenUrlContext);
  const url = String(href || '').trim();
  if (attachmentInfo(url).isAttachment) {
    return <AttachmentLink href={url}>{children}</AttachmentLink>;
  }
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

export function MarkdownImg({ src, alt, reserveSpace = false, ...props }: any) {
  const [zoom, setZoom] = useState(false);
  const url = assetAbsPathToURL(String(src || ''));
  useEffect(() => {
    if (!zoom) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setZoom(false); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [zoom]);
  return (
    <>
      <img
        {...props}
        data-id="current-history-md-img"
        src={url}
        alt={alt || ''}
        loading="lazy"
        className={reserveSpace
          ? 'h-64 w-full cursor-zoom-in rounded-lg bg-black/10 object-contain'
          : 'h-auto max-h-80 max-w-full cursor-zoom-in rounded-lg object-contain'}
        onClick={(e) => { e.preventDefault(); e.stopPropagation(); setZoom(true); }}
      />
      {zoom && createPortal(
        <div
          data-id="current-history-md-img-lightbox"
          className="fixed inset-0 z-[2147483647] flex cursor-zoom-out items-center justify-center bg-black/80 p-6 backdrop-blur-sm"
          onClick={() => setZoom(false)}
        >
          <img src={url} alt={alt || ''} className="max-h-full max-w-full rounded-md object-contain shadow-2xl" />
          <div
            data-id="current-history-md-img-actions"
            className="absolute right-4 top-4 flex items-center gap-2"
            onClick={(e) => e.stopPropagation()}
          >
            <a
              data-id="current-history-md-img-download"
              href={url}
              download={alt || 'image'}
              title="下载"
              className="flex h-9 items-center gap-1.5 rounded-lg bg-white/10 px-3 text-sm text-zinc-100 ring-1 ring-white/15 backdrop-blur hover:bg-white/20"
            >
              <Download className="h-4 w-4" /> 下载
            </a>
            <button
              data-id="current-history-md-img-close"
              type="button"
              onClick={() => setZoom(false)}
              title="关闭 (Esc)"
              className="flex h-9 w-9 items-center justify-center rounded-lg bg-white/10 text-lg leading-none text-zinc-100 ring-1 ring-white/15 backdrop-blur hover:bg-white/20"
            >
              ✕
            </button>
          </div>
        </div>,
        document.body,
      )}
    </>
  );
}

export const markdownComponents = { a: MarkdownLink, img: MarkdownImg } as const;

// react-markdown 没有内置 memo,每次渲染整篇重新 parse;且 remarkPlugins={[remarkGfm]}
// 内联数组每次都是新引用,就算外面包 memo 也会失效。流式期 live 尾巴每个 tick 重渲染,
// 不 memo 的话所有已完成段落 + thinking 全部跟着整篇重 parse。这里用稳定的 plugins
// 引用 + memo:只有文本真变的那个块才重 parse。
export const REMARK_PLUGINS = [remarkGfm];
export const MarkdownBlock = memo(function MarkdownBlock({ text }: { text: string }) {
  const previewable = String(text || '').replace(/\(file:\/\/(\/?[^)]+)\)/g, (_match, path: string) => `(/${path.replace(/^\/+/, '')})`);
  return <Markdown remarkPlugins={REMARK_PLUGINS} components={markdownComponents}>{previewable}</Markdown>;
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
