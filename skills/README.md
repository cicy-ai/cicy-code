# cicy-skills

`cicy-skills` manages the local CiCy command set and approved agent skill generation.

## Layout

- config only: `~/cicy-ai/skills/config.json`
- repo-owned commands: `~/projects/cicy-skills/bin`
- global entrypoints: `~/.local/bin`
- agent skill targets:
  - Codex: `~/.codex/skills`
  - Claude: `~/.claude/skills`
  - OpenCode: `~/.opencode/skills`

`~/cicy-ai/skills` stores the `cicy-skills` runtime config and installed repo copy. It does not own the global command entrypoints in `~/.local/bin`.

## Current Rule

Agent skill generation is allowlist-based.

- currently approved: `agent-code-server`, `agent-summary`, `agent-webpage`, `cf-tunnel`, `cping`, `docker-build-github-action`, `cicy-agent`, `cicy-ssh`, `frp-client`, `frp-server`, `globalApiToken`, `google`
- current supported target agents: `codex`, `claude`, `opencode`

Everything else is archived in-repo under [`legacy/skills`](./legacy/skills) but is not part of the approved generation list until you explicitly confirm it.

## Install

```bash
make install-local-cli
```

This builds the binaries into `dist/`, materializes the command entrypoints in `~/projects/cicy-skills/bin`, and links them into `~/.local/bin`.

## GitHub Release

GitHub Actions can publish release bundles for the Go binaries in this repo.

- tag-driven release: push a tag like `v0.1.0`
- manual release: run the `release-go-binaries` workflow and pass a tag like `v0.1.0`

Each release uploads bundled archives for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each archive includes:

- `cicy-skills`
- `cicy-skillsd`
- `cicy-hosttools`
- `stt`
- `tts`

## Local Command Management

```bash
cicy-skills install google-node
cicy-skills update google-node
cicy-skills remove google-node

cicy-skills install all
cicy-skills update all
cicy-skills remove all
```

`install` and `update` refresh the current repo state. If you changed the Go binaries, use `make install-local-cli` so `dist/` is rebuilt before relinking.

## Commands

```bash
cicy-skills help
cicy-skills config-path
cicy-skills init-config
cicy-skills list
cicy-skills install all
cicy-skills install google-node
cicy-skills remove google-node
cicy-skills update google-node
cicy-skills agent list codex
cicy-skills agent list claude
cicy-skills agent list opencode
cicy-skills agent tools codex agent-webpage
cicy-skills agent help codex cf-tunnel
cicy-skills agent help codex frp-client
cicy-skills agent help codex frp-server
cicy-skills agent help codex cicy-agent
cicy-skills agent help claude globalApiToken
cicy-skills agent help claude frp-client
cicy-skills agent help claude cicy-agent
cicy-skills agent help opencode google
cicy-skills agent help opencode frp-server
cicy-skills agent help opencode cicy-agent
cicy-skills agent install codex agent-webpage
cicy-skills agent install codex cf-tunnel
cicy-skills agent install codex frp-client
cicy-skills agent install codex frp-server
cicy-skills agent install codex cicy-agent
cicy-skills agent install claude globalApiToken
cicy-skills agent install claude frp-client
cicy-skills agent install claude cicy-agent
cicy-skills agent install opencode google
cicy-skills agent install opencode frp-server
cicy-skills agent install opencode cicy-agent
cicy-skills agent update codex agent-webpage
cicy-skills agent update codex cf-tunnel
cicy-skills agent update claude globalApiToken
cicy-skills agent update opencode google
cicy-skills agent remove codex agent-webpage
cicy-skills agent remove codex cf-tunnel
cicy-skills agent remove claude globalApiToken
cicy-skills agent remove opencode google
cicy-skills agent sync codex
cicy-skills agent sync claude
cicy-skills agent sync opencode
cicy-skills agent generate codex
cicy-skills agent generate claude
cicy-skills agent generate opencode
```

## Agent Skill Generation

```bash
cicy-skills agent list codex
cicy-skills agent list claude
cicy-skills agent list opencode
cicy-skills agent tools codex agent-webpage
cicy-skills agent help codex cf-tunnel
cicy-skills agent help codex frp-client
cicy-skills agent help codex frp-server
cicy-skills agent help codex cicy-agent
cicy-skills agent help claude globalApiToken
cicy-skills agent help claude frp-client
cicy-skills agent help claude cicy-agent
cicy-skills agent help opencode google
cicy-skills agent help opencode frp-server
cicy-skills agent help opencode cicy-agent
cicy-skills agent install codex agent-webpage
cicy-skills agent install codex cf-tunnel
cicy-skills agent install codex frp-client
cicy-skills agent install codex frp-server
cicy-skills agent install codex cicy-agent
cicy-skills agent install claude globalApiToken
cicy-skills agent install claude frp-client
cicy-skills agent install claude cicy-agent
cicy-skills agent install opencode google
cicy-skills agent install opencode frp-server
cicy-skills agent install opencode cicy-agent
cicy-skills agent update codex agent-webpage
cicy-skills agent update codex cf-tunnel
cicy-skills agent update claude globalApiToken
cicy-skills agent update opencode google
cicy-skills agent remove codex agent-webpage
cicy-skills agent remove codex cf-tunnel
cicy-skills agent remove claude globalApiToken
cicy-skills agent remove opencode google
cicy-skills agent sync codex
cicy-skills agent sync claude
cicy-skills agent sync opencode
```

That currently generates the same approved skill set into each profile's default target:

- [agent-webpage/SKILL.md](/home/w3c_offical/.codex/skills/agent-webpage/SKILL.md)
- [help.md](/home/w3c_offical/.codex/skills/agent-webpage/references/help.md)
- [tools.md](/home/w3c_offical/.codex/skills/agent-webpage/references/tools.md)
- [cf-tunnel/SKILL.md](/home/w3c_offical/.codex/skills/cf-tunnel/SKILL.md)
- [help.md](/home/w3c_offical/.codex/skills/cf-tunnel/references/help.md)
- [commands.md](/home/w3c_offical/.codex/skills/cf-tunnel/references/commands.md)
- [globalApiToken/SKILL.md](/home/w3c_offical/.codex/skills/globalApiToken/SKILL.md)
- [help.md](/home/w3c_offical/.codex/skills/globalApiToken/references/help.md)
- [commands.md](/home/w3c_offical/.codex/skills/globalApiToken/references/commands.md)
- [google/SKILL.md](/home/w3c_offical/.codex/skills/google/SKILL.md)
- [help.md](/home/w3c_offical/.codex/skills/google/references/help.md)
- [commands.md](/home/w3c_offical/.codex/skills/google/references/commands.md)
- [cicy-agent/SKILL.md](/home/w3c_offical/.codex/skills/cicy-agent/SKILL.md)
- [help.md](/home/w3c_offical/.codex/skills/cicy-agent/references/help.md)
- [commands.md](/home/w3c_offical/.codex/skills/cicy-agent/references/commands.md)

And by default target:

- Codex: `~/.codex/skills`
- Claude: `~/.claude/skills`
- OpenCode: `~/.opencode/skills`

## Google

The embedded Google provider lives in [`providers/google-node`](./providers/google-node) and exposes:

- `google help`
- `google gmail`
- `google sheets`
- `google drive`
- `google calendar`

These wrappers read credentials from `~/cicy-ai/global.json`.

## Cf Tunnel

The local `cf-tunnel` wrapper is exposed through the migrated hosttools bundle and supports:

- `cf-tunnel list`
- `cf-tunnel add <port> [port2 ...]`
- `cf-tunnel del <port> [port2 ...]`
- `CF_ENV=dev cf-tunnel ...`

This wrapper reads real Cloudflare config from `~/cicy-ai/global.json`.

## Global API Token

The local `globalApiToken` wrapper is exposed through the migrated hosttools bundle and supports:

- `globalApiToken show`
- `globalApiToken refresh`

This wrapper reads and updates `~/cicy-ai/global.json` field `api_token`.

## Test Gateway Provider

The local `test-gateway-provider` script verifies the real cicy-code `8008` AI gateway against providers defined in `~/cicy-ai/global.json`.

Examples:

- `test-gateway-provider --provider wucur --model gpt-5.5`
- `test-gateway-provider --provider sub2api --model claude-opus-4-7`
- `test-gateway-provider --all --model gpt-5.5`
- `test-gateway-provider --all --model claude-opus-4-7`

Behavior:

- temporarily switches `ai.currentProvider` to the target provider for the test
- sends a real request through `http://127.0.0.1:8008/api/ai-gateway/...`
- defaults to message `hi`
- automatically restores the original `currentProvider` afterward

## Archived Skills

The historical skill docs and scripts are kept under [`legacy/skills`](./legacy/skills) for reference and future approval, but they are not automatically generated into agent skill directories.

The agent install/remove/update interface already supports multiple names, but the allowlist is still explicit. Only approved and implemented skills will install.
