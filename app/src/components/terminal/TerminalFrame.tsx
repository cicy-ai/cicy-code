import React from 'react';
import { urls } from '../../config';
import { WebFrame } from '../WebFrame';

interface TerminalFrameProps {
  paneId: string;
  token: string;
}

const TerminalFrame: React.FC<TerminalFrameProps> = ({ paneId, token }) => {
  return (
    <div className="relative w-full h-full">
      <WebFrame
        src={urls.ttydOpen(paneId, token)}
        className="w-full h-full border-0 bg-black"
        title={`terminal-${paneId}`}
      />
    </div>
  );
};

export default TerminalFrame;
