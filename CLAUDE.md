# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

### Main build flow

- Recommended build path (prepares `go:embed` assets before compiling):
  ```bash
  cd /home/w3c_offical/projects/cicy-code
  bash ./build.sh build
  ```

- Faster Go-only rebuild when frontend assets can be reused:
  ```bash
  cd /home/w3c_offical/projects/cicy-code
  SKIP_NPM=1 bash ./build.sh build
  ```

- Cross-compile release binaries:
  ```bash
  cd /home/w3c_offical/projects/cicy-code
  bash ./build.sh all
  ```

- Make targets:
  ```bash
  cd /home/w3c_offical/projects/cicy-code
  make build
  make build-all
  make clean
  ```

### Frontend development

- Start Vite dev server:
  ```bash
  cd /home/w3c_offical/projects/cicy-code/app
  npm install
  npm run dev
  ```

- Typecheck/lint frontend (the repo uses TypeScript typecheck as lint):
  ```bash
  cd /home/w3c_offical/projects/cicy-code/app
  npm run lint
  ```

- Production frontend build:
  ```bash
  cd /home/w3c_offical/projects/cicy-code/app
  npm run build
  ```

### Backend development

- Run backend directly in dev mode:
  ```bash
  cd /home/w3c_offical/projects/cicy-code/api
  go run ./mgr/ --dev
  ```

- Run backend tests:
  ```bash
  cd /home/w3c_offical/projects/cicy-code/api
  go test ./...
  ```

- Run a single Go test:
  ```bash
  cd /home/w3c_offical/projects/cicy-code/api
  go test ./webtty -run TestWriteFromPTY
  ```

### Local/prod restart

- Supervisor-managed API restart:
  ```bash
  sudo supervisorctl restart cicy-api
  ```

- View API logs:
  ```bash
  tail -50 /var/log/cicy-api.log
  ```

### Release / npm publish

- The npm package version in `npm/package.json` is the source of truth for release versioning. `build.sh` syncs it into `api/mgr/main.go`.
- Tag-based release workflow publishes GitHub release binaries and then publishes npm:
  - `.github/workflows/release.yml`
- Manual npm publish helper:
  ```bash
  cd /home/w3c_offical/projects/cicy-code
  make npm-publish
  ```

## Architecture overview

### Big picture

This repository is a single product with three tightly coupled parts:

1. **Go backend (`api/mgr`)**
   - The main runtime and HTTP API.
   - Owns tmux worker lifecycle, ttyd/gotty terminal serving, code-server proxying, token auth, runtime/session APIs, and most product behavior.
   - Ships as a single binary and embeds frontend/static resources with `//go:embed`.

2. **React frontend (`app`)**
   - The management UI and end-user workspace UI.
   - Talks to the Go backend over HTTP/WebSocket/SSE.
   - In production it is not deployed separately; its built `dist/` is copied into `api/mgr/ui` and embedded into the backend binary.

3. **npm launcher (`npm`)**
   - The `npx cicy-code` entrypoint.
   - It is a thin installer/launcher package that downloads prebuilt binaries from GitHub Releases matching `npm/package.json`.
   - This means npm publishing is coupled to release asset availability for the same version.

### Why `build.sh` matters

Do not assume `go build ./mgr/` from `api/` is enough for normal builds.

`build.sh` does important preparation before compilation:
- copies `api/resources` into `api/mgr/resources`
- copies root `.tmux.conf` and `.cicy_tmux.conf` into `api/mgr/`
- builds `app/dist` and copies it into `api/mgr/ui`
- optionally copies `mitmproxy/` into embedded monitor resources

That is why the recommended build path is always the repo-root `build.sh`.

### Backend structure

The backend is centered in `api/mgr/` and combines several concerns that are easy to miss if you only read one file:

- `main.go`
  - process startup
  - mode flags (`--dev`, `--public`, desktop/audit/cloudrun-related behavior)
  - route registration
  - startup orchestration (`checkEnv`, watchers, sync loops)

- `setup.go`
  - local environment bootstrap
  - base tool checks and install commands
  - builtin worker creation and restoration
  - code-server startup
  - tmux config installation into the runtime home directory

- `tmux.go`, `instance.go`, `ws.go`
  - tmux pane/session management
  - ttyd instance startup per pane
  - terminal websocket and `/ttyd/...` proxying

- `proxy.go`
  - reverse proxies for code-server, mitmproxy, phpMyAdmin-style paths, and related auth wrappers

- `runtime.go`
  - runtime/session/task APIs
  - remote instance registration and runtime metadata

- `watcher.go`
  - background monitoring of worker state and pane/agent activity

- `auth.go` + `middleware.go`
  - bearer token auth
  - token verification and permission derivation
  - auth wrappers reused across most routes

The product is designed around **tmux-backed workers**. Each worker has a pane ID, workspace directory, ttyd port, and persisted config in the database. The API/UI are effectively a control plane over those tmux workers.

### Frontend structure

The frontend is not a generic SPA; it is tightly coupled to the backend runtime model.

Key layers:
- `app/src/config.ts`
  - computes API/code/ttyd base URLs from hostnames and environment
  - production defaults should fall back to same-origin unless explicitly overridden
- `app/src/services/api.ts`
  - central axios wrapper for all backend APIs
- `app/src/contexts/*`
  - app-wide state for panes, auth, dialogs, sending state, voice, etc.
- `app/src/components/Workspace.tsx` and `Desktop.tsx`
  - two top-level product modes
- `app/src/components/terminal/*`
  - terminal frame, command panel, tmux window management
- `app/src/components/chat/*`
  - chat/task interaction around workers

The frontend assumes the backend exposes both management APIs and terminal/editor proxies. Changes to `/api/tmux/*`, `/ttyd/*`, `/code/*`, or token handling usually require frontend awareness.

### Deployment model

There are three deployment ideas reflected across the repo and docs:

- **Local/dev machine** via `npx cicy-code`
- **Trial/demo Cloud Run path**
- **Stable paid VM path**

The product expectation in this repo is:
- Cloud Run is acceptable for trial/demo use
- stable/paid users should be routed to the VM offering

### Release model

Release coordination spans multiple directories:
- `npm/package.json` version is the public package version
- `build.sh` syncs that version into `api/mgr/main.go`
- `.github/workflows/release.yml` builds binaries, creates a GitHub Release, then publishes npm
- `npm/scripts/install.js` expects release binaries to exist at the matching tag/version

If npm/package.json and release assets diverge, `npx cicy-code` installs will break.

## Repository-specific notes

- Root `README.md` is the canonical product/deployment overview for this repo.
- There is a separate `desktop/CLAUDE.md` for the Electron desktop subproject; use it when working under `desktop/`.
- There are no root Cursor rules or Copilot instruction files in this repository at the time of writing.
- The frontend dev server uses port `8022` from `app/package.json`, while the product docs may mention older ports in some places. Prefer actual package/build scripts over stale prose when they conflict.
