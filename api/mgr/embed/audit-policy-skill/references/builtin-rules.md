# Builtin rule IDs

These ship in the binary. To change behaviour, add an entry to
`rules_override[]` matching on `id`. Do **not** invent new IDs here —
those belong in `custom_rules[]` and **must** be prefixed `custom.`.

| ID                      | Category | Default severity | Default action | Inline | Directions                  | What it catches                                |
| ----------------------- | -------- | ---------------- | -------------- | ------ | --------------------------- | ---------------------------------------------- |
| `secret.private_key`    | secret   | high             | **block**      | yes    | outbound                    | `-----BEGIN ... PRIVATE KEY-----` blocks       |
| `secret.aws_akid`       | secret   | high             | redact         | yes    | outbound                    | `AKIA…` / `ASIA…` access key IDs               |
| `secret.aws_secret`     | secret   | high             | redact         | yes    | outbound                    | Context-aware AWS secret key                    |
| `secret.jwt`            | secret   | medium           | log            | no     | outbound                    | `eyJ…` triple-segment JWT                      |
| `secret.bearer_token`   | secret   | medium           | log            | no     | outbound                    | `Authorization: Bearer <≥20 chars>`            |
| `secret.high_entropy`   | secret   | medium           | log            | no     | outbound                    | High-entropy token with surrounding keyword    |
| `pii.id_card_cn`        | pii      | medium           | log            | no     | outbound + inbound          | Chinese mainland ID card numbers                |
| `pii.bank_card`         | pii      | medium           | log            | no     | outbound + inbound          | Bank card (Luhn-validated)                      |
| `pii.phone_cn`          | pii      | low              | log            | no     | outbound + inbound          | Chinese mainland mobile numbers                 |
| `network.private_ip`    | network  | low              | log            | no     | outbound                    | RFC1918 private IP addresses                    |

## Severity scale

`low` → `medium` → `high` → `critical`

- `notify.channels[].min_severity` filters which findings reach which channel.
- `incident_response.trigger_min_severity` gates email dispatch.

## Action ladder (least → most intrusive)

`none` < `log` < `notify` < `redact` < `block`

- `none` — match but suppress; rarely useful; prefer allow-list.
- `log` — record finding only; no agent-visible change.
- `notify` — record + push through notify channels (in/out of policy).
- `redact` — runs only when `preventive.enabled=true` AND
  (rule is inline OR is overridden inline). Replaces matched bytes
  with `<redacted:<rule_id>>` markers in the forwarded payload.
- `block` — same conditions as redact. Returns HTTP 451 to the
  ingest caller (mitmproxy script must drop the upstream request).

## Common override patterns

Quiet a noisy rule without losing the signal:
```json
{ "id": "secret.bearer_token", "severity": "low", "default_action": "log" }
```

Promote a soft warning to a hard stop (requires `preventive.enabled=true`):
```json
{ "id": "secret.aws_akid", "default_action": "block" }
```

Disable on the fixtures agent only — **prefer** allow-list to disabling
the rule globally:
```json
// rules_override[]: don't touch
// allow_list.agents: ["w-fixtures"]
```
