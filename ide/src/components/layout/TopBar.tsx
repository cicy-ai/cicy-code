import React from 'react';
import { ArrowLeft, RefreshCw, RotateCcw } from 'lucide-react';

interface TopBarProps {
  title: string;
  paneId: string;
  status?: string;
  onBack: () => void;
  onRestart?: () => void;
  isRestarting?: boolean;
}

const statusColor: Record<string, string> = {
  thinking: 'bg-yellow-400 animate-pulse',
  idle: 'bg-emerald-400',
};

const TopBar: React.FC<TopBarProps> = ({ title, paneId, status, onBack, onRestart, isRestarting }) => (
  <div className="h-10 bg-vsc-bg-secondary border-b border-vsc-border flex items-center justify-between px-3 shrink-0">
    <div className="flex items-center gap-2 min-w-0">
      <button onClick={onBack} className="p-1 rounded hover:bg-vsc-bg text-vsc-text-secondary hover:text-white">
        <ArrowLeft size={16} />
      </button>
      <span className={`w-2 h-2 rounded-full ${statusColor[status || ''] || 'bg-gray-500'}`} />
      <span className="text-sm text-white font-medium truncate">{title}</span>
      <span className="text-xs text-vsc-text-muted">({paneId})</span>
      <span className="text-xs text-vsc-text-muted ml-2">v0.2.1</span>
    </div>
    <div className="flex items-center gap-1">
      {onRestart && (
        <button onClick={onRestart} disabled={isRestarting} className="p-1 rounded hover:bg-vsc-bg text-vsc-text-secondary hover:text-orange-400 disabled:opacity-30" title="Restart agent">
          <RotateCcw size={14} className={isRestarting ? 'animate-spin' : ''} />
        </button>
      )}
    </div>
  </div>
);

export default TopBar;
