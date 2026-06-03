/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig, loadEnv} from 'vite';

export default defineConfig(({mode}) => {
  const env = loadEnv(mode, '.', '');
  const isDev = mode === 'development';
  const appCdnPrefix = (process.env.CICY_APP_CDN_PREFIX || '').replace(/\/+$/, '');
  // base MUST come only from CICY_APP_VITE_BASE — NOT from appCdnPrefix. build.sh
  // exports CICY_APP_CDN_PREFIX (the R2 origin) to bake into the binary via
  // ldflags for the runtime `--cdn` rewrite, NOT to change the HTML base. If we
  // fell back to it here, the embedded index.html would hard-code R2 asset URLs,
  // so the default (no --cdn) self-contained binary would 404 whenever R2 lacks
  // that exact build's hashes. Keep base '/' → embedded HTML stays local; --cdn
  // rewrites /assets/ → R2 at serve time (api/mgr/ui.go cdnRewriteIndex).
  const viteBase = (process.env.CICY_APP_VITE_BASE || '').replace(/\/+$/, '');
  const defaultCicyRoot = '~/cicy-ai';
  return {
    base: isDev ? '/' : (viteBase ? `${viteBase}/` : '/'),
    plugins: [react(), tailwindcss()],
    define: {
      'process.env.GEMINI_API_KEY': JSON.stringify(env.GEMINI_API_KEY),
      'import.meta.env.VITE_API_BASE': JSON.stringify(env.VITE_API_BASE || ''),
      'import.meta.env.VITE_CICY_ROOT': JSON.stringify(defaultCicyRoot),
      'import.meta.env.VITE_HOST_HOME': JSON.stringify(env.VITE_HOST_HOME || defaultCicyRoot),
      'import.meta.env.VITE_PUBLIC_ASSET_PREFIX': JSON.stringify(appCdnPrefix),
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, 'src'),
      },
    },
    server: {
      hmr: process.env.DISABLE_HMR !== 'true',
      allowedHosts: true,
    },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
    },
  };
});
