import { useState, useEffect, useCallback } from 'react';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import { DialogProvider } from './contexts/DialogContext';
import Workspace from './components/Workspace';
import Login from './components/Login';
import ProvisionScreen from './components/ProvisionScreen';
import { TokenManager } from './services/tokenManager';

function parseHash(): string {
  const m = window.location.hash.match(/\/agent\/([^/]+)/);
  return m ? decodeURIComponent(m[1]).replace(/:.*$/, '') : 'w-10001';
}

function Main() {
  const { token, isChecking, provisioning } = useAuth();
  const [activeAgent, setActiveAgent] = useState(parseHash);

  useEffect(() => {
    const onChange = () => setActiveAgent(parseHash());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  const selectAgent = (id: string) => {
    const clean = id.replace(/:.*$/, '');
    setActiveAgent(clean);
    window.location.hash = `#/agent/${encodeURIComponent(clean)}`;
  };

  const handleProvisionReady = useCallback((_backend: string) => {
    // Provision done — redirect to workspace subdomain
    // Extract user slug from token payload
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

  return <Workspace agentId={activeAgent} onSelectAgent={selectAgent} />;
}

export default function App() {
  return (
    <AuthProvider>
      <DialogProvider>
        <Main />
      </DialogProvider>
    </AuthProvider>
  );
}
