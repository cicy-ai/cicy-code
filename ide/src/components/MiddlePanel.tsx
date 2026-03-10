import React from 'react';
import { urls } from '../config';
import { WebFrame } from './WebFrame';
import { useApp } from '../contexts/AppContext';
import { usePane } from '../contexts/PaneContext';

const MiddlePanel: React.FC = () => {
  const { currentPaneId, allPanes } = useApp();
  const { token } = usePane();
  const currentPane = allPanes.find((p: any) => p.pane_id === currentPaneId);

  if (!currentPane) {
    return (
      <div className="h-full flex items-center justify-center bg-vsc-bg text-vsc-text-secondary">
        No pane selected
      </div>
    );
  }

  return (
    <>
      <div className="h-10 bg-vsc-bg-titlebar border-b border-vsc-border flex items-center px-2">
        <div className="text-sm text-vsc-text truncate">
          {currentPane.title || currentPane.pane_id}
        </div>
      </div>
      <div className="h-[calc(100%-40px)]">
        <WebFrame
          src={urls.ttyd(currentPane.pane_id, token)}
          className="w-full h-full"
        />
      </div>
    </>
  );
};

export default MiddlePanel;
