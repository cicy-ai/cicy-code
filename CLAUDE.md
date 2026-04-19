# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

- Recommended local entrypoint: `python3 dev.py`
  - This is the main local-dev workflow. It stops any existing `cicy-code` process on port `8008`, refreshes embedded goTTY assets, syncs versions, builds `api/cicy-code`, then execs `api/cicy-code --public --dev`.
  - Default local ports from the current code/docs: API `8008`, Vite `8022`, code-server `8002`.
- Restart the running `cicy-code` API during dev: `cicy-dev-restart`
- Frontend hot reload: `cd app && npm ci && npm run dev`
  - Vite HMR is enabled; editing `app/src/*` refreshes automatically.
- Backend-only dev while using the Vite dev server: `cd api && go run ./mgr/ --dev --public`
- Frontend checks:
  - `cd app && npm run lint`
  - `cd app && npm run build`
  - There is no frontend test script in `app/package.json`.
- Standard build for the current platform: `./build.sh build`
- Explicit platform build: `./build.sh build linux amd64`
- Cross-compile all release binaries: `./build.sh all`
- Build app dist + embedded ttyd assets only: `./build.sh assets`
- Refresh embedded goTTY/WebTTY assets only:
  - `python3 dev.py --ttydAssets`
  - or `cd api && make asset`
- Rebuild the raw goTTY frontend bundle while iterating on `api/js/src`: `cd api/js && ./node_modules/.bin/webpack`
  - `api/js/package.json` has no `build` script; webpack is invoked directly or through `cd api && make asset`.
- Build runtime images:
  - `./build.sh docker <tag>`
  - `./build.sh docker-base <tag>`
- Go tests: `cd api && go test ./...`
- Run a single Go test: `cd api && go test ./webtty -run TestWriteFromPTY`
- Legacy note: `cd api && make test` only checks `go fmt`; it is not the main test suite.
- Convenience stop command: `make stop`

## Architecture overview

### 1. Build and launch pipeline

- `dev.py` is the authoritative local development entrypoint.
- `build.sh` is the authoritative packaging/embed pipeline. It:
  1. syncs versions,
  2. copies `api/resources` and tmux config files into `api/mgr/`,
  3. builds `app/dist` unless `SKIP_NPM=1`,
  4. refreshes embedded goTTY assets unless `SKIP_TTYD_ASSET=1`,
  5. copies UI into `api/mgr/ui`,
  6. then builds the Go binary from `api/mgr/`.
- `go build ./mgr/` by itself skips this embed pipeline, so it is not equivalent to a normal repo build.

### 2. Backend runtime (`api/mgr`)

- `api/mgr/main.go` is the monolithic runtime/API entrypoint. It registers auth, panes/tmux, chat, stats, queue, agents, groups, machines, runtime, shared-workspace, skills, settings, code-server proxy, ttyd proxy, and SPA UI routes.
- Major backend slices are organized by responsibility:
  - `setup.go`: environment checks, tool installation, builtin-agent setup
  - `tmux.go`: worker lifecycle, tmux commands, startup scripts
  - `db.go`: SQLite schema and migrations
  - `machines.go`: node registry and machine sync
  - `runtime.go`: runtime instance/session/task/artifact APIs
  - `shared_workspace.go`: filesystem-backed collaboration bridge
  - `proxy.go`: code-server / AI gateway / other proxy logic
  - `ui.go`: embedded UI serving or Vite reverse proxy in `--dev`

### 3. Terminal stack

- `api/server` + `api/webtty` implement the Go-side terminal transport.
- `api/js` is the browser-side goTTY/WebTTY frontend source.
- `app/src/*` changes use Vite HMR and do not require rebuilding assets or restarting the API.
- goTTY/WebTTY asset flow is:
  - edit `api/js/src/*`
  - rebuild `api/js/dist/gotty-bundle.js`
  - run `cd api && make asset`
  - `make asset` regenerates `api/server/asset.go`
  - reload the API with `cicy-dev-restart`
- If terminal UI changes appear to do nothing, the embedded asset refresh and API reload are usually what was missed.

### 4. Frontend app (`app`)

- `app/src/App.tsx` is the React entrypoint and route switcher. It chooses between `desktop`, `workspace`, and `audit` views using hash routes.
- `app/src/components/Workspace.tsx` is the main operator UI. Most visible workspace behavior flows through it: terminal area, agent stack, code-server pane, team panel, skills panel, and inspector/settings.
- `app/src/services/api.ts` is the single frontend API client. The frontend still calls many legacy `/api/tmux/*` aliases, so backend route changes need to preserve compatibility, not just the newer route shapes.

### 5. Runtime and agent model

- A pane/worker is the core runtime unit. The UI, tmux management, chat history, and inspector flows all key off pane IDs.
- The project is more than a tmux wrapper: queue, runtime tasks/artifacts, machines, skills, and shared-workspace APIs are first-class backend features.
- README still describes several agent types, but current code defaults `--dev` to `hermes` when `--agents` is omitted. Treat `api/mgr/main.go` and `api/mgr/setup.go` as the source of truth if docs and runtime behavior diverge.

### 6. External configuration and environment

- Local/dev behavior is driven mostly by environment variables plus `~/global.json`.
- `dev.py` reads AI provider selection and credentials from `~/global.json`.
- The node registry lives in `~/Private/cicy-node.json` and is shared by the backend plus the `skills/cicy-code` / `skills/cicy-master` CLIs.
- `shared_workspace.go` currently points the shared-workspace bridge at a hardcoded filesystem root. Verify that path before debugging shared-workspace behavior on a new machine.

### 7. Generated outputs worth recognizing

- `api/mgr/ui` and `api/mgr/resources` are build-prepared embed inputs, not primary source directories.
- `api/server/asset.go` is generated by `cd api && make asset`.
- `api/js/dist/gotty-bundle.js` is generated from `api/js/src`.
