// Unified Worker: app.cicy-ai.com + u-*-app.cicy-ai.com + u-*-free-api.cicy-ai.com
const VER = '1';
const COS_BASE = `https://cicy-1372193042.cos.ap-shanghai.myqcloud.com/app/v${VER}`;
const MGR_API = 'https://api.cicy-ai.com';
const CN_COUNTRIES = new Set(['CN', 'HK', 'MO', 'TW']);
const SKIP_SLUGS = new Set(['app', 'api', 'dev', 'dev-api', 'www', 'tn', 'ws', 'audit', 'audit-api']);

class AssetRewriter {
  constructor(cdn) { this.cdn = cdn; }
  element(el) {
    for (const attr of ['src', 'href']) {
      const val = el.getAttribute(attr);
      if (val && val.startsWith('/assets/')) {
        el.setAttribute(attr, this.cdn + val);
      }
    }
  }
}

function proxyTo(request, url, backend) {
  const target = backend + url.pathname + url.search;
  const req = new Request(target, request);
  req.headers.set('host', new URL(backend).host);
  return fetch(req);
}

function normalizeBackend(raw) {
  const backend = String(raw || '').trim();
  return backend.replace(/\/$/, '');
}

function freeApiBackendForHost(hostname, env) {
  const sub = hostname.split('.')[0] || '';
  const match = sub.match(/^(u-.+)-free-api$/);
  if (!match) return '';

  const slug = match[1];
  const exactKey = `FREE_API_BACKEND_${sub.replace(/-/g, '_').toUpperCase()}`;
  const slugKey = `FREE_API_BACKEND_${slug.replace(/-/g, '_').toUpperCase()}`;
  const direct = normalizeBackend(env?.[exactKey] || env?.[slugKey]);
  if (direct) return direct;

  const mappingRaw = String(env?.FREE_API_BACKENDS || '').trim();
  if (mappingRaw) {
    try {
      const mapping = JSON.parse(mappingRaw);
      const mapped = normalizeBackend(mapping[sub] || mapping[slug]);
      if (mapped) return mapped;
    } catch {}
  }

  if (sub === 'u-cicy-trial-free-api' || slug === 'u-cicy-trial') {
    return DEFAULT_TRIAL_BACKEND;
  }

  return normalizeBackend(env?.FREE_API_BACKEND_DEFAULT) || DEFAULT_TRIAL_BACKEND;
}

const PROXY_PREFIXES = ['/api/', '/ws/', '/code/', '/ttyd/', '/mitm/', '/stt/'];

const DEFAULT_TRIAL_BACKEND = 'https://cicy-trial-944897035502.asia-east1.run.app';

async function proxyWs(request, url, backend) {
  const target = backend + url.pathname + url.search;
  const resp = await fetch(target, { headers: request.headers });
  if (resp.status !== 101) return new Response('WebSocket upgrade failed', { status: resp.status });
  const ws = resp.webSocket;
  ws.accept();
  const pair = new WebSocketPair();
  const [client, server] = Object.values(pair);
  server.accept();
  server.addEventListener('message', e => { try { ws.send(e.data); } catch {} });
  server.addEventListener('close', e => { try { ws.close(e.code, e.reason); } catch {} });
  ws.addEventListener('message', e => { try { server.send(e.data); } catch {} });
  ws.addEventListener('close', e => { try { server.close(e.code, e.reason); } catch {} });
  const headers = new Headers();
  const proto = resp.headers.get('Sec-WebSocket-Protocol');
  if (proto) headers.set('Sec-WebSocket-Protocol', proto);
  return new Response(null, { status: 101, webSocket: client, headers });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const sub = url.hostname.split('.')[0];
    const freeApiBackend = freeApiBackendForHost(url.hostname, env);

    // u-xxx-api.cicy-ai.com → passthrough to CF Tunnel (Pro)
    if (sub.match(/^u-.+-api$/)) return fetch(request);

    // u-xxx-free-api.cicy-ai.com → Worker 代理到 Cloud Run (Trial)
    if (freeApiBackend) {
      if (request.headers.get('upgrade')?.toLowerCase() === 'websocket') {
        return proxyWs(request, url, freeApiBackend);
      }
      return proxyTo(request, url, freeApiBackend);
    }

    // u-xxx-app.cicy-ai.com → workspace
    const appMatch = sub.match(/^(u-.+)-app$/);
    const slug = appMatch ? appMatch[1] : sub;
    const isWorkspace = appMatch !== null;
    const isApp = sub === 'app';
    const isLegacyWorkspace = !isWorkspace && slug.startsWith('u-') && !SKIP_SLUGS.has(slug);

    if (!isApp && !isWorkspace && !isLegacyWorkspace) return fetch(request);

    const needsProxy = PROXY_PREFIXES.some(p => url.pathname.startsWith(p));

    if (needsProxy) {
      if (isWorkspace || isLegacyWorkspace) {
        // Workspace: 浏览器直连 api 域名，Worker 不代理
        return new Response('Use api domain directly', { status: 404 });
      }
      return proxyTo(request, url, MGR_API);
    }

    // Static assets
    const resp = await env.ASSETS.fetch(request);

    if (url.pathname.startsWith('/assets/')) {
      const h = new Headers(resp.headers);
      h.set('Cache-Control', 'public, max-age=31536000, immutable');
      return new Response(resp.body, { status: resp.status, headers: h });
    }

    const country = request.cf?.country || '';
    if (CN_COUNTRIES.has(country) && resp.headers.get('content-type')?.includes('text/html')) {
      const rewritten = new HTMLRewriter()
        .on('script[src], link[href]', new AssetRewriter(COS_BASE))
        .transform(resp);
      const h = new Headers(rewritten.headers);
      h.set('Cache-Control', 'no-cache');
      return new Response(rewritten.body, { status: rewritten.status, headers: h });
    }

    return resp;
  }
};
