// src/handlers/manifest.ts
//
// GET /v1/skills/:name/:version
// (also handles :version=latest by redirecting/transparent fetch)

import type { Env } from '../types';
import { ok, err } from '../lib/response';
import { getManifest, getLatestVersion } from '../lib/kv';

export async function manifest(_req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const { name } = params;
  let { version } = params;

  if (version === 'latest') {
    const v = await getLatestVersion(env, name);
    if (!v) return err('NOT_FOUND', `skill not found: ${name}`, 404);
    version = v;
  }

  const m = await getManifest(env, name, version);
  if (!m) return err('NOT_FOUND', `version not found: ${name}@${version}`, 404);

  const files = (await env.SKILLS_KV.get(`skill:${name}:${version}:files`, 'json')) || {};
  return ok({ manifest: m, files });
}
