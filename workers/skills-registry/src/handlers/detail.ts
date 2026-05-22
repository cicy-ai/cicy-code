// src/handlers/detail.ts
//
// GET /v1/skills/:name
//
// Returns latest non-yanked version manifest plus inlined doc files.

import type { Env } from '../types';
import { ok, err } from '../lib/response';
import { getLatestManifest } from '../lib/kv';

export async function detail(_req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const name = params.name;
  const manifest = await getLatestManifest(env, name);
  if (!manifest) return err('NOT_FOUND', `skill not found: ${name}`, 404);
  if (manifest.yanked) return err('YANKED', `latest version of ${name} is yanked`, 410);

  const files = (await env.SKILLS_KV.get(`skill:${name}:${manifest.version}:files`, 'json')) || {};
  return ok({ manifest, files });
}
