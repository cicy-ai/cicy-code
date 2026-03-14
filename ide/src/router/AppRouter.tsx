import React, { useState, useEffect } from 'react';
import { useAuth } from '../contexts/AuthContext';
import Spinner from '../components/shared/Spinner';
import LoginPage from '../pages/LoginPage';
import AgentListPage from '../pages/AgentListPage';
import AgentPage from '../pages/AgentPage';

function parseHash(): { page: string; paneId?: string } {
  const hash = window.location.hash.replace('#', '') || '/';
  const m = hash.match(/^\/agent\/(.+)$/);
  if (m) return { page: 'agent', paneId: decodeURIComponent(m[1]) };
  return { page: 'list' };
}

const AppRouter: React.FC = () => {
  const { token, isChecking } = useAuth();
  const [route, setRoute] = useState(parseHash);

  useEffect(() => {
    const onChange = () => setRoute(parseHash());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  if (isChecking) return <Spinner />;
  if (!token) return <LoginPage />;

  if (route.page === 'agent' && route.paneId) {
    return <AgentPage paneId={route.paneId} />;
  }
  return <AgentListPage />;
};

export default AppRouter;
