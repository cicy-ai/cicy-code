import { useState, useEffect } from 'react';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import Workspace from './components/Workspace';
import Login from './components/Login';

function parseHash(): string {
  const m = window.location.hash.match(/\/agent\/([^/]+)/);
  return m ? decodeURIComponent(m[1]).replace(/:.*$/, '') : 'w-10001';
}

function Main() {
  const { token, isChecking } = useAuth();
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

  if (isChecking) return (
    <div className="h-screen bg-[#0A0A0A] flex items-center justify-center">
      <div className="w-5 h-5 border-2 border-white/20 border-t-white/80 rounded-full animate-spin" />
    </div>
  );

  if (!token) return <Login />;

  return <Workspace agentId={activeAgent} onSelectAgent={selectAgent} />;
}

export default function App() {
  return (
    <AuthProvider>
      <Main />
    </AuthProvider>
  );
}
