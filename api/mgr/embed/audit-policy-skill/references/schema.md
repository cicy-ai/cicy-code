# policy.json schema

Location: `~/cicy-ai/audit/policy.json`. When absent, `DefaultPolicy()`
runs — `enabled=true`, `fail_mode=open`, no overrides, empty allow-list.

## Top-level

```json
{
  "version": 1,
  "enabled": true,
  "fail_mode": "open",
  "rules_override": [ ... ],
  "custom_rules":   [ ... ],
  "allow_list":     { "paths":[], "content_hashes":[], "agents":[] },
  "notify":         { ... },
  "preventive":     { "enabled": false, "fail_mode": "open" },
  "responsible_persons": { ... },
  "incident_response":   { "enabled": false, ... }
}
```

`fail_mode`:
- `open` — on internal pipeline error, log + let traffic through.
- `closed` — on internal pipeline error, drop the request. **Only use
  when the human explicitly understands the availability cost.**

## rules_override[]

Tweak a builtin rule without rebuilding the binary. Match on `id`.

```json
{ "id": "secret.bearer_token", "severity": "low", "default_action": "log", "disabled": false }
```

Fields:
- `id` (required) — see [builtin-rules.md](./builtin-rules.md)
- `disabled` — `true` removes the rule entirely. Strong signal you're
  hiding a real signal; prefer allow-list.
- `severity` — `low` | `medium` | `high` | `critical`. Shifts what
  notifications/incident dispatch consider this finding.
- `default_action` — `log` | `notify` | `redact` | `block` | `none`.
  See [actions.md](./actions.md).

## custom_rules[]

Enterprise rules layered on top of builtins. `id` **must** start with
`custom.` so it can never collide with a future builtin.

```json
{
  "id":              "custom.acme_internal_jira_ref",
  "label":           "Internal Jira ticket reference",
  "category":        "internal",
  "severity":        "low",
  "scan_directions": ["outbound"],
  "inline":          false,
  "default_action":  "redact",
  "match": { "type": "regex", "pattern": "ACME-\\d{4,6}", "flags": "" }
}
```

- `scan_directions` — `outbound` (request), `inbound` (response), or
  both. Most secrets are outbound; PII can be either.
- `inline` — if `true`, evaluated in the synchronous preventive check
  (only effective when `preventive.enabled=true`). Keep `false` for
  noisy heuristics — async post-event logging is enough.
- `match.type` — `regex` (RE2) or `dict_file` (UTF-8, one term per
  line, `#` comments).
- `match.flags` — for regex, e.g. `i` for case-insensitive, `m` for
  multiline. Don't use `s` unless you know `.` should cross newlines.

## allow_list

Suppress *findings* (not events) when the context matches.

```json
{
  "paths":          ["s3://staging-fixtures/*"],
  "content_hashes": ["sha256:abc..."],
  "agents":         ["w-fixtures", "w-test-*"]
}
```

- `paths` matches `Subject.PayloadRef`.
- `content_hashes` matches the SHA256 of the redacted payload (use
  this for known-test-data snapshots).
- `agents` matches `Identity.AgentID` — supports single trailing `*`
  wildcard.

## notify

Noise governance (P2-T5). Defaults are conservative:

```json
{
  "max_per_hour_per_agent_rule": 50,
  "cooldown_minutes":            1440,
  "channels": [ { "kind": "log", "min_severity": "medium" } ]
}
```

## preventive

Inline (pre-LLM) blocking. **Default off.** When on:

```json
{ "enabled": true, "fail_mode": "open" }
```

Rules with `inline: true` (custom) or builtins with
`default_action: block`/`redact` run synchronously in
`/api/audit/ingest`. Block → 451 response; redact → 200 with
`payload_encoding=base64` and the *modified* payload.

## responsible_persons

Maps event identity to email recipient lists. All matched lists are
union-ed and deduplicated.

```json
{
  "default":     ["secops@acme.com"],
  "by_severity": { "critical": ["oncall@acme.com"] },
  "by_agent":    { "w-billing*": ["billing-lead@acme.com"] },
  "by_user":     { "alice@acme.com": ["alice@acme.com"] },
  "by_rule":     { "pii.bank_card": ["compliance@acme.com"] }
}
```

`by_agent` keys support a **single trailing `*`** wildcard.

## incident_response

Email dispatch for high/critical findings. Default off.

```json
{
  "enabled":              true,
  "trigger_min_severity": "high",
  "cooldown_seconds":     1800,
  "output_dir":           "~/cicy-ai/audit/email-out",
  "email_template":       "default",
  "languages":            ["zh-CN", "en"],
  "email_from":           "noreply@acme.com"
}
```

When `email_from` is set AND `~/cicy-ai/db/email.json` has Resend
credentials, the pipeline switches from FileMailer to ResendMailer.
