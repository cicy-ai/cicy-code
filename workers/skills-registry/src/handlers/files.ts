// src/handlers/files.ts
//
// GET /v1/skills/:name/:version/files/:file
//
// Returns a single doc file's content as text/plain. Allowed file keys:
// skill_md, help_md, tools_md, readme.

import type { Env } from '../types';
import { err, text } from '../lib/response';
import { getLatestVersion } from '../lib/kv';

const ALLOWED = new Set(['skill_md', 'help_md', 'tools_md', 'readme']);

export async function files(_req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const { name, file } = params;
  let { version } = params;

  if (!ALLOWED.has(file)) {
    return err('INVALID_FILE', `file must be one of ${[...ALLOWED].join(',')}`, 400);
  }

  if (version === 'latest') {
    const v = await getLatestVersion(env, name);
    if (!v) return err('NOT_FOUND', `skill not found: ${name}`, 404);
    version = v;
  }

  const stored = await env.SKILLS_KV.get<Record<string, string>>(
    `skill:${name}:${version}:files`,
    'json',
  );
  if (!stored || typeof stored[file] !== 'string') {
    return err('NOT_FOUND', `file not found: ${name}@${version}/${file}`, 404);
  }

  return text(stored[file]);
}
