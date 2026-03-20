// CiCy Audit Worker — audit.cicy-ai.com
// Serves SPA from static assets, proxies /api/* to Go backend via CF Tunnel

const API_PREFIXES = ['/api/', '/ca.pem', '/install-ca', '/setup', '/health'];

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const backend = env.API_BACKEND || 'https://audit-api.cicy-ai.com';

    // Proxy API and utility paths to Go backend
    if (API_PREFIXES.some(p => url.pathname === p || url.pathname.startsWith(p))) {
      const target = backend + url.pathname + url.search;
      const headers = new Headers(request.headers);
      headers.set('Host', new URL(backend).host);

      const resp = await fetch(target, {
        method: request.method,
        headers,
        body: request.method !== 'GET' && request.method !== 'HEAD' ? request.body : undefined,
      });

      const respHeaders = new Headers(resp.headers);
      const origin = request.headers.get('Origin');
      if (origin) {
        respHeaders.set('Access-Control-Allow-Origin', origin);
        respHeaders.set('Access-Control-Allow-Credentials', 'true');
        respHeaders.set('Access-Control-Allow-Methods', 'GET, POST, PUT, PATCH, DELETE, OPTIONS');
        respHeaders.set('Access-Control-Allow-Headers', 'Authorization, Content-Type, Accept');
      }

      return new Response(resp.body, {
        status: resp.status,
        headers: respHeaders,
      });
    }

    // CORS preflight
    if (request.method === 'OPTIONS') {
      return new Response(null, {
        status: 204,
        headers: {
          'Access-Control-Allow-Origin': request.headers.get('Origin') || '*',
          'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
          'Access-Control-Allow-Headers': 'Authorization, Content-Type',
          'Access-Control-Max-Age': '86400',
        },
      });
    }

    // Static assets (CSS, JS, images)
    if (url.pathname.startsWith('/assets/') || url.pathname.match(/\.(js|css|png|svg|ico|woff2?)$/)) {
      const resp = await env.ASSETS.fetch(request);
      if (resp.status === 200) {
        const h = new Headers(resp.headers);
        h.set('Cache-Control', 'public, max-age=31536000, immutable');
        return new Response(resp.body, { status: 200, headers: h });
      }
    }

    // SPA fallback — serve index.html
    const indexReq = new Request(new URL('/', url).href, request);
    return env.ASSETS.fetch(indexReq);
  },
};
