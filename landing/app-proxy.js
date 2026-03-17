// cicy-ai.com / www.cicy-ai.com Worker (落地页)
const VER = '1';
const COS_BASE = `https://cicy-1372193042.cos.ap-shanghai.myqcloud.com/landing/v${VER}`;
const CN_COUNTRIES = new Set(['CN', 'HK', 'MO', 'TW']);

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

export default {
  async fetch(request, env) {
    const country = request.cf?.country || '';
    const isCN = CN_COUNTRIES.has(country);
    const url = new URL(request.url);

    // Kill old CF Pages service worker
    if (url.pathname === '/_service-worker.js') {
      return new Response('self.addEventListener("install",()=>self.skipWaiting());self.addEventListener("activate",e=>e.waitUntil(self.clients.claim()));', {
        headers: { 'Content-Type': 'application/javascript', 'Cache-Control': 'no-cache' }
      });
    }

    const resp = await env.ASSETS.fetch(request);

    // Static assets: long cache (hash in filename)
    if (url.pathname.startsWith('/assets/')) {
      const h = new Headers(resp.headers);
      h.set('Cache-Control', 'public, max-age=31536000, immutable');
      return new Response(resp.body, { status: resp.status, headers: h });
    }

    // HTML: no cache
    if (isCN && resp.headers.get('content-type')?.includes('text/html')) {
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
