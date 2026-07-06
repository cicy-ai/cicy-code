// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// src/handlers/versions.ts
//
// GET /v1/skills/:name/versions

import type { Env } from '../types';
import { ok, err } from '../lib/response';
import { getVersions, getLatestVersion } from '../lib/kv';
import { compareSemver } from '../lib/semver';

export async function versions(_req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const name = params.name;
  const list = await getVersions(env, name);
  if (list.length === 0) return err('NOT_FOUND', `skill not found: ${name}`, 404);

  // latest first
  const sorted = [...list].sort((a, b) => -compareSemver(a.version, b.version));
  const latest = await getLatestVersion(env, name);
  return ok({ name, latest, versions: sorted });
}
