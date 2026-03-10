import React, { useState } from 'react';
import apiService from '../services/api';

export const CaptureModal: React.FC<{ paneId: string; onClose: () => void }> = ({ paneId, onClose }) => {
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  React.useEffect(() => {
    apiService.capturePane(paneId, 50).then(({ data }) => {
      setContent(typeof data === 'string' ? data : data.output || data.content || JSON.stringify(data, null, 2));
    }).catch(e => setContent(`Error: ${e.message}`)).finally(() => setLoading(false));
  }, [paneId]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className="bg-[#1e1e1e] border border-vsc-border rounded-lg w-[700px] max-h-[80vh] flex flex-col" onClick={e => e.stopPropagation()}>
        <div className="flex justify-between items-center px-4 py-2 border-b border-vsc-border">
          <span className="text-vsc-text text-sm font-medium">📋 {paneId}</span>
          <button onClick={onClose} className="text-vsc-text-muted hover:text-vsc-text text-lg">✕</button>
        </div>
        <pre className="flex-1 overflow-auto p-4 text-xs text-green-300 font-mono whitespace-pre-wrap">
          {loading ? 'Loading...' : content}
        </pre>
      </div>
    </div>
  );
};
