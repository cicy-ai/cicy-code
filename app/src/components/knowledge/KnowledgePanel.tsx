import { lazy, Suspense, useState } from 'react';
import { FolderTree, Share2 } from 'lucide-react';
import FilesView from '../files/FilesView';

const KnowledgeGraphView = lazy(() => import('./KnowledgeGraphView'));

interface KnowledgePanelProps {
  agentId: string;
  /** Absolute workspace folder (required by FilesView; the tree itself is
   *  anchored to the "knowledge" fs root via scopeRoot). */
  workspaceFolder: string;
  /** Page chat-ws client id — forwarded so "send to agent" can route the path
   *  back to this tab. FilesView only registers the :code-ext editor bridge when
   *  NOT scoped, so passing it here is safe (no bridge collision). */
  pageClientId?: string;
}

// KnowledgePanel hosts the team knowledge store (~/cicy-ai/knowledge) two ways:
//   files — a scoped FileExplorer (browse/edit the markdown entries)
//   graph — a bipartite tag force-graph (entries ↔ tags), click a node to read it.
export default function KnowledgePanel({ agentId, workspaceFolder, pageClientId }: KnowledgePanelProps) {
  const [view, setView] = useState<'files' | 'graph'>('files');
  const tab = (key: 'files' | 'graph', icon: React.ReactNode, label: string) => (
    <button
      data-id={`knowledge-panel-tab-${key}`}
      type="button"
      onClick={() => setView(key)}
      className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium transition-colors ${
        view === key ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
      }`}
    >
      {icon}{label}
    </button>
  );
  return (
    <div data-id="knowledge-panel" className="flex h-full w-full flex-col bg-[#0b0b0d]">
      <div
        data-id="knowledge-panel-header"
        className="flex h-9 shrink-0 items-center justify-between gap-2 border-b border-[var(--vsc-border)] px-2"
      >
        <div className="flex items-center gap-1">
          {tab('files', <FolderTree className="h-3.5 w-3.5" />, '文件')}
          {tab('graph', <Share2 className="h-3.5 w-3.5" />, '图谱')}
        </div>
        <span data-id="knowledge-panel-title" className="truncate pr-1 text-[11px] text-zinc-500">
          团队知识库 · <span className="text-amber-400/80">_inbox</span> 待评审 · &lt;域&gt;/ 正典
        </span>
      </div>
      <div data-id="knowledge-panel-body" className="min-h-0 flex-1">
        {view === 'files' ? (
          <FilesView
            agentId={agentId}
            workspaceFolder={workspaceFolder}
            pageClientId={pageClientId}
            scopeRoot="knowledge"
            className="h-full w-full"
          />
        ) : (
          <Suspense fallback={<div className="flex h-full items-center justify-center text-[12px] text-zinc-600">加载图谱…</div>}>
            <KnowledgeGraphView className="h-full w-full" />
          </Suspense>
        )}
      </div>
    </div>
  );
}
