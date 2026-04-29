# CLAUDE.md

This file documents the current repo reality for code agents working inside `cicy-code`.

## Entry points

- Main local-dev entrypoint: `python3 dev.py`
- Standard build entrypoint: `./build.sh`
- Backend entrypoint: `api/mgr/main.go`
- Frontend entrypoint: `app/src/App.tsx`
- Main workspace UI: `app/src/components/Workspace.tsx`

## Commands

- Local dev: `python3 dev.py`
- Refresh ttyd assets only: `python3 dev.py --ttydAssets`
- Frontend HMR: `cd app && npm ci && npm run dev`
- Backend manual dev with Vite proxy: `cd api && go run ./mgr/ --dev --public`
- Go tests: `cd api && go test ./...`
- Frontend type check: `cd app && npm run lint`
- Frontend production build: `cd app && npm run build`
- Current-platform build: `./build.sh build`
- Cross build: `./build.sh all`
- Runtime image build: `./build.sh docker <tag>`
- Base image build: `./build.sh docker-base <tag>`

`make dev-api` currently runs plain `go run ./mgr/`; it does not pass `--dev --public`.

## Current repo truths

- State root is `~/cicy-ai`
- Global config/token file is `~/cicy-ai/global.json`
- Backend machine file is `~/cicy-ai/cicy-node.json`
- Skills CLI registry file defaults to `~/Private/cicy-node.json` unless `CICY_NODES_FILE` is set
- Version source is `npm/package.json`
- Version sync target files are:
  - `npm/package.json`
  - `app/package.json`
  - `app/package-lock.json`
  - `app/src/config.ts`
  - `api/mgr/main.go`
  - `.cicy_tmux.conf`
- Default builtin dev agent from `api/mgr/setup.go` is `claude`
- Primary builtin session is still `w-10001`
- Frontend still contains an audit view and audit API client calls, but `api/mgr/main.go` does not currently register `/api/audit/*`

## Build caveats

- `go build ./mgr/` is not equivalent to the repo build pipeline
- `./build.sh` prepares embedded resources before compiling
- `app/src/*` changes use Vite HMR and do not require a backend rebuild
- `api/js/src/*` changes do require `cd api && make asset`

## File map

- `api/mgr/main.go`: route registration and startup
- `api/mgr/setup.go`: env checks, dependency install, builtin workers, code-server bootstrap
- `api/mgr/tmux.go`: pane lifecycle, tmux send path, agent boot scripts
- `api/mgr/chatbus.go`: chat WebSocket, poll data, client broadcast
- `api/mgr/runtime.go`: runtime instances/tasks/artifacts and managed-runtime registration
- `api/mgr/machines.go`: machine config load/save/sync
- `api/mgr/ui.go`: SPA serve path or Vite reverse proxy
- `app/src/App.tsx`: route switcher
- `app/src/components/Workspace.tsx`: main operator UI
- `app/src/services/api.ts`: frontend API client
- `scripts/sync-version.py`: version sync implementation

## Runtime notes

- Pane IDs are normalized to `w-xxxxx:main.0`
- `handleChatWS` normalizes `agent_id` to the short pane ID (`w-xxxxx`)
- `poll_request` and `ping` are request/response events handled directly by `chatbus.go`
- Most other WS events are broadcast to other clients of the same agent and are not echoed back to the sender
- `CICY_RUNTIME_MODE=api-only` or `CICY_RUNTIME_API_ONLY=1` disables tmux/desktop-only interfaces through middleware

## Extension note

There are two extension folders in the repo:

- `code-server-extension/`
- `api/code-server-extension/`

They currently exist as duplicated copies and should stay aligned when edited.
