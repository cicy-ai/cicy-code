import React from 'react';
import { urls } from '../../config';

interface TerminalFrameProps {
  paneId: string;
  token: string;
}

const TerminalFrame: React.FC<TerminalFrameProps> = ({ paneId, token }) => (
  <iframe
    src={urls.ttydOpen(paneId, token)}
    className="w-full h-full border-0 bg-black"
    title={`terminal-${paneId}`}
  />
);

export default TerminalFrame;
