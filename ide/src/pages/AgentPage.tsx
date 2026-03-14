import React, { useEffect, useState, useRef } from 'react';
import { useAuth } from '../contexts/AuthContext';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';
import TerminalFrame from '../components/terminal/TerminalFrame';
import { CommandPanel, CommandPanelHandle } from '../components/terminal/CommandPanel';
import ChatView from '../components/chat/ChatView';
import { SettingsView } from '../components/SettingsView';
import { EditPaneData } from '../components/EditPaneDialog';
import { ArrowLeft, RotateCcw, Zap, Trash2, Settings, MessageSquare, Terminal, X, Folder, FolderOpen } from 'lucide-react';
import { useDesktopApps, openInElectron } from '../components/desktop/useDesktopApps';
import { VoiceFloatingButton } from '../components/VoiceFloatingButton';
import { WebFrame } from '../components/WebFrame';
import config, { urls } from '../config';

/* ── Settings floating window (iPhone style) ── */
const SettingsFloat: React.FC<{ paneId: string; fullPaneId: string; onClose: () => void }> = ({ paneId, fullPaneId, onClose }) => {
  const [paneData, setPaneData] = useState<EditPaneData>({ target: fullPaneId, title: paneId });
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => setPaneData(prev => ({ ...prev, ...data }))).catch(() => {}); }, [fullPaneId]);
  const save = async () => { setSaving(true); try { await apiService.updatePane(paneId, paneData); setMsg('Saved'); } catch { setMsg('Failed'); } finally { setSaving(false); setTimeout(() => setMsg(''), 1500); } };
  return (
    <div className="fixed inset-0 z-[99999] flex items-start justify-center pt-12" onClick={onClose}>
      <div data-id="settings-float" className="w-[340px] max-h-[70vh] bg-[#1c1c1e]/95 backdrop-blur-2xl rounded-2xl shadow-2xl border border-white/[0.08] overflow-hidden" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/[0.06]">
          <span className="text-[13px] font-semibold text-white">Settings</span>
          <button onClick={onClose} className="p-1 rounded-full hover:bg-white/10"><X size={14} className="text-white/60" /></button>
        </div>
        <div className="p-3 overflow-y-auto max-h-[calc(70vh-48px)]">
          <SettingsView pane={paneData} onChange={setPaneData} onSave={save} isSaving={saving} />
          {msg && <div className="mt-2 text-center text-xs text-emerald-400">{msg}</div>}
        </div>
      </div>
    </div>
  );
};

/* ── Draggable box (constrained to parent) ── */
const DraggableBox: React.FC<{ paneId: string; token: string | null; agentStatus: string; mouseMode: string }> = ({ paneId, token, agentStatus, mouseMode }) => {
  const W = 420, H = 180;
  const ref = useRef<HTMLDivElement>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [pos, setPos] = useState(() => {
    try { const c = JSON.parse(localStorage.getItem('terminal_drag_pos')!); if (c?.x != null) return c; } catch {} return { x: -1, y: -1 };
  });
  const startRef = useRef({ mx: 0, my: 0, px: 0, py: 0 });

  // 初始位置：中下偏上36px
  useEffect(() => {
    if (pos.x >= 0 || !ref.current?.parentElement) return;
    const pr = ref.current.parentElement.getBoundingClientRect();
    setPos({ x: (pr.width - W) / 2, y: pr.height - H - 36 });
  }, [pos]);

  const onDown = (e: React.MouseEvent) => {
    if (pos.x < 0) return;
    startRef.current = { mx: e.clientX, my: e.clientY, px: pos.x, py: pos.y };
    setIsDragging(true);
    const onMove = (ev: MouseEvent) => {
      const parent = ref.current?.parentElement;
      if (!parent) return;
      const pr = parent.getBoundingClientRect();
      const nx = Math.max(0, Math.min(pr.width - W, startRef.current.px + ev.clientX - startRef.current.mx));
      const ny = Math.max(0, Math.min(pr.height - H, startRef.current.py + ev.clientY - startRef.current.my));
      setPos({ x: nx, y: ny });
    };
    const onUp = () => {
      setIsDragging(false);
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      setPos(p => { if (p) localStorage.setItem('terminal_drag_pos', JSON.stringify(p)); return p; });
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    e.preventDefault();
  };

  if (pos.x < 0) return <div ref={ref} style={{ position: 'absolute', opacity: 0 }} />;
  return (
    <>
      {isDragging && <div data-id="drag-overlay" style={{ position: 'absolute', inset: 0, zIndex: 49 }} />}
      <div data-id="draggable-box" ref={ref} style={{ position: 'absolute', left: pos.x, top: pos.y, width: W, height: H, borderRadius: 8, zIndex: 50 }}>
        <div data-id="drag-handle" onMouseDown={onDown} style={{ position: 'absolute', top: 0, left: 0, right: 0, height: 36, cursor: 'move', zIndex: 10 }} />
        <CommandPanel paneTarget={paneId} title="" token={token} panelPosition={{ x: 0, y: 0 }} panelSize={{ width: W, height: H }} readOnly={false} onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}} onChange={() => {}} canSend={true} agentStatus={agentStatus} mouseMode={mouseMode} drawerTab="terminal" />
      </div>
    </>
  );
};

/* ── Right drawer (History / Terminal) ── */
const Drawer: React.FC<{ tab: string; onTabChange: (t: string) => void; children: React.ReactNode[] }> = ({ tab, onTabChange, children }) => (
  <div data-id="drawer" className="h-full flex flex-col bg-[#1c1c1e]/90 backdrop-blur-xl border-l border-white/[0.06]">
    <div data-id="drawer-tabs" className="flex items-center px-2 h-9 shrink-0 border-b border-white/[0.04]">
      <div className="flex gap-0.5">
        {['history', 'terminal'].map(t => (
          <button key={t} onClick={() => onTabChange(t)} className={`px-2.5 py-1 text-[11px] rounded-md transition-all ${tab === t ? 'text-white bg-white/[0.08]' : 'text-white/40 hover:text-white/70'}`}>
            {t === 'history' ? <span className="flex items-center gap-1"><MessageSquare size={11} />Chat</span> : <span className="flex items-center gap-1"><Terminal size={11} />Terminal</span>}
          </button>
        ))}
      </div>
    </div>
    <div data-id="drawer-content" className="flex-1 overflow-hidden relative">
      <div data-id="drawer-history" className="absolute inset-0" style={{ display: tab === 'history' ? 'block' : 'none' }}>{children[0]}</div>
      <div data-id="drawer-terminal" className="absolute inset-0" style={{ display: tab === 'terminal' ? 'block' : 'none' }}>{children[1]}</div>
    </div>
  </div>
);

/* ── Resizer ── */
const Resizer: React.FC<{ width: number; onChange: (w: number) => void; onDragging: (d: boolean) => void }> = ({ width, onChange, onDragging }) => {
  const onDown = (e: React.MouseEvent) => {
    e.preventDefault();
    onDragging(true);
    const startX = e.clientX, startW = width;
    const onMove = (ev: MouseEvent) => onChange(Math.max(280, Math.min(window.innerWidth * 0.6, startW - (ev.clientX - startX))));
    const onUp = () => { onDragging(false); document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
    document.addEventListener('mousemove', onMove); document.addEventListener('mouseup', onUp);
  };
  return <div data-id="resizer" className="w-1 cursor-col-resize hover:bg-white/10 active:bg-white/20 transition-colors shrink-0" onMouseDown={onDown} />;
};

/* ── Main ── */
const AgentPage: React.FC<{ paneId: string }> = ({ paneId }) => {
  const { token, hasPermission } = useAuth();
  const fullPaneId = paneId.includes(':') ? paneId : `${paneId}:main.0`;
  const [title, setTitle] = useState(paneId);
  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const commandPanelRef = useRef<CommandPanelHandle>(null);
  const [panelPos, setPanelPos] = useState(() => { try { const c = JSON.parse(localStorage.getItem('agent_panelPos')!); return c && c.x != null ? c : { x: 20, y: Math.max(60, window.innerHeight - 280) }; } catch { return { x: 20, y: Math.max(60, window.innerHeight - 280) }; } });
  const [panelSize, setPanelSize] = useState(() => { try { const c = JSON.parse(localStorage.getItem('agent_panelSize')!); return c && c.width ? c : { width: 360, height: 220 }; } catch { return { width: 360, height: 220 }; } });
  const [isDragging, setIsDragging] = useState(false);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; appId: string } | null>(null);
  const { apps, addApp, removeApp } = useDesktopApps(paneId);
  const [editMode, setEditMode] = useState(false);
  const [codeDrawerOpen, setCodeDrawerOpen] = useState(false);
  const [workspace, setWorkspace] = useState(`${config.hostHome}/Private/workers/${paneId}`);
  const [codeDrawerW, setCodeDrawerW] = useState(() => parseInt(localStorage.getItem('code_drawer_w') || '600'));

  // Drawer
  const [drawerTab, setDrawerTab] = useState(() => localStorage.getItem('agent_drawerTab') || 'history');
  const [drawerW, setDrawerW] = useState(() => parseInt(localStorage.getItem('agent_drawerW') || '360'));
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [showVoiceControl, setShowVoiceControl] = useState(false);
  const [voiceBtnPos, setVoiceBtnPos] = useState(() => ({ x: 20, y: Math.max(60, window.innerHeight - 400) }));
  const [voiceReply, setVoiceReply] = useState(() => localStorage.getItem('voice_reply') === 'true');
  const [voiceLoading, setVoiceLoading] = useState(false);
  const [voiceSettingsOpen, setVoiceSettingsOpen] = useState(false);
  const [autoPlayReply, setAutoPlayReply] = useState(() => localStorage.getItem('auto_play_reply') === 'true');
  const ttydContainerRef = useRef<HTMLDivElement>(null);
  const [ttydBounds, setTtydBounds] = useState<{ x: number; y: number; width: number; height: number } | undefined>();

  // 计算 ttyd 区域坐标
  useEffect(() => {
    if (!ttydContainerRef.current) {
      setTtydBounds(undefined);
      return;
    }
    const rect = ttydContainerRef.current.getBoundingClientRect();
    setTtydBounds({
      x: rect.left,
      y: rect.top,
      width: rect.width,
      height: rect.height
    });
  }, [drawerTab, drawerW]);

  // TTS: 语音回复
  useEffect(() => {
    if (!voiceReply || !autoPlayReply) return;
    const handler = (e: Event) => {
      const text = (e as CustomEvent).detail?.text;
      if (!text) return;
      const utterance = new SpeechSynthesisUtterance(text.slice(0, 500));
      utterance.lang = 'zh-CN';
      utterance.rate = 1.1;
      speechSynthesis.cancel();
      speechSynthesis.speak(utterance);
    };
    window.addEventListener('ai-reply-done', handler);
    return () => window.removeEventListener('ai-reply-done', handler);
  }, [voiceReply, autoPlayReply]);

  useEffect(() => { localStorage.setItem('agent_drawerW', drawerW.toString()); }, [drawerW]);
  useEffect(() => { localStorage.setItem('code_drawer_w', codeDrawerW.toString()); }, [codeDrawerW]);
  useEffect(() => { localStorage.setItem('agent_drawerTab', drawerTab); }, [drawerTab]);
  useEffect(() => { localStorage.setItem('agent_panelPos', JSON.stringify(panelPos)); }, [panelPos]);
  useEffect(() => { localStorage.setItem('agent_panelSize', JSON.stringify(panelSize)); }, [panelSize]);

  // Fetch title
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => { if (data?.title) setTitle(data.title); if (data?.workspace) setWorkspace((data.workspace as string).replace('~', config.hostHome)); }).catch(() => {}); }, [fullPaneId]);

  // Poll status
  useEffect(() => {
    const poll = async () => { try { const { data } = await apiService.getAllStatus(); const st = data?.[fullPaneId]; if (st?.status) setStatus(st.status); if (st?.title) setTitle(st.title); if (st?.contextUsage != null) setContextUsage(st.contextUsage); } catch {} };
    poll(); const id = setInterval(poll, 5000); return () => clearInterval(id);
  }, [fullPaneId]);

  // Handlers
  const handleToggleMouse = async () => { const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };
  const handleRestart = async () => {
    if (!confirm(`Restart ${paneId}?`)) return; setIsRestarting(true);
    try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') { setTimeout(() => location.reload(), 500); return; } } catch {} } setTimeout(() => location.reload(), 500); }
    catch { alert('Restart failed'); } finally { setIsRestarting(false); }
  };
  const handleCapture = async () => { try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) { await navigator.clipboard.writeText(data.output); showToast('Captured'); } } catch {} };
  const showToast = (m: string) => { setToast(m); setTimeout(() => setToast(null), 2000); };

  // Agent desktop events
  useEffect(() => {
    const handler = async (e: CustomEvent) => {
      const d = e.detail || {};
      console.log('[AgentPage] 收到 desktop event:', d.type, d);
      
      // Ping/Pong
      if (d.type === 'ping') {
        console.log('[AgentPage] 发送 pong:', d.requestId);
        window.dispatchEvent(new CustomEvent('agent-pong', { detail: { requestId: d.requestId, pong: 'ok' } }));
        return;
      }
      
      // IPC Ping - 测试 electronRPC 连通性
      if (d.type === 'ipc_ping') {
        if (typeof (window as any).electronRPC !== 'function') return;
        (window as any).electronRPC('ping', {}).then((result: any) => {
          console.log('[AgentPage] electronRPC 返回:', result);
          window.dispatchEvent(new CustomEvent('ipc-pong', { detail: { requestId: d.requestId, result } }));
        }).catch((err: any) => {
          console.error('[AgentPage] electronRPC 失败:', err);
          window.dispatchEvent(new CustomEvent('ipc-pong', { detail: { requestId: d.requestId, error: err.message } }));
        });
        return;
      }
      
      if (d.type === 'add_app') { addApp({ id: d.id || `app-${Date.now()}`, label: d.label || 'App', emoji: d.emoji || '📦', url: d.url || 'about:blank' }); if (d.autoOpen !== false) openInElectron(d.url, d.label); }
      else if (d.type === 'open_window' && d.url) openInElectron(d.url, d.title, true, d.width, d.height);
      else if (d.type === 'gemini_ask') {
        const rpc = (window as any).electronRPC;
        if (typeof rpc !== 'function') { console.warn('[AgentPage] gemini_ask 跳过: 非 Electron 环境'); return; }
        const wid = d.win_id || 2;
        try {
          console.log('[AgentPage] 调用 gemini_web_set_prompt...');
          await rpc('gemini_web_set_prompt', { win_id: wid, text: d.prompt });
          console.log('[AgentPage] 调用 gemini_web_click_send...');
          await rpc('gemini_web_click_send', { win_id: wid });
          let result = '';
          for (let i = 0; i < 30; i++) {
            await new Promise(r => setTimeout(r, 1000));
            console.log('[AgentPage] 检查状态 #' + i);
            const status = await rpc('gemini_web_status', { win_id: wid });
            const s = JSON.parse(status?.content?.[0]?.text || '{}');
            if (!s.isGenerating && i > 2) {
              // 取最后回复
              const reply = await rpc('exec_js', { win_id: wid, code: `(()=>{const els=document.querySelectorAll(".response-container");return els.length?els[els.length-1].innerText.trim():"no reply"})()` });
              const rt = reply?.content?.[0]?.text || '';
              result = rt || (typeof reply === 'string' ? reply : JSON.stringify(reply));
              break;
            }
          }
          console.log('[AgentPage] gemini_ask 完成，结果:', result);
          window.dispatchEvent(new CustomEvent('gemini-ask-result', { detail: { requestId: d.requestId, result } }));
        } catch (err: any) {
          console.error('[AgentPage] gemini_ask 错误:', err);
          window.dispatchEvent(new CustomEvent('gemini-ask-result', { detail: { requestId: d.requestId, error: err.message } }));
        }
      }
      else if (d.type === 'gemini_vision_request') {
        const rpc = (window as any).electronRPC;
        if (typeof rpc !== 'function') { console.warn('[AgentPage] gemini_vision 跳过: 非 Electron 环境'); return; }
        const wid = d.win_id || 4;
        const srcWid = d.src_win_id || 1;
        try {
          // 1. 截图到剪贴板
          await rpc('webpage_screenshot_to_clipboard', { win_id: srcWid });
          // 2. 聚焦 Gemini 输入框并粘贴
          await rpc('exec_js', { win_id: wid, code: 'var r=document.querySelector("rich-textarea");if(r){var e=r.querySelector("div.ql-editor");if(e)e.click()};return "ok"' });
          await rpc('control_electron_WebContents', { win_id: wid, code: 'webContents.paste()' });
          // 3. 等待图片上传
          for (let i = 0; i < 20; i++) {
            await new Promise(r => setTimeout(r, 500));
            const st = await rpc('gemini_web_status', { win_id: wid });
            const s = JSON.parse(st?.content?.[0]?.text || '{}');
            if (s.hasImage && !s.isUploading) break;
          }
          // 4. 设置问题并发送
          await rpc('gemini_web_set_prompt', { win_id: wid, text: d.prompt || 'Describe this image' });
          await rpc('gemini_web_click_send', { win_id: wid });
          // 5. 等待回复
          let result = '';
          for (let i = 0; i < 30; i++) {
            await new Promise(r => setTimeout(r, 1000));
            const st = await rpc('gemini_web_status', { win_id: wid });
            const s = JSON.parse(st?.content?.[0]?.text || '{}');
            if (!s.isGenerating && i > 8) {
              const reply = await rpc('exec_js', { win_id: wid, code: '(()=>{const els=document.querySelectorAll(".response-container");return els.length?els[els.length-1].innerText.trim():"no reply"})()' });
              result = reply?.content?.[0]?.text || (typeof reply === 'string' ? reply : JSON.stringify(reply));
              break;
            }
          }
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, result } }));
        } catch (err: any) {
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, error: err.message } }));
        }
      }
    };
    window.addEventListener('agent-desktop-event', handler as EventListener);
    return () => window.removeEventListener('agent-desktop-event', handler as EventListener);
  }, [addApp]);

  // Context menu dismiss
  useEffect(() => { if (!ctxMenu) return; const h = () => setCtxMenu(null); window.addEventListener('click', h); return () => window.removeEventListener('click', h); }, [ctxMenu]);

  const isThinking = status === 'thinking';

  return (
    <div data-id="agent-page" className="w-screen h-screen flex flex-col bg-[#0a0a0f] overflow-hidden">
      {/* ── Top bar: minimal, floating feel ── */}
      <div data-id="top-bar" className="h-10 flex items-center justify-between px-3 shrink-0 bg-black/40 backdrop-blur-xl border-b border-white/[0.04]">
        <div className="flex items-center gap-2 min-w-0">
          <button onClick={() => { window.location.hash = '#/'; }} className="p-1 rounded-lg text-white/30 hover:text-white hover:bg-white/5"><ArrowLeft size={14} /></button>
          {isThinking && <Zap size={12} className="text-yellow-400 animate-pulse" />}
          <span className="text-[13px] font-medium text-white/90 truncate">{title}</span>
          <span className="text-[10px] text-white/20 font-mono">{paneId}</span>
        </div>
        <div className="flex items-center gap-0.5">
          {/* Context bar */}
          {contextUsage != null && (
            <div className="flex items-center gap-1.5 mr-1.5 px-2 py-0.5 rounded-full bg-white/[0.04]">
              <div className="w-12 h-1 rounded-full bg-white/[0.06] overflow-hidden">
                <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400' : contextUsage > 50 ? 'bg-yellow-400' : 'bg-emerald-400'}`} style={{ width: `${contextUsage}%` }} />
              </div>
              <span className="text-[9px] text-white/30 font-mono">{contextUsage}%</span>
            </div>
          )}
          {isThinking && <span className="text-[9px] px-1.5 py-0.5 rounded-full bg-yellow-500/10 text-yellow-400/80 animate-pulse mr-1">thinking</span>}
          <button onClick={() => setSettingsOpen(true)} className="p-1.5 rounded-lg text-white/20 hover:text-white/60 hover:bg-white/5"><Settings size={13} /></button>
          <button onClick={() => setVoiceSettingsOpen(true)} className={`p-1.5 rounded-lg hover:bg-white/5 ${voiceReply ? 'text-green-400' : 'text-white/20 hover:text-white/60'}`} title="Voice settings">🔊</button>
          <button onClick={handleRestart} disabled={isRestarting} className="p-1.5 rounded-lg text-white/20 hover:text-orange-400 hover:bg-white/5 disabled:opacity-20"><RotateCcw size={13} className={isRestarting ? 'animate-spin' : ''} /></button>
        </div>
      </div>

      {/* ── Main area ── */}
      <div className="flex-1 flex overflow-hidden">
        {/* Desktop canvas */}
        <div data-id="desktop-canvas" className="flex-1 min-w-0 relative" onClick={() => { setCtxMenu(null); if (editMode) setEditMode(false); }}>
          {/* Background */}
          <div data-id="desktop-bg" className="absolute inset-0 bg-gradient-to-br from-[#0f0f1a] via-[#111827] to-[#0c1222]" />
          <div className="absolute inset-0 opacity-[0.03]" style={{ backgroundImage: 'radial-gradient(circle at 1px 1px, white 1px, transparent 0)', backgroundSize: '32px 32px' }} />

          {/* App icons - iPhone grid */}
          <div data-id="app-grid" className="absolute inset-0 z-10 p-6 pt-4 flex flex-wrap content-start gap-5 pointer-events-none overflow-y-auto" onClick={() => editMode && setEditMode(false)}>
            {/* Code folder icon - always present */}
            <div data-id="code-folder-icon" className="w-[68px] flex flex-col items-center gap-1.5 select-none pointer-events-auto"
              onClick={() => setCodeDrawerOpen(v => !v)}>
              <div className="w-[52px] h-[52px] rounded-[14px] bg-gradient-to-br from-blue-500/20 to-blue-400/10 backdrop-blur-md flex items-center justify-center shadow-lg shadow-black/20 active:scale-95 transition-all duration-150 border border-blue-400/20">
                {codeDrawerOpen ? <FolderOpen size={26} className="text-blue-400" /> : <Folder size={26} className="text-blue-400" />}
              </div>
              <span className="text-[10px] text-white/60 truncate w-full text-center leading-tight">Code</span>
            </div>
            {apps.map(app => (
              <div key={app.id} className={`w-[68px] flex flex-col items-center gap-1.5 select-none pointer-events-auto relative ${editMode ? 'animate-wiggle' : ''}`}
                onClick={() => { if (!editMode) openInElectron(app.url, app.label); }}
                onContextMenu={e => { e.preventDefault(); setEditMode(true); }}>
                {editMode && (
                  <button onClick={e => { e.stopPropagation(); removeApp(app.id); if (apps.length <= 1) setEditMode(false); }}
                    className="absolute -top-1 -left-1 z-20 w-5 h-5 rounded-full bg-red-500 flex items-center justify-center shadow-lg hover:bg-red-400">
                    <X size={10} className="text-white" />
                  </button>
                )}
                <div className="w-[52px] h-[52px] rounded-[14px] bg-gradient-to-br from-white/[0.12] to-white/[0.04] backdrop-blur-md flex items-center justify-center text-[28px] shadow-lg shadow-black/20 group-hover:scale-110 active:scale-95 transition-all duration-150 border border-white/[0.06]">
                  {app.emoji}
                </div>
                <span className="text-[10px] text-white/60 truncate w-full text-center leading-tight">{app.label}</span>
              </div>
            ))}
          </div>

          {/* Empty state */}
          {apps.length === 0 && (
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="text-center">
                <div className="text-5xl mb-3 opacity-20">✨</div>
                <div className="text-[11px] text-white/15">Ask your agent to build something</div>
              </div>
            </div>
          )}

          {/* Context menu */}
          {ctxMenu && (
            <div className="fixed z-[99999] bg-[#2a2a2e]/95 backdrop-blur-xl border border-white/[0.08] rounded-xl shadow-2xl py-1 min-w-[130px]" style={{ left: ctxMenu.x, top: ctxMenu.y }} onClick={e => e.stopPropagation()}>
              <button onClick={() => { const a = apps.find(x => x.id === ctxMenu.appId); if (a) openInElectron(a.url, a.label); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-[11px] text-white/80 hover:bg-white/10 rounded-md mx-0.5" style={{ width: 'calc(100% - 4px)' }}>Open</button>
              <div className="h-px bg-white/[0.06] my-0.5 mx-2" />
              <button onClick={() => { removeApp(ctxMenu.appId); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-[11px] text-red-400/80 hover:bg-white/10 rounded-md mx-0.5 flex items-center gap-1.5" style={{ width: 'calc(100% - 4px)' }}><Trash2 size={11} />Remove</button>
            </div>
          )}


          {/* Voice floating button */}
          {showVoiceControl && (
            <VoiceFloatingButton
              initialPosition={voiceBtnPos}
              onPositionChange={setVoiceBtnPos}
              onRecordStart={() => {
                const stream = (window as any).__voiceStream;
                if (stream) { stream.getTracks().forEach((t: any) => t.enabled = true); }
                navigator.mediaDevices.getUserMedia({ audio: true }).then(s => {
                  (window as any).__voiceStream = s;
                  const rec = new MediaRecorder(s, { mimeType: 'audio/webm;codecs=opus' });
                  (window as any).__voiceChunks = [] as Blob[];
                  rec.ondataavailable = e => { if (e.data.size > 0) (window as any).__voiceChunks.push(e.data); };
                  rec.start();
                  (window as any).__voiceRec = rec;
                });
              }}
              onRecordEnd={(shouldSend) => {
                const rec = (window as any).__voiceRec as MediaRecorder | undefined;
                if (rec && rec.state !== 'inactive') {
                  rec.onstop = async () => {
                    (window as any).__voiceStream?.getTracks().forEach((t: any) => t.enabled = false);
                    if (!shouldSend) return;
                    const blob = new Blob((window as any).__voiceChunks || [], { type: 'audio/webm' });
                    if (blob.size < 100) return;
                    const fd = new FormData();
                    fd.append('file', blob, 'voice.webm');
                    fd.append('engine', 'google');
                    setVoiceLoading(true);
                    try {
                      const { data } = await apiService.stt(fd);
                      if (data.text) {
                        window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneId, q: data.text } }));
                        sendCommandToTmux(data.text, paneId);
                      }
                    } catch (e) { console.error('STT error:', e); }
                    finally { setVoiceLoading(false); }
                  };
                  rec.stop();
                }
              }}
              isRecordingExternal={false}
              isLoading={voiceLoading}
            />
          )}
        </div>

        {/* Code-server left drawer */}
        <div
          data-id="code-drawer"
          className="fixed top-10 left-0 bottom-0 z-[9999] transition-transform duration-300 ease-in-out"
          style={{ width: codeDrawerW, transform: codeDrawerOpen ? 'translateX(0)' : 'translateX(-100%)' }}
        >
          <div className="h-full flex flex-col bg-[#1e1e1e] border-r border-white/[0.08] shadow-2xl">
            <div data-id="code-drawer-header" className="h-9 flex items-center justify-between px-3 shrink-0 border-b border-white/[0.06] bg-[#1c1c1e]">
              <span className="text-[12px] text-white/70 flex items-center gap-1.5"><FolderOpen size={12} />Code Server</span>
              <button onClick={() => setCodeDrawerOpen(false)} className="p-1 rounded hover:bg-white/10"><X size={14} className="text-white/50" /></button>
            </div>
            <div className="flex-1 overflow-hidden">
              {workspace && (
                <WebFrame
                  src={urls.codeServer(workspace, token || undefined)}
                  codeServer
                  className="w-full h-full border-0"
                  title="code-server"
                />
              )}
            </div>
          </div>
          {/* Drag resizer on right edge */}
          <div
            data-id="code-drawer-resizer"
            className="absolute top-0 right-0 w-1 h-full cursor-col-resize hover:bg-blue-400/30 active:bg-blue-400/50 transition-colors z-10"
            onMouseDown={e => {
              e.preventDefault();
              setIsDragging(true);
              const startX = e.clientX, startW = codeDrawerW;
              const onMove = (ev: MouseEvent) => setCodeDrawerW(Math.max(380, Math.min(window.innerWidth * 0.8, startW + (ev.clientX - startX))));
              const onUp = () => { setIsDragging(false); document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
              document.addEventListener('mousemove', onMove); document.addEventListener('mouseup', onUp);
            }}
          />
        </div>

        {/* Right drawer - always open */}
        <Resizer width={drawerW} onChange={w => setDrawerW(w)} onDragging={setIsDragging} />
        <div className="shrink-0" style={{ width: drawerW, minWidth: '380px' }} ref={ttydContainerRef}>
          <Drawer tab={drawerTab} onTabChange={setDrawerTab}>
            <ChatView paneId={paneId} token={token!} commandPanel={<CommandPanel ref={commandPanelRef} paneTarget={paneId} title={title} token={token} panelPosition={panelPos} panelSize={panelSize} readOnly={false} onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}} onChange={(pos, size) => { setPanelPos(pos); setPanelSize(size); }} onDraggingChange={setIsDragging} canSend={true} agentStatus={status} contextUsage={contextUsage} mouseMode={mouseMode} onToggleMouse={handleToggleMouse} onRestart={handleRestart} isRestarting={isRestarting} onCapturePane={handleCapture} hasEditPermission={hasPermission('edit')} hasRestartPermission={hasPermission('restart')} hasCapturePermission={hasPermission('capture')} showVoiceControl={showVoiceControl} onToggleVoiceControl={() => setShowVoiceControl(v => !v)} drawerTab={drawerTab} ttydBounds={ttydBounds} />} />
            <div className="h-full relative">
              <TerminalFrame paneId={paneId} token={token!} />
              {drawerTab === 'terminal' && <DraggableBox paneId={paneId} token={token} agentStatus={status} mouseMode={mouseMode} />}
            </div>
          </Drawer>
        </div>
      </div>

      {/* Drag mask - covers everything including iframes */}
      {isDragging && <div data-id="global-drag-mask" className="fixed inset-0 z-[99998]" />}

      {/* Settings float */}
      {settingsOpen && <SettingsFloat paneId={paneId} fullPaneId={fullPaneId} onClose={() => setSettingsOpen(false)} />}

      {/* Voice Settings Dialog */}
      {voiceSettingsOpen && (
        <div className="fixed inset-0 z-[9999] flex items-center justify-center" onClick={() => setVoiceSettingsOpen(false)}>
          <div className="bg-[#1e1e2e]/95 backdrop-blur-xl rounded-2xl border border-white/10 p-5 w-72 shadow-2xl" onClick={e => e.stopPropagation()}>
            <div className="text-[14px] font-semibold text-white mb-4">🔊 Voice Settings</div>
            <label className="flex items-center justify-between py-2">
              <span className="text-[13px] text-white/70">语音回复</span>
              <button onClick={() => { const v = !voiceReply; setVoiceReply(v); localStorage.setItem('voice_reply', String(v)); if (!v) speechSynthesis.cancel(); }}
                className={`w-10 h-5 rounded-full transition-colors ${voiceReply ? 'bg-green-500' : 'bg-white/20'}`}>
                <div className={`w-4 h-4 rounded-full bg-white shadow transition-transform mx-0.5 ${voiceReply ? 'translate-x-5' : ''}`} />
              </button>
            </label>
            <label className="flex items-center justify-between py-2">
              <span className="text-[13px] text-white/70">自动播放</span>
              <button onClick={() => { const v = !autoPlayReply; setAutoPlayReply(v); localStorage.setItem('auto_play_reply', String(v)); if (!v) speechSynthesis.cancel(); }}
                className={`w-10 h-5 rounded-full transition-colors ${autoPlayReply ? 'bg-green-500' : 'bg-white/20'}`}>
                <div className={`w-4 h-4 rounded-full bg-white shadow transition-transform mx-0.5 ${autoPlayReply ? 'translate-x-5' : ''}`} />
              </button>
            </label>
          </div>
        </div>
      )}

      {/* Toast */}
      {toast && <div data-id="toast" className="fixed top-3 left-1/2 -translate-x-1/2 px-4 py-1.5 text-white text-[11px] font-medium rounded-full shadow-2xl bg-white/10 backdrop-blur-xl border border-white/[0.06] z-[999999]">{toast}</div>}
    </div>
  );
};

export default AgentPage;
