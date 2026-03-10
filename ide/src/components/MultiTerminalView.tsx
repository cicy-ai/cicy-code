import React, { useState } from 'react';
import { Plus, X, Columns, Rows, Grid } from 'lucide-react';
import { TtydFrame } from './TtydFrame';
import { SplitPaneLayout } from './SplitPaneLayout';

interface Terminal {
  id: string;
  botName: string;
  token: string;
}

interface MultiTerminalViewProps {
  initialBotName: string;
  token: string;
  isInteracting: boolean;
  onInteractionStart: () => void;
  onInteractionEnd: () => void;
  onClose: () => void;
}

type LayoutMode = 'single' | 'horizontal' | 'vertical' | 'grid';

export const MultiTerminalView: React.FC<MultiTerminalViewProps> = ({
  initialBotName,
  token,
  isInteracting,
  onInteractionStart,
  onInteractionEnd,
  onClose
}) => {
  const [terminals, setTerminals] = useState<Terminal[]>([
    { id: '1', botName: initialBotName, token }
  ]);
  const [layoutMode, setLayoutMode] = useState<LayoutMode>('single');
  const [showAddMenu, setShowAddMenu] = useState(false);
  const [newBotName, setNewBotName] = useState('');

  const handleAddTerminal = () => {
    if (!newBotName.trim()) return;
    
    const newTerminal: Terminal = {
      id: Date.now().toString(),
      botName: newBotName.trim(),
      token
    };
    
    setTerminals(prev => [...prev, newTerminal]);
    setNewBotName('');
    setShowAddMenu(false);
    
    // Auto switch to appropriate layout
    if (terminals.length === 1) {
      setLayoutMode('horizontal');
    } else if (terminals.length === 2) {
      setLayoutMode('grid');
    }
  };

  const handleRemoveTerminal = (id: string) => {
    setTerminals(prev => {
      const filtered = prev.filter(t => t.id !== id);
      if (filtered.length === 1) setLayoutMode('single');
      return filtered;
    });
  };

  const renderTerminal = (terminal: Terminal, showClose: boolean = true) => (
    <div key={terminal.id} className="relative w-full h-full">
      <TtydFrame
        url={`/ttyd/${terminal.botName}/?token=${terminal.token}`}
        isInteractingWithOverlay={isInteracting}
      />
      
      {/* Terminal Label */}
      <div className="absolute top-2 left-2 bg-black/70 backdrop-blur-sm px-3 py-1 rounded-full text-xs text-white font-mono z-10">
        {terminal.botName}
      </div>
      
      {/* Close Button */}
      {showClose && terminals.length > 1 && (
        <button
          onClick={() => handleRemoveTerminal(terminal.id)}
          className="absolute top-2 right-2 p-1.5 bg-red-600/80 hover:bg-red-500 text-white rounded-full transition-colors z-10"
        >
          <X size={14} />
        </button>
      )}
    </div>
  );

  const renderLayout = () => {
    if (terminals.length === 0) {
      return (
        <div className="w-full h-full flex items-center justify-center text-vsc-text-muted">
          No terminals
        </div>
      );
    }

    if (terminals.length === 1 || layoutMode === 'single') {
      return renderTerminal(terminals[0], false);
    }

    if (layoutMode === 'horizontal' && terminals.length >= 2) {
      return (
        <SplitPaneLayout
          direction="horizontal"
          onInteractionStart={onInteractionStart}
          onInteractionEnd={onInteractionEnd}
        >
          {renderTerminal(terminals[0])}
          {terminals.length > 2 ? (
            <div className="w-full h-full flex flex-col">
              {terminals.slice(1).map(t => (
                <div key={t.id} className="flex-1 border-t border-vsc-border">
                  {renderTerminal(t)}
                </div>
              ))}
            </div>
          ) : (
            renderTerminal(terminals[1])
          )}
        </SplitPaneLayout>
      );
    }

    if (layoutMode === 'vertical' && terminals.length >= 2) {
      return (
        <SplitPaneLayout
          direction="vertical"
          onInteractionStart={onInteractionStart}
          onInteractionEnd={onInteractionEnd}
        >
          {renderTerminal(terminals[0])}
          {terminals.length > 2 ? (
            <div className="w-full h-full flex">
              {terminals.slice(1).map(t => (
                <div key={t.id} className="flex-1 border-l border-vsc-border">
                  {renderTerminal(t)}
                </div>
              ))}
            </div>
          ) : (
            renderTerminal(terminals[1])
          )}
        </SplitPaneLayout>
      );
    }

    if (layoutMode === 'grid') {
      return (
        <div className="w-full h-full grid grid-cols-2 grid-rows-2 gap-px bg-vsc-border">
          {terminals.slice(0, 4).map(t => renderTerminal(t))}
        </div>
      );
    }

    return renderTerminal(terminals[0]);
  };

  return (
    <div className="relative w-full h-full">
      {renderLayout()}

      {/* Control Bar */}
      <div className="absolute top-4 right-4 z-20 flex gap-2">
        {/* Layout Switcher */}
        {terminals.length > 1 && (
          <div className="flex gap-1 bg-vsc-bg/90 backdrop-blur-sm rounded-lg p-1 border border-vsc-border">
            <button
              onClick={() => setLayoutMode('horizontal')}
              className={`p-2 rounded transition-colors ${
                layoutMode === 'horizontal'
                  ? 'bg-vsc-button text-white'
                  : 'text-vsc-text-secondary hover:bg-vsc-bg-active hover:text-vsc-text'
              }`}
              title="Horizontal Split"
            >
              <Columns size={18} />
            </button>
            
            <button
              onClick={() => setLayoutMode('vertical')}
              className={`p-2 rounded transition-colors ${
                layoutMode === 'vertical'
                  ? 'bg-vsc-button text-white'
                  : 'text-vsc-text-secondary hover:bg-vsc-bg-active hover:text-vsc-text'
              }`}
              title="Vertical Split"
            >
              <Rows size={18} />
            </button>

            {terminals.length >= 3 && (
              <button
                onClick={() => setLayoutMode('grid')}
                className={`p-2 rounded transition-colors ${
                  layoutMode === 'grid'
                    ? 'bg-vsc-button text-white'
                    : 'text-vsc-text-secondary hover:bg-vsc-bg-active hover:text-vsc-text'
                }`}
                title="Grid Layout"
              >
                <Grid size={18} />
              </button>
            )}
          </div>
        )}

        {/* Add Terminal */}
        <div className="relative">
          <button
            onClick={() => setShowAddMenu(!showAddMenu)}
            className="p-2 bg-green-600 hover:bg-green-500 text-white rounded-lg transition-colors"
            title="Add Terminal"
          >
            <Plus size={18} />
          </button>

          {showAddMenu && (
            <div className="absolute top-full right-0 mt-2 bg-vsc-bg border border-vsc-border rounded-lg shadow-2xl p-3 w-64">
              <input
                type="text"
                value={newBotName}
                onChange={(e) => setNewBotName(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleAddTerminal()}
                placeholder="Bot name..."
                className="w-full bg-vsc-bg-secondary border border-vsc-border rounded px-3 py-2 text-white text-sm outline-none focus:ring-2 focus:ring-vsc-accent mb-2"
                autoFocus
              />
              <button
                onClick={handleAddTerminal}
                disabled={!newBotName.trim()}
                className="w-full py-2 bg-vsc-button hover:bg-vsc-button-hover text-white rounded text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                Add Terminal
              </button>
            </div>
          )}
        </div>

        {/* Close Multi-Terminal View */}
        <button
          onClick={onClose}
          className="p-2 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors"
          title="Exit Multi-Terminal Mode"
        >
          <X size={18} />
        </button>
      </div>
    </div>
  );
};
