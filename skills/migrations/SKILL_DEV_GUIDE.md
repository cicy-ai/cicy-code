# Skill Development Guide

This is the contract every cicy-skills CLI follows. Workers porting existing
Python / JS / Bash skills to Go MUST follow it. New skills MUST follow it.

The single goal: every command on a fresh CiCy machine should work the same
way — same config path, same flag conventions, same exit codes, same JSON
output shape — regardless of which person wrote it.

> **Read first:** [SKILL_MANIFEST.md](./SKILL_MANIFEST.md) and
> [SKILL_REGISTRY.md](./SKILL_REGISTRY.md) — they establish the central
> abstraction. This guide is the per-skill coding rulebook that fills in
> the details.

## 0. Quickstart: ship a new skill in 5 minutes

```go
// internal/hosttools/myskill.go
package hosttools

import (
    "fmt"
    "os"

    "github.com/cicy-ai/cicy-skills/internal/registry"
    "github.com/cicy-ai/cicy-skills/internal/skillsdb"
)

type myskillConfig struct {
    Endpoint string `json:"endpoint"`
}

func init() {
    registry.Register(&registry.Skill{
        Name:          "myskill",
        Title:         "My Skill",
        Description:   "Does the thing.",
        Version:       "1.0.0",
        Category:      "dev",
        Icon:          "wrench",
        BinaryAliases: []string{"myskill"},
        ConfigFile:    "~/cicy-ai/db/skills/myskill.yaml",
        AgentSkill:    true,
        SkillBody:     renderMyskillSkill,
        HelpBody:      renderMyskillHelp,
        UIVisible:     true,
        Run:           runMyskill,
        InstallCheck: func() registry.InstallStatus {
            return registry.InstallStatus{Installed: true, ConfigPresent: true}
        },
    })
}

func runMyskill(args []string) int {
    if len(args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: myskill <subcommand>")
        return 1
    }
    var cfg myskillConfig
    _ = skillsdb.Load("myskill", &cfg)

    switch args[1] {
    case "help":
        fmt.Println("Usage: myskill <help|do|status>")
        return 0
    case "do":
        fmt.Println("did the thing against", cfg.Endpoint)
        return 0
    default:
        fmt.Fprintln(os.Stderr, "unknown subcommand:", args[1])
        return 1
    }
}

func renderMyskillSkill() string {
    return `---
name: myskill
description: Does the thing.
---

# My Skill

Use the local ` + "`myskill`" + ` wrapper from ` + "`PATH`" + `.
`
}

func renderMyskillHelp() string {
    return `# My Skill Help

## Command
- primary command: ` + "`myskill`" + `

## Quick Start
- ` + "`myskill do`" + `
`
}
```

Build and install:

```bash
cd ~/projects/cicy-code/skills && make install-local-cli
myskill do
```

Open the cicy-code app, `/skills` → see "My Skill" card. Click [Install]
→ symlink and SKILL.md regenerated.

That's it. **No other files touched.**

---

## 1. Repo layout

cicy-skills lives at `~/projects/cicy-code/skills/` and is a Go monorepo.

```
skills/
├── cmd/
│   ├── cicy-hosttools/    ← multiplex binary, dispatches by argv[0]
│   │   └── main.go
│   ├── cicy-skills/       ← the install/admin CLI
│   ├── cicy-skillsd/      ← long-running daemon
│   ├── stt/               ← speech-to-text
│   └── tts/               ← text-to-speech
├── internal/
│   ├── agentgen/          ← SKILL.md / help.md / tools.md generation
│   ├── bundle/            ← symlink installer
│   ├── config/            ← runtime config loader (~/cicy-ai/skills/config.json)
│   ├── hosttools/         ← actual implementations of every "alias" command
│   │   ├── cf_tunnel.go
│   │   ├── cping.go
│   │   ├── frp.go
│   │   ├── ...            ← ONE FILE PER SKILL
│   │   └── hosttools.go   ← argv[0] dispatcher (already wired)
│   └── skillsdb/          ← (new) shared helpers for reading ~/cicy-ai/db/skills/<skill>.yaml
└── migrations/            ← migration specs (this file lives here)
```

### Where new code goes

| If your skill is | Put it in | Wire it via |
| --- | --- | --- |
| A simple CLI sharing the host-tools binary | `internal/hosttools/<skill>.go` | Add name to `bundle.HosttoolAliases` (one line) and add a `case "<skill>"` branch in `cmd/cicy-hosttools/main.go`'s dispatcher |
| A larger CLI deserving its own binary | `cmd/<skill>/main.go` | Add `LinkSpec{Name:"<skill>", Source:"dist/<skill>"}` to `bundle.BinaryLinks`; add `go build` line in `Makefile` |
| A long-running daemon | `cmd/<skill>d/main.go` | Same as own-binary, plus systemd-style supervision if needed |

Default to the **first** option — adding to `cicy-hosttools`. Only break out
your own binary if you have a heavy dependency (e.g. an SDK that explodes
the binary size for everyone), or you genuinely need a separate process.

---

## 2. Config files — unified under `~/cicy-ai/db/skills/`

### Rule

**Every skill stores its config in exactly one file: `~/cicy-ai/db/skills/<skill-name>.yaml`.**

- file is YAML (top-level mapping)
- file is `0600` (chmod via `os.WriteFile(path, data, 0o600)`) — credentials live here
- create the file lazily (don't error if missing; treat as empty `{}`)
- writes are atomic: write to `<file>.tmp` then `os.Rename` — never partial
- writes are flock'd while holding state for the duration of read-modify-write,
  to avoid two skill invocations racing
- `~/cicy-ai/db/skills/` is created on first install if missing

### Standard file names

| Skill | Config path |
| --- | --- |
| `cf-tunnel` | `~/cicy-ai/db/skills/cf-tunnel.yaml` |
| `cping` | `~/cicy-ai/db/skills/cping.yaml` (rarely needed) |
| `frp-server` / `frp-client` | `~/cicy-ai/db/skills/frp-server.yaml`, `~/cicy-ai/db/skills/frp-client.yaml` |
| `google` | `~/cicy-ai/db/skills/google.yaml` (OAuth client + refresh token) |
| `globalApiToken` | `~/cicy-ai/db/skills/globalApiToken.yaml` |
| `proxy_ssh` | `~/cicy-ai/db/skills/proxy_ssh.yaml` (already correct) |
| `cicy-agent` | `~/cicy-ai/db/skills/cicy-agent.yaml` (already correct) |
| `cicy-master` | `~/cicy-ai/db/skills/cicy-master.yaml` (was `~/Private/cicy-node.json`) |
| `us-spot-dev` | `~/cicy-ai/db/skills/us-spot-dev.yaml` |
| `us-spot-proxy` | `~/cicy-ai/db/skills/us-spot-proxy.yaml` |
| `tg` | `~/cicy-ai/db/skills/tg.yaml` |
| `cicy-code` (CLI client) | `~/cicy-ai/db/skills/cicy-code.yaml` (was `~/Private/cicy-node.json` registry) |

### Legacy `~/cicy-ai/global.json`

`global.json` is the legacy bag-of-everything from the pre-rewrite era. New
Go skills MUST:

1. **Read** from `~/cicy-ai/db/skills/<skill>.yaml` first.
2. **If empty / missing**, fall back to `~/cicy-ai/global.json` and **migrate**
   the relevant subtree once — copy fields into `~/cicy-ai/db/skills/<skill>.yaml` on
   first run, then write the new file.
3. **Never write** to `~/cicy-ai/global.json` from a new skill.

A shared helper in `internal/skillsdb/` MUST own the migration logic so
every skill does the same thing.

### Legacy field map (global.json → new files)

This is the authoritative migration table. The `internal/skillsdb` helper
must implement it.

| `global.json` key | New file | New key |
| --- | --- | --- |
| `GMAIL_CLIENT_ID` | `db/skills/google.yaml` | `client_id` |
| `GMAIL_CLIENT_SECRET` | `db/skills/google.yaml` | `client_secret` |
| `GMAIL_REFRESH_TOKEN` | `db/skills/google.yaml` | `refresh_token` |
| `GMAIL_WEB_CLIENT_ID` | `db/skills/google.yaml` | `web_client_id` |
| `GMAIL_WEB_CLIENT_SECRET` | `db/skills/google.yaml` | `web_client_secret` |
| `TG_BOT_TOKEN` | `db/skills/tg.yaml` | `bot_token` |
| `TG_CHAT_ID` | `db/skills/tg.yaml` | `chat_id` |
| `api_token` | `db/skills/globalApiToken.yaml` | `api_token` |
| `proxy_token` | `db/skills/globalApiToken.yaml` | `proxy_token` |
| `cf` | `db/skills/cf.yaml` | (subtree copied as-is) |
| `github_oauth` | `db/skills/github.yaml` | (subtree copied) |
| `providers` | `db/skills/providers.yaml` | (subtree) |
| `tencent` | `db/skills/tencent.yaml` | (subtree) |
| `images` | `db/skills/images.yaml` | (subtree) |
| `membership` | `db/skills/membership.yaml` | (subtree) |

`aliyun_AccessKey.csv` (the only non-JSON in `db/`) is a special case used
by `us-spot-dev` — leave it where it is; document it in that skill's spec.

---

## 3. State and logs

Persistent runtime state that is **not config** goes under
`~/.local/state/cicy-skills/<skill>/`. Examples:

- `~/.local/state/cicy-skills/frp/server/frps.pid` (already used)
- `~/.local/state/cicy-skills/cf-tunnel/last-list.json` (cache)
- `~/.local/state/cicy-skills/google/oauth.lock` (lockfile)

Logs go under `~/.local/state/cicy-skills/<skill>/log/<name>.log` and rotate
yourself if growth is unbounded.

Never write inside the repo (`<repo>/dist/*`, `<repo>/legacy/*`). The repo is
read-only for installed users — it lives inside `~/cicy-ai/skills/cicy-skills/`
which can be re-extracted at any time.

---

## 4. CLI conventions

### Naming

- Commands use `kebab-case`: `cf-tunnel`, `frp-server`. NOT `cfTunnel` or `cf_tunnel`.
- Single-word names preferred when they don't collide: `cping`, `google`.
- Subcommands are lowercase verbs: `list`, `add`, `del`, `start`, `stop`, `status`.
- Use `--json` (lowercase double-dash) to switch to machine output.
- Use `--help`/`-h` for usage; subcommands also accept `<cmd> help`.

### Exit codes

- `0` on success
- `1` on user error (bad args, missing config)
- `2` on internal error (network failed, file system error)
- `3` reserved for "feature not enabled"

### Output

**Human mode** (default): readable text, no JSON noise.

**JSON mode** (`--json`): single JSON value on stdout, no logging noise. Errors
still go to stderr; the JSON on stdout must be valid even on error (use
`{"error": "..."}`). Example:

```bash
cf-tunnel list --json
# stdout: [{"hostname":"...","port":8101,...}]

cf-tunnel add 8101 --json
# stdout: {"hostname":"g-8101.cicy-ai.com","port":8101,"id":"..."}

cf-tunnel list --json  # network failed
# stdout: {"error":"cloudflare api timeout","code":2}
# exit:   2
```

When fatal, also write a JSON error to stderr so existing callers that pipe
stderr can parse it:

```go
fmt.Fprintln(os.Stderr, `{"error":"...","code":2}`)
os.Exit(2)
```

### Logging during long ops

Long-running commands (multi-minute provisioning, etc.) should emit
human-readable progress to stderr line by line. Don't mix progress into the
stdout JSON channel.

---

## 5. Wiring a new skill: checklist

Under the registry pattern (see SKILL_REGISTRY.md), wiring is **one file**.

When you add a new skill called `myskill`:

1. **Code**: create `internal/hosttools/myskill.go` (or `cmd/myskill/main.go`
   for own-binary).
2. **Manifest**: put `registry.Register(&registry.Skill{Name: "myskill", ...})`
   in an `init()` block. The manifest carries the dispatch (`Run`),
   alias list (`BinaryAliases`), config path (`ConfigFile`), agent skill
   flag (`AgentSkill`), and UI visibility (`UIVisible`).
3. **Config helper**: call `skillsdb.Load("myskill", &cfg)` and
   `skillsdb.Save("myskill", cfg)`. Don't reinvent file IO.
4. **SKILL.md content** (if `AgentSkill: true`): implement `SkillBody`,
   `HelpBody`, optional `ToolsBody` — bodies are plain strings. No absolute
   paths; reference the command by bare name.
5. **Tests**: `internal/hosttools/myskill_test.go` with at least:
   - argument parsing roundtrip
   - one round of config load → mutate → save
   - one happy-path subcommand returning a non-trivial result (with HTTP
     mocked via `httptest.Server` if external)
6. **Help text contract**: `myskill help` must work. The output must list
   every subcommand `myskill <verb>` accepts.

That's everything. No edits to `bundle.go`, no edits to
`agentgen/generate.go`, no edits to dispatcher switches — those layers all
read the registry.

### What runs after `init()`

| Trigger | What happens |
| --- | --- |
| `make install-local-cli` | registry.List() → for each skill, symlinks for `BinaryAliases` + `OwnBinary` + SKILL.md emit |
| `cicy-skills install all` | same as above (the binary backs the make target) |
| User clicks "Install" in UI | `POST /api/skills/myskill/install` → server calls registry-driven install for just that skill |
| Argv[0] lookup at runtime | `~/.local/bin/myskill` → cicy-hosttools binary → `registry.Lookup("myskill").Run(args)` |
| Agent reads SKILL.md | `~/.claude/skills/myskill/SKILL.md` was emitted at install time from `SkillBody` |

### Deleting a skill

1. Delete the skill's `.go` file. That removes its `init()` → registry entry.
2. The next `cicy-skills install all` won't recreate its symlinks.
3. To clean **existing** symlinks, add the skill's old aliases to the
   `LegacyAliases` field of any nearby skill, OR run `cicy-skills remove`
   to wipe and reinstall everything.

No central list to prune; the file delete is the deletion.

---

## 6. Migrating an existing Python/JS/Bash skill

The standard migration recipe used by every worker:

### Step 0: Read the existing skill

- Read every line of the source script
- Make a list of every subcommand it accepts and the shape of its output
- Note every external file it reads/writes (config, state, logs)
- Note every external process it runs (`ssh`, `docker`, `curl`, etc.)
- Note every network call (`requests.get(...)`, `fetch(...)`)
- Note error behavior (silent? message to stderr? exit code?)

This list goes at the top of your migration spec.

### Step 1: Design the Go layout

- One file `internal/hosttools/<name>.go` for the public dispatcher
- Optional `internal/<name>/` package if helpers are big
- `internal/skillsdb/` helper for config (don't reinvent)

### Step 2: Write the dispatcher first

The thinnest possible top-level: parse argv[1] as subcommand, delegate.
Helps catch all subcommands before any deep work.

### Step 3: Implement one subcommand at a time

- Keep the human output identical to the Python/JS version (so users don't
  notice the swap) UNLESS the old output was bad
- Add `--json` for the same subcommand (often the old version had this)
- Write unit test before moving on

### Step 4: Re-run the old script and the new binary side by side

Pipe their outputs to `diff -u`. The diff should be empty for every
non-credential, non-timestamp field. Reconcile any differences.

### Step 5: Delete the old script

- Remove from `skills/` (Python/JS/Bash file)
- Remove `node_modules/` if it was a JS provider
- Remove `python3` runtime dependency if no Python skill remains

### Step 6: Update SKILL.md generators

- If `description`, `help`, or `commands` text referenced the old script,
  regenerate the templates
- Run `cicy-skills agent sync claude` and verify the new SKILL.md still
  uses bare command names (no `/home/.../bin` paths)

---

## 7. Testing

### What we require

| Test type | When required | Tool |
| --- | --- | --- |
| Unit | Always; one per non-trivial function | `go test ./...` |
| Subcommand-level | Always; one happy path per subcommand | table-driven `t.Run` |
| HTTP mocking | When skill calls external services | `httptest.Server` |
| Subprocess mocking | When skill exec's `ssh`/`docker` | inject a `runner func(...)` into the package and overwrite in tests |
| Full smoke | When skill is graduated to approved | manual `make smoke` target |

### What we don't require

- Mock filesystem (use `t.TempDir()` instead)
- Coverage thresholds (write the tests that catch real bugs, not coverage)
- E2E tests against real Aliyun / Google / Cloudflare (run in CI via
  smoke target only)

### Test naming

- `TestX_HappyPath`
- `TestX_RejectsInvalidArgs`
- `TestX_HandlesNetworkFailure`

---

## 8. Approval before merge (for workers)

Before a worker's PR is merged:

1. `go build ./...` clean
2. `go vet ./...` clean
3. `go test ./...` all pass
4. Diff between old script's `--help` and new binary's `--help` reviewed
5. Smoke test: one real subcommand against the dev environment with output
   captured and posted in the PR description
6. Old script file deleted from `skills/`
7. `bundle.go` and `agentgen/generate.go` updated correctly
8. SKILL.md is regenerated and reviewed (no absolute paths)
9. Confirm `~/cicy-ai/db/skills/<skill>.yaml` is the only config file used and
   it's `0600`

---

## 9. Anti-patterns

These are common mistakes. Don't do them.

- ❌ Reading `~/cicy-ai/global.json` directly. Use `skillsdb.Load` which
  handles the migration.
- ❌ Writing config in two places ("primary" + "cache"). Always one file.
- ❌ Hard-coding `/home/cicy/...` paths in templates. Use `os.UserHomeDir()`.
- ❌ Hard-coding paths in SKILL.md / help.md. Reference commands by bare
  name; PATH guarantees they resolve.
- ❌ Shelling out to Python or Node from a Go skill. The point of the
  rewrite is to have zero such interpreters.
- ❌ Skipping `--json` mode. Every machine-callable subcommand must support it.
- ❌ Logging to stdout in JSON mode. Stdout is reserved for the JSON value.
- ❌ Catching errors and silently returning success. Surface real errors.
- ❌ Adding a new flag without updating the SKILL.md help.md generator.
- ❌ Long docstrings repeating what the code does. Use the migration spec
  for context; code has function names and types.

---

## 10. Worker handoff requirements

When a worker finishes and hands back, the deliverable is:

1. New Go file(s) under `internal/hosttools/` or `cmd/<name>/`
2. Test file alongside
3. `bundle.go` update
4. `generate.go` update (if approved skill)
5. `Makefile` update (if own binary)
6. Old Python/JS/Bash file deleted
7. A short PR description listing:
   - subcommands implemented
   - flags added or changed
   - any output format change vs. the old script
   - any new config field
   - test summary (lines passed / lines added)
8. Branch name `migrate/<skill>` — one branch per skill, never combined

The reviewer (you) verifies the above with the checklist in §8 and reports
back the diff between old/new behavior. If anything is missing, the worker
re-iterates on the same branch.

---

## 11. Quick reference

### Files & directories

- Repo: `~/projects/cicy-code/skills/`
- Install: `make install-local-cli`
- Config: `~/cicy-ai/db/skills/<skill>.yaml` (NEVER touch `global.json`)
- State: `~/.local/state/cicy-skills/<skill>/`
- CLI install dir: `~/.local/bin/`
- SKILL.md outputs: `~/.codex/skills/`, `~/.claude/skills/`, `~/.opencode/skills/`

### Code

- Skill code lives in: `internal/hosttools/<skill>.go` (default) or `cmd/<skill>/main.go` (own binary)
- Manifest schema: [SKILL_MANIFEST.md](./SKILL_MANIFEST.md) / `internal/registry/skill.go`
- Registration: `registry.Register(&Skill{...})` in `init()` block of skill file
- Config helper: `internal/skillsdb/` — `skillsdb.Load(name, &cfg)` / `skillsdb.Save(name, cfg)`
- Registry-driven consumers: `internal/bundle/`, `internal/agentgen/`, `api/mgr/skills.go`

### Surfaces (all driven by registry)

- CLI install: derives symlinks from `BinaryAliases` + `OwnBinary`
- agent SKILL.md gen: derives from `AgentSkill` + `Profiles` + `SkillBody` etc.
- HTTP API (see [SKILL_API.md](./SKILL_API.md)): exposes registry list + install actions
- UI Skills page (see [SKILL_UI.md](./SKILL_UI.md)): renders registry as cards

### Doc map

- [README.md](./README.md) — index
- [SKILL_MANIFEST.md](./SKILL_MANIFEST.md) — metadata schema
- [SKILL_REGISTRY.md](./SKILL_REGISTRY.md) — Go registry pattern
- [SKILL_DEV_GUIDE.md](./SKILL_DEV_GUIDE.md) — this file (coding conventions)
- [SKILL_API.md](./SKILL_API.md) — HTTP API contract
- [SKILL_UI.md](./SKILL_UI.md) — UI design
