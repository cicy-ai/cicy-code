# Action semantics

There are five legal values for `default_action`:

| Action   | What the audit pipeline does                                                              | Visible to the LLM caller? |
| -------- | ----------------------------------------------------------------------------------------- | -------------------------- |
| `none`   | Match recorded but emit no finding (rarely useful — prefer `allow_list`).                  | No.                        |
| `log`    | Finding written to NDJSON. No notify, no email, no payload modification.                   | No.                        |
| `notify` | Same as `log` + push through `notify.channels[]` (also subject to incident_response gate). | No.                        |
| `redact` | (`preventive.enabled=true` only) — matched bytes replaced with `<redacted:rule_id>` in the forwarded body. | Yes — modified payload.    |
| `block`  | (`preventive.enabled=true` only) — `/api/audit/ingest` returns HTTP 451; mitmproxy script drops the upstream request. | Yes — request fails.       |

## Inline vs async

A rule is "inline" iff both:

1. `preventive.enabled = true` in policy, AND
2. The rule itself has `inline: true` OR an override forces it inline
   (via `default_action: block` / `redact`).

Inline runs synchronously inside the `POST /api/audit/ingest` handler.
Non-inline runs in the async pipeline goroutine — finding gets stored
but the request has already been forwarded.

`block` and `redact` are **only meaningful inline**. Setting them on a
non-inline rule when `preventive.enabled=false` is a logical no-op — the
pipeline will record the finding (treating it like `log`) but won't
modify the traffic.

## Fail mode

`policy.fail_mode` and `preventive.fail_mode` are independent:

- `policy.fail_mode` — what happens when the *audit pipeline itself*
  errors (e.g. disk full, regex panic). `open` lets traffic through;
  `closed` drops it.
- `preventive.fail_mode` — what happens when *preventive scanning*
  errors. Same `open` / `closed` semantics, but the blast radius is
  smaller (only preventive is bypassed, not the whole pipeline).

For 99% of operators, both stay `open`. Flip to `closed` only after
you've added health-checking + alerting on the audit subsystem itself.
