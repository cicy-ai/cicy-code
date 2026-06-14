import { useRef, useState } from 'react';
import { Upload } from 'lucide-react';
import FilesView from '../files/FilesView';
import { fsApi } from '../files/api';

interface KnowledgePanelProps {
  agentId: string;
  /** Absolute workspace folder (required by FilesView; the tree itself is
   *  anchored to the "knowledge" fs root via scopeRoot). */
  workspaceFolder: string;
}

// KnowledgePanel hosts the team knowledge store (~/cicy-ai/knowledge) as a
// scoped FileExplorer: browse/edit the markdown entries (_inbox = pending,
// <domain>/ = canon, _archive/ = rejected) and upload enterprise documents into
// docs/. Note: no pageClientId is passed to FilesView — the ":code-ext" editor
// bridge belongs to the workspace Files tab; this view is for human governance,
// so a second registration would collide.
export default function KnowledgePanel({ agentId, workspaceFolder }: KnowledgePanelProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [note, setNote] = useState('');

  const onPick = () => fileInputRef.current?.click();

  const onFiles = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || []);
    e.target.value = '';
    if (!files.length) return;
    setUploading(true);
    setNote('');
    try {
      for (const f of files) {
        // Empty filename in targetPath ('docs/') keeps the original filename.
        await fsApi.upload(agentId, 'docs/', f, { root: 'knowledge', overwrite: false });
      }
      setNote(`已上传 ${files.length} 个文档到 docs/`);
      // Reveal the last uploaded file so the explorer reloads the docs/ folder.
      window.dispatchEvent(new CustomEvent('cicy:open-file', {
        detail: { path: `docs/${files[files.length - 1].name}` },
      }));
    } catch (err: any) {
      setNote(`上传失败: ${err?.message || err}`);
    } finally {
      setUploading(false);
    }
  };

  return (
    <div data-id="knowledge-panel" className="flex h-full w-full flex-col bg-[#0b0b0d]">
      <div
        data-id="knowledge-panel-header"
        className="flex h-9 shrink-0 items-center justify-between gap-3 border-b border-[var(--vsc-border)] px-3"
      >
        <span data-id="knowledge-panel-title" className="truncate text-[11px] text-zinc-500">
          团队知识库 · <span className="text-amber-400/80">_inbox</span> 待评审 · &lt;域&gt;/ 正典 · docs/ 企业文档
        </span>
        <div className="flex shrink-0 items-center gap-2">
          {note && (
            <span data-id="knowledge-panel-note" className="max-w-[200px] truncate text-[11px] text-zinc-400">
              {note}
            </span>
          )}
          <button
            data-id="knowledge-panel-upload-btn"
            type="button"
            disabled={uploading}
            onClick={onPick}
            className="inline-flex items-center gap-1 rounded-md border border-white/[0.08] px-2 py-1 text-[11px] text-zinc-300 transition-colors hover:bg-white/[0.04] disabled:opacity-50"
          >
            <Upload className="h-3.5 w-3.5" />
            {uploading ? '上传中…' : '上传文档'}
          </button>
        </div>
      </div>
      <input
        ref={fileInputRef}
        data-id="knowledge-panel-file-input"
        type="file"
        multiple
        className="hidden"
        onChange={onFiles}
      />
      <div data-id="knowledge-panel-files" className="min-h-0 flex-1">
        <FilesView
          agentId={agentId}
          workspaceFolder={workspaceFolder}
          scopeRoot="knowledge"
          className="h-full w-full"
        />
      </div>
    </div>
  );
}
