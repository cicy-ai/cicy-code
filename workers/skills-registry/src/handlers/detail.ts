// src/handlers/detail.ts
//
// GET /v1/skills/:name
//
// Returns latest non-yanked version manifest plus inlined doc files.

import type { Env } from '../types';
import { ok, err } from '../lib/response';
import { getLatestManifest } from '../lib/kv';

export async function detail(req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const name = params.name;
  const lang = new URL(req.url).searchParams.get('lang') || '';

  const manifest = await getLatestManifest(env, name);
  if (!manifest) return err('NOT_FOUND', `skill not found: ${name}`, 404);
  if (manifest.yanked) return err('YANKED', `latest version of ${name} is yanked`, 410);

  // Resolve i18n: inject title_localized / description_localized alongside base fields.
  const i18n = manifest.i18n || {};
  let loc: { title?: string; description?: string } | undefined;
  if (lang) {
    loc = i18n[lang] || i18n[lang.split('-')[0]] || undefined;
  }

  const files = (await env.SKILLS_KV.get(`skill:${name}:${manifest.version}:files`, 'json')) || {};
  return ok({ manifest, files, title_localized: loc?.title, description_localized: loc?.description });
}
