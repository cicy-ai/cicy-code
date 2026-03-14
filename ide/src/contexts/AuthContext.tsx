import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import { TokenManager } from '../services/tokenManager';
import apiService from '../services/api';

interface AuthContextType {
  token: string | null;
  perms: string[];
  isChecking: boolean;
  login: (token: string) => void;
  logout: () => void;
  hasPermission: (perm: string) => boolean;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [token, setToken] = useState<string | null>(null);
  const [perms, setPerms] = useState<string[]>([]);
  const [isChecking, setIsChecking] = useState(true);

  useEffect(() => {
    const init = async () => {
      const urlToken = new URLSearchParams(window.location.search).get('token');
      const t = urlToken || TokenManager.getToken();
      if (t) {
        try {
          const { data } = await apiService.verifyAuth(t);
          if (data.valid) {
            TokenManager.saveToken(t);
            setToken(t);
            if (data.perms) setPerms(data.perms);
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
  }, []);

  const login = (t: string) => {
    TokenManager.saveToken(t);
    setToken(t);
    apiService.verifyAuth(t).then(({ data }) => {
      if (data.perms) setPerms(data.perms);
    }).catch(() => {});
  };

  const logout = () => {
    TokenManager.clearToken();
    setToken(null);
    setPerms([]);
  };

  const hasPermission = useCallback(
    (perm: string) => perms.includes('api_full') || perms.includes(perm),
    [perms]
  );

  return (
    <AuthContext.Provider value={{ token, perms, isChecking, login, logout, hasPermission }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
};
