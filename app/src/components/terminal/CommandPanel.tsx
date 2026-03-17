import React, { useEffect, useState, useRef, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Loader2, ArrowUp, Mic } from 'lucide-react';
import { FloatingPanel } from '../FloatingPanel';
import { TerminalControls } from '../TerminalControls';
import { Position, Size } from '../../types';
import { sendCommandToTmux } from '../../services/mockApi';
import apiService from '../../services/api';
import { useSending } from '../../contexts/SendingContext';

(window as any).__CP_VER__ = 'v2';

// ============================================================================
// Types
// ============================================================================

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

// ============================================================================
// Hooks
// ============================================================================

function useLocalStorage<T>(key: string, initial: T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try { const s = localStorage.getItem(key); return s ? JSON.parse(s) : initial; } catch { return initial; }
  });
  const set = useCallback((v: T) => { setValue(v); localStorage.setItem(key, JSON.stringify(v)); }, [key]);
  return [value, set];
}

function useCommandHistory(paneId: string) {
  const key = `cmd_history_${paneId.replace(/[^a-zA-Z0-9]/g, '_')}`;
  const [history, setHistory] = useLocalStorage<string[]>(key, []);
  const add = useCallback((cmd: string) => {
    setHistory([cmd, ...history.filter(c => c !== cmd)].slice(0, 50));
  }, [history, setHistory]);
  return { history, add };
}

function useDraft(paneId: string) {
  const key = `cmd_draft_${paneId.replace(/[^a-zA-Z0-9]/g, '_')}`;
  const [draft, setDraft] = useState(() => localStorage.getItem(key) || '');
  const save = useCallback((v: string) => { setDraft(v); localStorage.setItem(key, v); }, [key]);
  return [draft, save] as const;
}

// ============================================================================
// Send Logic (centralized)
// ============================================================================

interface SendOptions {
  paneTarget: string;
  setSending: (v: boolean) => void;
  onSuccess?: () => void;
  onError?: (e: Error) => void;
}

async function sendPrompt(cmd: string, opts: SendOptions) {
  const { paneTarget, setSending, onSuccess, onError } = opts;
  if (!cmd.trim() || !paneTarget) return false;
  
  setSending(true);
  window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
  
  try {
    await sendCommandToTmux(cmd, paneTarget);
    onSuccess?.();
    return true;
  } catch (e) {
    onError?.(e as Error);
    return false;
  }
}

// ============================================================================
// Component
// ============================================================================

export const CommandPanel = forwardRef<CommandPanelHandle, CommandPanelProps>(({
  paneTarget,
  token,
  panelPosition,
  panelSize,
  onInteractionStart,
  onInteractionEnd,
  onChange,
  onCapturePane,
  isCapturing,
  agentStatus = 'idle',
  mouseMode = 'off',
  isTogglingMouse = false,
  onToggleMouse,
  hasCapturePermission = false,
  onDraggingChange,
  disableDrag = false,
  showVoiceControl = false,
  onToggleVoiceControl,
  onShowCorrection,
  onCorrectionLoading,
  defaultModel = '',
  onModelChange,
}, ref) => {
  // ---- State ----
  const [selectedPane, setSelectedPane] = useState(paneTarget);
  const [promptText, setPromptText] = useState('');
  const [historyIndex, setHistoryIndex] = useState(-1);
  const [tempDraft, setTempDraft] = useState('');
  const [correctedResult, setCorrectedResult] = useState<[string, string] | null>(null);
  const [isCorrectingEnglish, setIsCorrectingEnglish] = useState(false);
  const [paneModes, setPaneModes] = useLocalStorage<Record<string, 'on' | 'off'>>('pane_mouse_modes', {});
  const [enterToSend, setEnterToSend] = useLocalStorage('enter_to_send', true);
  
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const selectedPaneRef = useRef(selectedPane);
  selectedPaneRef.current = selectedPane;
  const { sending, setSending, checkIdle } = useSending();
  const { history: commandHistory, add: addToHistory } = useCommandHistory(selectedPane);
  const [draft, saveDraft] = useDraft(selectedPane);

  // ---- Sync ----
  useEffect(() => { setSelectedPane(paneTarget); }, [paneTarget]);
  useEffect(() => { setPromptText(draft); }, [selectedPane]);
  useEffect(() => { checkIdle(agentStatus); }, [agentStatus, checkIdle]);

  // ---- Event Listeners ----
  useEffect(() => {
    const handler = (e: CustomEvent) => e.detail?.paneId && setSelectedPane(e.detail.paneId);
    window.addEventListener('selectPane', handler as EventListener);
    return () => window.removeEventListener('selectPane', handler as EventListener);
  }, []);

  // ---- Imperative Handle ----
  useImperativeHandle(ref, () => ({
    focusTextarea: () => setTimeout(() => textareaRef.current?.focus(), 50),
    setPrompt: (text: string) => { setPromptText(text); setTimeout(() => textareaRef.current?.focus(), 50); },
    sendText: async (text: string) => {
      if (!text.trim() || !paneTarget) return;
      setSending(true);
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: text } }));
      await sendCommandToTmux(text, paneTarget);
    },
    correctedResult,
  }));

  // ---- Handlers ----
  const handleSend = useCallback(async (text?: string) => {
    const cmd = (text ?? promptText).trim();
    
    // Block while sending (except slash commands)
    if (sending && !cmd.startsWith('/')) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: 'Agent is busy. Click the loading button to force reset.' }));
      return;
    }

    // Empty prompt + correction result → send corrected
    if (!cmd && correctedResult) {
      const correctedCmd = correctedResult[0];
      addToHistory(correctedCmd);
      setCorrectedResult(null);
      onShowCorrection?.(null as any);
      await sendPrompt(correctedCmd, { paneTarget, setSending });
      return;
    }

    if (!cmd) return;

    // @worker dispatch
    const atMatch = cmd.match(/^@(w-\d+)\s+(.+)$/s);
    if (atMatch) {
      const [, targetWorker, taskMsg] = atMatch;
      setPromptText(''); saveDraft('');
      setSending(true);
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
      await apiService.pushQueue({ pane_id: targetWorker, message: taskMsg, type: 'task' });
      return;
    }

    // Normal send
    addToHistory(cmd);
    setHistoryIndex(-1);
    setTempDraft('');
    setPromptText('');
    saveDraft('');
    await sendPrompt(cmd, { paneTarget, setSending });
  }, [promptText, paneTarget, sending, correctedResult, addToHistory, saveDraft, setSending, onShowCorrection]);

  const handleKeyDown = useCallback(async (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    const textarea = e.currentTarget;
    
    // Cmd+Enter = trigger correction
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.nativeEvent.isComposing) {
      e.preventDefault();
      
      // Has correction result → send it
      if (!promptText.trim() && correctedResult) {
        const cmd = e.shiftKey ? correctedResult[1] : correctedResult[0];
        addToHistory(cmd);
        setCorrectedResult(null);
        onShowCorrection?.(null as any);
        await sendPrompt(cmd, { paneTarget, setSending });
        return;
      }
      
      // Trigger correction
      const cmd = promptText.trim();
      if (cmd && token) {
        addToHistory(cmd);
        setPromptText('');
        saveDraft('');
        setIsCorrectingEnglish(true);
        onCorrectionLoading?.(true);
        try {
          const { data } = await apiService.correctEnglish(cmd);
          if (data.success && Array.isArray(data.result)) {
            setCorrectedResult(data.result);
            onShowCorrection?.(data.result);
          } else {
            setPromptText(cmd); saveDraft(cmd);
            window.dispatchEvent(new CustomEvent('show-toast', { detail: `Error: ${data.error || 'Correction failed'}` }));
          }
        } catch (err: any) {
          setPromptText(cmd); saveDraft(cmd);
          window.dispatchEvent(new CustomEvent('show-toast', { detail: `Error: ${err.message}` }));
        } finally {
          setIsCorrectingEnglish(false);
          onCorrectionLoading?.(false);
        }
      }
      return;
    }

    // Escape/Backspace on empty → send to tmux
    if ((e.key === 'Escape' || e.key === 'Backspace') && !promptText) {
      e.preventDefault();
      const keyMap: Record<string, string> = { backspace: 'BSpace', escape: 'Escape' };
      await apiService.sendKeys(selectedPane, keyMap[e.key.toLowerCase()]);
      return;
    }

    // Ctrl+C on empty → send C-c
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c' && !promptText) {
      e.preventDefault();
      await apiService.sendKeys(selectedPane, 'C-c');
      return;
    }

    // Enter = send
    if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      if (sending) {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: 'Agent is working, please wait...' }));
        return;
      }
      
      const shouldSend = enterToSend ? !e.shiftKey : e.shiftKey;
      if (!shouldSend) return; // newline
      
      e.preventDefault();
      
      // Empty + correction → fill prompt
      if (!promptText.trim() && correctedResult) {
        setPromptText(correctedResult[0]);
        setCorrectedResult(null);
        onShowCorrection?.(null as any);
        return;
      }
      
      // Send or send Enter key
      if (promptText.trim()) {
        await handleSend();
      } else {
        await apiService.sendKeys(selectedPane, 'Enter');
      }
      return;
    }

    // Arrow Up = history prev
    if (e.key === 'ArrowUp') {
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
      return;
    }

    // Arrow Down = history next
    if (e.key === 'ArrowDown') {
      const isOnLastLine = !textarea.value.substring(textarea.selectionStart).includes('\n');
      if (isOnLastLine && historyIndex >= 0) {
        e.preventDefault();
        if (historyIndex > 0) {
          const ni = historyIndex - 1;
          setHistoryIndex(ni);
          setPromptText(commandHistory[ni]);
        } else {
          setHistoryIndex(-1);
          setPromptText(tempDraft);
        }
      }
    }
  }, [promptText, correctedResult, token, selectedPane, sending, enterToSend, commandHistory, historyIndex, tempDraft, paneTarget, addToHistory, saveDraft, setSending, onShowCorrection, onCorrectionLoading, handleSend]);

  const handleModelChange = useCallback(async (model: string) => {
    if (!model || sending) return;
    setSending(true);
    onModelChange?.(model);
    window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: `/model ${model}` } }));
    await sendCommandToTmux(`/model ${model}`, selectedPane);
  }, [sending, setSending, onModelChange, paneTarget, selectedPane]);

  const handleQuickCmd = useCallback(async (key?: string, cmd?: string) => {
    if (key) {
      await apiService.sendKeys(selectedPaneRef.current, key);
    } else if (cmd) {
      setSending(true);
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneTarget, q: cmd } }));
      await sendCommandToTmux(cmd, selectedPaneRef.current);
    }
  }, [setSending, paneTarget]);

  // ---- Render ----
  return (
    <FloatingPanel
      title={
        <>
          <TerminalControls
            mouseMode={paneModes[selectedPane] || mouseMode}
            onToggleMouse={() => {
              const newMode = (paneModes[selectedPane] || mouseMode) === 'on' ? 'off' : 'on';
              setPaneModes({ ...paneModes, [selectedPane]: newMode });
              onToggleMouse?.();
            }}
            isTogglingMouse={isTogglingMouse}
            onCapture={hasCapturePermission ? () => onCapturePane?.(selectedPane) : undefined}
            isCapturing={isCapturing}
          />
          <ModelSelect value={defaultModel} onChange={handleModelChange} />
        </>
      }
      initialPosition={panelPosition}
      initialSize={panelSize}
      minSize={{ width: 600, height: 180 }}
      onInteractionStart={onInteractionStart}
      onInteractionEnd={onInteractionEnd}
      onChange={onChange}
      onDraggingChange={onDraggingChange}
      disableDrag={disableDrag}
      headerActions={
        <>
          <button
            type="button"
            onClick={() => setEnterToSend(!enterToSend)}
            className="cicy-btn text-xs px-1.5 py-0.5 border border-vsc-border select-none"
            title={enterToSend ? 'Enter=Send, Shift+Enter=Newline' : 'Enter=Newline, Shift+Enter=Send'}
          >
            {enterToSend ? '⏎Send' : '⇧⏎Send'}
          </button>
          {onToggleVoiceControl && (
            <button
              onClick={onToggleVoiceControl}
              className={`cicy-btn ${showVoiceControl ? 'text-red-400 bg-red-500/20' : ''}`}
              title={showVoiceControl ? 'Hide voice mode' : 'Show voice mode'}
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
      <form onSubmit={(e) => { e.preventDefault(); handleSend(); }} className="relative h-full flex flex-col p-2">
        {/* Send Button */}
        <div className="absolute top-3 right-3 z-10 flex gap-1">
          <SendButton sending={sending} disabled={!promptText.trim()} onReset={() => setSending(false)} />
        </div>

        {/* Textarea */}
        <textarea
          id="prompt-textarea"
          ref={textareaRef}
          value={promptText}
          onChange={(e) => { setPromptText(e.target.value); saveDraft(e.target.value); if (historyIndex === -1) setTempDraft(e.target.value); }}
          onKeyDown={handleKeyDown}
          placeholder="Type command..."
          className="w-full h-full bg-transparent text-vsc-text rounded-md p-2.5 pr-10 outline-none resize-none text-sm transition-colors placeholder:text-vsc-text-muted/40"
          style={{ paddingRight: '44px' }}
        />

        {/* Quick Commands */}
        <QuickCommands onCmd={handleQuickCmd} />
      </form>
    </FloatingPanel>
  );
});

// ============================================================================
// Sub-components
// ============================================================================

function SendButton({ sending, disabled, onReset }: { sending: boolean; disabled: boolean; onReset: () => void }) {
  return (
    <button
      id="terminal-send-btn"
      type="button"
      disabled={!sending && disabled}
      onClick={(e) => {
        if (sending) {
          e.preventDefault();
          onReset();
        } else {
          // Trigger form submit
          (e.currentTarget.closest('form') as HTMLFormElement)?.requestSubmit();
        }
      }}
      className={`p-1.5 rounded-md transition-all duration-150 disabled:opacity-30 disabled:cursor-not-allowed ${
        sending ? 'bg-orange-500 text-white' : 'bg-vsc-accent hover:bg-vsc-accent-hover text-white'
      }`}
      title={sending ? 'Click to stop' : 'Send'}
    >
      {sending ? <Loader2 size={14} className="animate-spin" /> : <ArrowUp size={14} />}
    </button>
  );
}

function ModelSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <select
      value={value}
      onChange={(e) => e.target.value && onChange(e.target.value)}
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
  );
}

function useTwiceConfirm(timeout = 2000) {
  const [pending, setPending] = useState(false);
  const ref = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const click = useCallback((action: () => void) => {
    if (!ref.current) {
      ref.current = true; setPending(true);
      timer.current = setTimeout(() => { ref.current = false; setPending(false); }, timeout);
      return;
    }
    clearTimeout(timer.current); ref.current = false; setPending(false);
    action();
  }, [timeout]);
  return { pending, click };
}

const QuickCommands = React.memo(function QuickCommands({ onCmd }: { onCmd: (key?: string, cmd?: string) => void }) {
  const ctrlC = useTwiceConfirm();
  const compact = useTwiceConfirm();

  return (
    <div className="flex gap-1 flex-wrap px-2 pb-1.5 pt-0.5">
      <button
        type="button"
        title="Send Ctrl+C to interrupt (click twice)"
        className={`px-2 py-0.5 text-[10px] rounded-full transition-colors ${
          ctrlC.pending
            ? 'bg-red-500/40 text-red-300 animate-pulse'
            : 'bg-red-500/15 text-red-400/70 hover:bg-red-500/25 hover:text-red-400'
        }`}
        onClick={() => ctrlC.click(() => onCmd('C-c'))}
      >
        {ctrlC.pending ? 'confirm?' : '^C'}
      </button>
      <button
        type="button"
        title="Compact chat history to save context (click twice)"
        className={`px-2 py-0.5 text-[10px] rounded-full transition-colors ${
          compact.pending
            ? 'bg-purple-500/40 text-purple-300 animate-pulse'
            : 'bg-white/[0.04] text-vsc-text-muted/40 hover:bg-white/[0.08] hover:text-vsc-text-secondary'
        }`}
        onClick={() => compact.click(() => onCmd(undefined, '/compact --truncate-large-messages true --max-message-length 500'))}
      >
        {compact.pending ? 'confirm?' : '/compact cut'}
      </button>
    </div>
  );
});
