# universal_parser.monitor.py — Universal AI provider traffic parser
#
# Auto-detect AI provider from domain + URL path, extract token usage + cost.
# Stores parsed usage in flow.metadata["ai_usage"] for multi_tenant to pick up.
# Also stores aggregated per-user/per-model stats in Redis.
#
# Supported providers:
#   - OpenAI        (api.openai.com)
#   - Anthropic     (api.anthropic.com)
#   - Google        (generativelanguage.googleapis.com)
#   - DeepSeek      (api.deepseek.com)
#   - Qwen/Alibaba  (dashscope.aliyuncs.com)
#   - Groq          (api.groq.com)
#   - Mistral       (api.mistral.ai)
#   - OpenRouter    (openrouter.ai)
#   - Azure OpenAI  (*.openai.azure.com)
#   - AWS Bedrock   (bedrock-runtime.*.amazonaws.com)

import json
import os
import re
import time
import redis
from mitmproxy import http, ctx

REDIS_PORT = int(os.environ.get("REDIS_PORT", 6379))
r = redis.Redis(host="127.0.0.1", port=REDIS_PORT, decode_responses=True)

# Price per 1M tokens (input, output) in USD — approximate mid-2025 pricing
MODEL_PRICING = {
    # OpenAI
    "gpt-4o":           (2.50,  10.00),
    "gpt-4o-mini":      (0.15,   0.60),
    "gpt-4-turbo":      (10.00, 30.00),
    "gpt-4":            (30.00, 60.00),
    "gpt-3.5-turbo":    (0.50,   1.50),
    "o1":               (15.00, 60.00),
    "o1-mini":          (3.00,  12.00),
    "o3-mini":          (1.10,   4.40),
    # Anthropic
    "claude-sonnet-4-20250514": (3.00, 15.00),
    "claude-3-5-sonnet": (3.00, 15.00),
    "claude-3-5-haiku":  (0.80,  4.00),
    "claude-3-opus":     (15.00, 75.00),
    "claude-3-haiku":    (0.25,  1.25),
    # Google
    "gemini-2.5-pro":   (1.25,  10.00),
    "gemini-2.5-flash": (0.15,   0.60),
    "gemini-2.0-flash": (0.10,   0.40),
    "gemini-1.5-pro":   (1.25,   5.00),
    "gemini-1.5-flash": (0.075,  0.30),
    # DeepSeek
    "deepseek-chat":    (0.14,   0.28),
    "deepseek-coder":   (0.14,   0.28),
    "deepseek-reasoner":(0.55,   2.19),
    # Qwen
    "qwen-turbo":       (0.30,   0.60),
    "qwen-plus":        (0.80,   2.00),
    "qwen-max":         (2.40,   9.60),
    # Groq
    "llama-3.3-70b":    (0.59,   0.79),
    "llama-3.1-8b":     (0.05,   0.08),
    # Mistral
    "mistral-large":    (2.00,   6.00),
    "mistral-small":    (0.20,   0.60),
}

PROVIDER_DOMAINS = {
    "api.openai.com":       "openai",
    "api.anthropic.com":    "anthropic",
    "api.deepseek.com":     "deepseek",
    "api.groq.com":         "groq",
    "api.mistral.ai":       "mistral",
    "openrouter.ai":        "openrouter",
}


def _detect_provider(host: str, path: str) -> str | None:
    if host in PROVIDER_DOMAINS:
        return PROVIDER_DOMAINS[host]
    if host.endswith(".openai.azure.com"):
        return "azure"
    if "generativelanguage.googleapis.com" in host:
        return "google"
    if "dashscope.aliyuncs.com" in host:
        return "qwen"
    if re.match(r"bedrock-runtime\..+\.amazonaws\.com", host):
        return "aws_bedrock"
    return None


def _parse_openai_usage(body: dict) -> dict | None:
    """Parse OpenAI-compatible response (also DeepSeek, Groq, Mistral, OpenRouter)."""
    usage = body.get("usage")
    if not usage:
        return None
    model = body.get("model", "unknown")
    return {
        "model": model,
        "input_tokens": usage.get("prompt_tokens", 0),
        "output_tokens": usage.get("completion_tokens", 0),
        "total_tokens": usage.get("total_tokens", 0),
    }


def _parse_anthropic_usage(body: dict) -> dict | None:
    usage = body.get("usage")
    if not usage:
        return None
    model = body.get("model", "unknown")
    input_t = usage.get("input_tokens", 0)
    output_t = usage.get("output_tokens", 0)
    return {
        "model": model,
        "input_tokens": input_t,
        "output_tokens": output_t,
        "total_tokens": input_t + output_t,
    }


def _parse_google_usage(body: dict) -> dict | None:
    meta = body.get("usageMetadata")
    if not meta:
        return None
    model = body.get("modelVersion", "unknown")
    input_t = meta.get("promptTokenCount", 0)
    output_t = meta.get("candidatesTokenCount", 0)
    return {
        "model": model,
        "input_tokens": input_t,
        "output_tokens": output_t,
        "total_tokens": meta.get("totalTokenCount", input_t + output_t),
    }


def _parse_qwen_usage(body: dict) -> dict | None:
    usage = body.get("usage")
    if not usage:
        return None
    model = body.get("model", "unknown")
    input_t = usage.get("input_tokens", usage.get("prompt_tokens", 0))
    output_t = usage.get("output_tokens", usage.get("completion_tokens", 0))
    return {
        "model": model,
        "input_tokens": input_t,
        "output_tokens": output_t,
        "total_tokens": usage.get("total_tokens", input_t + output_t),
    }


PARSERS = {
    "openai":      _parse_openai_usage,
    "azure":       _parse_openai_usage,
    "deepseek":    _parse_openai_usage,
    "groq":        _parse_openai_usage,
    "mistral":     _parse_openai_usage,
    "openrouter":  _parse_openai_usage,
    "anthropic":   _parse_anthropic_usage,
    "google":      _parse_google_usage,
    "qwen":        _parse_qwen_usage,
}


def _estimate_cost(usage: dict) -> float:
    """Estimate cost in USD based on model pricing table."""
    model = usage.get("model", "")
    input_t = usage.get("input_tokens", 0)
    output_t = usage.get("output_tokens", 0)

    # Try exact match first, then prefix match
    pricing = MODEL_PRICING.get(model)
    if not pricing:
        for key, val in MODEL_PRICING.items():
            if model.startswith(key) or key in model:
                pricing = val
                break
    if not pricing:
        pricing = (1.0, 3.0)  # default fallback

    cost = (input_t * pricing[0] + output_t * pricing[1]) / 1_000_000
    return round(cost, 6)


def _store_usage(user_id: str, provider: str, usage: dict, cost: float):
    """Store per-user/per-model aggregated stats in Redis."""
    today = time.strftime("%Y-%m-%d")
    model = usage.get("model", "unknown")
    input_t = usage.get("input_tokens", 0)
    output_t = usage.get("output_tokens", 0)

    stat_key = f"audit:user:{user_id}:stats:{today}"
    model_field = f"{provider}:{model}"

    existing = r.hget(stat_key, model_field)
    if existing:
        try:
            stat = json.loads(existing)
        except Exception:
            stat = {"calls": 0, "input_tokens": 0, "output_tokens": 0, "cost": 0}
    else:
        stat = {"calls": 0, "input_tokens": 0, "output_tokens": 0, "cost": 0}

    stat["calls"] += 1
    stat["input_tokens"] += input_t
    stat["output_tokens"] += output_t
    stat["cost"] = round(stat["cost"] + cost, 6)

    pipe = r.pipeline()
    pipe.hset(stat_key, model_field, json.dumps(stat))
    pipe.expire(stat_key, 90 * 86400)
    pipe.execute()


def response(flow: http.HTTPFlow):
    if not flow.response or not flow.response.content:
        return

    host = flow.request.host
    path = flow.request.path
    provider = _detect_provider(host, path)
    if not provider:
        return

    parser = PARSERS.get(provider)
    if not parser:
        return

    try:
        body = json.loads(flow.response.content)
    except Exception:
        return

    usage = parser(body)
    if not usage:
        return

    usage["provider"] = provider
    usage["cost_usd"] = _estimate_cost(usage)
    usage["ts"] = int(time.time())

    flow.metadata["ai_usage"] = usage

    user = flow.metadata.get("audit_user")
    user_id = user.get("user_id", "unknown") if user else "unknown"

    _store_usage(user_id, provider, usage, usage["cost_usd"])

    ctx.log.info(
        f"[parser] {provider} {usage['model']} "
        f"in={usage['input_tokens']} out={usage['output_tokens']} "
        f"${usage['cost_usd']:.4f} user={user_id}"
    )
