import React from 'react';
import { Lock } from 'lucide-react';
import apiService from '../services/api';
import { TokenManager } from '../services/tokenManager';

interface LoginFormProps {
  onLogin: (token: string) => void;
}

export const LoginForm: React.FC<LoginFormProps> = ({ onLogin }) => {
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const input = document.querySelector('#login-token-input') as HTMLInputElement;
    const tokenValue = input?.value?.trim() || '';
    
    if (!tokenValue) return;

    try {
      TokenManager.saveToken(tokenValue);
      await apiService.verifyAuth(tokenValue);
      onLogin(tokenValue);
    } catch (err) {
      TokenManager.clearToken();
      alert('Invalid token or connection failed');
    }
  };

  return (
    <div className="fixed inset-0 bg-vsc-bg flex items-center justify-center p-4">
      <div className="bg-vsc-bg border border-vsc-border rounded-lg shadow-2xl w-full max-w-md p-8">
        <div className="flex flex-col items-center mb-8">
          <div className="w-16 h-16 bg-vsc-button rounded-full flex items-center justify-center mb-4">
            <Lock size={32} className="text-white" />
          </div>
          <h1 className="text-2xl font-bold text-white">CiCy IDE</h1>
          <p className="text-vsc-text-secondary text-sm mt-2">Enter your access token</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-vsc-text-secondary mb-2">Access Token</label>
            <input
              id="login-token-input"
              type="password"
              placeholder="Enter your token..."
              className="w-full bg-vsc-bg-secondary border border-vsc-border rounded-lg px-4 py-3 text-white focus:ring-2 focus:ring-vsc-accent focus:border-transparent outline-none"
              autoFocus
            />
          </div>

          <button
            type="submit"
            className="w-full bg-vsc-button hover:bg-vsc-button-hover text-white font-medium py-3 rounded-lg transition-colors"
          >
            Login
          </button>
        </form>

        <div className="mt-6 text-center text-xs text-vsc-text-muted">
          Contact your administrator for access
        </div>
      </div>
    </div>
  );
};
