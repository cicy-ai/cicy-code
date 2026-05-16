# Skill Author Tools

The `skill-author` CLI is on `PATH` after the skill is installed.

## `skill-author new <name>`

Scaffold a new user skill at `~/cicy-ai/skills/<name>/` from the templates.

**Usage:**

```sh
skill-author new <name> [--description "..."]
```

**Flags:**

- `--description "..."` — pre-fill the SKILL.md frontmatter `description:` field

**Behavior:**

- Validates `<name>` (lowercase, kebab-case, no collision with built-ins or `cicy-skills`)
- Creates `~/cicy-ai/skills/<name>/SKILL.md`, `references/help.md`, `references/tools.md` from `templates/`
- Replaces `__NAME__` placeholders with `<name>`
- Errors if the directory already exists (use `$EDITOR` to modify in place, then `install`)

**Example:**

```sh
skill-author new my-deployer --description "Deploy this app to staging via SSH."
```

## `skill-author install <name>`

Copy `~/cicy-ai/skills/<name>/` into the three agent profile directories.

**Usage:**

```sh
skill-author install <name>
skill-author install all          # install every user skill under ~/cicy-ai/skills/
```

**Behavior:**

- Resolves `~/cicy-ai/skills/<name>/`
- Removes any existing `~/.claude/skills/<name>/`, `~/.codex/skills/<name>/`, `~/.opencode/skills/<name>/`
- Recursively copies the source into each of the three profile dirs
- Idempotent — safe to re-run after edits

**Example:**

```sh
skill-author install my-deployer
```

## `skill-author list`

List user skills under `~/cicy-ai/skills/`.

**Usage:**

```sh
skill-author list                 # only user skills
skill-author list --builtin       # also print the reserved built-in names
skill-author list --installed     # only those that exist in all three profiles
```

**Output:**

```
NAME           STATUS         DESCRIPTION
my-deployer    installed      Deploy this app to staging via SSH.
half-baked     source-only    A skill that hasn't been installed yet.
```

`source-only` means the source dir exists but at least one profile is missing the copy. Run `skill-author install <name>` to fix.

## `skill-author remove <name>`

Delete a user skill — both the source and all profile copies.

**Usage:**

```sh
skill-author remove <name> [--yes] [--json]
```

**Flags:**

- `--yes` — skip the confirmation prompt
- `--json` — emit a single-line JSON result and skip the prompt (implies `--yes`); intended for agents and scripts

**Behavior:**

- Asks for confirmation unless `--yes` or `--json` is passed
- Removes `~/cicy-ai/skills/<name>/`
- Removes `~/.claude/skills/<name>/`, `~/.codex/skills/<name>/`, `~/.opencode/skills/<name>/`
- Refuses to remove built-in skills or `skill-author` itself (use `cicy-skills` for built-ins)

**Example (interactive):**

```sh
skill-author remove my-deployer --yes
```

**Agent / script usage:**

```sh
skill-author remove my-deployer --json
# → {"ok":true,"name":"my-deployer","removed":["/home/cicy/cicy-ai/skills/my-deployer", ...]}
```

Parse the JSON to confirm which paths were actually deleted; an empty `removed` array means the skill was already gone. Exit codes: 0=ok, 2=not found, 3=reserved/builtin, 1=bad usage.

## `skill-author show <name>`

Print the SKILL.md frontmatter + body of a user skill (handy for quick inspection).

**Usage:**

```sh
skill-author show <name>
```

## `skill-author validate <name>`

Lint a skill's directory layout and frontmatter without installing it.

**Usage:**

```sh
skill-author validate <name>
```

**Checks:**

- `SKILL.md` exists and has a YAML frontmatter block
- frontmatter `name:` matches the directory name
- frontmatter has a non-empty `description:`
- `references/help.md` and `references/tools.md` exist and are non-empty

Exit code 0 = valid, non-zero = problems printed to stderr.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | usage error (bad flags, missing arg) |
| 2 | skill not found / source missing |
| 3 | name collides with built-in / reserved |
| 4 | validation failure |
