# cicy-skills Migration & Platform Docs

This directory is the contract for anyone (human or agent) shipping a CiCy
skill. Read in this order:

| # | Document | Audience | What it tells you |
| --- | --- | --- | --- |
| 1 | [SKILL_MANIFEST.md](./SKILL_MANIFEST.md) | everyone | The metadata schema. Every skill starts here. |
| 2 | [SKILL_REGISTRY.md](./SKILL_REGISTRY.md) | skill authors | How a skill registers itself once and shows up in install, SKILL.md, and UI automatically. |
| 3 | [SKILL_DEV_GUIDE.md](./SKILL_DEV_GUIDE.md) | skill authors | Coding conventions: config layout, CLI rules, tests, output format. |
| 4 | [SKILL_API.md](./SKILL_API.md) | cicy-code backend devs | The HTTP endpoints cicy-code exposes so the UI can list / install / uninstall skills. |
| 5 | [SKILL_UI.md](./SKILL_UI.md) | cicy-code frontend devs | The Skills page in the app. What the user sees and clicks. |

## TL;DR for new skill authors

1. **Define your manifest** in your skill's Go file (`internal/hosttools/myskill.go`).
2. **Implement `Run(args)`** that handles your subcommands.
3. **Implement `Render*`** functions if your skill should produce SKILL.md.
4. **`init()` calls `registry.Register(&Skill{...})`** — one line wires up all
   surfaces (install, SKILL.md, UI).
5. **Config** goes to `~/cicy-ai/db/skills/<name>.yaml` via `skillsdb.Load/Save`.
6. **Test**: `go test ./internal/hosttools/`. CLI smoke: `cicy-skills install all && <name> help`.
7. **PR** on branch `migrate/<name>`. Reviewer verifies against the checklist
   in SKILL_DEV_GUIDE.md §8.

That's it. No other files to touch.
