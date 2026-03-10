import React from 'react';
import MainApp from './MainApp';
import { AppProvider } from './contexts/AppContext';
import { DialogProvider } from './contexts/DialogContext';
import { PaneProvider } from './contexts/PaneContext';
import { VoiceProvider } from './contexts/VoiceContext';

export const Router: React.FC = () => {
  return (
    <AppProvider>
      <PaneProvider>
        <VoiceProvider>
          <DialogProvider>
            <MainApp />
          </DialogProvider>
        </VoiceProvider>
      </PaneProvider>
    </AppProvider>
  );
};
