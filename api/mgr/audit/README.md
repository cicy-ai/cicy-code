# audit/ — audit-v2

Pipeline that ingests AI traffic events (from the gateway path AND from cicy-mitm), scans them for findings, applies a policy, and — in audit-v2 — runs an **autonomous policy agent** that writes policy.json without human review.

Full design rationale: [`docs/v1/audit-v2-design.md`](../../../docs/v1/audit-v2-design.md).

## File map

### Inherited from origin/audit (untouched in audit-v2)

| File | Purpose |
|------|---------|
| `audit.go` | Process-global pipeline singleton + `Init` / `Submit` / `Wait` |
| `pipeline.go` | The 6-stage process: identify → timestamp → scan → decide → append → notify |
| `policy.go` | Typed `Policy` + `Load` / `WriteGlobalPolicy` |
| `scanner.go` + `builtin_rules.go` | Regex-based scanner + 10 default rules (PII / secrets) |
| `ruleset.go` | Combines builtins + custom_rules + overrides per agent |
| `query.go` | Read events from per-agent ndjson + global index (`Query(opts)`) |
| `store.go` | NDJSON append + hash-chain integrity |
| `canonical.go` + `identity.go` + `types.go` | Event/Envelope/Finding types + canonical event ID |
| `chain.go` | Hash chain state (per-agent + global) |
| `preventive.go` | Inline scan (block / redact) used by MITM `BreakerHook` |
| `noise.go` / `override.go` | (legacy: noise governance + per-agent policy override) |
| `incident.go` + `mailer*.go` + `ack.go` + `ai_remediation.go` | Email incident response (kept compiled but no human review loop in v2) |
| `verify.go` | `cicy-code audit verify` — offline hash-chain validation |

### NEW in audit-v2 (the autonomy module)

| File | LOC | Purpose |
|------|-----|---------|
| **`autonomy.go`** | 830 | The autonomous policy agent loop. `StartAutonomy(ctx, cfg)` spawns a goroutine that wakes every `Interval`, queries events, calls the LLM, applies returned patches, appends one `AutonomyDecision` to `decisions.ndjson` per tick |
| `autonomy_cli.go` | 165 | `cicy-code audit autonomy {run,decisions,explain,revert,show-config}` |
| `autonomy_test.go` | 314 | 9 unit tests covering config defaults / parse / constraints / merge / persistence / rate-limit / full-path with stub LLM |
| **`policy_git.go`** | 184 | After each applied tick, `git add+commit` in `~/cicy-ai/audit/`. Bootstraps the repo if missing. `GitRevertCommit(sha)` is the rollback primitive |
| `policy_git_test.go` | 93 | bootstrap + no-op-on-unchanged + 3-mutation chain |
| **`decision_explain.go`** | 235 | LLM in "forensic reviewer" role narrates a past decision. Stub fallback when LLM unavailable so the UI button never 500s |
| `decision_explain_test.go` | 134 | stub / not-found / LLM success / LLM-failure-falls-back |
| **`decision_revert.go`** | 82 | Looks up a decision by ID, calls `GitRevertCommit(decision.GitSHA)`, appends a `trigger=revert` decision so the audit timeline stays complete |
| `decision_revert_test.go` | 104 | end-to-end round trip + 2 error paths |

## On-disk layout

```
~/cicy-ai/
  audit/                       — audit-v2 single source of truth
    .git/                      — auto-bootstrapped on first applied tick
    policy.json                — owned by autonomy, mutated by WriteGlobalPolicy
    machine_id
    audit-chain.state          — global hash-chain state
    index/                     — per-day NDJSON
  autonomy/
    autonomy.json              — HUMAN-AUTHORED guardrails (interval, max changes,
                                 forbidden_actions, llm config)
    decisions.ndjson           — append-only, one line per tick (newest at bottom)
  workers/<agent>/.cicy/history/
    mitm/<turn>/current.json   — request snapshot (provider + body + headers)
    mitm/<turn>/reply.json     — response snapshot
    audit.ndjson               — per-agent hash-chained events
```

## Extension points

### Add a new built-in rule

Edit `builtin_rules.go` — adds a `BuiltinRule` to the registry. The autonomy agent will surface it in its LLM prompt automatically (it doesn't hard-code the rule list).

### Add a new policy patch field

`autonomy.go:PolicyPatch` is the typed payload the LLM is told to fill. Extend it + add a merge clause in `mergeAutonomyPatch` and `policy_suggester_apply.go:mergePolicyPatch` (the latter is the manual-mode helper kept around for compatibility, even though v2 routes everything through the autonomy loop).

### Add a new autonomy decision trigger

`runOneTick(ctx, cfg, trigger)` accepts an arbitrary trigger string. Today: `interval` / `manual` / `revert`. Add e.g. `webhook` from your own caller — no schema changes needed.

### Add a new autonomy CLI subcommand

Wire it in `autonomy_cli.go:runAutonomyCmd` switch.

## How autonomy interacts with the rest of the pipeline

```
                         pipeline.Submit(envelope)
                                  │
                          scanner.Scan + decide
                                  │
                       append to per-agent ndjson
                                  │
                              ◀──┘ (autonomy reads via Query)
                              │
                              │
┌─────────────────────────────┴───────────────────────────────────┐
│  autonomy.runOneTick                                            │
│                                                                 │
│  1. recentDecisionsCount(1h) ≤ MaxChangesPerHour ?              │
│  2. Query(events, lookback)                                     │
│  3. summarizePolicyForSuggesterMin + buildAutonomyStats         │
│  4. callAutonomyLLM(prompt)  → JSON patches                     │
│  5. for each patch:                                             │
│       violatesConstraints? skip.                                │
│       applyPatch → mergeAutonomyPatch → WriteGlobalPolicy       │
│  6. dec.GitSHA = GitAutoCommitDecisionReturningSHA              │
│  7. appendDecision(dec) → decisions.ndjson                      │
└─────────────────────────────────────────────────────────────────┘
                                  │
                  WriteGlobalPolicy fsnotify reload (~200ms)
                                  │
                            ◀────┘ pipeline picks up new policy
                            │
                  next event scanned with new ruleset
```

## Testing

```bash
go test ./api/mgr/audit/ -count=1
```

The autonomy_test full-path test (`TestRunOneTick_FullPath_WithStubLLM`) is the highest-confidence anchor: it spins up a real `NewPipeline`, seeds one event, runs a tick against an in-process LLM stub, then verifies (a) the patch landed in `policy.json` on disk, (b) the decision was persisted with policy hash transitions, and (c) the rate limiter behavior with 5 seeded decisions.

For end-to-end smoke (with optional LLM), see [`scripts/audit-v2-demo.sh`](../../../scripts/audit-v2-demo.sh).
