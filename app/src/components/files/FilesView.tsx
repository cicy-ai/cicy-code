import { useCallback, useState } from 'react';
import FileExplorer from './FileExplorer';
import CodeEditor from './CodeEditor';
import { FsEntry } from './api';

interface FilesViewProps {
  agentId: string;
  /** Absolute workspace folder, e.g. ~/cicy-ai/workers/w-10001 expanded. */
  workspaceFolder: string;
  className?: string;
}

/**
 * MVP layout: left explorer, right single editor.
 * Multi-tab and search are layered on top in v1.
 */
export default function FilesView({ agentId, workspaceFolder, className }: FilesViewProps) {
  const [activePath, setActivePath] = useState<string>('');

  const handleOpenFile = useCallback((path: string, _entry: FsEntry) => {
    setActivePath(path);
  }, []);

  return (
    <div className={`flex h-full ${className || ''}`}>
      <div className="w-64 shrink-0 border-r border-zinc-800 bg-zinc-950">
        <FileExplorer
          agentId={agentId}
          workspaceFolder={workspaceFolder}
          activePath={activePath}
          onOpenFile={handleOpenFile}
        />
      </div>
      <div className="flex-1 min-w-0">
        <CodeEditor
          agentId={agentId}
          path={activePath}
          workspaceFolder={workspaceFolder}
        />
      </div>
    </div>
  );
}
