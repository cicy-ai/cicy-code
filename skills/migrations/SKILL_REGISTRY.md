# Skill Registry

The registry is one package (`internal/registry/`) that every skill
registers into via `init()`. Bundle installer, agent SKILL.md generator,
HTTP API, UI — everything consumes the registry.

This document describes the registry's contract and how to use it.

---

## 1. Why a registry?

Today, adding a new skill requires edits in **three places**:

1. `internal/bundle/bundle.go` — `HosttoolAliases` or `BinaryLinks`
2. `cmd/cicy-hosttools/main.go` — argv[0] dispatcher `case "<name>"`
3. `internal/agentgen/generate.go` — `ApprovedCodexSkills` + `renderXxxSkill`

Plus the skill code itself, plus tests, plus updating two SKILL.md
generator functions. Six surface edits for one logical addition.

The registry collapses all of that to **one** `init()` block in the
skill's own file. Three benefits:

- No more drift between bundle and agentgen (they read the same source).
- Adding/removing a skill is grep-friendly: search by `Name:`.
- The UI list is automatic — no per-skill rendering code in the frontend.

---

## 2. Package layout

```
internal/registry/
├── registry.go        ← Register(), Lookup(), List() — public API
├── skill.go           ← struct types (Skill, InstallStatus, Action, etc.)
├── categories.go      ← list of valid categories
└── registry_test.go   ← lint that every registered skill is valid
```

```go
// internal/registry/registry.go
package registry

import (
    "fmt"
    "sort"
    "sync"
)

var (
    mu       sync.RWMutex
    skills   = map[string]*Skill{}
)

// Register adds a skill to the global registry. Called from init() in each
// skill's own file. Panics on duplicate Name or invalid Skill.
func Register(s *Skill) {
    if err := s.Validate(); err != nil {
        panic(fmt.Sprintf("registry: invalid skill %q: %v", s.Name, err))
    }
    mu.Lock()
    defer mu.Unlock()
    if _, exists := skills[s.Name]; exists {
        panic(fmt.Sprintf("registry: skill %q already registered", s.Name))
    }
    skills[s.Name] = s
}

// Lookup returns the skill with the given name, or nil.
func Lookup(name string) *Skill {
    mu.RLock()
    defer mu.RUnlock()
    return skills[name]
}

// List returns all registered skills, sorted by Name.
func List() []*Skill {
    mu.RLock()
    defer mu.RUnlock()
    out := make([]*Skill, 0, len(skills))
    for _, s := range skills {
        out = append(out, s)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out
}

// Filter returns skills matching a predicate (e.g. UI-visible only).
func Filter(pred func(*Skill) bool) []*Skill {
    var out []*Skill
    for _, s := range List() {
        if pred(s) {
            out = append(out, s)
        }
    }
    return out
}
```

```go
// internal/registry/skill.go
type Skill struct {
    // (see SKILL_MANIFEST.md for full field list)
}

func (s *Skill) Validate() error {
    if s.Name == "" { return errors.New("Name required") }
    if !nameRegex.MatchString(s.Name) { return errors.New("Name must be kebab-case") }
    if s.Title == "" { return errors.New("Title required") }
    if s.Description == "" { return errors.New("Description required") }
    if s.Version == "" { return errors.New("Version required") }
    if s.Category == "" { return errors.New("Category required") }
    if !validCategory(s.Category) { return fmt.Errorf("invalid Category %q", s.Category) }
    if len(s.BinaryAliases) == 0 && s.OwnBinary == "" {
        return errors.New("at least one of BinaryAliases or OwnBinary required")
    }
    if s.Run == nil { return errors.New("Run required") }
    if s.InstallCheck == nil { return errors.New("InstallCheck required") }
    if s.AgentSkill && (s.SkillBody == nil || s.HelpBody == nil) {
        return errors.New("AgentSkill: SkillBody and HelpBody required")
    }
    return nil
}
```

---

## 3. Where each skill registers

Skills register **in their own implementation file**, not in a central list.

### Hosttools-shared skill

```go
// internal/hosttools/cf_tunnel.go
package hosttools

import "github.com/cicy-ai/cicy-skills/internal/registry"

func init() {
    registry.Register(&registry.Skill{
        Name: "cf-tunnel",
        // ... full manifest as in SKILL_MANIFEST.md §4
        Run: runCFTunnel,
        InstallCheck: cfTunnelCheck,
    })
}

func runCFTunnel(args []string) int {
    // dispatcher: parse args[1] as subcommand, call corresponding func
    // returns exit code
}

func cfTunnelCheck() registry.InstallStatus {
    return registry.InstallStatus{
        Installed: symlinkOk("cf-tunnel"),
        ConfigPresent: fileExists("~/cicy-ai/db/skills/cf.yaml"),
        RequiresMet: map[string]bool{"cloudflared": which("cloudflared") != ""},
    }
}
```

### Own-binary skill

```go
// cmd/google/main.go
package main

import (
    "os"
    "github.com/cicy-ai/cicy-skills/internal/registry"
)

func init() {
    registry.Register(&registry.Skill{
        Name: "google",
        OwnBinary: "google",
        Run: run,
        // ...
    })
}

func main() {
    s := registry.Lookup("google")
    os.Exit(s.Run(os.Args))
}
```

For own-binary skills, `Run` is the actual implementation; `main()` is a
3-line trampoline. This keeps `main()` consistent across all skills.

---

## 4. Side-effect imports (the only "central" change)

Go's `init()` only runs when the package is **imported**. So
`cmd/cicy-hosttools` and `cmd/cicy-skills` need to import every
hosttools-shared skill package so their init() runs:

```go
// cmd/cicy-hosttools/main.go
import (
    _ "github.com/cicy-ai/cicy-skills/internal/hosttools" // import all skills
    "github.com/cicy-ai/cicy-skills/internal/registry"
    "os"
    "path/filepath"
)

func main() {
    name := filepath.Base(os.Args[0])
    s := registry.Lookup(name)
    if s == nil {
        // also handle BinaryAliases lookup
        for _, sk := range registry.List() {
            for _, a := range sk.BinaryAliases {
                if a == name {
                    s = sk
                    break
                }
            }
        }
    }
    if s == nil {
        fmt.Fprintln(os.Stderr, "unknown command:", name)
        os.Exit(2)
    }
    os.Exit(s.Run(os.Args))
}
```

This is the **single touchpoint** for registering all hosttools-shared skills:
the `_ "internal/hosttools"` import. Since every file in that package
runs its `init()`, every skill becomes available.

For own-binary skills, `cmd/<name>/main.go` directly imports the registry
package; no import-side-effect needed.

For the `cicy-skills` admin CLI, it imports the same `_ "internal/hosttools"`
so its install command sees the full list.

---

## 5. Consumers

### bundle.Install (replaces current explicit lists)

```go
// internal/bundle/install.go (new)
func Install(root, globalBinDir string) error {
    for _, s := range registry.List() {
        for _, alias := range s.BinaryAliases {
            target := filepath.Join(root, "dist", "cicy-hosttools")
            symlink(target, filepath.Join(globalBinDir, alias))
        }
        if s.OwnBinary != "" {
            target := filepath.Join(root, "dist", s.OwnBinary)
            symlink(target, filepath.Join(globalBinDir, s.OwnBinary))
        }
        for _, src := range s.Sources {
            symlink(filepath.Join(root, src.Source), filepath.Join(globalBinDir, src.Name))
        }
        for _, legacy := range s.LegacyAliases {
            removeLink(filepath.Join(globalBinDir, legacy))
        }
        if s.OnInstall != nil {
            s.OnInstall(context.Background())
        }
    }
    return nil
}
```

`HosttoolAliases`, `BinaryLinks`, `LegacyLinks`, `RetiredLocalLinks`,
`ProviderLinks`, `DeprecatedProviderLinks` — all derived from the registry,
no separate var declarations.

### agentgen.Sync (replaces ApprovedCodexSkills + renderXxx)

```go
// internal/agentgen/sync.go (new)
func Sync(profile string, targetRoot string) error {
    for _, s := range registry.Filter(func(s *registry.Skill) bool {
        return s.AgentSkill && includesProfile(s.Profiles, profile)
    }) {
        skillDir := filepath.Join(targetRoot, s.Name)
        os.MkdirAll(skillDir, 0o755)
        writeFile(filepath.Join(skillDir, "SKILL.md"), s.SkillBody())
        refDir := filepath.Join(skillDir, "references")
        os.MkdirAll(refDir, 0o755)
        writeFile(filepath.Join(refDir, "help.md"), s.HelpBody())
        if s.ToolsBody != nil {
            writeFile(filepath.Join(refDir, "tools.md"), s.ToolsBody())
        }
    }
    return nil
}
```

### HTTP API (NEW — added to cicy-code api/)

See [SKILL_API.md](./SKILL_API.md).

### CLI completion (`cicy-skills list-skills`)

```go
// cmd/cicy-skills/main.go
case "list-skills":
    for _, s := range registry.List() {
        if jsonFlag {
            json.NewEncoder(os.Stdout).Encode(s.PublicView()) // strip handler funcs
        } else {
            fmt.Printf("%-25s %s\n", s.Name, s.Title)
        }
    }
```

---

## 6. Validation at startup

Every skill's `Validate()` runs at `Register()` time and panics on failure.
This makes invalid skills impossible to deploy: the binary won't start.

The CI runs `go test ./internal/registry/` which loads all skills (via the
side-effect import) and confirms every registered skill validates. This is
the lint gate.

```go
// internal/registry/registry_test.go
import (
    _ "github.com/cicy-ai/cicy-skills/internal/hosttools"
    "testing"
)

func TestAllSkillsValid(t *testing.T) {
    for _, s := range List() {
        if err := s.Validate(); err != nil {
            t.Errorf("skill %q: %v", s.Name, err)
        }
    }
}

func TestNoNameCollisions(t *testing.T) {
    seen := map[string]string{}
    for _, s := range List() {
        for _, a := range s.BinaryAliases {
            if owner, ok := seen[a]; ok {
                t.Errorf("alias %q claimed by %q and %q", a, owner, s.Name)
            }
            seen[a] = s.Name
        }
    }
}
```

---

## 7. Adding a third-party skill (future)

When we eventually allow out-of-tree skills:

1. Third party publishes a Go module with their `init()` block.
2. We compile a build that imports it (or load via plugin if Go allows it).
3. Their `Register()` runs, their skill appears in UI.

The registry pattern is the foundation. Today, "out-of-tree" means another
file in `internal/hosttools/`. Tomorrow, it could mean a different repo.

---

## 8. Migration order for existing skills

Each existing skill migrates to the registry in two steps:

**Phase 1** (per skill, one PR each):
1. Move the skill's implementation into `internal/hosttools/<name>.go`
2. Add the `init()` `Register()` call.
3. Verify the skill still works via current bundle.Install/agentgen.Sync.
   (The registry exists but isn't yet authoritative — the old paths still work.)

**Phase 2** (one PR, after all skills migrated):
1. Delete `HosttoolAliases`, `BinaryLinks`, `LegacyLinks`, `RetiredLocalLinks`,
   `ApprovedCodexSkills`, `canonicalCodexSkillName`, and all `renderXxx`
   refs from outside the skill files.
2. Replace `bundle.Install` and `agentgen.Sync` with the registry-driven
   versions.
3. Delete `cmd/cicy-hosttools/main.go`'s argv[0] switch — replace with the
   single `registry.Lookup` lookup shown in §4.

After Phase 2: the registry is the only source of truth.
