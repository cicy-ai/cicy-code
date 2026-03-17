import { useState } from 'react';
import { X, Trash2, Folder, FolderOpen } from 'lucide-react';
import { useDesktopApps, openInElectron } from '../desktop/useDesktopApps';

interface Props {
  paneId: string;
  codeDrawerOpen: boolean;
  onToggleCodeDrawer: () => void;
}

export default function DesktopCanvas({ paneId, codeDrawerOpen, onToggleCodeDrawer }: Props) {
  const { apps, addApp, removeApp } = useDesktopApps(paneId);
  const [editMode, setEditMode] = useState(false);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; appId: string } | null>(null);

  // Expose addApp globally for desktop events
  (window as any).__desktopAddApp = addApp;

  return (
    <div data-id="desktop-canvas" className="absolute inset-0" onClick={() => { setCtxMenu(null); if (editMode) setEditMode(false); }}>
      {/* Background */}
      <div className="absolute inset-0 bg-gradient-to-br from-[#0f0f1a] via-[#111827] to-[#0c1222]" />
      <div className="absolute inset-0 opacity-[0.03]" style={{ backgroundImage: 'radial-gradient(circle at 1px 1px, white 1px, transparent 0)', backgroundSize: '32px 32px' }} />

      {/* App grid */}
      <div className="absolute inset-0 z-10 p-6 pt-4 flex flex-wrap content-start gap-4 pointer-events-none overflow-y-auto">
        {apps.map(app => app.type === 'widget' ? (
          <div key={app.id} className="pointer-events-auto select-none relative group"
            style={{ width: app.size === 'lg' ? 340 : app.size === 'md' ? 340 : 160, height: app.size === 'lg' ? 340 : app.size === 'md' ? 160 : 140 }}
            onContextMenu={e => { e.preventDefault(); setEditMode(true); }}>
            {editMode && <button onClick={e => { e.stopPropagation(); removeApp(app.id); if (apps.length <= 1) setEditMode(false); }} className="absolute -top-1.5 -left-1.5 z-20 w-5 h-5 rounded-full bg-red-500 flex items-center justify-center shadow-lg hover:bg-red-400"><X size={10} className="text-white" /></button>}
            <div className={`w-full h-full rounded-2xl bg-[#1c1c1e]/80 backdrop-blur-xl border border-white/[0.08] shadow-lg overflow-hidden flex flex-col ${editMode ? 'animate-wiggle' : ''}`}>
              <div className="h-7 flex items-center justify-between px-2.5 shrink-0">
                <span className="text-base text-white/50 truncate flex items-center gap-1">{app.emoji} {app.label}</span>
                {app.url && <button onClick={() => openInElectron(app.url, app.label)} className="text-base text-white/30 hover:text-white/60 opacity-0 group-hover:opacity-100 transition-opacity">↗</button>}
              </div>
              <div className="flex-1 overflow-hidden">
                {app.srcdoc ? <iframe srcDoc={app.srcdoc} className="w-full h-full border-0" sandbox="allow-scripts allow-same-origin" /> : app.url ? <iframe src={app.url} className="w-full h-full border-0" sandbox="allow-scripts allow-same-origin" /> : null}
              </div>
            </div>
          </div>
        ) : (
          <div key={app.id} className={`w-[68px] flex flex-col items-center gap-1.5 select-none pointer-events-auto relative ${editMode ? 'animate-wiggle' : ''}`}
            onClick={() => { if (!editMode) openInElectron(app.url, app.label); }}
            onContextMenu={e => { e.preventDefault(); setEditMode(true); }}>
            {editMode && <button onClick={e => { e.stopPropagation(); removeApp(app.id); if (apps.length <= 1) setEditMode(false); }} className="absolute -top-1 -left-1 z-20 w-5 h-5 rounded-full bg-red-500 flex items-center justify-center shadow-lg hover:bg-red-400"><X size={10} className="text-white" /></button>}
            <div className="w-[52px] h-[52px] rounded-[14px] bg-gradient-to-br from-white/[0.12] to-white/[0.04] backdrop-blur-md flex items-center justify-center text-[28px] shadow-lg shadow-black/20 active:scale-95 transition-all duration-150 border border-white/[0.06]">{app.emoji}</div>
            <span className="text-base text-white/60 truncate w-full text-center leading-tight">{app.label}</span>
          </div>
        ))}
      </div>

      {/* Empty state */}
      {apps.length === 0 && (
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
          <div className="text-center"><div className="text-5xl mb-3 opacity-20">✨</div><div className="text-base text-white/15">Ask your agent to build something</div></div>
        </div>
      )}

      {/* Context menu */}
      {ctxMenu && (
        <div className="fixed z-[99999] bg-[#2a2a2e]/95 backdrop-blur-xl border border-white/[0.08] rounded-xl shadow-2xl py-1 min-w-[130px]" style={{ left: ctxMenu.x, top: ctxMenu.y }} onClick={e => e.stopPropagation()}>
          <button onClick={() => { const a = apps.find(x => x.id === ctxMenu.appId); if (a) openInElectron(a.url, a.label); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-base text-white/80 hover:bg-white/10 rounded-md mx-0.5" style={{ width: 'calc(100% - 4px)' }}>Open</button>
          <div className="h-px bg-white/[0.06] my-0.5 mx-2" />
          <button onClick={() => { removeApp(ctxMenu.appId); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-base text-red-400/80 hover:bg-white/10 rounded-md mx-0.5 flex items-center gap-1.5" style={{ width: 'calc(100% - 4px)' }}><Trash2 size={11} />Remove</button>
        </div>
      )}
    </div>
  );
}
