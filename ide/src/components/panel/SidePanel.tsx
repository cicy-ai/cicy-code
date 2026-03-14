import React, { useState } from 'react';

interface Tab {
  id: string;
  label: string;
  content: React.ReactNode;
}

interface SidePanelProps {
  tabs: Tab[];
  defaultTab?: string;
}

const SidePanel: React.FC<SidePanelProps> = ({ tabs, defaultTab }) => {
  const [active, setActive] = useState(defaultTab || tabs[0]?.id || '');
  const current = tabs.find(t => t.id === active);

  return (
    <div className="h-full flex flex-col bg-vsc-bg border-l border-white/[0.06]">
      {/* Tab bar */}
      <div className="flex items-center px-1 h-9 shrink-0 border-b border-white/[0.04] bg-white/[0.01]">
        {tabs.map(t => (
          <button
            key={t.id}
            onClick={() => setActive(t.id)}
            className={`px-2.5 py-1 text-[11px] rounded-md transition-all ${active === t.id ? 'text-white bg-white/[0.08] font-medium' : 'text-vsc-text-muted hover:text-white hover:bg-white/[0.04]'}`}
          >
            {t.label}
          </button>
        ))}
      </div>
      {/* Content */}
      <div className="flex-1 overflow-hidden">{current?.content}</div>
    </div>
  );
};

export default SidePanel;
