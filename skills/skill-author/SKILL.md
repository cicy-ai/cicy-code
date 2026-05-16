---
name: skill-author
description: Author and publish custom Claude/Codex/OpenCode skills under ~/cicy-ai/skills/. Use when the user asks to create a new skill, edit an existing user-authored skill, or document the SKILL.md / references file convention.
---

# Skill Author

Use this skill to scaffold, install, list, and remove **user-authored skills** that live under `~/cicy-ai/skills/<name>/` and are surfaced in the cicy-code marketplace.

The `skill-author` command on `PATH` is the official entry point — read [tools.md](./references/tools.md) for the full command surface.

## Scope

Use this skill when the task involves:

- creating a brand-new user skill (scaffold the directory layout + template files)
- syncing an existing user skill into the three agent profiles (`.claude`, `.codex`, `.opencode`)
- listing or removing user-authored skills
- documenting the SKILL.md frontmatter / references file convention

Do **not** use this skill to modify the built-in `agentgenApprovedMarketSkills` (cf-tunnel, cicy-agent, etc.) — those are managed by the `cicy-skills` Go binary and live inside the cicy-code repo.

## Rules

1. Every user skill lives at `~/cicy-ai/skills/<name>/` with a top-level `SKILL.md` plus a `references/` subdirectory.
2. `SKILL.md` MUST start with a YAML frontmatter block containing exactly `name:` and `description:` — no other fields are read by the agents today.
3. The skill's `name:` field MUST equal its directory name (e.g. `~/cicy-ai/skills/my-skill/SKILL.md` → `name: my-skill`).
4. `description:` is the single line the agent uses to decide whether to invoke this skill — write it as an imperative sentence, mention the trigger condition, keep it under 200 characters.
5. The skill name MUST NOT collide with any built-in skill (run `skill-author list --builtin` to check) and MUST NOT be `cicy-skills` or `skill-author` (reserved).
6. After editing any file under `~/cicy-ai/skills/<name>/`, run `skill-author install <name>` so the three agent profiles pick up the change. Skipping this step leaves the agents reading stale content.
7. To delete a user skill, use `skill-author remove <name>` — it removes both the source dir and all three installed copies. Do NOT `rm -rf` the profile copies directly; they will be re-installed on the next `install` call and the source will be the source of truth.

## For Agents: Removing a User Skill

When the user asks an agent to "delete skill X" / "remove the X skill" / "uninstall X" and `X` is a user-authored skill listed by `skill-author list`, the agent SHOULD run:

```sh
skill-author remove <name> --json
```

`--json` implies `--yes`, so the command never prompts. The output is a single-line JSON object the agent can parse:

```json
{"ok":true,"name":"<name>","removed":["/path/1","/path/2",...]}
```

Exit codes:

- `0` — removed successfully (the `removed` array lists every path actually deleted; may be empty if the skill was already gone)
- `2` — skill source not found (agent should report this and stop)
- `3` — refused: name is a built-in or reserved (e.g. `cf-tunnel`, `cicy-skills`, `skill-author` itself) — agent must NOT retry with `rm -rf`
- `1` — bad usage

Do NOT shell out to `rm -rf` directly — `skill-author remove` cleans up all three profile copies AND the source dir atomically and refuses dangerous targets.

## Help

Read [help.md](./references/help.md) first for the file layout, frontmatter format, and a complete worked example.
Read [tools.md](./references/tools.md) for the CLI command reference.
