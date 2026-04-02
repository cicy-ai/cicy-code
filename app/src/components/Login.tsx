import { useState } from 'react';
import { useAuth } from '../contexts/AuthContext';
import apiService from '../services/api';
import config from '../config';

export default function Login() {
  const { login } = useAuth();
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

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
        setError('令牌无效');
      }
    } catch {
      setError('连接失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="h-screen bg-[#0A0A0A] flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <div className="text-4xl mb-3">{config.isAudit ? '🔍' : '✨'}</div>
          <h1 className="text-xl font-semibold text-white">{config.isAudit ? 'CiCy Audit' : 'CiCy Code'}</h1>
          <p className="text-sm text-zinc-500 mt-1">{config.isAudit ? '使用你的 API 令牌查看 AI 使用情况面板' : '使用你的 API 令牌登录'}</p>
        </div>

        <div className="text-center mb-6">
          <p className="text-sm text-zinc-400">API Token</p>
          <p className="text-xs text-zinc-600 mt-1">粘贴有效令牌以继续</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <input
            type="password"
            value={value}
            onChange={e => setValue(e.target.value)}
            placeholder="在这里粘贴令牌..."
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
            ) : '使用令牌登录'}
          </button>
        </form>
      </div>
    </div>
  );
}
