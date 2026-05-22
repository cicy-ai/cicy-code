// src/handlers/download.ts
//
// GET /v1/skills/:name/:version/download
//
// Redirects to the GitHub Releases asset URL stored in manifest.publish.download_url.

import type { Env } from '../types';
import { err, redirect } from '../lib/response';
import { getManifest, getLatestVersion } from '../lib/kv';

export async function download(_req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const { name } = params;
  let { version } = params;

  if (version === 'latest') {
    const v = await getLatestVersion(env, name);
    if (!v) return err('NOT_FOUND', `skill not found: ${name}`, 404);
    version = v;
  }

  const m = await getManifest(env, name, version);
  if (!m) return err('NOT_FOUND', `version not found: ${name}@${version}`, 404);
  if (m.yanked) return err('YANKED', `version yanked: ${name}@${version}`, 410);
  if (!m.publish?.download_url) {
    return err('NO_DOWNLOAD_URL', 'publish.download_url is missing', 500);
  }

  return redirect(m.publish.download_url);
}
