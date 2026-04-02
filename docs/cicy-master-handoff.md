# cicy-master handoff for implementers

This document is for engineers who need to implement or iterate on `skills/cicy-master` without repeated verbal clarification.

## What problem this solves

`cicy-code` should not manage the node registry.

We split responsibilities as follows:

- `cicy-code` = **node API client**
  - selects a node from local registry
  - calls that node's `/api/*`, `/ttyd/*`, `/code/*`

- `cicy-master` = **node registry manager**
  - creates/updates/normalizes the local registry file
  - probes node health/ping
  - manages the canonical node inventory used by `cicy-code`

This split exists to stop mixing control-plane concerns with per-node API usage.

---

## Hard product rules

### 1. Local file is the source of truth

Canonical file:
- `~/Private/cicy-node.json`

Override:
- `CICY_NODES_FILE`

Legacy import only:
- `~/Private/cicy-nodes.json`

Do not make master DB/API the primary source for v1.

### 2. Nodes do not call master

Do not require these on nodes:
- `CICY_MASTER_URL`
- `CICY_MASTER_TOKEN`

The node is passive.
Master/tooling probes it using:
- `/api/ping`
- `/api/health`

### 3. `CICY_API_TOKEN` is a long-lived per-node token

`CICY_API_TOKEN` is not a per-session token and not something the node should generate for itself.

It is:
- created by master/tooling
- injected into the node at deploy time
- stored in the registry as `machines[].token`

### 4. Cloud Run is not automatically API-only

Do not hardcode `runtime_kind=cloudrun` as “no tmux/ttyd/code-server”.
Whether a node supports those capabilities should be determined by explicit capability flags.

---

## Canonical registry schema

This must align with `api/mgr/machines.go`.

```json
{
  "default": "node-a",
  "machines": [
    {
      "id": "node-a",
      "machine_key": "node-a",
      "label": "Node A",
      "host": "1.2.3.4",
      "port": 8008,
      "url": "http://1.2.3.4:8008",
      "token": "<long-lived token>",
      "status": "online",
      "online": true,
      "last_seen_at": "2026-03-27T12:00:00Z",
      "capabilities": {
        "runtime_kind": "container"
      }
    }
  ]
}
```

---

## Required normalization behavior

Must match the Go rules in `api/mgr/machines.go`:

1. `machine_key` empty → use `id`
2. `id` empty → use `machine_key`
3. still empty → use `url`
4. `label` empty → first non-empty of `(machine_key, id, url)`
5. `port` empty/0 → `8008`
6. `capabilities` missing → `{}`
7. `capabilities.runtime_kind` missing → `"container"`
8. if `online == true` then `status = "online"`
9. if status empty and node offline → `"offline"`
10. if `online == true` and `last_seen_at` empty → `now(RFC3339)`

### Alias compatibility

`register` must accept instance-style aliases, but canonical storage stays machine-style:

- `machine_key <- machine_key | instance_key | instance_id | id`
- `id <- id | instance_id | machine_key`
- `label <- label | instance_label`
- `url <- url | endpoint`

`list --json` must include:
- `machines[]` (canonical)
- `instances[]` (derived aliases)

---

## Required v1 commands

### `cicy-master list`
- Reads local registry
- Normalizes in memory
- Default output: human table
- `--json`: outputs `default + config_path + machines + instances`

### `cicy-master register`
- Upsert by `machine_key`
- Requires key + url
- Must preserve existing unspecified fields
- Must merge `capabilities` instead of replacing them wholesale
- Optional `--set-default`

### `cicy-master sync`
- Normalize + dedupe + sort by `machine_key`
- Optional `--migrate-legacy`
- Optional `--from-registry`
- Optional `--registry-secret`
- Local registry remains authoritative after merge

### `cicy-master ping`
- GET `<url>/api/ping`
- Optional `--all`
- Optional `--write`

### `cicy-master health`
- GET `<url>/api/health`
- Optional `--all`
- Optional `--write`

When `--write` is set:
- success → `online=true`, `status=online`, `last_seen_at=now`
- failure → `online=false`, `status=offline`

---

## File safety requirements

Any write to the canonical registry must:
- use a lock file (for example `<registry>.lock`)
- use file locking (`flock` / `fcntl.flock`)
- write atomically via temp file + rename in the same directory

This is mandatory because multiple tools may touch the same registry.

---

## Reuse points

Do not invent new semantics if the repo already defines them.

Use these files as the source of truth:
- `api/mgr/machines.go` — schema, normalization, sorting semantics
- `api/mgr/main.go` — ping/health endpoints exist here
- `api/mgr/node.go` — node probing semantics
- `skills/cicy-code` — local registry path conventions and client-side expectations

---

## Acceptance checklist

The implementation passes only if all of these are true:

1. `list --json` returns both `machines[]` and `instances[]`
2. `register` performs merge/upsert and does **not** drop unknown capabilities
3. `sync` supports `--from-registry` and `--registry-secret`
4. file writes are locked and atomic
5. `ping/health --write` persist `online/status/last_seen_at`
6. after `register`, `skills/cicy-code -n <key> ping` resolves correctly
7. `cicy-code` does not hard-block a node just because `runtime_kind=cloudrun`

---

## Minimal verification commands

```bash
python3 skills/cicy-master list --json

python3 skills/cicy-master register node-a http://1.2.3.4:8008 --token TOK --set-default
python3 skills/cicy-master sync --json

python3 skills/cicy-master ping node-a --write
python3 skills/cicy-master health node-a --write

skills/cicy-code -n node-a ping
```

If any of the checklist items above fail, the work is not complete.
