import { createContext, useContext, useState, useCallback, useRef, ReactNode } from 'react';
import { useDevRegister } from '../lib/devStore';

interface SendingContextType {
  sending: boolean;
  setSending: (v: boolean) => void;
  checkIdle: (status: string) => void;
}

const SendingContext = createContext<SendingContextType>({ sending: false, setSending: () => {}, checkIdle: () => {} });

export function SendingProvider({ children }: { children: ReactNode }) {
  const [sending, setSendingRaw] = useState(false);
  const sendingRef = useRef(false);
  const sentAt = useRef(0);
  
  const setSending = useCallback((v: boolean) => {
    sendingRef.current = v;
    setSendingRaw(v);
    if (v) sentAt.current = Date.now();
  }, []);
  
  const checkIdle = useCallback((status: string) => {
    if (sendingRef.current && status === 'idle' && Date.now() - sentAt.current > 1000) {
      sendingRef.current = false;
      setSendingRaw(false);
    }
  }, []);

  useDevRegister('Sending', { sending });

  return <SendingContext.Provider value={{ sending, setSending, checkIdle }}>{children}</SendingContext.Provider>;
}

export const useSending = () => useContext(SendingContext);
