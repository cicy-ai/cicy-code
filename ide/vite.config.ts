import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: './',
  server: {
    host: '0.0.0.0',
    port: 6902,
    strictPort: true,
    allowedHosts: true,
    cors: true,
  },
});
