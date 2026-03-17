// Workspace subdomain Worker
// {slug}.cicy-ai.com → proxy API to user's Cloud Run, serve frontend from app.cicy-ai.com

const MGR_API = 'https://api.cicy-ai.com';
const APP_ORIGIN = 'https://app.cicy-ai.com';
const SKIP = new Set(['app', 'api', 'dev', 'dev-api', 'www', '']);

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const slug = url.hostname.split('.')[0];

    if (SKIP.has(slug) || !slug.startsWith('u-')) {
      return fetch(request);
    }

    // API + WS → resolve backend and proxy
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) {
      const backend = await resolveBackend(slug);
      if (!backend) return new Response('Workspace not found', { status: 404 });

      const target = backend + url.pathname + url.search;
      const headers = new Headers(request.headers);
      headers.set('Host', new URL(backend).host);

      const resp = await fetch(target, {
        method: request.method,
        headers,
        body: ['GET', 'HEAD'].includes(request.method) ? undefined : request.body,
      });

      const h = new Headers(resp.headers);
      h.set('Access-Control-Allow-Origin', '*');
      return new Response(resp.body, { status: resp.status, headers: h });
    }

    // Everything else → serve frontend from app.cicy-ai.com
    const appUrl = APP_ORIGIN + url.pathname + url.search;
    const h = new Headers(request.headers);
    h.set('Host', 'app.cicy-ai.com');
    return fetch(appUrl, { headers: h, redirect: 'follow' });
  }
};

const cache = new Map();

async function resolveBackend(slug) {
  const now = Date.now();
  const c = cache.get(slug);
  if (c && now - c.ts < 300000) return c.url;

  try {
    const r = await fetch(`${MGR_API}/api/resolve?slug=${slug}`);
    if (!r.ok) return null;
    const { backend_url } = await r.json();
    if (backend_url) cache.set(slug, { url: backend_url, ts: now });
    return backend_url || null;
  } catch { return null; }
}
