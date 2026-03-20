# multi_tenant.monitor.py — SaaS multi-tenant proxy auth + per-user tracking
#
# Proxy auth format: https://{token}:x@audit.cicy-ai.com:8003
# Token → user_id lookup via Redis hash "audit:tokens"
# Per-user rate limiting via Redis sorted set
#
# Redis keys:
#   audit:tokens           — HASH  {token: user_json}  (user_id, plan, quota)
#   audit:user:{uid}:log   — LIST  per-user traffic log
#   audit:user:{uid}:daily — HASH  {YYYY-MM-DD: call_count}
#   audit:usage            — LIST  all-user aggregated usage (for admin)
#   audit:live             — PUBSUB  real-time usage stream

import json
import os
import time
import redis
from mitmproxy import http, ctx

REDIS_PORT = int(os.environ.get("REDIS_PORT", 6379))
r = redis.Redis(host="127.0.0.1", port=REDIS_PORT, decode_responses=True)

MAX_LOG_PER_USER = 5000
MAX_LOG_GLOBAL = 20000

PLAN_LIMITS = {
    "free": 1000,
    "pro": 50000,
    "team": -1,
    "enterprise": -1,
}

_token_cache = {}
_cache_ttl = 30


def _lookup_user(token: str) -> dict | None:
    """Resolve proxy auth token to user info. Cached for 30s."""
    now = time.time()
    cached = _token_cache.get(token)
    if cached and now - cached["_ts"] < _cache_ttl:
        return cached

    raw = r.hget("audit:tokens", token)
    if not raw:
        return None
    try:
        user = json.loads(raw)
        user["_ts"] = now
        _token_cache[token] = user
        return user
    except Exception:
        return None


def _check_quota(user: dict) -> bool:
    """Check if user is within their monthly call quota."""
    plan = user.get("plan", "free")
    limit = PLAN_LIMITS.get(plan, 1000)
    if limit == -1:
        return True

    uid = user.get("user_id", "unknown")
    today = time.strftime("%Y-%m-%d")
    month_key = today[:7]  # YYYY-MM
    count = r.hget(f"audit:user:{uid}:monthly", month_key)
    count = int(count) if count else 0
    return count < limit


def _inc_usage(user: dict):
    """Increment usage counters."""
    uid = user.get("user_id", "unknown")
    today = time.strftime("%Y-%m-%d")
    month_key = today[:7]

    pipe = r.pipeline()
    pipe.hincrby(f"audit:user:{uid}:daily", today, 1)
    pipe.hincrby(f"audit:user:{uid}:monthly", month_key, 1)
    pipe.expire(f"audit:user:{uid}:daily", 90 * 86400)
    pipe.expire(f"audit:user:{uid}:monthly", 400 * 86400)
    pipe.execute()


class MultiTenant:
    def request(self, flow: http.HTTPFlow):
        auth = flow.metadata.get("proxyauth")
        if not auth:
            return

        token = auth[0]

        # Legacy pane-based auth (e.g., "w-10001") — pass through
        if token.startswith("w-"):
            flow.metadata["audit_user"] = {
                "user_id": token, "plan": "internal", "source": "pane"
            }
            return

        user = _lookup_user(token)
        if not user:
            ctx.log.warn(f"[tenant] invalid token: {token[:8]}...")
            flow.response = http.Response.make(
                407,
                json.dumps({"error": "invalid audit token", "hint": "Register at audit.cicy-ai.com"}),
                {"Content-Type": "application/json"},
            )
            return

        if not _check_quota(user):
            uid = user.get("user_id", "unknown")
            plan = user.get("plan", "free")
            ctx.log.warn(f"[tenant] quota exceeded: {uid} ({plan})")
            flow.response = http.Response.make(
                429,
                json.dumps({
                    "error": "audit quota exceeded",
                    "plan": plan,
                    "upgrade": "https://audit.cicy-ai.com/pricing",
                }),
                {"Content-Type": "application/json"},
            )
            return

        flow.metadata["audit_user"] = user

    def response(self, flow: http.HTTPFlow):
        user = flow.metadata.get("audit_user")
        if not user:
            return

        uid = user.get("user_id", "unknown")
        source = user.get("source", "saas")

        ts = int(time.time())
        req_raw = flow.request.content or b""
        res_raw = flow.response.content or b""

        entry = {
            "user_id": uid,
            "source": source,
            "method": flow.request.method,
            "url": flow.request.pretty_url,
            "host": flow.request.host,
            "status": flow.response.status_code,
            "req_kb": round(len(req_raw) / 1024, 1),
            "res_kb": round(len(res_raw) / 1024, 1),
            "ts": ts,
        }

        # Token usage from universal_parser (if available)
        usage = flow.metadata.get("ai_usage")
        if usage:
            entry["ai_usage"] = usage

        entry_json = json.dumps(entry)

        pipe = r.pipeline()
        # Per-user log
        pipe.lpush(f"audit:user:{uid}:log", entry_json)
        pipe.ltrim(f"audit:user:{uid}:log", 0, MAX_LOG_PER_USER - 1)
        # Global admin log
        pipe.lpush("audit:usage", entry_json)
        pipe.ltrim("audit:usage", 0, MAX_LOG_GLOBAL - 1)
        # Live stream
        pipe.publish("audit:live", entry_json)
        pipe.execute()

        _inc_usage(user)


addons = [MultiTenant()]
