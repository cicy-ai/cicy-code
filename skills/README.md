# cicy-code/skills

> NOTE: this directory used to host the v1 monolithic skill bundle
> (`cicy-hosttools`, `cicy-skills` CLI, `cicy-skillsd` daemon, embedded
> SKILL.md generator). All of that has migrated to the standalone
> [cicy-ai/cicy-skills](https://github.com/cicy-ai/cicy-skills) v2 monorepo
> on the `v2` branch and is published per-skill at
> `https://skills.cicy-ai.com`. Use `cicy-code skill install <name>` from
> the cicy-code main binary.
>
> What's left here:
> - `cmd/stt` and `cmd/tts` — voice binaries (still built by ttyd-go's release pipeline)
> - `internal/voice`, `internal/voicecmd` — supporting packages for stt/tts
> - free-standing Bash / Node skill scripts (`cicy-todo`,
>   `cicy-master`, `proxy_ssh`, `us-spot-*`, `hk-spot-dev`, `cicy-code`,
>   `cf/`) — these are skill payloads that didn't migrate to v2 yet.

## Build

```sh
make build-local-binaries        # → dist/{stt,tts}
make test-go                     # run voice tests
```

## Migrating a leftover skill to v2

Take any of the Bash / Node scripts in this directory, package them under
`~/projects/cicy-skills/skills/<name>/` per the v2 manifest schema, and
publish via `node tools/publish.js skills/<name>`. See the v2 monorepo's
`CONTRIBUTING.md` and `GOVERNANCE.md` for the authoring and publish flow.
