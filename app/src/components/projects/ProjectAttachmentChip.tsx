// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Attachment chip shared by the project card composer (pending uploads) and
// the queued-message strip. Images render as a real thumbnail (click → the
// same lightbox the history view uses); everything else is a labelled chip
// with a type icon, the file name and size — not an anonymous 64px square.

import type { SyntheticEvent } from 'react';
import { AlertCircle, FileArchive, FileAudio, FileCode, FileSpreadsheet, FileText, FileVideo, RotateCw, X } from 'lucide-react';
import { MarkdownImg } from '../chat/history/shared/Markdown';

export interface ProjectAttachmentChipItem {
  id: string;
  name: string;
  size: number;
  mediaType?: 'image' | 'video' | 'audio';
  previewURL?: string;
  fileRef?: string;
  status: 'uploading' | 'done' | 'error';
  progress: number;
}

export function formatAttachmentSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function fileIcon(name: string, mediaType?: string) {
  const ext = (name.split('.').pop() || '').toLowerCase();
  const cls = 'h-4 w-4';
  if (mediaType === 'video') return <FileVideo className={cls} />;
  if (mediaType === 'audio') return <FileAudio className={cls} />;
  if (['zip', 'gz', 'tgz', 'tar', 'rar', '7z', 'bz2', 'xz'].includes(ext)) return <FileArchive className={cls} />;
  if (['csv', 'xls', 'xlsx', 'tsv'].includes(ext)) return <FileSpreadsheet className={cls} />;
  if (['js', 'ts', 'tsx', 'jsx', 'go', 'py', 'rs', 'java', 'c', 'h', 'cpp', 'sh', 'json', 'yaml', 'yml', 'toml', 'html', 'css', 'sql'].includes(ext)) return <FileCode className={cls} />;
  return <FileText className={cls} />;
}

function extLabel(name: string): string {
  const ext = (name.split('.').pop() || '').toUpperCase();
  return ext && ext !== name.toUpperCase() ? ext.slice(0, 5) : 'FILE';
}

export default function ProjectAttachmentChip({ attachment, onRemove, onRetry, compact = false, idPrefix = 'project-agent-card-attachment' }: {
  attachment: ProjectAttachmentChipItem;
  onRemove?: () => void;
  onRetry?: () => void;
  // Queued strip: slightly smaller, no remove (the whole queued message is
  // edited / deleted as one unit).
  compact?: boolean;
  // data-id prefix, so the composer strip and the queue strip stay addressable separately.
  idPrefix?: string;
}) {
  const uploading = attachment.status === 'uploading';
  const errored = attachment.status === 'error';
  const size = formatAttachmentSize(attachment.size);
  const isImage = attachment.mediaType === 'image' && Boolean(attachment.previewURL || attachment.fileRef);
  const height = compact ? 'h-14' : 'h-16';
  const stop = (event: SyntheticEvent) => event.stopPropagation();

  const stateOverlay = uploading ? (
    <span data-id="project-attachment-progress" className="absolute inset-0 grid place-items-center rounded-lg bg-black/55 text-[11px] font-medium tabular-nums text-white">
      <span className="relative grid h-8 w-8 place-items-center rounded-full" style={{ background: `conic-gradient(#fbbf24 ${attachment.progress * 3.6}deg, rgba(255,255,255,0.18) 0)` }}>
        <span className="grid h-6 w-6 place-items-center rounded-full bg-black/80">{attachment.progress}</span>
      </span>
    </span>
  ) : errored ? (
    <button
      type="button"
      data-id="project-attachment-retry"
      onClick={(event) => { stop(event); onRetry?.(); }}
      title={onRetry ? '上传失败，点击重试' : '上传失败'}
      className="absolute inset-0 grid place-items-center gap-0.5 rounded-lg bg-red-950/75 text-[10px] text-red-200"
    >
      {onRetry ? <RotateCw className="h-4 w-4" /> : <AlertCircle className="h-4 w-4" />}
      <span>上传失败</span>
    </button>
  ) : null;

  // Remove button: always shown while uploading / errored (the user needs a
  // way out), hover-only once the upload is done to keep the strip calm.
  const removeBtn = onRemove ? (
    <button
      type="button"
      data-id="project-agent-card-attachment-remove"
      aria-label="Remove attachment"
      title="移除"
      onClick={(event) => { stop(event); onRemove(); }}
      className={`absolute -right-1.5 -top-1.5 z-[1] grid h-5 w-5 place-items-center rounded-full bg-zinc-700 text-zinc-100 shadow-md ring-1 ring-black/40 transition hover:bg-red-500 hover:text-white focus:opacity-100 ${uploading || errored ? '' : 'opacity-0 group-hover:opacity-100'}`}
    >
      <X className="h-3 w-3" />
    </button>
  ) : null;

  if (isImage) {
    return (
      <div
        data-id={`${idPrefix}-${attachment.id}`}
        data-kind="image"
        title={[attachment.name, size].filter(Boolean).join(' · ')}
        onPointerDown={stop}
        className={`group relative ${height} w-auto max-w-[9rem] shrink-0 overflow-visible rounded-lg`}
      >
        <span data-id={`${idPrefix}-media`} className={`block ${height} overflow-hidden rounded-lg border border-white/10 bg-white/[0.04] [&_[data-id=current-history-md-img]]:!m-0 [&_[data-id=current-history-md-img]]:!h-full [&_[data-id=current-history-md-img]]:!w-auto [&_[data-id=current-history-md-img]]:!max-w-[9rem] [&_[data-id=current-history-md-img]]:!rounded-lg [&_[data-id=current-history-md-img]]:object-cover`}>
          <MarkdownImg src={attachment.previewURL || attachment.fileRef || ''} alt={attachment.name} />
        </span>
        {/* File name pinned to the bottom edge, visible on hover — so the
            user can tell screenshot-1 from screenshot-2 without opening them. */}
        <span data-id="project-attachment-caption" className="pointer-events-none absolute inset-x-0 bottom-0 truncate rounded-b-lg bg-gradient-to-t from-black/75 to-transparent px-1.5 pb-1 pt-3 text-[10px] leading-none text-white/90 opacity-0 transition group-hover:opacity-100">
          {attachment.name}
        </span>
        {stateOverlay}
        {removeBtn}
      </div>
    );
  }

  return (
    <div
      data-id={`${idPrefix}-${attachment.id}`}
      data-kind="file"
      title={[attachment.name, size].filter(Boolean).join(' · ')}
      onPointerDown={stop}
      className={`group relative flex ${height} w-44 max-w-full shrink-0 items-center gap-2 overflow-visible rounded-lg border border-white/10 bg-white/[0.04] pl-2 pr-2.5`}
    >
      <span className="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-white/[0.06] text-zinc-300">{fileIcon(attachment.name, attachment.mediaType)}</span>
      <span className="flex min-w-0 flex-1 flex-col justify-center gap-0.5">
        <span data-id="project-attachment-name" className="truncate text-[12px] leading-4 text-zinc-200">{attachment.name}</span>
        <span className="truncate text-[10px] leading-3 text-zinc-500">{[extLabel(attachment.name), size].filter(Boolean).join(' · ')}</span>
      </span>
      {stateOverlay}
      {removeBtn}
    </div>
  );
}
