import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import { useDevRegister } from '../lib/devStore';
import { TokenManager } from '../services/tokenManager';
import apiService, { setBackend } from '../services/api';
import config from '../config';

interface AuthContextType {
  token: string | null;
  perms: string[];
  authType: string | null;
  plan: string | null;
  provisioning: boolean;
  isChecking: boolean;
  login: (token: string) => void;
  logout: () => void;
  hasPermission: (perm: string) => boolean;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [token, setToken] = useState<string | null>(null);
  const [perms, setPerms] = useState<string[]>([]);
  const [authType, setAuthType] = useState<string | null>(null);
  const [plan, setPlan] = useState<string | null>(null);
  const [provisioning, setProvisioning] = useState(false);
  const [isChecking, setIsChecking] = useState(true);

  const handleVerify = useCallback(async (t: string) => {
    const { data } = await apiService.verifyAuth(t);
    if (!data.valid) return false;
    setToken(t);
    setPerms(data.perms || []);
    setAuthType(data.auth_type || 'token');
    setPlan(data.plan || null);
    if (data.home) config.hostHome = data.home;
    if (data.auth_type === 'saas' && data.backend) {
      setBackend(data.backend);
    } else if (data.auth_type === 'saas' && !data.backend) {
      setProvisioning(true);
    } else {
      setBackend(null);
    }
    return true;
  }, []);

  useEffect(() => {
    const init = async () => {
      const params = new URLSearchParams(window.location.search);

      // OAuth code exchange (workspace mode)
      const code = params.get('code');
      if (code && config.isWorkspace) {
        try {
          const resp = await fetch(`${config.mgrBase}/api/auth/exchange?code=${code}`);
          const data = await resp.json();
          if (data.status === 'ok' && data.token) {
            TokenManager.saveToken(data.token);
            setToken(data.token);
            setPerms(['api_full']);
            setAuthType('saas');
            setPlan('free');
            // Clean URL
            const url = new URL(window.location.href);
            url.searchParams.delete('code');
            window.history.replaceState({}, '', url.toString());
            setIsChecking(false);
            return;
          } else if (data.status === 'provisioning') {
            setProvisioning(true);
            setIsChecking(false);
            return;
          }
        } catch {}
      }

      // Normal token flow (dev mode or saved token)
      const urlToken = params.get('token');
      const t = urlToken || TokenManager.getToken();
      if (t) {
        try {
          const ok = await handleVerify(t);
          if (ok) {
            TokenManager.saveToken(t);
            if (urlToken) {
              const url = new URL(window.location.href);
              url.searchParams.delete('token');
              window.history.replaceState({}, '', url.toString());
            }
          } else {
            TokenManager.clearToken();
          }
        } catch {
          TokenManager.clearToken();
        }
      }
      setIsChecking(false);
    };
    init();
  }, [handleVerify]);

  const login = (t: string) => {
    TokenManager.saveToken(t);
    handleVerify(t).catch(() => {});
  };

  const logout = () => {
    TokenManager.clearToken();
    setBackend(null);
    setToken(null);
    setPerms([]);
    setAuthType(null);
    setPlan(null);
    setProvisioning(false);
  };

  const hasPermission = useCallback(
    (perm: string) => perms.includes('api_full') || perms.includes(perm),
    [perms]
  );

  useDevRegister('Auth', { hasToken: !!token, authType, plan, provisioning, isChecking, perms });

  return (
    <AuthContext.Provider value={{ token, perms, authType, plan, provisioning, isChecking, login, logout, hasPermission }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
};
