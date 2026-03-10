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
        <div className="fixed inset-0 z-[9999999] flex items-center justify-center">
          <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={() => { confirmState.onCancel?.(); closeDialog(); }} />
          <div className="relative bg-vsc-bg border border-vsc-border rounded-lg shadow-lg p-6 min-w-[350px] max-w-[500px]">
            <div className="text-vsc-text text-sm mb-5">{confirmState.message}</div>
            <div className="flex justify-end gap-2">
              <button onClick={() => { confirmState.onCancel?.(); closeDialog(); }} className="px-4 py-1.5 text-sm bg-vsc-bg-secondary hover:bg-vsc-bg-hover text-vsc-text rounded border border-vsc-border">
                Cancel
              </button>
              <button onClick={() => { confirmState.onConfirm(); closeDialog(); }} className="px-4 py-1.5 text-sm bg-vsc-button hover:bg-vsc-button-hover text-white rounded">
                Confirm
              </button>
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
