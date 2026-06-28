import FilesView from '../files/FilesView';

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

// KnowledgePanel hosts the team knowledge store (~/cicy-ai/knowledge) as a
// scoped FileExplorer: browse/edit the markdown entries (_inbox = pending,
// <domain>/ = canon, _archive/ = rejected).
export default function KnowledgePanel({ agentId, workspaceFolder, pageClientId }: KnowledgePanelProps) {
  return (
    <div data-id="knowledge-panel" className="flex h-full w-full flex-col bg-[#0b0b0d]">
      <div
        data-id="knowledge-panel-header"
        className="flex h-9 shrink-0 items-center border-b border-[var(--vsc-border)] px-3"
      >
        <span data-id="knowledge-panel-title" className="truncate text-[11px] text-zinc-500">
          团队知识库 · <span className="text-amber-400/80">_inbox</span> 待评审 · &lt;域&gt;/ 正典 · docs/ 企业文档
        </span>
      </div>
      <div data-id="knowledge-panel-files" className="min-h-0 flex-1">
        <FilesView
          agentId={agentId}
          workspaceFolder={workspaceFolder}
          pageClientId={pageClientId}
          scopeRoot="knowledge"
          className="h-full w-full"
        />
      </div>
    </div>
  );
}
