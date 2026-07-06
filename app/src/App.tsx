// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState, useEffect, useCallback } from 'react';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import { AppProvider, useApp } from './contexts/AppContext';
import Workspace from './components/Workspace';
import Login from './components/Login';
import ProvisionScreen from './components/ProvisionScreen';
import AuditDashboard from './components/audit/AuditDashboard';
import { TokenManager } from './services/tokenManager';
import DevPanel from './components/dev/DevPanel';
import apiService from './services/api';
import config from './config';
import { useTranslation } from 'react-i18next';
import { chatWs } from './services/chatWs';
import { Spinner } from './components/ui/Spinner';
import SpeedUp from './components/SpeedUp';
import WSLInstall from './components/WSLInstall';

type ViewType = 'desktop' | 'workspace' | 'audit' | 'speedup' | 'wsl-install';

function parseHash(): { view: ViewType; agentId: string } {
  const hash = window.location.hash;
  if (hash.startsWith('#/audit')) {
    return { view: 'audit', agentId: '' };
  }
  if (hash.startsWith('#/speedup')) {
    return { view: 'speedup', agentId: '' };
  }
  if (hash.startsWith('#/wsl-install')) {
    return { view: 'wsl-install', agentId: '' };
  }
  if (hash.startsWith('#/agent/')) {
    const m = hash.match(/\/agent\/([^/]+)/);
    return { view: 'workspace', agentId: m ? decodeURIComponent(m[1]).replace(/:.*$/, '') : 'w-1001' };
  }
  return { view: 'desktop', agentId: 'w-1001' };
}

// After login + token verify, the workspace is only usable once the realtime
// chat-ws is up (agent status, system stats, live AI all flow over it). Mount
// the workspace (so it configures/connects the ws) but cover it with a full
// screen overlay until connected; after a few failed attempts show an error
// with a "reconnect" button. Once connected once, never block again on a later
// transient drop (the in-app network indicator shows that, and it auto-retries).
function WsGate({ children }: { children: any }) {
  const { t } = useTranslation('common');
  const { globalLoaded, loadGlobalVar } = useApp();
  const [connected, setConnected] = useState<boolean>(() => chatWs.isConnected());
  const [attempts, setAttempts] = useState<number>(() => chatWs.currentAttempts());
  const [everReady, setEverReady] = useState<boolean>(false);

  useEffect(() => chatWs.onConnectedChange(setConnected), []);
  useEffect(() => chatWs.onAttemptsChange(setAttempts), []);

  const ready = connected && globalLoaded;
  useEffect(() => { if (ready) setEverReady(true); }, [ready]);

  const blocking = !ready && !everReady;
  // attempts is incremented BEFORE each connect, so the very first connection
  // is attempts === 1 — that's the initial connect, NOT a retry. Only show the
  // "retry n/3" text once we're actually retrying (attempts > 1); the n shown
  // is the retry count (attempts - 1), so the 2nd attempt reads "retry 1/3".
  const retrying = !connected && attempts > 1;
  const shownRetry = Math.min(attempts - 1, 3);
  const failed = blocking && attempts >= 4 && !connected;
  return (
    <>
      {children}
      {blocking ? (
        <div data-id="ws-connect-gate" className="fixed inset-0 z-[9999] bg-[#0A0A0A] flex flex-col items-center justify-center gap-4">
          {failed ? (
            <>
              <div data-id="ws-connect-error" className="text-sm text-zinc-300">{t('wsConnectError')}</div>
              <button
                type="button"
                data-id="ws-connect-retry"
                className="rounded-md border border-white/15 bg-white/[0.06] px-4 py-1.5 text-sm text-zinc-200 transition-colors hover:bg-white/[0.10]"
                onClick={() => { chatWs.forceReconnect(); loadGlobalVar(); }}
              >
                {t('wsReconnect')}
              </button>
            </>
          ) : (
            <>
              <Spinner size="md" />
              <div data-id="ws-connecting-label" className="text-xs text-zinc-500">{retrying ? t('wsConnectingRetry', { n: shownRetry }) : t('wsConnecting')}</div>
            </>
          )}
        </div>
      ) : null}
    </>
  );
}

function Main() {
  const { token, isChecking, provisioning } = useAuth();
  const [route, setRoute] = useState(parseHash);

  useEffect(() => {
    const onChange = () => setRoute(parseHash());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  // Ensure w-1001 exists on login
  useEffect(() => {
    if (!token || config.isAudit) return;
    apiService.getPane('w-1001:main.0').catch(() => {
      apiService.createPane({ win_name: 'w-1001', title: '项目经理', agent_type: 'cicy' }).catch(() => {});
    });
  }, [token]);

  const selectAgent = useCallback((id: string) => {
    const clean = id.replace(/:.*$/, '');
    setRoute({ view: 'workspace', agentId: clean });
    window.location.hash = `#/agent/${encodeURIComponent(clean)}`;
  }, []);

  const handleProvisionReady = useCallback((_backend: string) => {
    const t = TokenManager.getToken();
    if (t) {
      try {
        const payload = JSON.parse(atob(t.split('.')[1]));
        const slug = 'u-' + payload.sub.slice(0, 8);
        window.location.href = `https://${slug}.cicy-ai.com?token=${t}`;
        return;
      } catch {}
    }
    window.location.reload();
  }, []);

  if (isChecking) return (
    <div data-id="loading-spinner" className="h-screen bg-[#0A0A0A] flex items-center justify-center">
      <Spinner size="md" />
    </div>
  );

  // Audit mode
  if (config.isAudit) {
    document.title = 'CiCy Audit';
    if (!token) return <Login />;
    return <AuditDashboard />;
  }

  if (provisioning) return <ProvisionScreen onReady={handleProvisionReady} />;

  // #/speedup and #/wsl-install render before auth — they only call the local
  // electronRPC bridge and the user may need them before they can even log in
  // (e.g. CN Windows user who can't reach the cicy-code release first).
  if (route.view === 'speedup') return <SpeedUp />;
  if (route.view === 'wsl-install') return <WSLInstall />;

  // Not authenticated
  if (!token) return <Login />;

  // #/audit → Audit Dashboard
  if (route.view === 'audit') {
    return <AuditDashboard onBack={() => { window.location.hash = '#/agent/w-1001'; }} />;
  }

  // #/agent/xxx or default → Workspace
  const agentId = route.view === 'workspace' ? route.agentId : 'w-1001';
  return <WsGate><Workspace agentId={agentId} onSelectAgent={selectAgent} /></WsGate>;
}

export default function App() {
  return (
    <AuthProvider>
      <AppProvider>
        <Main />
        <DevPanel />
      </AppProvider>
    </AuthProvider>
  );
}
