import React, { createContext, useContext, useState, useEffect, useRef, useCallback, ReactNode } from 'react';
import apiService from '../services/api';
import { usePane } from './PaneContext';

interface VoiceContextType {
  isListening: boolean;
  voiceMode: 'append' | 'direct';
  startRecording: (mode: 'append' | 'direct') => Promise<void>;
  stopRecording: (shouldSend: boolean) => void;
}

const VoiceContext = createContext<VoiceContextType | undefined>(undefined);

export const VoiceProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const { displayPaneId, settings, setReadOnly } = usePane();
  const [isListening, setIsListening] = useState(false);
  const voiceModeRef = useRef<'append' | 'direct'>('append');
  const voiceShouldSendRef = useRef(false);
  const voiceTranscriptRef = useRef('');
  const mediaStreamRef = useRef<MediaStream | null>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);

  // Pre-acquire mic permission
  useEffect(() => {
    if (settings.showVoiceControl) {
      navigator.mediaDevices?.getUserMedia({ audio: true }).then(stream => {
        mediaStreamRef.current = stream;
        stream.getTracks().forEach(t => t.enabled = false);
      }).catch(() => {});
    }
  }, [settings.showVoiceControl]);

  const sendTranscript = useCallback(async () => {
    const text = voiceTranscriptRef.current.trim();
    voiceTranscriptRef.current = '';
    if (!text) return;
    try { await apiService.sendCommand(displayPaneId, text); } catch (e) { console.error('Failed to send voice command:', e); }
  }, [displayPaneId]);

  const startRecording = useCallback(async (mode: 'append' | 'direct') => {
    voiceModeRef.current = mode;
    voiceTranscriptRef.current = '';
    voiceShouldSendRef.current = false;
    try {
      let stream = mediaStreamRef.current;
      if (!stream || stream.getTracks().every(t => t.readyState === 'ended')) {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        mediaStreamRef.current = stream;
      }
      stream.getTracks().forEach(t => t.enabled = true);
      audioChunksRef.current = [];
      const recorder = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' });
      recorder.ondataavailable = e => { if (e.data.size > 0) audioChunksRef.current.push(e.data); };
      recorder.start();
      mediaRecorderRef.current = recorder;
      setIsListening(true);
      setReadOnly(true);
    } catch (e) {
      console.error('Mic error:', e);
      setIsListening(false);
      setReadOnly(false);
    }
  }, [setReadOnly]);

  const stopRecording = useCallback((shouldSend: boolean) => {
    voiceShouldSendRef.current = shouldSend;
    const recorder = mediaRecorderRef.current;
    if (recorder && recorder.state !== 'inactive') {
      recorder.onstop = async () => {
        setIsListening(false);
        mediaStreamRef.current?.getTracks().forEach(t => t.enabled = false);
        try {
          if (!voiceShouldSendRef.current) return;
          const blob = new Blob(audioChunksRef.current, { type: 'audio/webm' });
          if (blob.size < 100) return;
          const fd = new FormData();
          fd.append('file', blob, 'voice.webm');
          fd.append('engine', 'google');
          const { data: d } = await apiService.stt(fd);
          if (d.text) { voiceTranscriptRef.current = d.text; sendTranscript(); }
        } catch (e) { console.error('STT error:', e); }
        finally { setReadOnly(false); }
      };
      recorder.stop();
    } else {
      setIsListening(false);
      setReadOnly(false);
    }
  }, [sendTranscript, setReadOnly]);

  return (
    <VoiceContext.Provider value={{ isListening, voiceMode: voiceModeRef.current, startRecording, stopRecording }}>
      {children}
    </VoiceContext.Provider>
  );
};

export const useVoice = () => {
  const ctx = useContext(VoiceContext);
  if (!ctx) throw new Error('useVoice must be used within VoiceProvider');
  return ctx;
};
