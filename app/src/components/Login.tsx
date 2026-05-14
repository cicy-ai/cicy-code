import {useState} from 'react';
import {useTranslation} from 'react-i18next';
import {useAuth} from '../contexts/AuthContext';
import config from '../config';
import { Spinner } from './ui/Spinner';

export default function Login() {
  const {login} = useAuth();
  const {t} = useTranslation('login');
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const token = value.trim();
    if (!token) return;
    setLoading(true);
    setError('');
    try {
      const ok = await login(token);
      if (!ok) {
        setError(t('errorInvalid'));
      }
    } catch {
      setError(t('errorConnection'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div data-id="login-root" className="h-screen bg-[#0A0A0A] flex items-center justify-center px-4">
      <div data-id="login-container" className="w-full max-w-sm">
        <div data-id="login-header" className="text-center mb-8">
          <div data-id="login-emoji" className="text-4xl mb-3">{config.isAudit ? '🔍' : '✨'}</div>
          <h1 data-id="login-title" className="text-xl font-semibold text-white">{config.isAudit ? t('titleAudit') : t('titleCode')}</h1>
          <p data-id="login-subtitle" className="text-sm text-zinc-500 mt-1">{config.isAudit ? t('subtitleAudit') : t('subtitleCode')}</p>
        </div>

        <div data-id="login-api-token-info" className="text-center mb-6">
          <p data-id="login-api-token-label" className="text-sm text-zinc-400">{t('apiToken')}</p>
          <p data-id="login-api-token-hint" className="text-xs text-zinc-600 mt-1">{t('apiTokenHint')}</p>
        </div>

        <form data-id="login-form" onSubmit={handleSubmit} className="space-y-4">
          <input
            data-id="login-token-input"
            type="password"
            value={value}
            onChange={e => setValue(e.target.value)}
            placeholder={t('placeholder')}
            autoFocus
            className="w-full bg-[#141414] border border-white/10 rounded-xl px-4 py-3 text-sm text-white placeholder:text-zinc-600 focus:outline-none focus:border-blue-500/50 focus:ring-1 focus:ring-blue-500/20 transition-all font-mono"
          />

          {error && <p data-id="login-error" className="text-red-400 text-xs text-center">{error}</p>}

          <button
            data-id="login-submit"
            type="submit"
            disabled={loading || !value.trim()}
            className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:hover:bg-blue-600 text-white text-sm font-medium py-3 rounded-xl transition-all"
          >
            {loading ? (
              <span data-id="login-submit-loading" className="flex items-center justify-center gap-2">
                <Spinner size="sm" />
                {t('submitting')}
              </span>
            ) : t('submit')}
          </button>
        </form>
      </div>
    </div>
  );
}
