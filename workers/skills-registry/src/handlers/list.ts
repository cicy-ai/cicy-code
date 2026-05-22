// src/handlers/list.ts
//
// GET /v1/skills?q=&category=&agent=&limit=&offset=
//
// Returns array of SkillSummary (always the latest non-yanked version of each
// skill). Filters apply post-fetch since the catalog is small (<1k skills).

import type { Env, Manifest, SkillSummary } from '../types';
import { ok } from '../lib/response';
import { getCatalog, getLatestManifest } from '../lib/kv';

function summarize(m: Manifest): SkillSummary {
  return {
    name: m.name,
    version: m.version,
    title: m.title,
    description: m.description,
    category: m.category,
    tags: m.tags || [],
    author: m.author,
    license: m.license,
    compatible_agents: m.compatible_agents || ['*'],
    size: m.publish?.size || 0,
    published_at: m.publish?.published_at || '',
  };
}

export async function list(req: Request, env: Env): Promise<Response> {
  const url = new URL(req.url);
  const q = url.searchParams.get('q')?.toLowerCase().trim() || '';
  const category = url.searchParams.get('category') || '';
  const agent = url.searchParams.get('agent') || '';
  const limit = clampInt(url.searchParams.get('limit'), 1, 200, 100);
  const offset = clampInt(url.searchParams.get('offset'), 0, 1_000_000, 0);

  const names = await getCatalog(env);

  // fetch all latest manifests in parallel
  const manifests = await Promise.all(
    names.map((n) => getLatestManifest(env, n)),
  );

  let summaries: SkillSummary[] = [];
  for (const m of manifests) {
    if (!m) continue;
    if (m.yanked) continue;

    if (category && m.category !== category) continue;
    if (agent && agent !== '*') {
      const ok = m.compatible_agents?.includes('*') || m.compatible_agents?.includes(agent);
      if (!ok) continue;
    }
    if (q) {
      const hay = [m.name, m.title, m.description, ...(m.tags || [])].join(' ').toLowerCase();
      if (!hay.includes(q)) continue;
    }
    summaries.push(summarize(m));
  }

  const total = summaries.length;
  summaries = summaries.slice(offset, offset + limit);

  return ok({ skills: summaries, total, limit, offset });
}

function clampInt(s: string | null, min: number, max: number, fallback: number): number {
  if (!s) return fallback;
  const n = parseInt(s, 10);
  if (Number.isNaN(n)) return fallback;
  return Math.max(min, Math.min(max, n));
}
