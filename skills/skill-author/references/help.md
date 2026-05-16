# Skill Author Help

## File Layout

A user skill is a directory under `~/cicy-ai/skills/`:

```
~/cicy-ai/skills/<name>/
├── SKILL.md                   # required — frontmatter + main entry
└── references/
    ├── help.md                # required — quick start, examples, rules
    └── tools.md               # required — CLI command reference
```

`<name>` must equal the directory name and the `name:` field in the frontmatter.

## SKILL.md Frontmatter

The first lines of `SKILL.md` MUST be a YAML frontmatter block delimited by `---`:

```markdown
---
name: my-skill
description: One-line imperative sentence that tells an agent when to invoke this skill.
---

# My Skill

[main content...]
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Must match the directory name. Lowercase, kebab-case. |
| `description` | yes | One sentence, imperative, mentions trigger condition. < 200 chars. |

Anything else in the frontmatter is ignored by the agents today.

## SKILL.md Body Structure

After the frontmatter, follow this convention so users (and agents) find content predictably:

```markdown
# <Title Case Name>

<one-paragraph summary of what this skill does and when to use it>

## Scope

Use this skill when the task involves:

- <bullet 1>
- <bullet 2>

## Rules

1. <ordered constraint>
2. <ordered constraint>

## Help

Read [help.md](./references/help.md) first for ...
Read [tools.md](./references/tools.md) for the CLI reference.
```

## references/help.md

Detailed how-to content. Include:

- the primary CLI command
- a "Quick Start" section with copy-pasteable commands
- common examples
- error / troubleshooting notes

## references/tools.md

A flat list of every command this skill provides, one section per command:

```markdown
# <Skill Name> Tools

## `<command> <subcommand>`

<one-line summary>

**Usage:**

```sh
<command> <subcommand> [flags]
```

**Flags:**

- `--foo VALUE` — description

**Example:**

```sh
<command> <subcommand> --foo bar
```
```

## Worked Example: `cf-tunnel`

A canonical built-in skill that follows the convention exactly. Read it on disk:

```sh
cat ~/.claude/skills/cf-tunnel/SKILL.md
cat ~/.claude/skills/cf-tunnel/references/help.md
cat ~/.claude/skills/cf-tunnel/references/tools.md
```

Note how:

- the frontmatter is exactly `name:` + `description:`
- the body has Scope / Rules / Help sections
- `help.md` opens with the primary command name + a Quick Start
- `tools.md` lists every subcommand with its flags

## Workflow: Create → Edit → Install

```sh
# 1. Scaffold a fresh skill from the templates
skill-author new my-skill

# 2. Edit SKILL.md / references/* under ~/cicy-ai/skills/my-skill/
$EDITOR ~/cicy-ai/skills/my-skill/SKILL.md

# 3. Install into all three agent profiles (.claude, .codex, .opencode)
skill-author install my-skill

# 4. Verify it's picked up
skill-author list

# 5. The skill now appears in the cicy-code marketplace under "user" category.
```

## Workflow: Update an Existing Skill

```sh
$EDITOR ~/cicy-ai/skills/my-skill/references/help.md
skill-author install my-skill        # re-sync into all profiles
```

`install` is idempotent — it overwrites the per-profile copies from the source.

## Workflow: Remove a Skill

```sh
skill-author remove my-skill
```

This deletes:

1. `~/cicy-ai/skills/my-skill/` (the source)
2. `~/.claude/skills/my-skill/`, `~/.codex/skills/my-skill/`, `~/.opencode/skills/my-skill/` (the per-profile copies)

After removal the skill disappears from the marketplace on the next list refresh.

## Naming Rules

- Lowercase ASCII, digits, and `-`
- Must start with a letter
- Cannot collide with a built-in skill name (run `skill-author list --builtin` to see the reserved set)
- Cannot be `cicy-skills` (the directory is reserved for the cicy-skills source)

## Where Things Live

| Path | Purpose |
|------|---------|
| `~/cicy-ai/skills/<name>/` | source of truth — edit here |
| `~/.claude/skills/<name>/` | what Claude reads |
| `~/.codex/skills/<name>/` | what Codex reads |
| `~/.opencode/skills/<name>/` | what OpenCode reads |
| `~/.local/bin/skill-author` | this CLI |
| `~/.local/bin/<your-cmd>` | optional — symlink your skill's binary here if it ships one |

If your skill ships an executable, put it under `~/cicy-ai/skills/<name>/scripts/<cmd>` and symlink to `~/.local/bin/<cmd>` manually (or via your skill's installer). The marketplace does not auto-link binaries for user skills.
