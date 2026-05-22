// src/handlers/health.ts

import type { Env } from '../types';
import { ok } from '../lib/response';

export const health = (_req: Request, env: Env) => {
  return ok({
    status: 'ok',
    schema_version: env.SCHEMA_VERSION,
    default_repo: env.DEFAULT_REPO,
    time: new Date().toISOString(),
  });
};
