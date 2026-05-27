# Intent → patch walkthroughs

Each example shows the user's request, what you should print as a
diff, and the resulting `cicy-policy` command.

---

### "It's too noisy — bearer-token alerts are flooding the channel"

Read first:

```sh
$ cicy-policy summary
enabled: true   fail_mode: open
rules_override: (none)
custom_rules:   (none)
allow_list:     paths=[] hashes=[] agents=[]
preventive:     disabled
```

Propose:

```diff
 rules_override:
+  - id: secret.bearer_token
+    severity: low
+    default_action: log
```

Apply:

```sh
cicy-policy patch '{"rules_override":[{"id":"secret.bearer_token","severity":"low","default_action":"log"}]}'
```

Verify:

```sh
cicy-policy summary | grep bearer
cicy-policy recent --rule secret.bearer_token --limit 3
```

---

### "Make sure the billing agent never leaks SSNs or bank cards"

The relevant builtin is `pii.bank_card`. SSN is not yet a builtin, so
add a custom rule. Severity high, redact inline (requires preventive).

Propose:

```diff
 preventive:
+  enabled: true
 custom_rules:
+  - id: custom.pii_us_ssn
+    label: US SSN
+    category: pii
+    severity: high
+    scan_directions: [outbound]
+    inline: true
+    default_action: redact
+    match: { type: regex, pattern: "\\b\\d{3}-\\d{2}-\\d{4}\\b" }
 rules_override:
+  - id: pii.bank_card
+    severity: high
+    default_action: redact
```

⚠ Flag for confirmation: `preventive.enabled=true` is a runtime
behaviour change for **all** agents, not just billing. Confirm before
applying. If the user only wants billing covered, switch to a
per-agent policy via `/api/audit/policy/agents/w-billing`.

---

### "Don't trip on the fixtures agent — it has fake secrets on purpose"

Don't disable rules. Allow-list the agent:

```diff
 allow_list:
   agents:
+    - w-fixtures
+    - w-test-*
```

```sh
cicy-policy patch '{"allow_list":{"agents":["w-fixtures","w-test-*"]}}'
```

---

### "What did we change in the last hour?"

```sh
git -C ~/cicy-ai/audit log --since=1.hour.ago --oneline
```

The autonomy tick auto-commits. Manual `cicy-policy patch` does
**not** auto-commit (intentional — humans drive their own VCS).
If the user wants every manual edit committed too, tell them so and
offer to wrap it.

---

### "Roll back the last autonomy change"

```sh
cicy-code audit autonomy decisions --limit 5
# pick a dec-... that has a git_sha and applied actions
cicy-code audit autonomy revert <dec-id>
```

The revert appends a new decision (trigger=`revert`) with its own
git SHA, so the audit log preserves the full history.

---

### "I want incident emails for high/critical, cooldown 10 min, to secops"

```sh
cicy-policy patch '{
  "incident_response": {
    "enabled": true,
    "trigger_min_severity": "high",
    "cooldown_seconds": 600
  },
  "responsible_persons": {
    "default": ["secops@acme.com"]
  }
}'
```

If `~/cicy-ai/db/email.json` has Resend credentials and
`incident_response.email_from` is set, the pipeline starts dispatching
real emails. Otherwise emails accumulate in `~/cicy-ai/audit/email-out/`
(FileMailer) — point that out.

---

### Sanity checks before patching

Always run these mentally:

1. **Is the change reversible?** Adding a rule override / custom rule
   / allow-list entry is reversible. Removing one is reversible by
   re-adding. Enabling `preventive.block` mid-traffic can drop live
   requests — confirm.
2. **Does the user have one intent or two?** Don't bundle "quiet
   bearer-token" + "tighten bank-card" into one patch unless the user
   asked for both — they're independently revertable when split.
3. **Did you verify with `cicy-policy show` after the write?** The
   policy_hash should change. fsnotify-reload is async; allow ~1s.
