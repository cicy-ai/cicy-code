// src/handlers/categories.ts
//
// GET /v1/categories

import type { Env } from '../types';
import { ok } from '../lib/response';
import { getCategories } from '../lib/kv';

export async function categories(_req: Request, env: Env): Promise<Response> {
  const cats = await getCategories(env);
  const items = Object.entries(cats)
    .map(([name, skills]) => ({ name, count: skills.length, skills }))
    .sort((a, b) => a.name.localeCompare(b.name));
  return ok({ categories: items });
}
