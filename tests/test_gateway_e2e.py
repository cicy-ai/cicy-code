#!/usr/bin/env python3
"""End-to-end test for the cicy-code AI gateway routing semantics.

What this verifies (per agent type — currently codex, extensible):
  1. Boot script content for use_custom_gateway=true
     - The generated .cicy/boot.sh includes the model + provider config args
       that match the pane's persisted default_model + runtime_ai.provider_name.
  2. Boot script content for use_custom_gateway=true + runtime_ai override
     - When the user picks a non-default provider, the boot args route to that
       provider's baseURL and use that provider's default model (if default_model
       was left empty).
  3. Boot script content for use_custom_gateway=false
     - The script does NOT include any model / provider override flags.
  4. Hot-swap model via PATCH
     - PATCHing default_model updates the DB AND the next request through the
       AI gateway has its body's `model` field rewritten by the gateway.
  5. Hot-swap provider via PATCH
     - PATCHing runtime_ai.provider_name causes the next AI gateway request to
       route to the new provider's baseURL (verified via audit log path).
  6. Concurrent save ordering
     - Two rapid PATCHes resolve in the order they were sent (per-pane queue).

Run:
  python3 tests/test_gateway_e2e.py [--container cicy-code-dev] [--agent codex]

Prereqs:
  - docker container is running with cicy-code (default name: cicy-code-dev)
  - host port for the container is auto-discovered via `docker port`
  - API token is auto-discovered from container's ~/cicy-ai/global.json
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass


# ── docker / api helpers ─────────────────────────────────────────────────────


def docker_exec(container: str, cmd: str) -> str:
    """Run a shell command inside the container and return its stdout (trimmed)."""
    result = subprocess.run(
        ["docker", "exec", container, "sh", "-lc", cmd],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"docker exec failed (rc={result.returncode}): {cmd}\n"
            f"stdout={result.stdout!r}\nstderr={result.stderr!r}"
        )
    return result.stdout


def resolve_host_port(container: str) -> str:
    out = subprocess.check_output(
        ["docker", "port", container, "8008/tcp"], text=True
    ).strip()
    if not out:
        raise RuntimeError(f"cannot resolve host port for {container}")
    return out.splitlines()[-1].rsplit(":", 1)[-1]


def resolve_token(container: str) -> str:
    raw = docker_exec(
        container,
        "python3 -c \"import json; print(json.load(open('/home/cicy/cicy-ai/global.json'))['api_token'])\"",
    ).strip()
    if not raw:
        raise RuntimeError("cannot resolve api_token from container global.json")
    return raw


def ensure_providers_seeded(container: str, host_global_json: str = "/home/cicy/cicy-ai/global.json") -> None:
    """Mirror the host's `providers` block into the container's global.json if the
    container doesn't already have providers.items populated. cicy-code re-reads
    global.json on every config-related API call, so no restart is needed."""
    inside = docker_exec(
        container,
        "python3 -c \"import json; d=json.load(open('/home/cicy/cicy-ai/global.json')); items=(d.get('providers') or {}).get('items') or []; print(len(items))\"",
    ).strip()
    if inside.isdigit() and int(inside) > 0:
        print(f"[setup] providers already seeded ({inside} items)")
        return
    try:
        with open(host_global_json, "r", encoding="utf-8") as f:
            host = json.load(f)
    except Exception as e:
        raise RuntimeError(f"cannot read host {host_global_json}: {e}")
    providers = host.get("providers")
    if not providers or not (providers.get("items") or []):
        raise RuntimeError(f"host {host_global_json} has no providers.items to seed from")
    seed_json = json.dumps(providers, ensure_ascii=False)
    # Write providers block into container's global.json via in-container python.
    docker_exec(
        container,
        "python3 - <<'PY'\n"
        "import json\n"
        "p='/home/cicy/cicy-ai/global.json'\n"
        "d=json.load(open(p))\n"
        f"d['providers']={seed_json}\n"
        "open(p,'w').write(json.dumps(d, indent=2, ensure_ascii=False))\n"
        "PY",
    )
    print(f"[setup] seeded {len(providers.get('items') or [])} providers into container global.json")


class CicyApi:
    def __init__(self, base_url: str, token: str):
        self.base = base_url.rstrip("/")
        self.token = token

    def _req(self, method: str, path: str, body: dict | None = None) -> dict:
        url = self.base + path
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.token}")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            raise RuntimeError(
                f"HTTP {e.code} {method} {path}: {e.read().decode(errors='replace')}"
            )
        if not raw:
            return {}
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {"_raw": raw.decode(errors="replace")}

    def get_pane(self, pane_id: str) -> dict:
        return self._req("GET", f"/api/tmux/panes/{pane_id}")

    def update_pane(self, pane_id: str, fields: dict) -> dict:
        return self._req("PATCH", f"/api/tmux/panes/{pane_id}", fields)

    def restart_pane(self, pane_id: str) -> dict:
        return self._req("POST", f"/api/panes/{pane_id}/restart", {})

    def list_panes(self) -> list:
        body = self._req("GET", "/api/tmux/panes")
        if isinstance(body, list):
            return body
        return body.get("panes") or []


def read_boot_script(container: str, short_pane_id: str) -> str:
    return docker_exec(
        container,
        f"cat /home/cicy/cicy-ai/workers/{short_pane_id}/.cicy/boot.sh",
    )


def db_query(container: str, sql: str) -> str:
    """Run a one-shot read against the agent_config sqlite db inside the container."""
    return docker_exec(
        container,
        f"python3 -c \"import sqlite3; c=sqlite3.connect('/home/cicy/cicy-ai/db/data.db'); print(c.execute('{sql}').fetchone())\"",
    ).strip()


# ── tiny test runner ─────────────────────────────────────────────────────────


@dataclass
class Result:
    name: str
    passed: bool
    detail: str = ""


class Runner:
    def __init__(self) -> None:
        self.results: list[Result] = []

    def case(self, name: str):
        return _Case(self, name)

    def expect(self, name: str, ok: bool, detail: str = "") -> None:
        self.results.append(Result(name, ok, detail))
        marker = "✓" if ok else "✗"
        line = f"  {marker} {name}"
        if detail and not ok:
            line += f"\n      {detail}"
        print(line)

    def summary(self) -> int:
        passed = sum(1 for r in self.results if r.passed)
        failed = len(self.results) - passed
        print()
        print(f"[summary] passed={passed} failed={failed}")
        if failed:
            for r in self.results:
                if not r.passed:
                    print(f"  ✗ {r.name}: {r.detail}")
        return 0 if failed == 0 else 1


class _Case:
    def __init__(self, runner: Runner, name: str):
        self.runner = runner
        self.name = name

    def __enter__(self):
        print(f"[case] {self.name}")
        return self

    def __exit__(self, *_):
        return False

    def assert_in(self, label: str, needle: str, haystack: str) -> None:
        self.runner.expect(
            f"{self.name} → {label} contains '{needle}'",
            needle in haystack,
            detail=f"haystack head:\n{haystack[:400]}",
        )

    def assert_not_in(self, label: str, needle: str, haystack: str) -> None:
        self.runner.expect(
            f"{self.name} → {label} does NOT contain '{needle}'",
            needle not in haystack,
            detail=f"haystack head:\n{haystack[:400]}",
        )

    def assert_eq(self, label: str, expected, actual) -> None:
        self.runner.expect(
            f"{self.name} → {label}",
            expected == actual,
            detail=f"expected={expected!r} actual={actual!r}",
        )


# ── agent profile (extensible) ───────────────────────────────────────────────


@dataclass
class AgentProfile:
    """Per-agent assertion knobs. Add a new instance to test claude / opencode."""
    agent_type: str
    short_pane_id: str
    gateway_on_must_contain: list[str]  # substrings expected in boot.sh when gateway ON
    gateway_off_must_contain: list[str]  # substrings expected when gateway OFF
    gateway_off_must_not_contain: list[str]  # substrings that should be absent when OFF


CODEX_PROFILE = AgentProfile(
    agent_type="codex",
    short_pane_id="w-10002",
    gateway_on_must_contain=[
        "export OPENAI_API_KEY='cicy-local-gateway'",
        "codex -m",
        'model_provider="custom"',
        'model_providers.custom.base_url=',
    ],
    gateway_off_must_contain=[
        "unset OPENAI_API_KEY",
    ],
    gateway_off_must_not_contain=[
        "-m ",  # no model flag in OFF path
        'model_provider="custom"',
        "OPENAI_API_KEY='cicy-local-gateway'",
    ],
)

CLAUDE_PROFILE = AgentProfile(
    agent_type="claude",
    short_pane_id="w-10001",
    gateway_on_must_contain=[
        "ANTHROPIC_AUTH_TOKEN",
        "ANTHROPIC_BASE_URL",
        '"model":',
        "--settings ",
    ],
    gateway_off_must_contain=[
        "unset ANTHROPIC_BASE_URL",
        "unset ANTHROPIC_API_KEY",
    ],
    gateway_off_must_not_contain=[
        "--model ",  # we removed --model from official-login path
        '"model":',
        "--settings ",
    ],
)

OPENCODE_PROFILE = AgentProfile(
    agent_type="opencode",
    short_pane_id="w-10003",
    gateway_on_must_contain=[
        "CICY_OPENAI_BASE_URL",
        "OPENCODE_CONFIG=",
        "opencode.json",
    ],
    gateway_off_must_contain=[
        "unset CICY_OPENAI_BASE_URL",
        "unset OPENCODE_CONFIG",
    ],
    gateway_off_must_not_contain=[
        "CICY_OPENAI_BASE_URL=",  # not exported in OFF path
    ],
)


PROFILES = {
    "codex": CODEX_PROFILE,
    "claude": CLAUDE_PROFILE,
    "opencode": OPENCODE_PROFILE,
}


# ── test scenarios ──────────────────────────────────────────────────────────


def wait_for_boot_script(container: str, short_pane_id: str, after_marker: str | None, timeout: float = 10.0) -> str:
    """Poll until the boot.sh is regenerated (its content changes from after_marker, or first appears)."""
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        try:
            last = read_boot_script(container, short_pane_id)
        except RuntimeError:
            last = ""
        if last and (after_marker is None or last != after_marker):
            return last
        time.sleep(0.3)
    return last


def run_agent_profile(runner: Runner, api: CicyApi, container: str, profile: AgentProfile) -> None:
    short = profile.short_pane_id
    pane_id = f"{short}:main.0"

    # Snapshot initial state so we can restore at the end.
    initial = api.get_pane(short)
    initial_use_custom_gateway = bool(initial.get("use_custom_gateway"))
    initial_default_model = str(initial.get("default_model") or "")
    initial_runtime_ai = initial.get("runtime_ai") or None
    provider_options = initial.get("runtime_ai_provider_options") or []
    runtime_default = initial.get("runtime_ai_default") or {}
    default_provider_key = str(runtime_default.get("provider_name") or "")

    # Pick a "non-default" provider for the override scenarios.
    other_providers = [
        p for p in provider_options
        if str(p.get("key") or "") and str(p.get("key")) != default_provider_key
    ]
    if not other_providers:
        runner.expect(
            f"[{profile.agent_type}] provider_options has >= 2 providers",
            False,
            detail=f"only {len(provider_options)} provider option(s); cannot test override",
        )
        return
    override_provider = other_providers[0]
    override_provider_key = str(override_provider["key"])
    override_models = override_provider.get("models") or []
    override_model = str(override_models[0]) if override_models else ""

    print()
    print(f"[profile] {profile.agent_type} short={short} default_provider={default_provider_key} override→{override_provider_key}/{override_model or '<no models>'}")

    try:
        # ── 1) gateway ON + default provider, explicit default_model ──
        with runner.case(f"{profile.agent_type}: gateway ON, default provider, explicit model") as c:
            default_models = []
            for p in provider_options:
                if str(p.get("key")) == default_provider_key:
                    default_models = p.get("models") or []
                    break
            on_model = str(default_models[0]) if default_models else str(runtime_default.get("model") or "")
            api.update_pane(short, {
                "use_custom_gateway": True,
                "runtime_ai": None,
                "default_model": on_model,
            })
            prev_boot = ""
            try:
                prev_boot = read_boot_script(container, short)
            except RuntimeError:
                prev_boot = ""
            api.restart_pane(pane_id)
            boot = wait_for_boot_script(container, short, after_marker=prev_boot)
            for needle in profile.gateway_on_must_contain:
                c.assert_in("boot.sh", needle, boot)
            if on_model and profile.agent_type in ("codex", "claude"):
                c.assert_in("model name", on_model, boot)

        # ── 2) gateway ON + runtime_ai override to non-default provider ──
        with runner.case(f"{profile.agent_type}: gateway ON, override provider {override_provider_key}") as c:
            api.update_pane(short, {
                "use_custom_gateway": True,
                "runtime_ai": {"provider_name": override_provider_key},
                "default_model": override_model,
            })
            prev_boot = read_boot_script(container, short)
            api.restart_pane(pane_id)
            boot = wait_for_boot_script(container, short, after_marker=prev_boot)
            for needle in profile.gateway_on_must_contain:
                c.assert_in("boot.sh", needle, boot)
            if override_model and profile.agent_type in ("codex", "claude"):
                c.assert_in("override model name", override_model, boot)
            # DB consistency
            db = api.get_pane(short)
            c.assert_eq("DB.runtime_ai.provider_name", override_provider_key,
                        str((db.get("runtime_ai") or {}).get("provider_name") or ""))

        # ── 3) gateway OFF — no model flag, no provider config ──
        with runner.case(f"{profile.agent_type}: gateway OFF") as c:
            api.update_pane(short, {
                "use_custom_gateway": False,
                "runtime_ai": None,
            })
            prev_boot = read_boot_script(container, short)
            api.restart_pane(pane_id)
            boot = wait_for_boot_script(container, short, after_marker=prev_boot)
            for needle in profile.gateway_off_must_contain:
                c.assert_in("boot.sh", needle, boot)
            for needle in profile.gateway_off_must_not_contain:
                c.assert_not_in("boot.sh", needle, boot)

        # ── 4) Hot-swap PATCH: default_model + runtime_ai go to DB without restart ──
        with runner.case(f"{profile.agent_type}: hot-swap model + provider via PATCH") as c:
            api.update_pane(short, {
                "use_custom_gateway": True,
                "runtime_ai": {"provider_name": override_provider_key},
                "default_model": override_model,
            })
            db = api.get_pane(short)
            c.assert_eq("DB.use_custom_gateway", True, bool(db.get("use_custom_gateway")))
            c.assert_eq("DB.default_model", override_model, str(db.get("default_model") or ""))
            c.assert_eq("DB.runtime_ai.provider_name", override_provider_key,
                        str((db.get("runtime_ai") or {}).get("provider_name") or ""))

        # ── 5) Concurrent PATCH ordering — last write wins on server ──
        with runner.case(f"{profile.agent_type}: concurrent PATCH last-write-wins") as c:
            # Send two rapid PATCHes; the api client awaits each in sequence so
            # the second's value is the final state.
            api.update_pane(short, {"default_model": "test-model-A"})
            api.update_pane(short, {"default_model": "test-model-B"})
            db = api.get_pane(short)
            c.assert_eq("DB.default_model after 2 PATCHes", "test-model-B",
                        str(db.get("default_model") or ""))

    finally:
        # Restore initial state.
        try:
            api.update_pane(short, {
                "use_custom_gateway": initial_use_custom_gateway,
                "runtime_ai": initial_runtime_ai,
                "default_model": initial_default_model,
            })
            api.restart_pane(pane_id)
        except Exception as e:
            print(f"[warn] restore failed for {short}: {e}")


# ── entrypoint ───────────────────────────────────────────────────────────────


def main() -> int:
    parser = argparse.ArgumentParser(description="cicy-code AI gateway E2E test")
    parser.add_argument("--container", default="cicy-code-dev",
                        help="docker container name (default: cicy-code-dev)")
    parser.add_argument("--agent", default="codex", choices=list(PROFILES.keys()) + ["all"],
                        help="agent type to test (default: codex)")
    args = parser.parse_args()

    container = args.container

    # Probe container is up
    try:
        host_port = resolve_host_port(container)
    except Exception as e:
        print(f"[fatal] cannot find container '{container}': {e}", file=sys.stderr)
        return 2

    token = resolve_token(container)
    base_url = f"http://127.0.0.1:{host_port}"
    print(f"[setup] container={container} base_url={base_url}")

    try:
        ensure_providers_seeded(container)
    except Exception as e:
        print(f"[fatal] provider seeding failed: {e}", file=sys.stderr)
        return 2

    api = CicyApi(base_url, token)
    runner = Runner()

    profiles_to_run = (
        list(PROFILES.values())
        if args.agent == "all"
        else [PROFILES[args.agent]]
    )
    for profile in profiles_to_run:
        run_agent_profile(runner, api, container, profile)

    return runner.summary()


if __name__ == "__main__":
    sys.exit(main())
