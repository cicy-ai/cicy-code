# Skill Manifest

Every skill declares one manifest. The manifest is the single source of
truth for installer, agent SKILL.md generator, HTTP API, and UI.

If a field isn't in the manifest, it isn't displayed anywhere. If you want
something visible to users, put it in the manifest.

---

## 1. Schema

```go
// internal/registry/skill.go
type Skill struct {
    // ── identity ──────────────────────────────────────────────────
    Name        string   // canonical, kebab-case, used as install name & file basename
    Title       string   // human-readable, shown in UI cards
    Description string   // one-sentence tagline shown in UI cards and SKILL.md description
    Version     string   // semver; bump on breaking changes
    Category    string   // grouping in UI: "network", "ai", "infra", "dev", "comms", "ops"
    Icon        string   // lucide-react icon name, e.g. "globe", "terminal", "cloud"
    Tags        []string // free-form search tags

    // ── install behavior ──────────────────────────────────────────
    BinaryAliases []string // command names this skill exposes via cicy-hosttools dispatch
    OwnBinary     string   // if non-empty, ship a separate binary at dist/<OwnBinary>
    Sources       []SourceLink // additional source-tree symlinks (e.g. providers/google-node/google.js)
    LegacyAliases []string // RetiredLocalLinks — removed on install

    // ── runtime dependencies ──────────────────────────────────────
    Requires []Requirement // external programs/files the skill needs to function

    // ── config ────────────────────────────────────────────────────
    ConfigFile string // "~/cicy-ai/db/skills/<name>.yaml" — leave "" if skill has no config
    StateDir   string // "~/.local/state/cicy-skills/<name>" — leave "" for no state

    // ── agent SKILL.md generation ─────────────────────────────────
    AgentSkill bool     // whether to emit SKILL.md
    Profiles   []string // ["codex","claude","opencode"]; empty means "all"
    SkillBody  func() string // SKILL.md body
    HelpBody   func() string // references/help.md body
    ToolsBody  func() string // references/tools.md body (optional — defaults to "")

    // ── UI visibility ─────────────────────────────────────────────
    UIVisible bool     // show in cicy-code Skills page (default false until graduated)
    UIAction  []Action // optional buttons beyond install/uninstall

    // ── implementation handlers ───────────────────────────────────
    Run          func(args []string) int           // the CLI dispatcher (argv[0] === Name)
    InstallCheck func() InstallStatus              // is this skill installed correctly RIGHT NOW?
    OnInstall    func(ctx context.Context) error   // optional post-install hook (run after symlinks built)
    OnUninstall  func(ctx context.Context) error   // optional cleanup hook
}

type SourceLink struct {
    Name   string // symlink name in ~/.local/bin/
    Source string // relative path from repo root, e.g. "providers/google-node/google.js"
}

type Requirement struct {
    Kind     string // "command" | "file" | "env" | "tcp_port"
    Value    string // command name, file path, env var name, "host:port"
    Optional bool   // if true, skill works without it but with reduced functionality
    Help     string // one line explaining how to satisfy it
}

type InstallStatus struct {
    Installed     bool              // CLI symlinks exist and resolve
    ConfigPresent bool              // ConfigFile exists and parses as YAML
    RequiresMet   map[string]bool   // per-requirement satisfaction
    LastError     string            // empty if healthy
    Detail        map[string]string // free-form extra fields (e.g. "version": "v0.1.4")
}

type Action struct {
    ID    string // unique per skill, e.g. "rotate-token"
    Label string // button text
    HTTP  string // "POST /api/skills/<name>/actions/<id>"
}
```

---

## 2. Required vs optional fields

### Required for every skill

- `Name`, `Title`, `Description`, `Version`, `Category`
- `BinaryAliases` OR `OwnBinary` (at least one — otherwise it's not installable)
- `Run` (the CLI handler)
- `InstallCheck` (so the UI can show a real status)

### Required if it's an agent skill (SKILL.md generated)

- `AgentSkill: true`
- `SkillBody`, `HelpBody`
- `Profiles` (empty = all three)

### Required for UI visibility

- `UIVisible: true`
- `Icon` (default is `"box"` if not set)
- `Category` must be one of the recognized categories below

### Recognized categories (UI groups skills by these)

| Category | Examples | Description |
| --- | --- | --- |
| `network` | cf-tunnel, frp-server, frp-client, cping, proxy_ssh | tunnels, routing, latency |
| `ai` | google, agent-summary, agent-webpage | LLM-adjacent integrations |
| `infra` | us-spot-dev, us-spot-proxy, docker-build-github-action | cloud provisioning, CI |
| `dev` | cicy-code, cicy-master, cicy-agent | CiCy development tools |
| `comms` | tg | messaging |
| `ops` | globalApiToken, cicy-ssh | credentials, machine ops |

Pick the closest one. New categories must be added to `internal/registry/categories.go`.

---

## 3. Naming rules

- `Name` is **kebab-case**, lowercase. Examples: `cf-tunnel`, `frp-server`.
- `Name` matches the CLI command name a user types (`cf-tunnel list`).
- `Name` matches the SKILL.md directory name (`~/.claude/skills/cf-tunnel/`).
- `Name` matches the config file basename (`~/cicy-ai/db/skills/cf-tunnel.yaml`).
- `Name` matches the state dir name (`~/.local/state/cicy-skills/cf-tunnel/`).

This four-way collision is intentional: it makes every artifact discoverable
from one identifier.

`Title` is a free human label; it can contain spaces, capitalization, and
non-ASCII. Examples: `"Cloudflare Tunnel"`, `"FRP Server"`, `"飞书机器人"`.

---

## 4. Manifest examples

### Minimal hosttools-shared skill

```go
// internal/hosttools/cf_tunnel.go

func init() {
    registry.Register(&registry.Skill{
        Name:          "cf-tunnel",
        Title:         "Cloudflare Tunnel",
        Description:   "Manage Cloudflare Tunnel routes and DNS records on this host.",
        Version:       "1.0.0",
        Category:      "network",
        Icon:          "globe",
        Tags:          []string{"cloudflare","tunnel","dns"},

        BinaryAliases: []string{"cf-tunnel", "cf-tunnel-py", "cf-tunnel.py"},
        ConfigFile:    "~/cicy-ai/db/skills/cf.yaml",
        Requires: []registry.Requirement{
            {Kind: "command", Value: "cloudflared", Help: "install via apt: sudo apt install cloudflared"},
        },

        AgentSkill:    true,
        Profiles:      nil, // all three
        SkillBody:     renderCFTunnelSkill,
        HelpBody:      renderCFTunnelHelp,
        ToolsBody:     renderCFTunnelTools,

        UIVisible:     true,
        Run:           runCFTunnel,
        InstallCheck:  checkCFTunnelStatus,
    })
}
```

### Own-binary skill (Google after rewrite)

```go
// cmd/google/main.go  -- single-file standalone binary

func init() {
    registry.Register(&registry.Skill{
        Name:        "google",
        Title:       "Google Workspace",
        Description: "Gmail / Sheets / Drive / Calendar via Google APIs.",
        Version:     "2.0.0",
        Category:    "ai",
        Icon:        "google",
        Tags:        []string{"gmail","sheets","drive","calendar","oauth"},

        OwnBinary:   "google", // ships at dist/google
        ConfigFile:  "~/cicy-ai/db/skills/google.yaml",
        StateDir:    "~/.local/state/cicy-skills/google",
        Requires:    nil,

        AgentSkill:  true,
        SkillBody:   renderGoogleSkill,
        HelpBody:    renderGoogleHelp,
        ToolsBody:   renderGoogleTools,

        UIVisible:   true,
        UIAction: []registry.Action{
            {ID: "reauthorize", Label: "Re-authorize", HTTP: "POST /api/skills/google/actions/reauthorize"},
        },
        Run:          run,
        InstallCheck: checkInstall,
    })
}
```

### No-config skill (cping)

```go
func init() {
    registry.Register(&registry.Skill{
        Name:          "cping",
        Title:         "cping",
        Description:   "Quick network latency check for a domain or IP.",
        Version:       "1.0.0",
        Category:      "network",
        Icon:          "activity",

        BinaryAliases: []string{"cping"},
        // ConfigFile and StateDir omitted: cping is stateless

        AgentSkill:   true,
        SkillBody:    renderCPingSkill,
        HelpBody:     renderCPingHelp,
        ToolsBody:    renderCPingTools,

        UIVisible:    true,
        Run:          runCPing,
        InstallCheck: func() registry.InstallStatus {
            return registry.InstallStatus{Installed: true}
        },
    })
}
```

---

## 5. Lifecycle: what reads the manifest?

| Surface | Reads which fields | Outcome |
| --- | --- | --- |
| `cicy-skills install all` | `Name`, `BinaryAliases`, `OwnBinary`, `Sources`, `LegacyAliases` | Creates symlinks in `~/.local/bin/` |
| `cicy-skills install all` (continued) | `AgentSkill`, `Profiles`, `SkillBody`, `HelpBody`, `ToolsBody` | Writes SKILL.md / help.md / tools.md to `~/.{profile}/skills/<Name>/` |
| `cicy-skills agent list <profile>` | `AgentSkill`, `Profiles`, `Name` | Lists approved skills |
| `cicy-skills install all` (continued) | `OnInstall` | Calls hook for npm install / OAuth pre-flight |
| `cicy-skills remove all` | `OnUninstall`, `BinaryAliases`, `OwnBinary` | Removes symlinks and runs cleanup hook |
| Argv[0] dispatcher | `Name`, `BinaryAliases`, `Run` | Calls `Run(args)` when matched name invoked |
| HTTP `GET /api/skills` | every field except `Run`/`*Body` | JSON list for UI |
| HTTP `POST /api/skills/<name>/install` | `OnInstall` + symlink + SKILL.md | Performs install |
| HTTP `GET /api/skills/<name>/status` | `InstallCheck` | Live health check |
| UI Skills page | `Name`, `Title`, `Description`, `Category`, `Icon`, `Tags`, `UIVisible`, install status | Renders cards |

You do not write code for any of these consumers. The registry does it.

---

## 6. Versioning

`Version` is semver:
- **patch** bump (1.0.0 → 1.0.1): bug fix, no behavior change
- **minor** bump (1.0.0 → 1.1.0): new subcommand or flag, backwards compatible
- **major** bump (1.0.0 → 2.0.0): breaking change to existing subcommand/output

When `Version` major bumps, the UI flags the user "X has a new major version
available — review before upgrading".

The version is also embedded in SKILL.md frontmatter so agents know which
behavior they're seeing.

---

## 7. Why a manifest?

So that:
- **One file** in the skill's own package owns identity + behavior. Bundle
  registry, agentgen registry, and approval list don't drift apart.
- **New skill** = create one Go file + `init()` block. Nothing else.
- **UI** can render a list without code per skill.
- **Agents** (or LLMs) reading code can see a single struct that fully
  describes the skill.
- **External skill repos** (future): the manifest schema is stable enough
  that an out-of-tree skill can register via a plugin pattern.
