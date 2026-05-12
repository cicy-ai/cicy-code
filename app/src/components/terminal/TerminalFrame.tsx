import React from 'react';
import { useTranslation } from 'react-i18next';
import { urls } from '../../config';
import { WebFrame } from '../WebFrame';

interface TerminalFrameProps {
  paneId: string;
  token: string;
}

const TerminalFrame: React.FC<TerminalFrameProps> = ({ paneId, token }) => {
  const { i18n: i18nLive } = useTranslation();
  const lang = i18nLive.resolvedLanguage ?? i18nLive.language ?? 'en';
  return (
    <div className="relative w-full h-full">
      <WebFrame
        src={urls.ttydOpen(paneId, token, lang)}
        className="w-full h-full border-0 bg-black"
        title={`terminal-${paneId}`}
      />
    </div>
  );
};

export default TerminalFrame;
