import { useState, useEffect, useCallback } from 'react';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import { DialogProvider } from './contexts/DialogContext';
import Workspace from './components/Workspace';
import Desktop from './components/Desktop';
import Login from './components/Login';
import ProvisionScreen from './components/ProvisionScreen';
import AuditDashboard from './components/audit/AuditDashboard';
import { TokenManager } from './services/tokenManager';
import DevPanel from './components/dev/DevPanel';
import apiService from './services/api';
import config from './config';

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

function Main() {
  const { token, authType, isChecking, provisioning } = useAuth();
  const [route, setRoute] = useState(parseHash);

  useEffect(() => {
    const onChange = () => setRoute(parseHash());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  // On audit.cicy-ai.com, show audit dashboard (but require login first)
  if (config.isAudit) {
    document.title = 'CiCy Audit';
    if (isChecking) return (
      <div className="h-screen bg-[#0A0A0A] flex items-center justify-center">
        <div className="w-5 h-5 border-2 border-white/20 border-t-white/80 rounded-full animate-spin" />
      </div>
    );
    if (!token) return <Login />;
    return <AuditDashboard />;
  }

  // Ensure w-10001 exists on login
  useEffect(() => {
    if (!token) return;
    apiService.getPane('w-10001:main.0').catch(() => {
      apiService.createPane({ win_name: 'w-10001', title: 'Master' }).catch(() => {});
    });
  }, [token]);

  const selectAgent = (id: string) => {
    const clean = id.replace(/:.*$/, '');
    setRoute({ view: 'workspace', agentId: clean });
    window.location.hash = `#/agent/${encodeURIComponent(clean)}`;
  };

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
    <div className="h-screen bg-[#0A0A0A] flex items-center justify-center">
      <div className="w-5 h-5 border-2 border-white/20 border-t-white/80 rounded-full animate-spin" />
    </div>
  );

  if (!token) return <Login />;

  if (provisioning) return <ProvisionScreen onReady={handleProvisionReady} />;

  // #/audit → Audit Dashboard
  if (route.view === 'audit') {
    return <AuditDashboard onBack={() => { window.location.hash = '#/agent/w-10001'; }} />;
  }

  // No hash → redirect to default agent
  if (route.view !== 'workspace') {
    window.location.hash = '#/agent/w-10001';
    return null;
  }

  // #/agent/xxx → Workspace, default → Desktop
  if (route.view === 'workspace') {
    return <Workspace agentId={route.agentId} onSelectAgent={selectAgent} />;
  }
  return <Desktop />;
}

export default function App() {
  return (
    <AuthProvider>
      <DialogProvider>
        <Main />
        <DevPanel />
      </DialogProvider>
    </AuthProvider>
  );
}
