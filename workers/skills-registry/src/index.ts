// src/index.ts — skills.cicy-ai.com entrypoint
//
// Routes:
//   GET    /v1/health
//   GET    /v1/skills
//   GET    /v1/skills/:name
//   GET    /v1/skills/:name/versions
//   GET    /v1/skills/:name/:version
//   GET    /v1/skills/:name/:version/files/:file
//   GET    /v1/skills/:name/:version/download
//   GET    /v1/categories
//   POST   /v1/admin/publish
//   DELETE /v1/admin/skills/:name/:version

import type { Env } from './types';
import { Router } from './lib/router';
import { err, preflight } from './lib/response';

import { health } from './handlers/health';
import { list } from './handlers/list';
import { detail } from './handlers/detail';
import { versions } from './handlers/versions';
import { manifest } from './handlers/manifest';
import { files } from './handlers/files';
import { download } from './handlers/download';
import { categories } from './handlers/categories';
import { publish } from './handlers/publish';
import { yank } from './handlers/yank';

const router = new Router();

// read API
router.get('/v1/health', health);
router.get('/v1/skills', list);
router.get('/v1/skills/:name', detail);
router.get('/v1/skills/:name/versions', versions);
router.get('/v1/skills/:name/:version', manifest);
router.get('/v1/skills/:name/:version/download', download);
router.get('/v1/skills/:name/:version/files/:file', files);
router.get('/v1/categories', categories);

// write API
router.post('/v1/admin/publish', publish);
router.delete('/v1/admin/skills/:name/:version', yank);

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    if (req.method === 'OPTIONS') return preflight();

    const url = new URL(req.url);

    // root → simple landing
    if (url.pathname === '/' || url.pathname === '') {
      return new Response(landingPage(), {
        headers: { 'Content-Type': 'text/html; charset=utf-8' },
      });
    }

    // serve schema URL referenced by manifest.$schema
    if (url.pathname === '/v1/manifest.schema.json') {
      return Response.redirect(
        'https://raw.githubusercontent.com/cicy-ai/cicy-skills/main/schemas/manifest.schema.json',
        302,
      );
    }

    try {
      return await router.handle(req, env, ctx);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      return err('INTERNAL', msg, 500);
    }
  },
} satisfies ExportedHandler<Env>;

function landingPage(): string {
  return `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>cicy-skills registry</title>
<style>
  body { font: 14px/1.5 system-ui, -apple-system, sans-serif; max-width: 720px; margin: 4rem auto; padding: 0 1.5rem; color:#222; }
  h1 { margin-bottom: .25em; }
  code, pre { background:#f4f4f4; padding: .15em .4em; border-radius: 4px; }
  pre { padding: 1em; overflow-x: auto; }
  a { color:#0366d6; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .meta { color: #888; font-size: 13px; }
</style>
<h1>cicy-skills registry</h1>
<p class="meta">A transparent, source-only skills index for cicy-code.</p>
<p>Public read endpoints:</p>
<ul>
  <li><a href="/v1/health"><code>GET /v1/health</code></a></li>
  <li><a href="/v1/skills"><code>GET /v1/skills</code></a></li>
  <li><a href="/v1/categories"><code>GET /v1/categories</code></a></li>
  <li><code>GET /v1/skills/:name</code></li>
  <li><code>GET /v1/skills/:name/versions</code></li>
  <li><code>GET /v1/skills/:name/:version</code></li>
  <li><code>GET /v1/skills/:name/:version/files/:file</code> (skill_md / help_md / tools_md / readme)</li>
  <li><code>GET /v1/skills/:name/:version/download</code> (302 → GitHub Releases)</li>
</ul>
<p>Source: <a href="https://github.com/cicy-ai/cicy-skills">github.com/cicy-ai/cicy-skills</a></p>
<p>Spec: <a href="https://github.com/cicy-ai/cicy-code/blob/main/docs/skills-v2-design.md">design docs</a></p>
</html>`;
}
