import { useState, useEffect } from 'react';
import { useAuth } from '../contexts/AuthContext';
import apiService from '../services/api';
import config from '../config';

export default function Login() {
  const { login } = useAuth();
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [isLocalMode, setIsLocalMode] = useState(config.isAudit);

  useEffect(() => {
    if (config.isAudit) return;
    fetch(`${config.mgrBase}/api/mode`).then(r => r.json())
      .then(d => setIsLocalMode(d.mode !== 'saas'))
      .catch(() => setIsLocalMode(true));
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const t = value.trim();
    if (!t) return;
    setLoading(true);
    setError('');
    try {
      const { data } = await apiService.verifyAuth(t);
      if (data.valid) {
        login(t);
      } else {
        setError('Invalid token');
      }
    } catch {
      setError('Connection failed');
    } finally {
      setLoading(false);
    }
  };

  const handleGithubLogin = () => {
    window.location.href = `${config.mgrBase}/api/auth/github`;
  };

  const handleGoogleLogin = () => {
    window.location.href = `${config.mgrBase}/api/auth/google`;
  };

  return (
    <div className="h-screen bg-[#0A0A0A] flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <div className="text-4xl mb-3">{config.isAudit ? '🔍' : '✨'}</div>
          <h1 className="text-xl font-semibold text-white">{config.isAudit ? 'CiCy Audit' : 'CiCy Code'}</h1>
          <p className="text-sm text-zinc-500 mt-1">{config.isAudit ? 'Sign in to view your AI usage dashboard' : 'Sign in to get started'}</p>
        </div>

        {/* OAuth 按钮 - 仅在 SaaS 模式显示 */}
        {!isLocalMode && (
          <>
            {/* Google OAuth */}
            <button
              onClick={handleGoogleLogin}
              className="w-full flex items-center justify-center gap-2 bg-[#141414] border border-white/10 hover:border-white/20 text-white text-sm font-medium py-3 rounded-xl transition-all mb-3"
            >
              <svg className="w-5 h-5" viewBox="0 0 24 24"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/></svg>
              Sign in with Google
            </button>

            {/* GitHub OAuth */}
            <button
              onClick={handleGithubLogin}
              className="w-full flex items-center justify-center gap-2 bg-[#141414] border border-white/10 hover:border-white/20 text-white text-sm font-medium py-3 rounded-xl transition-all mb-4"
            >
              <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
              Sign in with GitHub
            </button>

            <div className="flex items-center gap-3 mb-4">
              <div className="flex-1 h-px bg-white/10" />
              <span className="text-xs text-zinc-600">or</span>
              <div className="flex-1 h-px bg-white/10" />
            </div>
          </>
        )}

        {/* 本地模式提示 */}
        {isLocalMode && (
          <div className="text-center mb-6">
            <p className="text-sm text-zinc-400">本地部署模式</p>
            <p className="text-xs text-zinc-600 mt-1">请使用 API Token 登录</p>
          </div>
        )}

        {/* Token login */}
        <form onSubmit={handleSubmit} className="space-y-4">
          <input
            type="password"
            value={value}
            onChange={e => setValue(e.target.value)}
            placeholder="Paste token here..."
            autoFocus
            className="w-full bg-[#141414] border border-white/10 rounded-xl px-4 py-3 text-sm text-white placeholder:text-zinc-600 focus:outline-none focus:border-blue-500/50 focus:ring-1 focus:ring-blue-500/20 transition-all font-mono"
          />

          {error && <p className="text-red-400 text-xs text-center">{error}</p>}

          <button
            type="submit"
            disabled={loading || !value.trim()}
            className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:hover:bg-blue-600 text-white text-sm font-medium py-3 rounded-xl transition-all"
          >
            {loading ? (
              <span className="flex items-center justify-center gap-2">
                <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                Verifying...
              </span>
            ) : 'Login with Token'}
          </button>
        </form>
      </div>
    </div>
  );
}
