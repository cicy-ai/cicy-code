import React from 'react';
import { urls } from '../../config';
import { usePointerLock } from '../../lib/pointerLock';

interface TerminalFrameProps {
  paneId: string;
  token: string;
}

const TerminalFrame: React.FC<TerminalFrameProps> = ({ paneId, token }) => {
  const locked = usePointerLock();
  return (
    <div className="relative w-full h-full">
      {locked && <div className="absolute inset-0 z-20" />}
      <iframe
        src={urls.ttydOpen(paneId, token)}
        className="w-full h-full border-0 bg-black"
        title={`terminal-${paneId}`}
      />
    </div>
  );
};

export default TerminalFrame;
