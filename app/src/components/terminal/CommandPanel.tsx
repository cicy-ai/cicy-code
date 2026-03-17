import React, { useEffect ,useState, useRef, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Loader2, CheckCircle, History, Mic, ArrowUp } from 'lucide-react';
import { FloatingPanel } from '../FloatingPanel';
import { TerminalControls } from '../TerminalControls';
import { Position, Size } from '../../types';
import { sendCommandToTmux } from '../../services/mockApi';
import apiService from '../../services/api';
import { useSending } from '../../contexts/SendingContext';

const style = document.createElement('style');
style.textContent = `
  @keyframes slideUp {
    from { transform: translateY(20px); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }
`;
document.head.appendChild(style);

interface CommandPanelProps {
  paneTarget: string;
  title: string;
  token: string | null;
  panelPosition: Position;
  panelSize: Size;
  readOnly: boolean;
  onReadOnlyToggle: () => void;
  onInteractionStart: () => void;
  onInteractionEnd: () => void;
  onChange: (pos: Position, size: Size) => void;
  onCapturePane?: (pane_id?: string) => void;
  isCapturing?: boolean;
  canSend?: boolean;
  agentStatus?: string;
  contextUsage?: number | null;
  mouseMode?: 'on' | 'off';
  isTogglingMouse?: boolean;
  onToggleMouse?: () => void;
  onEditPane?: () => void;
  onRestart?: (pane_id?: string) => void;
  isRestarting?: boolean;
  hasEditPermission?: boolean;
  hasRestartPermission?: boolean;
  hasCapturePermission?: boolean;
  networkLatency?: number | null;
  networkStatus?: 'excellent' | 'good' | 'poor' | 'offline';
  onDraggingChange?: (isDragging: boolean) => void;
  boundAgents?: string[];
  onPaneTargetChange?: (target: string) => void;
  disableDrag?: boolean;
  showVoiceControl?: boolean;
  onToggleVoiceControl?: () => void;
  voiceReply?: boolean;
  onToggleVoiceReply?: () => void;
  mode?: string | null;
  onShowHistory?: (history: string[], onSelect: (cmd: string) => void) => void;
  onShowCorrection?: (result: [string, string]) => void;
  onCorrectionLoading?: (loading: boolean) => void;
  onShowPromptModal?: () => void;
  defaultModel?: string;
  onModelChange?: (model: string) => void;
  onOpenDrawer?: () => void;
}

export interface CommandPanelHandle {
  focusTextarea: () => void;
  setPrompt: (text: string) => void;
  correctedResult: [string, string] | null;
}

export const CommandPanel = forwardRef<CommandPanelHandle, CommandPanelProps>(({
  paneTarget,
  title,
  token,
  panelPosition,
  panelSize,
  readOnly,
  onReadOnlyToggle,
  onInteractionStart,
  onInteractionEnd,
  onChange,
  onCapturePane,
  isCapturing,
  canSend = true,
  agentStatus = 'idle',
  contextUsage,
  mouseMode = 'off',
  isTogglingMouse = false,
  onToggleMouse,
  onEditPane,
  onRestart,
  isRestarting = false,
  hasEditPermission = false,
  hasRestartPermission = false,
  hasCapturePermission = false,
  networkLatency = null,
  networkStatus = 'good',
  onDraggingChange,
  boundAgents = [],
  onPaneTargetChange,
  disableDrag = false,
  showVoiceControl = false,
  onToggleVoiceControl,
  voiceReply = false,
  onToggleVoiceReply,
  mode = null,
  onShowHistory,
  onShowCorrection,
  onCorrectionLoading,
  onShowPromptModal,
  defaultModel = '',
  onModelChange,
  onOpenDrawer,
}, ref) => {
  const [selectedPane, setSelectedPane] = useState(paneTarget);

  // Sync selectedPane when paneTarget changes (switching agents)
  useEffect(() => { setSelectedPane(paneTarget); }, [paneTarget]);

  const [promptText, setPromptText] = useState('');
  const [commandHistory, setCommandHistory] = useState<string[]>([]);
  const [historyIndex, setHistoryIndex] = useState(-1);
  const [tempDraft, setTempDraft] = useState('');
  const [paneModes, setPaneModes] = useState<Record<string, 'on' | 'off'>>(() => {
    const saved = localStorage.getItem('pane_mouse_modes');
    return saved ? JSON.parse(saved) : {};
  });

  const tempPaneId = (selectedPane || '').replace(/[^a-zA-Z0-9]/g, '_');

  const CMD_HISTORY_KEY = `cmd_history_${tempPaneId}`;

  // 当切换 pane 时，应用该 pane 的鼠标模式
  useEffect(() => {
    const mode = paneModes[selectedPane] || mouseMode;
    if (mode !== mouseMode && onToggleMouse) {
      apiService.toggleMouse(mode, selectedPane);
    }
  }, [selectedPane]);

  useEffect(() => {
    const handleSelectPane = (e: CustomEvent) => {
      const paneId = e.detail?.paneId;
      console.log('[CommandPanel] Received selectPane event:', paneId);
      console.log('[CommandPanel] Current boundAgents:', boundAgents);
      console.log('[CommandPanel] paneTarget:', paneTarget);
      if (paneId) {
        setSelectedPane(paneId);
        console.log('[CommandPanel] Updated selectedPane to:', paneId);
      }
    };
    window.addEventListener('selectPane', handleSelectPane as EventListener);
    return () => window.removeEventListener('selectPane', handleSelectPane as EventListener);
  }, [boundAgents, paneTarget]);

  useEffect(() => {
    const saved = localStorage.getItem(CMD_HISTORY_KEY);
    try { setCommandHistory(saved ? JSON.parse(saved) : []); } catch { setCommandHistory([]); }
    setHistoryIndex(-1);
    setTempDraft('');
  }, [selectedPane]);

  const saveCommandHistory = (history: string[]) => {
    localStorage.setItem(CMD_HISTORY_KEY, JSON.stringify(history));
  };

  const DRAFT_KEY = `cmd_draft_${tempPaneId}`;
  const saveDraft = (text: string) => {
    localStorage.setItem(DRAFT_KEY, text);
  };

  useEffect(() => {
    const savedDraft = localStorage.getItem(DRAFT_KEY);
    setPromptText(savedDraft || '');
  }, [selectedPane]);

  const [isSending, setIsSending] = useState(false);
  const [sendSuccess, setSendSuccess] = useState(false);
  const [isBroadcasting, setIsBroadcasting] = useState(false);
  const [broadcastSuccess, setBroadcastSuccess] = useState(false);
  const [correctedResult, setCorrectedResult] = useState<[string, string] | null>(null);
  const [isCorrectingEnglish, setIsCorrectingEnglish] = useState(false);
  const [autoCorrectEnabled, setAutoCorrectEnabled] = useState(true);
  const [showHistory, setShowHistory] = useState(false);
  const [enterToSend, setEnterToSend] = useState<boolean>(() => {
    const saved = localStorage.getItem('enter_to_send');
    return saved === null ? true : saved === 'true';
  });
  const sendQueueRef = useRef<string[]>([]);
  const [queueLen, setQueueLen] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [currentPos, setCurrentPos] = useState(panelPosition);
  const [currentSize, setCurrentSize] = useState(panelSize);
  const [isFocused, setIsFocused] = useState(false);

  // Sending state from context
  const { sending, setSending, checkIdle } = useSending();

  // Check idle on agentStatus change
  useEffect(() => {
    checkIdle(agentStatus);
  }, [agentStatus, checkIdle]);

  useEffect(() => {
    setCurrentPos(panelPosition);
    setCurrentSize(panelSize);
  }, [panelPosition, panelSize]);

  const sendTextDirect = useCallback(async (text: string) => {
    const cmd = text.trim();
    if (!cmd || !paneTarget) return;
    setSending(true);
    window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
    await sendCommandToTmux(cmd, paneTarget);
  }, [paneTarget, setSending]);

  useImperativeHandle(ref, () => ({
    focusTextarea: () => { setTimeout(() => textareaRef.current?.focus(), 50); },
    setPrompt: (text: string) => { setPromptText(text); setTimeout(() => textareaRef.current?.focus(), 50); },
    sendText: sendTextDirect,
    correctedResult: correctedResult,
  }));

  const handleSendPrompt = useCallback(async (e?: React.FormEvent) => {
    e?.preventDefault();
    const cmd = promptText.trim();
    
    // Block ALL prompts while sending (except slash commands)
    if (sending && !cmd.startsWith('/')) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: 'Agent is busy. Click the loading button to force reset.' }));
      return;
    }
    
    // If prompt is empty but correction result exists, send the corrected English
    if (!cmd && correctedResult) {
      const correctedCmd = correctedResult[0]; // Use English part
      const newHistory = [correctedCmd, ...commandHistory.filter(c => c !== correctedCmd)].slice(0, 50);
      setCommandHistory(newHistory);
      saveCommandHistory(newHistory);
      setCorrectedResult(null);
      if (onShowCorrection) {
        onShowCorrection(null as any);
      }
      setSending(true);
      setIsSending(true);
      setSendSuccess(false);
      try {
        await sendCommandToTmux(correctedCmd, paneTarget);
        setSendSuccess(true);
        setTimeout(() => setSendSuccess(false), 2000);
      } catch (e) { 
        console.error(e);
      }
      finally {
        setIsSending(false);
        setTimeout(() => textareaRef.current?.focus(), 50);
      }
      return;
    }
    
    if (!cmd || !paneTarget) return;
    
    // @worker dispatch: "@w-20147 do something" → queue to worker
    const atMatch = cmd.match(/^@(w-\d+)\s+(.+)$/s);
    if (atMatch) {
      const [, targetWorker, taskMsg] = atMatch;
      setPromptText(''); saveDraft('');
      setSending(true);
      setIsSending(true);
      try {
        window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
        await apiService.pushQueue({ pane_id: targetWorker, message: taskMsg, type: 'task' });
        setSendSuccess(true); setTimeout(() => setSendSuccess(false), 2000);
      } catch (e) { console.error(e); }
      finally { setIsSending(false); setTimeout(() => textareaRef.current?.focus(), 50); }
      return;
    }

    const newHistory = [cmd, ...commandHistory.filter(c => c !== cmd)].slice(0, 50);
    setPromptText('');
    saveDraft('');
    setSending(true);
    setIsSending(true);
    setSendSuccess(false);
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
      await sendCommandToTmux(cmd, paneTarget);
      setSendSuccess(true);
      setTimeout(() => setSendSuccess(false), 2000);
    } catch (e) { console.error(e); }
    finally {
      setIsSending(false);
      setTimeout(() => textareaRef.current?.focus(), 50);
    }
  }, [promptText, paneTarget, autoCorrectEnabled, token, correctedResult, commandHistory, selectedPane, onShowCorrection, sending]);

  const handleBroadcast = useCallback(async () => {
    const cmd = promptText.trim();
    if (!cmd) return;
    
    const saved = localStorage.getItem('pinnedPanes');
    const pinnedPanes: string[] = saved ? JSON.parse(saved) : [];
    
    if (pinnedPanes.length === 0) return;
    
    setIsBroadcasting(true);
    setBroadcastSuccess(false);
    
    try {
      await Promise.all(pinnedPanes.map(paneId => sendCommandToTmux(cmd, paneId)));
      setBroadcastSuccess(true);
      setTimeout(() => setBroadcastSuccess(false), 2000);
    } catch (e) {
      console.error('Broadcast error:', e);
    } finally {
      setIsBroadcasting(false);
    }
  }, [promptText]);

  // 队列自动发送已禁用
  // useEffect(() => {
  //   if (!canSend || sendQueueRef.current.length === 0) return;
  //   const queued = sendQueueRef.current.join('\n');
  //   sendQueueRef.current = [];
  //   setQueueLen(0);
  //   setIsSending(true);
  //   sendCommandToTmux(queued, paneTarget)
  //     .then(() => { setSendSuccess(true); setTimeout(() => setSendSuccess(false), 2000); })
  //     .catch(console.error)
  //     .finally(() => { setIsSending(false); });
  // }, [canSend, paneTarget]);

  const handleCorrectEnglish = async () => {
    if (!promptText.trim() || isCorrectingEnglish || !token) return;
    setIsCorrectingEnglish(true); if (onCorrectionLoading) onCorrectionLoading(true);
    setCorrectedResult(null);
    try {
      const { data } = await apiService.correctEnglish(promptText);
      console.log('Correct English result:', data);
      if (data.success && data.result && Array.isArray(data.result)) {
        // result is [English, Chinese]
        setCorrectedResult(data.result);
        if (onShowCorrection) {
          onShowCorrection(data.result);
        }
      }
    } catch (e) { 
      console.error('Correct English error:', e); 
    } finally { 
      setIsCorrectingEnglish(false); if (onCorrectionLoading) onCorrectionLoading(false); 
    }
  };

  const handleAcceptCorrection = () => {
    if (correctedResult) {
      setPromptText(correctedResult[0]);
      setCorrectedResult(null);
      setTimeout(() => textareaRef.current?.focus(), 50);
    }
  };

  const handleSelectHistory = (cmd: string) => {
    setPromptText(cmd);
    setShowHistory(false);
    setTimeout(() => textareaRef.current?.focus(), 50);
  };

  return (
    <>
      <FloatingPanel
        title={
          <>
            <TerminalControls
              mouseMode={paneModes[selectedPane] || mouseMode}
              onToggleMouse={() => {
                const newMode = (paneModes[selectedPane] || mouseMode) === 'on' ? 'off' : 'on';
                const updated = { ...paneModes, [selectedPane]: newMode };
                setPaneModes(updated);
                localStorage.setItem('pane_mouse_modes', JSON.stringify(updated));
                onToggleMouse?.();
              }}
              isTogglingMouse={isTogglingMouse}
              onCapture={hasCapturePermission ? () => onCapturePane?.(selectedPane) : undefined}
              isCapturing={isCapturing}
            />
            <select
              className="cicy-select hidden"
              value=""
              onChange={async (e) => {
                const v = e.target.value;
                if (!v) return;
                e.target.value = '';
                if (['Left', 'Down', 'Up', 'Right'].includes(v)) {
                  await apiService.sendKeys(selectedPane, v);
                } else if (v === 'C-c') {
                  await apiService.sendKeys(selectedPane, 'C-c');
                } else {
                  if (sending) return;
                  setSending(true);
                  window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: v } }));
                  await sendCommandToTmux(v, selectedPane);
                }
              }}
            >
              <option value="">⚡</option>
              <option value="C-c">^C</option>
              <option value="/chat resume">/chat resume</option>
              <option value="/tools trust-all">/tools trust-all</option>
              <option value="/compact">/compact</option>
              <option value="/compact --truncate-large-messages true --max-message-length 500">/compact cut</option>
              <option value="kiro-cli chat -a">kiro-cli chat -a</option>
            </select>
            <select
              value={defaultModel}
              onChange={async (e) => {
                const v = e.target.value;
                if (!v) return;
                if (sending) return;
                setSending(true);
                onModelChange?.(v);
                window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: `/model ${v}` } }));
                await sendCommandToTmux(`/model ${v}`, selectedPane);
              }}
              className="cicy-select"
              title="Select model"
            >
              <option value="">🧠</option>
              <option value="claude-opus-4.6">opus-4.6</option>
              <option value="claude-opus-4.5">opus-4.5</option>
              <option value="claude-sonnet-4.5">sonnet-4.5</option>
              <option value="claude-sonnet-4">sonnet-4</option>
              <option value="claude-haiku-4.5">haiku-4.5</option>
              <option value="deepseek-3.2">deepseek-3.2</option>
              <option value="minimax-m2.1">minimax-m2.1</option>
              <option value="qwen3-coder-next">qwen3-coder</option>
            </select>
            <button
              type="button"
              onClick={() => {
                if (onShowHistory) {
                  onShowHistory(commandHistory, handleSelectHistory);
                }
              }}
              className="p-1.5 rounded transition-colors text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-active hidden"
              title="Command history"
            >
              <History size={14} />
            </button>
            <button
              type="button"
              onClick={() => {
                if (onShowPromptModal) {
                  onShowPromptModal();
                }
              }}
              className="p-1.5 rounded transition-colors text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-active hidden"
              title="Edit common prompts"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>
            </button>
          </>
        }
        initialPosition={panelPosition}
        initialSize={panelSize}
      minSize={{ width: 600, height: 180 }}
      onInteractionStart={onInteractionStart}
      onInteractionEnd={onInteractionEnd}
      onChange={(pos, size) => {
        setCurrentPos(pos);
        setCurrentSize(size);
        onChange(pos, size);
      }}
      onDraggingChange={onDraggingChange}
      disableDrag={disableDrag}
      headerActions={
        <>
          <button
            type="button"
            onClick={() => {
              const next = !enterToSend;
              setEnterToSend(next);
              localStorage.setItem('enter_to_send', String(next));
            }}
            className="cicy-btn text-xs px-1.5 py-0.5 border border-vsc-border select-none"
            title={enterToSend ? 'Enter=Send, Shift+Enter=Newline' : 'Enter=Newline, Shift+Enter=Send'}
          >
            {enterToSend ? '⏎Send' : '⇧⏎Send'}
          </button>
          {onToggleVoiceControl && (
            <button
              onClick={onToggleVoiceControl}
              className={`cicy-btn ${showVoiceControl ? 'text-red-400 bg-red-500/20' : ''}`}
              title={showVoiceControl ? "Hide voice mode" : "Show voice mode"}
            >
              <Mic size={14} />
            </button>
          )}
          {isCorrectingEnglish && (
            <div className="flex items-center gap-1 px-2 py-1 text-purple-400 text-xs">
              <Loader2 size={12} className="animate-spin" />
            </div>
          )}
        </>
      }
    >
      <form onSubmit={handleSendPrompt} className="relative h-full flex flex-col p-2">
        <div className="absolute top-3 right-3 z-10 flex gap-1">
          <button
            id="terminal-send-btn"
            type={sending ? 'button' : 'submit'}
            disabled={!sending && (!promptText.trim() || isSending)}
            onClick={sending ? () => setSending(false) : undefined}
            className={`p-1.5 rounded-md transition-all duration-150 disabled:opacity-30 disabled:cursor-not-allowed ${sending ? 'bg-orange-500 text-white' : 'bg-vsc-accent hover:bg-vsc-accent-hover text-white'}`}
            title={sending ? 'Click to stop' : 'Send'}
          >
            {sending ? <Loader2 size={14} className="animate-spin" /> : <ArrowUp size={14} />}
          </button>
        </div>
        <textarea
              id="prompt-textarea"
              ref={textareaRef}
              value={promptText}
              onChange={(e) => {
                setPromptText(e.target.value);
                saveDraft(e.target.value);
                if (historyIndex === -1) setTempDraft(e.target.value);
              }}
              onFocus={() => setIsFocused(true)}
              onBlur={() => setIsFocused(false)}
              onKeyDown={async (e) => {
                // Ctrl+Enter = trigger correction or send result
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.nativeEvent.isComposing) {
                  console.log('Cmd+Enter pressed', {promptText, correctedResult, token});
                  e.preventDefault();
                  
                  // If no text but has correction result
                  if (!promptText.trim() && correctedResult) {
                    // Cmd+Shift+Enter = send Chinese
                    if (e.shiftKey) {
                      const cmd = correctedResult[1];
                      const newHistory = [cmd, ...commandHistory.filter(c => c !== cmd)].slice(0, 50);
                      setCommandHistory(newHistory);
                      saveCommandHistory(newHistory);
                      setCorrectedResult(null);
                      if (onShowCorrection) {
                        onShowCorrection(null as any);
                      }
                      setSending(true);
                      setIsSending(true);
                      setSendSuccess(false);
                      sendCommandToTmux(cmd, paneTarget)
                        .then(() => { setSendSuccess(true); setTimeout(() => setSendSuccess(false), 2000); })
                        .catch(console.error)
                        .finally(() => { setIsSending(false); setTimeout(() => textareaRef.current?.focus(), 50); });
                      return;
                    }
                    
                    // Cmd+Enter = send English
                    const cmd = correctedResult[0];
                    const newHistory = [cmd, ...commandHistory.filter(c => c !== cmd)].slice(0, 50);
                    setCommandHistory(newHistory);
                    saveCommandHistory(newHistory);
                    setCorrectedResult(null);
                    if (onShowCorrection) {
                      onShowCorrection(null as any);
                    }
                    setSending(true);
                    setIsSending(true);
                    setSendSuccess(false);
                    sendCommandToTmux(cmd, paneTarget)
                      .then(() => { setSendSuccess(true); setTimeout(() => setSendSuccess(false), 2000); })
                      .catch(console.error)
                      .finally(() => { setIsSending(false); setTimeout(() => textareaRef.current?.focus(), 50); });
                    return;
                  }
                  
                  // Otherwise, trigger correction
                  const cmd = promptText.trim();
                  console.log('Triggering correction', {cmd, token, hasCallback: !!onCorrectionLoading});
                  if (cmd && token) {
                    // Add to history before clearing
                    const newHistory = [cmd, ...commandHistory.filter(c => c !== cmd)].slice(0, 50);
                    setCommandHistory(newHistory);
                    saveCommandHistory(newHistory);
                    
                    setPromptText('');
                    saveDraft('');
                    console.log('Setting loading true');
                    setIsCorrectingEnglish(true); if (onCorrectionLoading) onCorrectionLoading(true);
                    apiService.correctEnglish(cmd)
                      .then(({ data }) => {
                        if (data.success && data.result && Array.isArray(data.result)) {
                          setCorrectedResult(data.result);
                          if (onShowCorrection) {
                            onShowCorrection(data.result);
                          }
                        } else {
                          setPromptText(cmd); saveDraft(cmd);
                          window.dispatchEvent(new CustomEvent('show-toast', { detail: `Error: ${data.error || 'Correction failed'}` }));
                        }
                      })
                      .catch(e => { console.error('Correct English error:', e); setPromptText(cmd); saveDraft(cmd); window.dispatchEvent(new CustomEvent('show-toast', { detail: `Error: ${e.message}` })); })
                      .finally(() => setIsCorrectingEnglish(false));
                  }
                  return;
                }

  
               if ((
                  e.key === 'Escape'|| e.key === 'Backspace'
                ) && !promptText) {
                  e.preventDefault();

                  const key_map: Record<string, string> = {
                      "backspace": "BSpace",
                      "escape": "Escape",
                      "esc": "Escape",
                  }
                  await apiService.sendKeys(selectedPane, key_map[e.key.toLowerCase()]);
                } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c' && !promptText) {
                  e.preventDefault();
                  await apiService.sendKeys(selectedPane, "C-c");
                } else  if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
                  if (sending) {
                    window.dispatchEvent(new CustomEvent('show-toast', { detail: 'Agent is working, please wait...' }));
                    return;
                  }
                  const shouldSend = enterToSend ? !e.shiftKey : e.shiftKey;
                  if (!shouldSend) {
                    // newline (default behavior)
                    return;
                  } else {
                    // Enter = send directly (no correction)
                    e.preventDefault();
                    if (!promptText.trim() && correctedResult) {
                      // Empty prompt + has result = fill prompt with result
                      setPromptText(correctedResult[0]);
                      setCorrectedResult(null);
                      if (onShowCorrection) {
                        onShowCorrection(null as any);
                      }
                    } else {
                      const cmd = promptText.trim();
                      if (cmd) {
                        const newHistory = [cmd, ...commandHistory.filter(c => c !== cmd)].slice(0, 50);
                        setCommandHistory(newHistory);
                        saveCommandHistory(newHistory);
                        setHistoryIndex(-1);
                        setTempDraft('');
                        setPromptText('');
                        saveDraft('');
                        setSending(true);
                        setIsSending(true);
                        setSendSuccess(false);
                        window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
                        sendCommandToTmux(cmd, paneTarget)
                          .then(() => { setSendSuccess(true); setTimeout(() => setSendSuccess(false), 2000); })
                          .catch(console.error)
                          .finally(() => { setIsSending(false); setTimeout(() => textareaRef.current?.focus(), 50); });
                      } else {
                        // Empty prompt, no correction result → send Enter to tmux
                        setPromptText('');
                        await apiService.sendKeys(selectedPane, "Enter");
                      }
                    }
                  }
                } else if (e.key === 'ArrowUp') {
                  const textarea = e.currentTarget;
                  const isOnFirstLine = !textarea.value.substring(0, textarea.selectionStart).includes('\n');
                  if (isOnFirstLine && commandHistory.length > 0) {
                    e.preventDefault();
                    if (historyIndex === -1) {
                      setTempDraft(promptText);
                      setHistoryIndex(0);
                      setPromptText(commandHistory[0]);
                    } else if (historyIndex < commandHistory.length - 1) {
                      const ni = historyIndex + 1;
                      setHistoryIndex(ni);
                      setPromptText(commandHistory[ni]);
                    }
                  }
                } else if (e.key === 'ArrowDown') {
                  const textarea = e.currentTarget;
                  const isOnLastLine = !textarea.value.substring(textarea.selectionStart).includes('\n');
                  if (isOnLastLine) {
                    e.preventDefault();
                    if (historyIndex > 0) {
                      const ni = historyIndex - 1;
                      setHistoryIndex(ni);
                      setPromptText(commandHistory[ni]);
                    } else if (historyIndex === 0) {
                      setHistoryIndex(-1);
                      setPromptText(tempDraft);
                    }
                  }
                }
              }}
              placeholder="Type command..."
              className="w-full h-full bg-transparent text-vsc-text rounded-md p-2.5 pr-10 outline-none resize-none text-sm transition-colors placeholder:text-vsc-text-muted/40"
              style={{paddingRight: '44px'}}
              disabled={isSending}
            />
        {/* Quick commands bar */}
        <div className="flex gap-1 flex-wrap px-2 pb-1.5 pt-0.5">
          {[
            { label: '^C', key: 'C-c', accent: true, confirm: true },
            { label: '/compact cut', cmd: '/compact --truncate-large-messages true --max-message-length 500' },
          ].map(({ label, key, cmd, accent, confirm }) => {
            const [pending, setPending] = React.useState(false);
            const timerRef = React.useRef<ReturnType<typeof setTimeout>>(undefined);
            return (
            <button
              key={label}
              type="button"
              className={`px-2 py-0.5 text-[10px] rounded-full transition-colors ${
                pending
                  ? 'bg-red-500/40 text-red-300 animate-pulse'
                  : accent 
                    ? 'bg-red-500/15 text-red-400/70 hover:bg-red-500/25 hover:text-red-400' 
                    : 'bg-white/[0.04] text-vsc-text-muted/40 hover:bg-white/[0.08] hover:text-vsc-text-secondary'
              }`}
              onClick={async () => {
                if (confirm && !pending) {
                  setPending(true);
                  timerRef.current = setTimeout(() => setPending(false), 2000);
                  return;
                }
                if (timerRef.current) clearTimeout(timerRef.current);
                setPending(false);
                if (key) {
                  await apiService.sendKeys(selectedPane, key);
                } else if (cmd) {
                  if (sending && !cmd.startsWith('/')) return;
                  setSending(true);
                  window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
                  await sendCommandToTmux(cmd, selectedPane);
                }
              }}
            >
              {pending ? 'confirm?' : label}
            </button>
            );
          })}
        </div>
      </form>
    </FloatingPanel>


    {/* 队列显示面板 */}
    {queueLen > 0 && (
      <div 
        className="fixed bg-vsc-bg/95 border border-orange-500/50 rounded-lg p-2 shadow-xl backdrop-blur-sm z-[50]"
        style={{ 
          left: currentPos.x,
          top: currentPos.y + currentSize.height + 8,
          width: currentSize.width
        }}
      >
        <div className="text-xs text-vsc-text mb-2 max-h-24 overflow-y-auto whitespace-pre-wrap bg-black/30 p-2 rounded">
          {sendQueueRef.current.join('\n\n')}
        </div>
        <button
          onClick={() => {
            const merged = sendQueueRef.current.join('\n\n');
            setPromptText(merged);
            sendQueueRef.current = [];
            setQueueLen(0);
            setTimeout(() => textareaRef.current?.focus(), 50);
          }}
          className="w-full text-xs px-2 py-1 bg-vsc-button hover:bg-vsc-button-hover text-white rounded transition-colors"
        >
          Edit
        </button>
      </div>
    )}
  </>
  );
});
