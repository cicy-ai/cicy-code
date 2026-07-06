// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// oauth-flow: Google OAuth code-relay for cicy-code workers.
//
// Architecture: stateless code relay. The Worker briefly holds the OAuth
// authorization code (TTL 10min, single-use). It NEVER sees:
//   - client_secret (stays on the user's local cicy-code worker)
//   - refresh_token / access_token (token exchange happens locally)
//
// Endpoints:
//   GET /                  — info page
//   GET /start             — redirect to Google OAuth (?session, ?client_id, ?scopes)
//   GET /callback          — Google → us; store code in KV by state
//   GET /poll              — local CLI polls; one-shot retrieve

export interface Env {
  OAUTH_CODES: KVNamespace;
}

const GOOGLE_AUTHORIZE_URL = "https://accounts.google.com/o/oauth2/v2/auth";
const SESSION_RE = /^[a-zA-Z0-9_-]{16,128}$/;
const CLIENT_ID_RE = /^[a-zA-Z0-9._-]+\.apps\.googleusercontent\.com$/;
const CODE_TTL_SECONDS = 600; // matches Google's auth code lifetime

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);
    switch (url.pathname) {
      case "/":
        return htmlResponse(homePage());
      case "/start":
        return handleStart(url);
      case "/callback":
        return handleCallback(url, env);
      case "/poll":
        return handlePoll(url, env);
      default:
        return new Response("not found", { status: 404 });
    }
  },
};

function handleStart(url: URL): Response {
  const session = url.searchParams.get("session") || "";
  const clientId = url.searchParams.get("client_id") || "";
  const scopes = url.searchParams.get("scopes") || "";

  if (!SESSION_RE.test(session)) {
    return new Response("invalid session id", { status: 400 });
  }
  if (!CLIENT_ID_RE.test(clientId)) {
    return new Response("invalid client_id (expected *.apps.googleusercontent.com)", { status: 400 });
  }
  if (!scopes.trim()) {
    return new Response("missing scopes", { status: 400 });
  }

  const params = new URLSearchParams({
    client_id: clientId,
    redirect_uri: `${url.origin}/callback`,
    response_type: "code",
    scope: scopes,
    access_type: "offline",
    prompt: "consent",
    state: session,
    include_granted_scopes: "true",
  });
  return Response.redirect(`${GOOGLE_AUTHORIZE_URL}?${params}`, 302);
}

async function handleCallback(url: URL, env: Env): Promise<Response> {
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const error = url.searchParams.get("error");
  const errorDesc = url.searchParams.get("error_description") || "";

  if (error) {
    return htmlResponse(closePage(`授权失败: ${error}${errorDesc ? " — " + errorDesc : ""}`, false));
  }
  if (!code || !state) {
    return new Response("missing code or state", { status: 400 });
  }
  if (!SESSION_RE.test(state)) {
    return new Response("invalid state", { status: 400 });
  }

  await env.OAUTH_CODES.put(state, code, { expirationTtl: CODE_TTL_SECONDS });

  return htmlResponse(closePage("授权成功！请回到终端，cicy-code 会自动继续。", true));
}

async function handlePoll(url: URL, env: Env): Promise<Response> {
  const session = url.searchParams.get("session") || "";
  if (!SESSION_RE.test(session)) {
    return jsonResponse({ error: "invalid session" }, 400);
  }

  const code = await env.OAUTH_CODES.get(session);
  if (!code) {
    return jsonResponse({ status: "pending" }, 404);
  }

  // single-use: delete immediately so a leaked URL can't be re-polled
  await env.OAUTH_CODES.delete(session);

  return jsonResponse({ status: "ready", code });
}

function htmlResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Frame-Options": "DENY",
      "Strict-Transport-Security": "max-age=31536000",
    },
  });
}

function jsonResponse(obj: unknown, status = 200): Response {
  return new Response(JSON.stringify(obj), {
    status,
    headers: {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
      "Access-Control-Allow-Origin": "*",
    },
  });
}

function homePage(): string {
  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<title>oauth-flow</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 640px; margin: 60px auto; padding: 0 20px; color: #333; line-height: 1.6; }
  code { background: #f4f4f4; padding: 2px 6px; border-radius: 3px; font-size: 0.9em; }
  h1 { font-weight: 600; }
  .hint { color: #888; font-size: 0.9em; margin-top: 30px; }
</style>
</head><body>
<h1>oauth-flow</h1>
<p>Stateless OAuth code relay for cicy-code workers.</p>
<p>Endpoints: <code>/start</code>, <code>/callback</code>, <code>/poll</code>.</p>
<p class="hint">This service only briefly relays single-use authorization codes. It never sees client secrets or tokens.</p>
</body></html>`;
}

function closePage(message: string, success: boolean): string {
  const color = success ? "#22c55e" : "#ef4444";
  const icon = success ? "✓" : "✗";
  const title = success ? "Success" : "Failed";
  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<title>${title} — oauth-flow</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 480px; margin: 80px auto; padding: 0 20px; text-align: center; color: #333; }
  .icon { font-size: 72px; line-height: 1; color: ${color}; margin-bottom: 16px; }
  h1 { color: ${color}; font-weight: 600; margin: 0 0 16px; }
  p { color: #555; line-height: 1.6; }
  .hint { margin-top: 40px; font-size: 14px; color: #999; }
</style>
</head><body>
<div class="icon">${icon}</div>
<h1>${title}</h1>
<p>${message}</p>
<p class="hint">您可以关闭此页面。</p>
</body></html>`;
}
