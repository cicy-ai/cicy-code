import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig, loadEnv} from 'vite';

export default defineConfig(({mode}) => {
  const env = loadEnv(mode, '.', '');
  const isDev = mode === 'development';
  const appCdnPrefix = (process.env.CICY_APP_CDN_PREFIX || '').replace(/\/+$/, '');
  return {
    base: isDev ? '/' : (appCdnPrefix ? `${appCdnPrefix}/` : '/'),
    plugins: [react(), tailwindcss()],
    define: {
      'process.env.GEMINI_API_KEY': JSON.stringify(env.GEMINI_API_KEY),
      'import.meta.env.VITE_API_BASE': JSON.stringify(env.VITE_API_BASE || ''),
      'import.meta.env.VITE_HOST_HOME': JSON.stringify('/home/w3c_offical'),
      'import.meta.env.VITE_APP_VERSION': JSON.stringify(process.env.npm_package_version || '1.0.0'),
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
  };
});
