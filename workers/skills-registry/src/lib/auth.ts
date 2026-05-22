// src/lib/auth.ts

import type { Env } from '../types';
import { err } from './response';

export function requireAdmin(req: Request, env: Env): Response | null {
  const auth = req.headers.get('Authorization') || '';
  const m = auth.match(/^Bearer\s+(.+)$/i);
  if (!m) return err('UNAUTHORIZED', 'Authorization: Bearer <token> required', 401);
  const provided = m[1].trim();
  // constant-time-ish compare
  if (provided.length !== env.ADMIN_TOKEN.length) {
    return err('FORBIDDEN', 'invalid admin token', 403);
  }
  let diff = 0;
  for (let i = 0; i < provided.length; i++) {
    diff |= provided.charCodeAt(i) ^ env.ADMIN_TOKEN.charCodeAt(i);
  }
  if (diff !== 0) return err('FORBIDDEN', 'invalid admin token', 403);
  return null;
}
