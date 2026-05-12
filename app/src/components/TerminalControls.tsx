import React from 'react';
import { Mouse, FileText, Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';

interface TerminalControlsProps {
  mouseMode?: 'on' | 'off';
  onToggleMouse?: () => void;
  isTogglingMouse?: boolean;
  onCapture?: () => void;
  isCapturing?: boolean;
}

export const TerminalControls: React.FC<TerminalControlsProps> = ({
  mouseMode,
  onToggleMouse,
  isTogglingMouse,
  onCapture,
  isCapturing
}) => {
  const { t } = useTranslation('ui');
  return (
    <>
      {false && onToggleMouse && (
        <button
          type="button"
          onClick={onToggleMouse}
          disabled={isTogglingMouse}
          className={`p-1 rounded transition-colors ${mouseMode === 'on' ? 'text-green-400 bg-green-500/20' : 'text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-active'}`}
          title={mouseMode === 'on' ? t('terminalMouseOn') : t('terminalMouseOff')}
        >
          {isTogglingMouse ? <Loader2 size={14} className="animate-spin" /> : <Mouse size={14} />}
        </button>
      )}
      {false && onCapture && (
        <button
          type="button"
          onClick={onCapture}
          disabled={isCapturing}
          className="p-1 rounded text-yellow-400 hover:text-yellow-300 hover:bg-vsc-bg-active disabled:opacity-40"
          title={t('terminalCaptureFrame')}
        >
          {isCapturing ? <Loader2 size={14} className="animate-spin" /> : <FileText size={14} />}
        </button>
      )}
    </>
  );
};
