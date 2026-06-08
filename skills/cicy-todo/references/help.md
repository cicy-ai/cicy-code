# Cicy Todo — Help

## Command

- `cicy-todo` (PATH binary, bash script)

## Quick start

```sh
# OWN pane (= $CICY_PANE_ID, else w-1001)
cicy-todo                           # list own active todos
cicy-todo add "Setup CI pipeline"
cicy-todo list --status=all         # everything
cicy-todo list --status=done -q ci  # filter by status + keyword
cicy-todo start  t-1779             # status → doing  (prefix match)
cicy-todo done   t-1779             # status → done
cicy-todo drop   t-1779             # status → dropped
cicy-todo back   t-1779             # status → todo
cicy-todo rm     t-1779             # remove
cicy-todo show   t-1779             # full detail

# OTHER agent — leading positional pane id
cicy-todo w-1001                   # list w-1001's active todos
cicy-todo w-1001 list --all        # full list for w-1001
cicy-todo w-1001 add "ship it"     # add a todo to w-1001
cicy-todo w-1001 done t-1779       # mark w-1001's todo done
```

## Pane scoping

Each worker (`w-xxxxx`) has its own `todos.yaml` under `<workspace>/.cicy/`. Pane is resolved in this order:

1. Positional pane arg, e.g. `cicy-todo w-1001 ...` (recommended)
2. `--pane <w-xxxxx>` flag (equivalent to the positional form)
3. `CICY_PANE_ID` env var
4. fallback: `w-1001`

The resolved pane is sent to the backend as `X-Agent-Show-Id: <pane>` on every request.

## Output

- Human: `id title status updated` columns
- Script: pass `--json` for raw API responses

## Rules

- Use the local cicy-code REST API; never edit `todos.yaml` directly
- Status values are exactly: `todo | doing | done | dropped`
- Empty title is rejected
- Id matching: full id or unique prefix; multiple matches → list candidates and abort

## More

- tool / env map: [tools.md](./tools.md)
