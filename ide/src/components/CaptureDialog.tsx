import React, { useEffect } from 'react';

interface CaptureDialogProps {
  output: string | null;
  onClose: () => void;
  onRefresh?: (paneId: string | undefined, lines: number) => void;
  isRefreshing?: boolean;
  paneId?: string;
}

export const CaptureDialog: React.FC<CaptureDialogProps> = ({ output, onClose, onRefresh, isRefreshing, paneId }) => {
  const contentRef = React.useRef<HTMLPreElement>(null);
  const [lines, setLines] = React.useState(100);
  const cachedPaneIdRef = React.useRef<string | undefined>(paneId);
  
  // Update cached pane_id when it changes
  React.useEffect(() => {
    if (paneId) {
      cachedPaneIdRef.current = paneId;
    }
  }, [paneId]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && output !== null) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [output, onClose]);

  useEffect(() => {
    if (output && contentRef.current) {
      contentRef.current.scrollTop = contentRef.current.scrollHeight;
    }
  }, [output]);

  if (output === null) return null;

  return (
    <div className="fixed inset-0 flex flex-col bg-vsc-bg" style={{zIndex: 99999999}}>
      <div className="flex items-center justify-between px-4 py-2 border-b border-vsc-border bg-vsc-bg">
        <div className="flex items-center gap-3">
          <span className="text-sm font-semibold text-white">Captured Output</span>
          <div className="flex items-center gap-2">
            <label className="text-xs text-vsc-text-secondary">Lines:</label>
            <input 
              type="number" 
              value={lines} 
              onChange={(e) => setLines(Math.max(1, parseInt(e.target.value) || 10))}
              className="w-20 px-2 py-1 text-xs bg-vsc-bg-secondary text-white border border-vsc-border-subtle rounded"
              min="1"
            />
          </div>
        </div>
        <div className="flex gap-2">
          {onRefresh && (
            <button onClick={() => onRefresh(cachedPaneIdRef.current, lines)} disabled={isRefreshing} className="px-3 py-1 rounded bg-vsc-button hover:bg-vsc-button-hover text-white text-xs disabled:opacity-50">
              {isRefreshing ? '...' : 'Refresh'}
            </button>
          )}
          {paneId && (
            <button 
              onClick={() => {
                const blob = new Blob([output || ''], { type: 'text/plain' });
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = `${paneId}_capture_${Date.now()}.txt`;
                a.click();
                URL.revokeObjectURL(url);
              }}
              className="px-3 py-1 rounded bg-green-600 hover:bg-green-500 text-white text-xs"
            >
              Export
            </button>
          )}
          <button onClick={onClose} className="px-3 py-1 rounded bg-red-600 hover:bg-red-500 text-white text-xs">Close</button>
        </div>
      </div>
      <pre ref={contentRef} className="flex-1 overflow-auto p-4 text-xs text-green-400 font-mono whitespace-pre-wrap break-all bg-vsc-bg">
        {output || '(empty)'}
      </pre>
    </div>
  );
};
