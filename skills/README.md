# skills

This directory contains small CLI helpers (“skills”) used to operate and automate `cicy-code`.

## cicy-master (node registry manager)

> **Audience:** `w-20091` implementer

### Purpose

`cicy-master` is a dedicated skill to manage **all nodes** via a **local node registry file**.

- `cicy-code` remains the *client* for calling a node’s API.
- `cicy-master` is the *registry manager* for creating/updating/inspecting node entries.

### Hard requirements

1. **Source of truth is local file**
   - Default: `~/Private/cicy-node.json`
   - Override: `CICY_NODES_FILE`
   - Legacy (import/migrate only): `~/Private/cicy-nodes.json`

2. **Nodes MUST NOT call master**
   - Do not require `CICY_MASTER_URL` / `CICY_MASTER_TOKEN` on nodes.
   - Master (or tooling) probes nodes using `ping/health`.

3. **Long-lived per-node token**
   - `CICY_API_TOKEN` is redefined as a **long-lived per-node API token**.
   - It is **created by the master/tooling and injected into the node** at deploy time.
   - The registry stores it as `machines[].token`.

4. **Canonical schema + normalization must match Go**
   - Schema + rules are defined in:
     - `api/mgr/machines.go`
   - `cicy-master` must implement the same normalization rules and preserve unknown `capabilities` keys.

### Canonical registry schema (stored on disk)

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

### Normalization rules (must mirror `api/mgr/machines.go`)

- `machine_key` empty → use `id`
- `id` empty → use `machine_key`
- still empty → use `url`
- `label` empty → first non-empty of `(machine_key, id, url)`
- `port` empty/0 → `8008`
- `capabilities` missing → `{}`
- `capabilities.runtime_kind` missing → `"container"`
- `status`: if `online==true` then `"online"`, else if empty then `"offline"`
- `last_seen_at`: if `online==true` and empty → set `now(RFC3339)`

### Alias compatibility

`cicy-master register` must accept instance-style inputs but always persist canonical fields:

- `machine_key <- machine_key | instance_key | instance_id | id`
- `id <- id | instance_id | machine_key`
- `label <- label | instance_label`
- `url <- url | endpoint`

`cicy-master list --json` should include:
- `machines[]` (canonical)
- `instances[]` (derived view with `instance_key/instance_id/instance_label/runtime_kind`)

### v1 command contract

Must implement:

- `list` (+ `--json`)
- `register` (upsert by `machine_key`, require key+url, optional `--set-default`)
- `sync`
  - normalize + dedupe + sort by `machine_key`
  - optional legacy migration flag
  - optional registry merge (local remains authoritative)
  - MUST write atomically and use file lock
- `ping` / `health`
  - `GET <url>/api/ping` and `GET <url>/api/health`
  - support `--all` and `--write` to persist `online/status/last_seen_at`

### File write safety

Any write to the registry must:
- use a lock (e.g. `flock` on `<file>.lock`)
- be atomic (tmp file in same dir + rename)

### Verification (minimum)

1. Golden-file tests using temp `CICY_NODES_FILE`:
   - `register` then `list --json` normalization
   - `sync` dedupe + stable sort

2. Network test:
   - run `ping/health --write` against a stub or real node and confirm file updates

3. Compatibility:
   - after `register`, `skills/cicy-code -n <key> ping` should resolve and work.
