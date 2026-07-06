// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Origin for /cos-assets/* proxy. Switched from Tencent COS to Cloudflare R2
// (r2.deepfetch.de5.net) — COS returns HTTP 451 to overseas IPs and freezes
// the whole bucket on arrears. R2 has a permanent free tier, no ICP filter,
// and CF edge caching. Path layout is unchanged (/app/v3/...), so only the
// host moved. Kept the var name COS_BASE for minimal churn; it is the asset
// origin, now R2.
const COS_BASE = "https://r2.deepfetch.de5.net/app";

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname.startsWith("/cos-assets/")) {
      // /cos-assets/v3/assets/foo.js → COS /app/v3/assets/foo.js
      const cosPath = url.pathname.replace(/^\/cos-assets\//, "/");
      const cosUrl = `${COS_BASE}${cosPath}`;
      const cosReq = new Request(cosUrl, {
        method: request.method,
        headers: { "User-Agent": "CloudflareWorker/1.0" },
      });
      const cosRes = await fetch(cosReq);
      const headers = new Headers(cosRes.headers);
      headers.set("Access-Control-Allow-Origin", "*");
      headers.set("Cache-Control", "public, max-age=31536000, immutable");
      return new Response(cosRes.body, {
        status: cosRes.status,
        headers,
      });
    }

    return env.ASSETS.fetch(request);
  },
};
