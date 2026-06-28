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

**MANDATORY after any code change**: agents MUST run `python3 dev.py` to rebuild the binary and restart the server before reporting work as done. The user runs the local dev server and expects changes to be live without manually restarting. Never tell the user "you need to restart" — restart it yourself. Do NOT use `--hot` (the user does not want Vite HMR).

For frontend changes: `dev.py` defaults to `SKIP_NPM=1` (uses cached `app/dist`). To pick up frontend source changes, first run `cd app && npm run build`, then `python3 dev.py`.

- Frontend HMR: `cd app && npm ci && npm run dev`
- Backend manual dev with Vite proxy: `cd api && go run ./mgr/ --dev --public`
- Go tests: `./build.sh test-go`
- Go tests for a specific package: `./build.sh test-go ./mgr/...`
- Frontend type check: `cd app && npm run lint`
- Frontend production build: `cd app && npm run build`
- Current-platform build: `./build.sh build`
- Cross build: `./build.sh all`
- Runtime image build: `./build.sh docker <tag>`
- Base image build: `./build.sh docker-base <tag>`

`make dev-api` currently runs plain `go run ./mgr/`; it does not pass `--dev --public`.

## Local Mac desktop update (faster than an npm release)

The Mac desktop runs `~/.local/bin/cicy-code --public`, where `~/.local/bin/cicy-code`
is a **symlink** → `cicy-code-<ver>-darwin-<arch>` (a versioned binary). cicy-desktop's
`src/sidecar/localbin.js` owns this layout; it also keeps a manifest at
`~/.local/bin/.cicy-localbin.json` and reads the version from the symlink target.

To ship a local build to the desktop WITHOUT going through commit→tag→push→CI→npm:

**ALWAYS bump the version on every local iteration.** If you rebuild at the same
version and overwrite the binary, the desktop shows the same number and the user
cannot tell an update happened (and "检查更新" can't distinguish builds). Bump first:

```bash
# 0) Bump the version (pick the next patch) BEFORE building
python3 scripts/sync-version.py --set 2.3.NN

# 1) Build the native binary at the synced version (arm64 on Apple Silicon)
./build.sh build darwin amd64                 # → api/cicy-code

# 2) Drop it in ~/.local/bin under a versioned name
VER=$(node -p "require('./npm/package.json').version")
cp api/cicy-code ~/.local/bin/cicy-code-$VER-darwin-x64
chmod +x ~/.local/bin/cicy-code-$VER-darwin-x64

# 3) Atomically repoint the symlink (tmp + rename, like localbin.js does)
ln -sf cicy-code-$VER-darwin-x64 ~/.local/bin/.cicy-code.tmp
mv -f ~/.local/bin/.cicy-code.tmp ~/.local/bin/cicy-code

# 4) Keep the manifest in sync so the desktop reports the right version
node -e "const f=require('os').homedir()+'/.local/bin/.cicy-localbin.json';const j=require(f);j['cicy-code']=process.env.VER||require('./npm/package.json').version;require('fs').writeFileSync(f,JSON.stringify(j,null,2)+'\n')"
```

Then restart cicy-code from cicy-desktop. The running process keeps the old binary
until restart, so steps 1-4 are non-disruptive. Because `localbin.update()` skips the
download when the installed version ≥ npm-latest and `sidecar.update()` restarts through
the symlink regardless, clicking **更新** in the desktop also works: it finds the local
build "已是最新", skips npm, and restarts into it. Old versioned binaries stay in
`~/.local/bin` — roll back by repointing the symlink.

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
- Primary builtin session is still `w-1001`
- Frontend still contains an audit view and audit API client calls, but `api/mgr/main.go` does not currently register `/api/audit/*`

## Build caveats

- `go build ./mgr/` is not equivalent to the repo build pipeline
- `./build.sh` prepares embedded resources before compiling
- `./build.sh test-go` is the stable Go test entrypoint for agents
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

## Agent memory / guidance files

Each agent owns a self-contained native guidance file (`CLAUDE.md` / `AGENTS.md` / `.kiro/steering/memory.md`) written into its workspace at creation time. Content is COMPOSED from layered, user-editable templates under `~/cicy-ai/memory/` (global + optional project/role) and copied in verbatim — there is no inheritance and no gateway-side injection. The CLI reads the file natively. Implementation: `api/mgr/agent_memory_template.go` (compose) and `api/mgr/tmux.go` (`writeAgentGuidanceFile`).

The gateway no longer rewrites outgoing request system prompts to inject memory/rules — agents read their own guidance files natively, keeping gateway and non-gateway paths consistent (`agentInspectorRewriteRequestBody` in `api/mgr/agent_inspector.go` only does model override + Anthropic system normalization).

## Extension note

There are two extension folders in the repo:

- `api/code-server-extension/`

They currently exist as duplicated copies and should stay aligned when edited.
