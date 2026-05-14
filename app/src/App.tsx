import { useState, useEffect, useCallback } from 'react';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import { DialogProvider } from './contexts/DialogContext';
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

type ViewType = 'desktop' | 'workspace' | 'audit';

function parseHash(): { view: ViewType; agentId: string } {
  const hash = window.location.hash;
  if (hash.startsWith('#/audit')) {
    return { view: 'audit', agentId: '' };
  }
  if (hash.startsWith('#/agent/')) {
    const m = hash.match(/\/agent\/([^/]+)/);
    return { view: 'workspace', agentId: m ? decodeURIComponent(m[1]).replace(/:.*$/, '') : 'w-10001' };
  }
  return { view: 'desktop', agentId: 'w-10001' };
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
  const shownAttempts = Math.min(attempts, 3);
  const failed = blocking && attempts >= 3 && !connected;
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
              <div data-id="ws-connecting-label" className="text-xs text-zinc-500">{shownAttempts > 0 ? t('wsConnectingRetry', { n: shownAttempts }) : t('wsConnecting')}</div>
            </>
          )}
        </div>
      ) : null}
    </>
  );
}

function Main() {
  const { token, authType, isChecking, provisioning } = useAuth();
  const [route, setRoute] = useState(parseHash);

  useEffect(() => {
    const onChange = () => setRoute(parseHash());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  // Ensure w-10001 exists on login
  useEffect(() => {
    if (!token || config.isAudit) return;
    apiService.getPane('w-10001:main.0').catch(() => {
      apiService.createPane({ win_name: 'w-10001', title: 'Master', agent_type: 'hermes' }).catch(() => {});
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

  // Not authenticated
  if (!token) return <Login />;

  // #/audit → Audit Dashboard
  if (route.view === 'audit') {
    return <AuditDashboard onBack={() => { window.location.hash = '#/agent/w-10001'; }} />;
  }

  // #/agent/xxx or default → Workspace
  const agentId = route.view === 'workspace' ? route.agentId : 'w-10001';
  return <WsGate><Workspace agentId={agentId} onSelectAgent={selectAgent} /></WsGate>;
}

export default function App() {
  return (
    <AuthProvider>
      <AppProvider>
        <DialogProvider>
          <Main />
          <DevPanel />
        </DialogProvider>
      </AppProvider>
    </AuthProvider>
  );
}
