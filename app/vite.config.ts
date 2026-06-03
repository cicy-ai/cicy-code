/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig, loadEnv} from 'vite';

export default defineConfig(({mode}) => {
  const env = loadEnv(mode, '.', '');
  const isDev = mode === 'development';
  // Both the HTML base AND the runtime asset prefix must come ONLY from
  // CICY_APP_VITE_BASE — never from CICY_APP_CDN_PREFIX. build.sh exports
  // CICY_APP_CDN_PREFIX (the R2 origin) to bake into the binary via ldflags for
  // the runtime `--cdn` index rewrite, NOT to change how the SPA emits URLs. If
  // either fell back to it, the self-contained (no --cdn) binary would point at
  // R2: the embedded index.html would hard-code R2 bundle URLs (404 on hash
  // mismatch), and assetUrl() would fetch logos/icons from R2 (ORB-blocked when
  // R2 serves them as application/xml). Keeping both local means the binary
  // serves everything itself; --cdn only rewrites the index (api/mgr/ui.go).
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
      'import.meta.env.VITE_PUBLIC_ASSET_PREFIX': JSON.stringify(viteBase),
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
