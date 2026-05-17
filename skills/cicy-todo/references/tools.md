# Cicy Todo — Tool Map

## CLI subcommands

The leading `[pane]` is optional — without it the command targets the current
pane. With it (`cicy-todo w-10001 ...`) the command targets that agent.

| Command | What it does |
|---|---|
| `cicy-todo`                              | shortcut for `list` on own pane |
| `cicy-todo w-10001`                      | shortcut for `list` on w-10001 |
| `cicy-todo [pane] add "<title>"`            | create a new todo (status=todo) |
| `cicy-todo [pane] list [--status=<s>] [-q <kw>] [--all] [--json]` | list todos; default hides `done`+`dropped` |
| `cicy-todo [pane] show <id>`                | full detail for one todo |
| `cicy-todo [pane] start <id>`               | transition to `doing` |
| `cicy-todo [pane] done  <id>`               | transition to `done` |
| `cicy-todo [pane] drop  <id>`               | transition to `dropped` |
| `cicy-todo [pane] back  <id>`               | transition back to `todo` |
| `cicy-todo [pane] edit  <id> "<new title>"` | rename |
| `cicy-todo [pane] rm    <id>`               | delete |

Global flags: `--pane <w-xxxxx>` (same effect as the positional form); `--json` emits raw API response.

## Underlying REST endpoints

All routed through the cicy-code server (default `http://127.0.0.1:8008`):

| Method | Path | Notes |
|---|---|---|
| `GET`    | `/api/todo/list[?status=&q=]` | list + filter |
| `GET`    | `/api/todo/counts`            | per-status counts |
| `POST`   | `/api/todo/add`                | body: `{title}` |
| `PATCH`  | `/api/todo/{id}`               | body: `{status?, title?}` |
| `DELETE` | `/api/todo/{id}`               | — |

Headers (every request):

- `Authorization: Bearer <api_token>` — read from `~/cicy-ai/global.json:api_token`
- `X-Agent-Show-Id: <pane>` — which agent's `todos.yaml` to act on (e.g. `w-10001`). Set by the CLI from the positional pane arg or `$CICY_PANE_ID`.

Backwards compat: `pane_id` query param or body field is still accepted and overrides the header.

## Storage

- Single file per pane: `<workspace>/.cicy/todos.yaml`
- YAML schema:
  ```yaml
  todos:
    - id: t-1747440000-3a7b
      title: "Setup CI"
      status: todo            # todo | doing | done | dropped
      created_at: 2026-05-17T10:00:00Z
      updated_at: 2026-05-17T10:00:00Z
  ```
- Empty file or missing file is treated as empty list
- Writes are atomic (`.tmp` + `rename`)

## Environment

| Var | Default | Purpose |
|---|---|---|
| `CICY_PANE_ID`     | `w-10001`             | pane targeted when no `--pane` flag |
| `CICY_API_PORT`    | `8008`                | cicy-code listen port |
| `CICY_API_TOKEN`   | (read from global.json) | overrides token lookup |

## Exit codes

- `0` — success
- `1` — bad usage / validation error
- `2` — pane / todo not found
- `3` — server unreachable or auth failure
- `4` — ambiguous id prefix (multiple matches)
