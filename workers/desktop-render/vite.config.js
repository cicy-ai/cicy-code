import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { execSync } from "node:child_process";

// Build-time stamps so the running SPA can prove which deploy it is.
// __WORKER_BUILD_TIME__ — ISO date of the build (UTC, second precision).
// __WORKER_GIT_SHA__    — short git rev of the current HEAD, "local" if not in a repo.
const buildTime = new Date().toISOString().replace(/\..*/, "Z");
let gitSha = "local";
try {
  gitSha = execSync("git rev-parse --short HEAD", { stdio: ["ignore", "pipe", "ignore"] })
    .toString().trim() || "local";
} catch {}

// Dev server intentionally binds 0.0.0.0 so Electron (or any other host
// on the LAN / via cf-tunnel) can load the page.
export default defineConfig({
  plugins: [react()],
  define: {
    __WORKER_BUILD_TIME__: JSON.stringify(buildTime),
    __WORKER_GIT_SHA__: JSON.stringify(gitSha),
  },
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
