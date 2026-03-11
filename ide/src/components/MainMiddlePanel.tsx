import React, { useState, useRef } from 'react';
import { SplitSquareHorizontal, SplitSquareVertical, XSquare, RefreshCw, MoreVertical, Pin, ExternalLink, Trash2 } from 'lucide-react';
import { CommandPanel, CommandPanelHandle } from './CommandPanel';
import { urls } from '../config';
import apiService from '../services/api';
import { WebFrame } from './WebFrame';
import { useApp } from '../contexts/AppContext';
import { useDialog } from '../contexts/DialogContext';
import { usePane } from '../contexts/PaneContext';

interface MainMiddlePanelProps {
  ttydWidth: number;
  boundAgents: string[];
  mainIframeRef: React.RefObject<HTMLIFrameElement>;
  commandPanelRef: React.RefObject<CommandPanelHandle>;
  pinnedPanes: string[];
  setPinnedPanes: (p: string[]) => void;
  leftWidth: number;
  leftCollapsed: boolean;
  rightCollapsed: boolean;
  onToggleLeft: () => void;
  onToggleRight: () => void;
  drawerOpen?: boolean;
  onToggleDrawer?: () => void;
}

const MainMiddlePanel: React.FC<MainMiddlePanelProps> = ({ ttydWidth, boundAgents, mainIframeRef, commandPanelRef, pinnedPanes, setPinnedPanes, leftWidth, leftCollapsed, rightCollapsed, onToggleLeft, onToggleRight, drawerOpen, onToggleDrawer }) => {
  const { allPanes, paneDetail, api, setPaneDetail, selectPane } = useApp();
  const { confirm: confirmDialog } = useDialog();
  const {
    displayPaneId, displayPaneTitle, token, hasPermission,
    isDragging, setIsDragging, isInteracting, setIsInteracting, commandPanelHeight,
    agentStatus, contextUsage, mouseMode, readOnly, setReadOnly, isRestarting,
    visitedPanes, settings, setSettings,
    networkLatency, networkStatus, toast, setToast,
    handleRestart, handleCapturePane, handleToggleMouse,
    captureOutput, isCapturing,
  } = usePane();

  const [showHistoryOverlay, setShowHistoryOverlay] = useState(false);
  const [historyData, setHistoryData] = useState<{history: string[], onSelect: (cmd: string) => void} | null>(null);
  const [showCorrectionResult, setShowCorrectionResult] = useState(false);
  const [correctionData, setCorrectionData] = useState<[string, string] | null>(null);
  const [showMoreMenu, setShowMoreMenu] = useState(false);
  const [showPromptModal, setShowPromptModal] = useState(false);

  return (
    <div 
      id="main-middle" 
      className="absolute inset-0 border-r border-vsc-border" 
      style={{width: `${ttydWidth}px`, left: `${leftWidth}px`}}
      onMouseLeave={(e) => {
        const target = e.currentTarget.querySelector('.ttyd-mask') as HTMLElement;
        if (target) target.style.display = 'block';
      }}
    >
      <div className="h-10 bg-vsc-bg-titlebar border-b border-vsc-border flex items-center justify-between px-2">
        <div id="main-middle-topbar" className="flex items-center gap-2 flex-1 min-w-0">
          <button onClick={onToggleLeft} className="p-1 rounded text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-active" title={leftCollapsed ? 'Show sidebar' : 'Hide sidebar'}>
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/></svg>
          </button>
          <div className="relative group flex items-center gap-1">
            {displayPaneId && (
              <button
                onClick={() => {
                  const isPinned = pinnedPanes.includes(displayPaneId);
                  const updated = isPinned 
                    ? pinnedPanes.filter((id: string) => id !== displayPaneId)
                    : [...pinnedPanes, displayPaneId];
                  setPinnedPanes(updated);
                  localStorage.setItem('pinnedPanes', JSON.stringify(updated));
                  window.dispatchEvent(new CustomEvent('pinnedPanesChanged'));
                }}
                className="p-1 hover:bg-vsc-bg-hover rounded"
                title={pinnedPanes.includes(displayPaneId) ? 'Unpin' : 'Pin'}
              >
                <Pin className={`w-4 h-4 ${pinnedPanes.includes(displayPaneId) ? 'text-yellow-500 fill-yellow-500' : 'text-vsc-text-secondary'}`} />
              </button>
            )}
            <button className="px-3 py-1 rounded text-sm bg-vsc-button text-vsc-button-text">
              {displayPaneTitle}
            </button>
            <span className="text-xs text-vsc-text-secondary px-2">v0.0.3</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1 text-xs text-vsc-text-secondary">
            <div className={`w-2 h-2 rounded-full ${networkStatus === 'excellent' ? 'bg-green-500' : networkStatus === 'good' ? 'bg-yellow-500' : networkStatus === 'poor' ? 'bg-orange-500' : 'bg-red-500'}`}></div>
            <span>{networkLatency}ms</span>
          </div>
          <button onClick={onToggleRight} className={`p-1 rounded hover:bg-vsc-bg-active ${!rightCollapsed ? 'text-vsc-accent' : 'text-vsc-text-secondary hover:text-vsc-text'}`} title={rightCollapsed ? 'Show right panel' : 'Hide right panel'}>
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/></svg>
            </button>
          <div className="relative">
            <button 
              type="button" 
              onClick={() => setShowMoreMenu(!showMoreMenu)} 
              className="p-1 rounded text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-active transition-colors" 
              title="More"
            >
              <MoreVertical size={16} />
            </button>
            {showMoreMenu && (
              <>
                <div className="fixed inset-0 z-40" onClick={() => setShowMoreMenu(false)}></div>
                <div id="middle-top-dropdown" className="absolute right-0 top-full mt-1 bg-vsc-bg border border-vsc-border rounded shadow-lg z-50 min-w-[180px]">
                  <button type="button" onClick={async () => { const paneId = displayPaneId.replace(':main.0', ''); await apiService.chooseSession(paneId); setShowMoreMenu(false); }} className="w-full px-3 py-2 text-left text-xs text-vsc-text hover:bg-vsc-bg-hover">^bs Choose Session</button>
                  <div className="border-t border-vsc-border my-1"></div>
                  <button type="button" onClick={() => { if (confirm('Reload this page?')) { if (mainIframeRef.current) mainIframeRef.current.src = mainIframeRef.current.src; } setShowMoreMenu(false); }} className="w-full px-3 py-2 text-left text-xs text-vsc-text hover:bg-vsc-bg-hover flex items-center gap-2"><RefreshCw size={12} /> Reload</button>
                  {hasPermission('prompt') && (
                    <button type="button" onClick={() => { handleRestart(); setShowMoreMenu(false); }} className="w-full px-3 py-2 text-left text-xs text-red-400 hover:bg-vsc-bg-hover flex items-center gap-2"><RefreshCw size={12} className={isRestarting ? 'animate-spin' : ''} /> Restart</button>
                  )}
                  <div className="border-t border-vsc-border my-1"></div>
                  <button type="button" onClick={() => { window.open(urls.ttydOpen(displayPaneId, token), '_blank'); setShowMoreMenu(false); }} className="w-full px-3 py-2 text-left text-xs text-vsc-text hover:bg-vsc-bg-hover flex items-center gap-2"><ExternalLink size={12} /> Open in New Tab</button>
                  <button type="button" onClick={() => { confirmDialog(`Remove agent ${displayPaneId}? This will delete the pane.`, async () => { await apiService.deleteAgent(displayPaneId); window.dispatchEvent(new CustomEvent('refresh-panes')); const otherPane = allPanes.find(p => p.pane_id !== displayPaneId); if (otherPane) selectPane(otherPane.pane_id); }); setShowMoreMenu(false); }} className="w-full px-3 py-2 text-left text-xs text-red-400 hover:bg-vsc-bg-hover flex items-center gap-2"><Trash2 size={12} /> Remove Agent</button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
      <MiddleContent
        mainIframeRef={mainIframeRef}
        commandPanelRef={commandPanelRef}
        showHistoryOverlay={showHistoryOverlay} setShowHistoryOverlay={setShowHistoryOverlay}
        historyData={historyData} setHistoryData={setHistoryData}
        showCorrectionResult={showCorrectionResult} setShowCorrectionResult={setShowCorrectionResult}
        correctionData={correctionData} setCorrectionData={setCorrectionData}
        boundAgents={boundAgents}
        onShowPromptModal={() => setShowPromptModal(true)}
      />

      {showPromptModal && <PromptModal onClose={() => setShowPromptModal(false)} />}
    </div>
  );
};

const PromptModal: React.FC<{onClose: () => void}> = ({onClose}) => {
  const { globalVar, updateGlobalVar } = useApp();
  const { setToast } = usePane();
  const [text, setText] = useState(globalVar?.common_prompts || '');

  return (
    <div className="fixed inset-0 bg-black/50 backdrop-blur-sm z-[9999999] flex items-center justify-center" onClick={onClose}>
      <div className="bg-vsc-bg border border-vsc-border rounded-lg shadow-2xl w-[600px] max-h-[80vh] flex flex-col" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-vsc-border">
          <h3 className="text-sm font-semibold text-vsc-text">Common Prompts</h3>
          <button onClick={onClose} className="p-1 rounded text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <div className="flex-1 p-4 overflow-auto">
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Enter common prompts here... Shared across all agents."
            className="w-full h-[400px] bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-3 py-2 focus:outline-none focus:border-vsc-accent resize-none"
          />
        </div>
        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-vsc-border">
          <button onClick={onClose} className="px-4 py-2 text-sm text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover rounded">
            Cancel
          </button>
          <button
            onClick={async () => {
              try {
                await updateGlobalVar({ ...globalVar, common_prompts: text });
                setToast('Prompt saved');
                setTimeout(() => setToast(null), 2000);
                onClose();
              } catch {
                setToast('Failed to save');
                setTimeout(() => setToast(null), 2000);
              }
            }}
            className="px-4 py-2 bg-vsc-button hover:bg-vsc-button-hover text-white text-sm font-medium rounded"
          >
            Save
          </button>
        </div>
      </div>
    </div>
  );
};

export default MainMiddlePanel;

interface MiddleContentProps {
  mainIframeRef: React.RefObject<HTMLIFrameElement>;
  commandPanelRef: React.RefObject<CommandPanelHandle>;
  showHistoryOverlay: boolean; setShowHistoryOverlay: (v: boolean) => void;
  historyData: {history: string[], onSelect: (cmd: string) => void} | null; setHistoryData: (v: any) => void;
  showCorrectionResult: boolean; setShowCorrectionResult: (v: boolean) => void;
  correctionData: [string, string] | null; setCorrectionData: (v: [string, string] | null) => void;
  boundAgents: string[];
  onShowPromptModal: () => void;
}

const MiddleContent: React.FC<MiddleContentProps> = ({
  mainIframeRef, commandPanelRef,
  showHistoryOverlay, setShowHistoryOverlay, historyData, setHistoryData,
  showCorrectionResult, setShowCorrectionResult, correctionData, setCorrectionData,
  boundAgents,
  onShowPromptModal,
}) => {
  const {
    displayPaneId, displayPaneTitle, token, hasPermission,
    isDragging, setIsDragging, isInteracting, setIsInteracting, commandPanelHeight,
    agentStatus, contextUsage, mouseMode, readOnly, setReadOnly, isRestarting,
    visitedPanes, settings, setSettings,
    networkLatency, networkStatus, toast, setToast,
    handleRestart, handleCapturePane, handleToggleMouse,
    isCapturing, ttydWidth, captureOutput, setCaptureOutput,
  } = usePane();

  // ESC to close capture/history panels
  React.useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (captureOutput !== null) { setCaptureOutput(null); e.stopPropagation(); }
        else if (showHistoryOverlay) { setShowHistoryOverlay(false); e.stopPropagation(); }
      }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [captureOutput, showHistoryOverlay]);

  return (
    <>
      <div id="main-middle-content" className="relative w-full" style={{height: hasPermission('prompt') ? `calc(100% - 40px - ${commandPanelHeight}px)` : 'calc(100% - 40px)'}}>
        
        {showHistoryOverlay && historyData && (
          <div style={{position: 'absolute', top: 0, left: 0, width: '100%', height: '100%', backgroundColor: 'rgba(0,0,0,0.5)', backdropFilter: 'blur(4px)', zIndex: 1000, display: 'flex', flexDirection: 'column'}}>
            <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 16px', borderBottom: '1px solid #474747', backgroundColor: '#1e1e1e'}}>
              <span style={{fontSize: '14px', color: '#cccccc', fontWeight: 500}}>Command History</span>
              <button onClick={() => setShowHistoryOverlay(false)} style={{color: '#858585', background: 'none', border: 'none', cursor: 'pointer'}}>
                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </div>
            <div style={{flex: 1, overflowY: 'auto'}}>
              {historyData.history.map((cmd, idx) => (
                <div 
                  key={idx}
                  style={{padding: '12px 16px', borderBottom: '1px solid #2a2d2e', cursor: 'pointer', color: '#cccccc', backgroundColor: '#1e1e1e', display: 'flex', alignItems: 'center', gap: '8px'}}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = '#2a2d2e'; const btn = e.currentTarget.querySelector('.delete-btn') as HTMLElement; if (btn) btn.style.opacity = '1'; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = '#1e1e1e'; const btn = e.currentTarget.querySelector('.delete-btn') as HTMLElement; if (btn) btn.style.opacity = '0'; }}
                >
                  <span style={{fontSize: '14px', fontFamily: 'monospace', flex: 1}} onClick={() => { historyData.onSelect(cmd); setShowHistoryOverlay(false); }}>{cmd}</span>
                  <button className="delete-btn" onClick={(e) => { e.stopPropagation(); const newHistory = historyData.history.filter((_, i) => i !== idx); setHistoryData({...historyData, history: newHistory}); }} style={{opacity: 0, color: '#ef4444', background: 'none', border: 'none', cursor: 'pointer', padding: '4px', transition: 'opacity 0.2s'}}>
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                  </button>
                </div>
              ))}
            </div>
          </div>
        )}
        {visitedPanes.filter(id => id && id !== '' && id !== 'undefined').map((paneId) => (
          <div key={`terminal-${paneId}`} className="absolute inset-0" style={{ backgroundColor:"#474747", zIndex: paneId === displayPaneId ? 1 : 0, visibility: paneId === displayPaneId ? 'visible' : 'hidden' }}>
            <WebFrame ref={paneId === displayPaneId ? mainIframeRef : undefined} loading="lazy" src={urls.ttyd(paneId, token)} className="w-full h-full" />
          </div>
        ))}
        <div id="main-middle-mask" className="ttyd-mask absolute inset-0 bg-transparent z-10" style={{display: 'none', pointerEvents: 'auto'}} onClick={(e) => { window.dispatchEvent(new CustomEvent('selectPane', { detail: { paneId: displayPaneId } })); (e.target as HTMLElement).style.display = 'none'; }} />
        {/* Capture overlay - on top of iframe */}
        {captureOutput !== null && (
          <CaptureOverlay output={captureOutput} paneId={displayPaneId} isRefreshing={isCapturing} onClose={() => setCaptureOutput(null)} onRefresh={(lines) => { setCaptureOutput(''); handleCapturePane(displayPaneId, lines); }} />
        )}
      </div>
      {isDragging && <div className="absolute inset-0 z-20"></div>}
      {isInteracting && <div className="absolute inset-0 z-20"></div>}
      {hasPermission('prompt') && (
        <div className="absolute bottom-0 left-0 right-0" style={{height: `${commandPanelHeight}px`}}>
          {showCorrectionResult && correctionData ? (
            <CorrectionPanel correctionData={correctionData} commandPanelHeight={commandPanelHeight} onClose={() => setShowCorrectionResult(false)} commandPanelRef={commandPanelRef} />
          ) : null}
          <div className="absolute top-0 left-0 right-0 h-1 bg-vsc-border" style={{zIndex: 1}}></div>
          <CommandPanel
            ref={commandPanelRef}
            paneTarget={displayPaneId}
            title={displayPaneTitle}
            token={token}
            panelPosition={{x: 0, y: 0}}
            panelSize={{width: ttydWidth, height: commandPanelHeight}}
            readOnly={readOnly}
            onReadOnlyToggle={() => setReadOnly(v => !v)}
            onInteractionStart={() => setIsInteracting(true)}
            onInteractionEnd={() => setIsInteracting(false)}
            onChange={(pos, size) => setSettings(prev => ({ ...prev, panelSize: size }))}
            onCapturePane={(paneId) => { if (captureOutput !== null) { setCaptureOutput(null); } else { handleCapturePane(paneId); } }}
            isCapturing={isCapturing}
            canSend={agentStatus === 'idle' || agentStatus === 'wait_startup'}
            mode="ttyd"
            onShowHistory={(history, onSelect) => {
              if (showHistoryOverlay) { setShowHistoryOverlay(false); }
              else { setHistoryData({history, onSelect}); setShowHistoryOverlay(true); setShowCorrectionResult(false); }
            }}
            onShowCorrection={(result) => {
              if (result === null) { setCorrectionData(null); setShowCorrectionResult(false); }
              else { setCorrectionData(result); setShowCorrectionResult(true); setShowHistoryOverlay(false); }
            }}
            agentStatus={agentStatus}
            contextUsage={contextUsage}
            mouseMode={mouseMode}
            onDraggingChange={setIsDragging}
            isTogglingMouse={false}
            onToggleMouse={handleToggleMouse}
            onReload={() => { if (mainIframeRef.current) mainIframeRef.current.src = mainIframeRef.current.src; }}
            boundAgents={boundAgents}
            onRestart={handleRestart}
            isRestarting={isRestarting}
            hasEditPermission={hasPermission('agent_manage')}
            hasRestartPermission={hasPermission('prompt')}
            hasCapturePermission={hasPermission('ttyd_read')}
            networkLatency={networkLatency}
            networkStatus={networkStatus}
            disableDrag={true}
            showVoiceControl={settings.showVoiceControl}
            onToggleVoiceControl={() => setSettings(prev => ({ ...prev, showVoiceControl: !prev.showVoiceControl}))}
            onShowPromptModal={onShowPromptModal}
          />
        </div>
      )}
    </>
  );
};

const CorrectionPanel: React.FC<{correctionData: [string, string], commandPanelHeight: number, onClose: () => void, commandPanelRef: React.RefObject<CommandPanelHandle>}> = ({correctionData, commandPanelHeight, onClose, commandPanelRef}) => (
  <div style={{position: 'absolute', bottom: `${commandPanelHeight + 4}px`, left: 0, right: 0, maxHeight: "300px", minHeight: "140px", zIndex: 1000}}>
    <div style={{width: '100%', height: '100%', backgroundColor: '#1e1e1e', border: '1px solid #474747', borderRadius: '12px', display: 'flex', flexDirection: 'column', overflow: 'hidden', boxShadow: '0 10px 25px rgba(0,0,0,0.5)', position: 'relative'}}>
      <div style={{height: '32px', backgroundColor: '#252526', borderBottom: '1px solid #474747', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 12px', flexShrink: 0}}>
        <span style={{fontSize: '12px', color: '#858585', fontWeight: 600, letterSpacing: '0.5px'}}>CORRECTION</span>
        <button onClick={onClose} style={{color: '#6a6a6a', background: 'none', border: 'none', cursor: 'pointer', padding: '4px', borderRadius: '6px', transition: 'all 0.2s', display: 'flex', alignItems: 'center'}} onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = '#474747'; e.currentTarget.style.color = '#cccccc'; }} onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; e.currentTarget.style.color = '#6a6a6a'; }}>
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
      <div style={{flex: 1, display: 'flex', flexDirection: 'column', gap: '1px', backgroundColor: '#474747', overflow: 'hidden'}}>
        <CorrectionRow text={correctionData[0]} bg="#1e1e1e" btnClass="action-btns" commandPanelRef={commandPanelRef} onClose={onClose} />
        <CorrectionRow text={correctionData[1]} bg="#252526" btnClass="action-btns-cn" textColor="#858585" commandPanelRef={commandPanelRef} onClose={onClose} />
      </div>
    </div>
  </div>
);

const CorrectionRow: React.FC<{text: string, bg: string, btnClass: string, textColor?: string, commandPanelRef: React.RefObject<CommandPanelHandle>, onClose: () => void}> = ({text, bg, btnClass, textColor = '#cccccc', commandPanelRef, onClose}) => (
  <div style={{flex: 1, padding: '16px', backgroundColor: bg, position: 'relative', overflowY: 'auto'}}
    onMouseEnter={(e) => { const btns = e.currentTarget.querySelector(`.${btnClass}`) as HTMLElement; if (btns) btns.style.setProperty('opacity', '1', 'important'); }}
    onMouseLeave={(e) => { const btns = e.currentTarget.querySelector(`.${btnClass}`) as HTMLElement; if (btns) btns.style.setProperty('opacity', '0', 'important'); }}
  >
    <div style={{fontSize: '15px', color: textColor, fontFamily: 'monospace', lineHeight: '1.6', fontWeight: textColor === '#cccccc' ? 500 : undefined, paddingRight: '60px'}}>{text}</div>
    <div className={btnClass} style={{opacity: 0, position: 'absolute', top: '8px', right: '8px', display: 'flex', gap: '4px', transition: 'opacity 0.2s'}}>
      <button onClick={() => navigator.clipboard.writeText(text)} style={{padding: '6px', backgroundColor: '#252526', color: '#858585', border: 'none', borderRadius: '6px', cursor: 'pointer', transition: 'all 0.2s', display: 'flex', alignItems: 'center'}} onMouseEnter={(e) => {e.currentTarget.style.backgroundColor = '#474747'; e.currentTarget.style.color = '#cccccc';}} onMouseLeave={(e) => {e.currentTarget.style.backgroundColor = '#252526'; e.currentTarget.style.color = '#858585';}} title="Copy">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
      </button>
      <button onClick={() => { commandPanelRef.current?.setPrompt(text); onClose(); }} style={{padding: '6px', backgroundColor: '#252526', color: '#858585', border: 'none', borderRadius: '6px', cursor: 'pointer', transition: 'all 0.2s', display: 'flex', alignItems: 'center'}} onMouseEnter={(e) => {e.currentTarget.style.backgroundColor = '#474747'; e.currentTarget.style.color = '#cccccc';}} onMouseLeave={(e) => {e.currentTarget.style.backgroundColor = '#252526'; e.currentTarget.style.color = '#858585';}} title="Edit">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
      </button>
    </div>
  </div>
);

const CaptureOverlay: React.FC<{output: string, paneId: string, isRefreshing: boolean, onClose: () => void, onRefresh: (lines: number) => void}> = ({output, paneId, isRefreshing, onClose, onRefresh}) => {
  const [lines, setLines] = React.useState(100);
  const [copied, setCopied] = React.useState(false);
  const [search, setSearch] = React.useState('');
  const preRef = React.useRef<HTMLPreElement>(null);
  React.useEffect(() => { if (preRef.current) preRef.current.scrollTop = preRef.current.scrollHeight; }, [output]);

  const handleCopy = () => {
    navigator.clipboard.writeText(output);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  // Highlight search matches
  const renderOutput = () => {
    if (!output) return <span className="text-vsc-text-secondary italic">( empty )</span>;
    if (!search) return output;
    const parts = output.split(new RegExp(`(${search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi'));
    return parts.map((part, i) => 
      part.toLowerCase() === search.toLowerCase() ? <mark key={i} className="bg-yellow-500/40 text-yellow-200 rounded-sm px-0.5">{part}</mark> : part
    );
  };

  return (
    <div className="absolute inset-0 z-50 flex flex-col bg-[#0d1117]">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-[#30363d] bg-[#161b22]">
        <div className="flex items-center gap-2">
          <span className="text-xs font-semibold text-[#8b949e]">{paneId.replace(':main.0','')}</span>
          <input type="number" value={lines} onChange={(e) => setLines(Math.max(1, parseInt(e.target.value) || 10))} className="w-14 px-1.5 py-0.5 text-xs bg-[#0d1117] text-[#c9d1d9] border border-[#30363d] rounded" min="1" />
          <button onClick={() => onRefresh(lines)} disabled={isRefreshing} className="px-2 py-0.5 text-xs bg-[#238636] hover:bg-[#2ea043] text-white rounded disabled:opacity-50">{isRefreshing ? '...' : '↻'}</button>
          <span className="text-[10px] text-[#484f58]">|</span>
          <input type="text" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search..." className="w-24 px-1.5 py-0.5 text-xs bg-[#0d1117] text-[#c9d1d9] border border-[#30363d] rounded placeholder:text-[#484f58]" />
          <button onClick={handleCopy} className="px-2 py-0.5 text-xs bg-[#21262d] hover:bg-[#30363d] text-[#c9d1d9] border border-[#30363d] rounded">
            {copied ? '✓' : 'Copy'}
          </button>
        </div>
        <button onClick={onClose} className="p-1 rounded text-[#484f58] hover:text-[#c9d1d9] hover:bg-[#21262d]">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
      <pre ref={preRef} className="flex-1 overflow-auto px-4 py-3 text-[13px] leading-5 text-[#c9d1d9] font-mono whitespace-pre-wrap break-all selection:bg-blue-500/30">{renderOutput()}</pre>
    </div>
  );
};
