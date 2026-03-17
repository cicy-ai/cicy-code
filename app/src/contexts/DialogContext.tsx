import React, { createContext, useContext, useState, useCallback, useEffect, ReactNode } from 'react';
import ReactDOM from 'react-dom';

type DialogType = 'confirm' | 'createAgent' | 'addAgent' | null;

interface DialogContextType {
  activeDialog: DialogType;
  dialogData: any;
  openDialog: (type: NonNullable<DialogType>, data?: any) => void;
  closeDialog: () => void;
  /** Shortcut: generic confirm with message + callbacks */
  confirm: (message: ReactNode, onConfirm: () => void, onCancel?: () => void) => void;
}

const DialogContext = createContext<DialogContextType | undefined>(undefined);

export const DialogProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [activeDialog, setActiveDialog] = useState<DialogType>(null);
  const [dialogData, setDialogData] = useState<any>(null);
  const [confirmState, setConfirmState] = useState<{ message: ReactNode; onConfirm: () => void; onCancel?: () => void } | null>(null);

  // Disable iframe pointer-events when any dialog is open
  useEffect(() => {
    const iframes = document.querySelectorAll('iframe');
    iframes.forEach(f => f.style.pointerEvents = activeDialog ? 'none' : '');
    return () => { iframes.forEach(f => f.style.pointerEvents = ''); };
  }, [activeDialog]);

  const openDialog = useCallback((type: NonNullable<DialogType>, data?: any) => {
    setActiveDialog(type);
    setDialogData(data);
    setConfirmState(null);
  }, []);

  const closeDialog = useCallback(() => {
    setActiveDialog(null);
    setDialogData(null);
    setConfirmState(null);
  }, []);

  const confirm = useCallback((message: ReactNode, onConfirm: () => void, onCancel?: () => void) => {
    setActiveDialog('confirm');
    setConfirmState({ message, onConfirm, onCancel });
  }, []);

  const value: DialogContextType = { activeDialog, dialogData, openDialog, closeDialog, confirm };

  return (
    <DialogContext.Provider value={value}>
      {children}
      {/* Generic confirm dialog via Portal */}
      {activeDialog === 'confirm' && confirmState && ReactDOM.createPortal(
        <div className="fixed inset-0 z-[9999999] flex items-center justify-center bg-black/50 backdrop-blur-sm">
          <div className="absolute inset-0" onClick={() => { confirmState.onCancel?.(); closeDialog(); }} />
          <div className="relative bg-[#1e1e1e] border border-[var(--vsc-border)] rounded-lg p-4 mx-4 max-w-xs w-full shadow-xl">
            <p className="text-sm text-zinc-300 mb-4">{confirmState.message}</p>
            <div className="flex justify-end gap-2">
              <button onClick={() => { confirmState.onCancel?.(); closeDialog(); }} className="text-sm px-3 py-1.5 rounded bg-white/[0.06] text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.1] transition-colors cursor-pointer">Cancel</button>
              <button onClick={() => { confirmState.onConfirm(); closeDialog(); }} className="text-sm px-3 py-1.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 transition-colors cursor-pointer">Confirm</button>
            </div>
          </div>
        </div>,
        document.body
      )}
    </DialogContext.Provider>
  );
};

export const useDialog = () => {
  const ctx = useContext(DialogContext);
  if (!ctx) throw new Error('useDialog must be used within DialogProvider');
  return ctx;
};
