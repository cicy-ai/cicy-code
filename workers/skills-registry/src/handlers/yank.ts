// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// src/handlers/yank.ts
//
// DELETE /v1/admin/skills/:name/:version
// Authorization: Bearer <admin_token>
//
// Marks a version as yanked (it stays in the versions list but no longer
// appears as latest, and download will return 410).

import type { Env } from '../types';
import { ok, err } from '../lib/response';
import { requireAdmin } from '../lib/auth';
import { yankVersion, getLatestVersion } from '../lib/kv';

export async function yank(req: Request, env: Env, _ctx: ExecutionContext, params: Record<string, string>): Promise<Response> {
  const authErr = requireAdmin(req, env);
  if (authErr) return authErr;

  const { name, version } = params;
  const yanked = await yankVersion(env, name, version);
  if (!yanked) return err('NOT_FOUND', `${name}@${version} not found`, 404);

  const latest = await getLatestVersion(env, name);
  return ok({ name, version, yanked: true, latest });
}
