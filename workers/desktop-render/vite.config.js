import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server intentionally binds 0.0.0.0 so Electron (or any other host
// on the LAN / via cf-tunnel) can load the page.
export default defineConfig({
  plugins: [react()],
  // Relative asset paths so the production build works under file://
  // when copied into cicy-desktop/src/backends/homepage-react/.
  base: "./",
  server: {
    host: "0.0.0.0",
    port: 8173,
    strictPort: true,
    allowedHosts: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
