# Skill HTTP API

cicy-code (the Go server in `~/projects/cicy-code/api/`) exposes a small
HTTP API that the cicy-code app (React/Vite SPA in `~/projects/cicy-code/app/`)
uses to render the Skills page and trigger installs.

Every endpoint reads from the in-process skill registry (see
[SKILL_REGISTRY.md](./SKILL_REGISTRY.md)). cicy-code links the
`internal/registry` package and the side-effect import for
`internal/hosttools`, so the same single source of truth backs the API.

Auth: existing `Authorization: Bearer <api_token>` pattern. The API checks
the token like every other cicy-code endpoint.

---

## 1. Endpoint summary

| Method | Path | Purpose |
| --- | --- | --- |
| GET    | `/api/skills` | List all skills with status |
| GET    | `/api/skills/<name>` | Detail for one skill |
| GET    | `/api/skills/<name>/status` | Live status (cheap; for polling) |
| POST   | `/api/skills/<name>/install` | Install (or repair) one skill |
| POST   | `/api/skills/<name>/uninstall` | Uninstall one skill |
| POST   | `/api/skills/<name>/actions/<id>` | Run a custom action declared in `Skill.UIAction` |
| POST   | `/api/skills/install-all` | Run `cicy-skills install all` (matches CLI) |
| POST   | `/api/skills/sync-agent/<profile>` | Regenerate `~/.<profile>/skills/` |
| GET    | `/api/skills/categories` | List recognized categories with counts |
| GET    | `/api/skills/health` | Aggregate: how many installed / how many failing |

All responses are JSON. All errors return `{"error":"...","code":N}` with
HTTP status reflecting category (400 for user error, 500 for internal).

---

## 2. GET /api/skills

List every UI-visible skill with its current install status.

**Query params:**
- `category=<name>` — filter to a single category
- `installed=true|false` — filter by install state
- `q=<text>` — search across Name, Title, Description, Tags

**Response:**

```json
{
  "skills": [
    {
      "name": "cf-tunnel",
      "title": "Cloudflare Tunnel",
      "description": "Manage Cloudflare Tunnel routes and DNS records on this host.",
      "version": "1.0.0",
      "category": "network",
      "icon": "globe",
      "tags": ["cloudflare","tunnel","dns"],

      "binary_aliases": ["cf-tunnel","cf-tunnel-py","cf-tunnel.py"],
      "config_file": "/home/cicy/cicy-ai/db/skills/cf.yaml",
      "agent_skill": true,
      "profiles": ["codex","claude","opencode"],
      "ui_actions": [],

      "status": {
        "installed": true,
        "config_present": true,
        "requires_met": {"cloudflared": true},
        "last_error": "",
        "detail": {"alias_count": "3"}
      }
    }
  ],
  "total": 13,
  "installed": 13,
  "needs_attention": 0
}
```

Field `binary_aliases` is shown so the UI can render the actual commands a
user can type. `config_file` is shown so the UI can render a "this skill
stores credentials at <path>" tooltip.

**Implementation sketch:**

```go
// api/mgr/skills.go (new)
func handleSkillsList(w http.ResponseWriter, r *http.Request) {
    skills := registry.Filter(func(s *registry.Skill) bool { return s.UIVisible })
    // apply filters from query params
    var resp struct {
        Skills           []skillView `json:"skills"`
        Total            int         `json:"total"`
        Installed        int         `json:"installed"`
        NeedsAttention   int         `json:"needs_attention"`
    }
    for _, s := range skills {
        status := s.InstallCheck()
        resp.Skills = append(resp.Skills, skillView{
            // ... project Skill struct minus handler funcs
            Status: status,
        })
        if status.Installed && status.LastError == "" {
            resp.Installed++
        } else if !status.RequiresAllMet() {
            resp.NeedsAttention++
        }
    }
    resp.Total = len(resp.Skills)
    json.NewEncoder(w).Encode(resp)
}
```

`skillView` is the API-facing projection of `registry.Skill` — same fields
minus the function members.

---

## 3. GET /api/skills/<name>

Detail for one skill, including the full SKILL.md / help.md content so the
UI can show a preview.

**Response:**

```json
{
  "name": "cf-tunnel",
  "title": "Cloudflare Tunnel",
  "description": "...",
  "version": "1.0.0",
  "category": "network",
  "icon": "globe",
  "tags": ["cloudflare","tunnel","dns"],
  "binary_aliases": ["cf-tunnel"],
  "config_file": "/home/cicy/cicy-ai/db/skills/cf.yaml",
  "state_dir": "/home/cicy/.local/state/cicy-skills/cf-tunnel",
  "requires": [
    {"kind":"command","value":"cloudflared","optional":false,"help":"install via apt"}
  ],
  "agent_skill": true,
  "profiles": ["codex","claude","opencode"],
  "ui_actions": [],
  "status": {
    "installed": true,
    "config_present": true,
    "requires_met": {"cloudflared": true},
    "last_error": "",
    "detail": {}
  },
  "skill_body": "---\nname: cf-tunnel\ndescription: ...\n---\n\n# Cf Tunnel\n...",
  "help_body":  "# Cf Tunnel Help\n\n## Command\n- primary command: `cf-tunnel`\n..."
}
```

---

## 4. GET /api/skills/<name>/status

Cheap, no body content. Used by UI for live polling without re-fetching the
description/SKILL.md text.

**Response:**

```json
{
  "installed": true,
  "config_present": true,
  "requires_met": {"cloudflared": true},
  "last_error": "",
  "detail": {}
}
```

The UI polls this when the user clicks "Install" to show progress.

---

## 5. POST /api/skills/<name>/install

Run the install procedure for one skill:

1. Look up the skill in the registry.
2. Call `s.OnInstall(ctx)` if defined.
3. Recreate the symlinks that this skill owns (from
   `BinaryAliases` / `OwnBinary` / `Sources`).
4. If `AgentSkill`, regenerate SKILL.md / help.md / tools.md in every
   profile under `Profiles`.
5. Run `s.InstallCheck()` and return the result.

**Body:** empty or `{"force": true}` (re-run even if installed)

**Response:**

```json
{
  "ok": true,
  "status": {
    "installed": true,
    "config_present": true,
    "requires_met": {"cloudflared": true},
    "last_error": "",
    "detail": {"version": "1.0.0"}
  },
  "log": [
    "linked: cf-tunnel -> /home/cicy/projects/cicy-code/skills/dist/cicy-hosttools",
    "linked: cf-tunnel-py -> ...",
    "wrote: ~/.claude/skills/cf-tunnel/SKILL.md",
    "wrote: ~/.codex/skills/cf-tunnel/SKILL.md",
    "wrote: ~/.opencode/skills/cf-tunnel/SKILL.md",
    "OnInstall: skipped (none)"
  ]
}
```

If the skill's `InstallCheck` returns `LastError`, the call still returns
`ok: true` but `status.last_error` is non-empty. The UI shows a warning
banner with the error text.

If install itself fails (couldn't create symlink, OnInstall returned error),
return HTTP 500 with `{"ok": false, "error": "...", "log": [...]}`.

---

## 6. POST /api/skills/<name>/uninstall

1. Call `s.OnUninstall(ctx)` if defined.
2. Remove symlinks owned by this skill from `~/.local/bin/`.
3. Remove SKILL.md directories under every profile.
4. **Do not** delete `ConfigFile` or `StateDir` — these may contain user
   data. Show a "Also delete config?" checkbox in the UI; if checked, the
   body becomes `{"purge_config": true}` and the server then deletes them.

**Response:**

```json
{
  "ok": true,
  "log": [
    "removed: ~/.local/bin/cf-tunnel",
    "removed: ~/.local/bin/cf-tunnel-py",
    "removed: ~/.claude/skills/cf-tunnel/",
    "OnUninstall: skipped (none)",
    "config preserved at ~/cicy-ai/db/skills/cf.yaml"
  ]
}
```

---

## 7. POST /api/skills/<name>/actions/<id>

Run a custom action that the skill declared via `Skill.UIAction`. The
server validates that the action `<id>` exists on the skill, then invokes
the corresponding handler (registered alongside the action in code).

Example: `POST /api/skills/google/actions/reauthorize` triggers Google
OAuth re-authorization.

**Body:** action-specific JSON, passed verbatim to the handler.

**Response:** action-specific JSON.

The handler is a separate function on the Skill struct (not shown in the
manifest schema directly for brevity, but each `Action` carries a
`Handler func(ctx, body) (resp any, err error)` in the Go code).

---

## 8. POST /api/skills/install-all

Run the same logic as `cicy-skills install all` — install every registered
skill, sync every profile's SKILL.md, ensure PATH.

Body: `{}` or `{"force": true}`.

Response: aggregate of per-skill install results.

```json
{
  "ok": true,
  "results": [
    {"name": "cf-tunnel", "ok": true, "status": {...}},
    {"name": "google",    "ok": true, "status": {...}}
  ],
  "summary": {"total": 13, "installed": 13, "failed": 0}
}
```

This is the UI button "Install all skills".

---

## 9. POST /api/skills/sync-agent/<profile>

Regenerate SKILL.md output for `<profile>` (`codex` / `claude` / `opencode`).

```json
{
  "ok": true,
  "target": "/home/cicy/.claude/skills",
  "synced": ["cf-tunnel","cping","frp-server",...]
}
```

---

## 10. GET /api/skills/categories

Returns categories and how many skills (UI-visible) are in each.

```json
{
  "categories": [
    {"id": "network", "label": "Network",       "count": 4},
    {"id": "ai",      "label": "AI & Agents",   "count": 3},
    {"id": "infra",   "label": "Infrastructure","count": 3},
    {"id": "dev",     "label": "Development",   "count": 2},
    {"id": "comms",   "label": "Communications","count": 1},
    {"id": "ops",     "label": "Operations",    "count": 2}
  ]
}
```

---

## 11. GET /api/skills/health

Aggregate health for a dashboard tile.

```json
{
  "total": 13,
  "installed": 13,
  "needs_attention": 0,
  "last_install_at": "2026-05-13T09:22:14Z",
  "errors": []
}
```

When something is broken:

```json
{
  "total": 13,
  "installed": 12,
  "needs_attention": 1,
  "errors": [
    {"name": "google", "error": "OAuth refresh token expired; re-authorize"}
  ]
}
```

---

## 12. Server implementation

```go
// api/mgr/skills.go (new file)
package main

import (
    "encoding/json"
    "net/http"
    "github.com/cicy-ai/cicy-skills/internal/registry"
    _ "github.com/cicy-ai/cicy-skills/internal/hosttools"
)

func registerSkillRoutes(mux *http.ServeMux) {
    mux.HandleFunc("/api/skills",                        handleSkillsList)
    mux.HandleFunc("/api/skills/install-all",            handleInstallAll)
    mux.HandleFunc("/api/skills/sync-agent/",            handleSyncAgent)
    mux.HandleFunc("/api/skills/categories",             handleCategories)
    mux.HandleFunc("/api/skills/health",                 handleHealth)
    mux.HandleFunc("/api/skills/",                       handleSkillByName) // routes /api/skills/<name>[/status|install|uninstall|actions/<id>]
}
```

The cicy-code main HTTP handler imports `internal/hosttools` for the
init() side-effect — that's the only "wiring" code outside the skill
files.

---

## 13. Auth & rate limiting

- Standard `Authorization: Bearer <api_token>` from `~/cicy-ai/global.json`
  (legacy) or `~/cicy-ai/db/skills/globalApiToken.yaml` (new). The auth check
  reuses the existing middleware.
- Install/uninstall is rate limited at 1/sec/skill to prevent UI button
  spam from filling the filesystem with relink churn.

---

## 14. Errors & exit codes (HTTP)

| HTTP | Code | When |
| --- | --- | --- |
| 200  | -    | Success |
| 400  | invalid_request | Bad path, bad body, unknown filter |
| 401  | unauthorized    | Missing or invalid Authorization |
| 404  | not_found       | Unknown skill name |
| 409  | conflict        | Install in progress for this skill |
| 422  | requires_unmet  | Skill cannot install — a `Requirement` failed (e.g. `cloudflared` not installed) |
| 500  | internal        | Filesystem error, hook error, etc. |

Body:
```json
{"ok": false, "error": "human readable", "code": "requires_unmet", "detail": {"missing": ["cloudflared"]}}
```

---

## 15. SSE for long install (optional)

`POST /api/skills/install-all` may take seconds. Optional: support
`Accept: text/event-stream` for line-by-line streaming progress:

```
data: {"phase":"install","name":"cf-tunnel","msg":"linking 3 aliases"}

data: {"phase":"sync","name":"cf-tunnel","msg":"writing SKILL.md to ~/.claude/skills/cf-tunnel"}

data: {"phase":"done","result":{...}}
```

Implement if the UI ends up needing real-time progress for a "Install all"
operation across 13+ skills. Default to single-JSON response for simplicity.
