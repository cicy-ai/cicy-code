import React, {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  ReactNode,
} from "react";
import { useDevRegister } from "../lib/devStore";
import { TokenManager } from "../services/tokenManager";
import apiService, { setBackend } from "../services/api";
import config, { setHostHome } from "../config";

interface AuthContextType {
  token: string | null;
  perms: string[];
  authType: string | null;
  plan: string | null;
  provisioning: boolean;
  isChecking: boolean;
  login: (token: string) => Promise<boolean>;
  logout: () => void;
  hasPermission: (perm: string) => boolean;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

const pendingVerifyRequests = new Map<string, Promise<any>>();

function fetchVerifyData(token: string) {
  const cached = pendingVerifyRequests.get(token);
  if (cached) return cached;
  const request = apiService
    .verifyAuth(token)
    .then(({ data }) => data)
    .finally(() => {
      pendingVerifyRequests.delete(token);
    });
  pendingVerifyRequests.set(token, request);
  return request;
}

export const AuthProvider: React.FC<{ children: ReactNode }> = ({
  children,
}) => {
  const [token, setToken] = useState<string | null>(null);
  const [perms, setPerms] = useState<string[]>([]);
  const [authType, setAuthType] = useState<string | null>(null);
  const [plan, setPlan] = useState<string | null>(null);
  const [globalHome, setGlobalHome] = useState<string | null>(null);
  const [provisioning, setProvisioning] = useState(false);
  const [isChecking, setIsChecking] = useState(true);

  const handleVerify = useCallback(async (t: string) => {
    const data = await fetchVerifyData(t);
    if (!data.valid) return false;
    try {
      const { data: settings } = await apiService.getGlobalSettings(t);
      const home = typeof settings?.home === "string" ? settings.home.trim() : "";
      if (home) {
        setGlobalHome(home);
        setHostHome(home);
      }
    } catch {}
    setToken(t);
    setPerms(data.perms || []);
    setAuthType(data.auth_type || "token");
    setPlan(data.plan || null);
    if (data.home) setHostHome(data.home);
    if (data.auth_type === "saas" && data.backend) {
      setBackend(data.backend);
    } else if (data.auth_type === "saas" && !data.backend) {
      setProvisioning(true);
    } else {
      setBackend(null);
    }
    return true;
  }, []);

  useEffect(() => {
    const init = async () => {
      const params = new URLSearchParams(window.location.search);
      const next = params.get("next");

      // OAuth code exchange (workspace mode)
      const code = params.get("code");
      if (code && config.isWorkspace) {
        try {
          const { data } = await apiService.exchangeOAuthCode(code);
          if (data.status === "ok" && data.token) {
            TokenManager.saveToken(data.token);
            setToken(data.token);
            setPerms(["api_full"]);
            setAuthType("saas");
            setPlan("free");
            if (next) {
              window.location.href = next;
              return;
            }
            // Clean URL
            const url = new URL(window.location.href);
            url.searchParams.delete("code");
            url.searchParams.delete("next");
            window.history.replaceState({}, "", url.toString());
            setIsChecking(false);
            return;
          } else if (data.status === "provisioning") {
            setProvisioning(true);
            setIsChecking(false);
            return;
          }
        } catch {}
      }

      // Normal token flow (dev mode or saved token).
      // URL token preempts everything: if it's there, we commit it to
      // localStorage BEFORE the verify round-trip so any axios call that
      // races init() (api.ts reads TokenManager.getToken() per request)
      // sees the URL token, not a stale cached one. Verify failure
      // still clears localStorage at the bottom.
      const urlToken = params.get("token");
      if (urlToken) {
        TokenManager.saveToken(urlToken);
        const url = new URL(window.location.href);
        url.searchParams.delete("token");
        window.history.replaceState({}, "", url.toString());
      }
      const t = urlToken || TokenManager.getToken();
      if (t) {
        try {
          const ok = await handleVerify(t);
          if (!ok) TokenManager.clearToken();
        } catch {
          TokenManager.clearToken();
        }
      }
      setIsChecking(false);
    };
    init();
  }, [handleVerify]);

  const login = useCallback(async (t: string) => {
    const next = t.trim();
    if (!next) return false;
    try {
      const ok = await handleVerify(next);
      if (!ok) {
        TokenManager.clearToken();
        return false;
      }
      TokenManager.saveToken(next);
      return true;
    } catch {
      TokenManager.clearToken();
      throw new Error("verify failed");
    }
  }, [handleVerify]);

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
    (perm: string) => perms.includes("api_full") || perms.includes(perm),
    [perms],
  );

  useDevRegister("Auth", {
    hasToken: !!token,
    authType,
    plan,
    provisioning,
    isChecking,
  });

  return (
    <AuthContext.Provider
      value={{
        token,
        perms,
        authType,
        plan,
        provisioning,
        isChecking,
        login,
        logout,
        hasPermission,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
};
