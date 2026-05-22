import { defineConfig } from "vite";

// Dev server intentionally binds 0.0.0.0 so Electron (or any other host
// on the LAN / via cf-tunnel) can load the page. The Electron app's
// homepage window points at this URL during UI iteration; rebuild
// the .app only after the layout stabilises.
export default defineConfig({
  server: {
    host: "0.0.0.0",
    port: 8173,
    strictPort: true,
    // Allow any Host header so we can hit this dev server through a
    // public IP / tunnel without Vite rejecting the request.
    allowedHosts: true,
  },
});
