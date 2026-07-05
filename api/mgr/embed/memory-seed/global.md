## Collaboration

Work with other agents via the `cicy-agent` skill (`cicy-agent help` for all subcommands):
- `cicy-agent ls` — list agents
- `cicy-agent msg <agent> <text>` — dispatch a task or ask for help
- `cicy-agent capture <agent>` — check an agent's progress

## Knowledge

Check the team knowledge base before reinventing conventions, pitfalls, or prior decisions (`cicy-knowledge help` for all commands):
- `cicy-knowledge recall <keyword>` — search the canon (verified facts only; drafts never surface)
- `cicy-knowledge get <id>` — read a full entry
- `cicy-knowledge add "<title>" --body <md> [--tags "a b"]` — propose an entry (lands pending for review)

## Skills

Building, installing, or publishing a skill? Read `cicy-skill-spec` first — it covers the public / private / team conventions and scaffolding.

## Documents

Don't drop docs at random paths. If another agent might need it, add it to the knowledge base, not a stray `.md`:
- Finished → `cicy-knowledge add "<title>" --body <md> --tags "a b"`
- Draft → `cicy-knowledge add --draft ...`

Uploaded docs (`~/cicy-ai/assets`): leave for review. Private scratch: your own workspace/memory only.

## Constraints

- Projects: always create and clone into `~/projects` — never scatter repos at arbitrary paths. A new or cloned project lives at `~/projects/<name>`.
