// src/lib/router.ts
//
// Pattern-matching router with URL params. No external dependency.
// Supports:
//   /v1/skills/:name           → params: { name }
//   /v1/skills/:name/:version  → params: { name, version }

import type { Env } from '../types';

export type Handler = (
  req: Request,
  env: Env,
  ctx: ExecutionContext,
  params: Record<string, string>,
) => Promise<Response> | Response;

interface Route {
  method: string;
  pattern: string;
  segments: { kind: 'lit' | 'param'; value: string }[];
  handler: Handler;
}

export class Router {
  private routes: Route[] = [];

  add(method: string, pattern: string, handler: Handler): this {
    const segments = pattern
      .split('/')
      .filter(Boolean)
      .map((seg) =>
        seg.startsWith(':')
          ? ({ kind: 'param', value: seg.slice(1) } as const)
          : ({ kind: 'lit', value: seg } as const),
      );
    this.routes.push({ method: method.toUpperCase(), pattern, segments, handler });
    return this;
  }

  get(p: string, h: Handler) { return this.add('GET', p, h); }
  post(p: string, h: Handler) { return this.add('POST', p, h); }
  delete(p: string, h: Handler) { return this.add('DELETE', p, h); }

  async handle(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname;
    const segs = path.split('/').filter(Boolean);

    if (req.method === 'OPTIONS') {
      // delegate preflight to caller via no-match → 204
      return new Response(null, {
        status: 204,
        headers: {
          'Access-Control-Allow-Origin': '*',
          'Access-Control-Allow-Methods': 'GET, POST, DELETE, OPTIONS',
          'Access-Control-Allow-Headers': 'Authorization, Content-Type',
        },
      });
    }

    for (const route of this.routes) {
      if (route.method !== req.method) continue;
      if (route.segments.length !== segs.length) continue;
      const params: Record<string, string> = {};
      let match = true;
      for (let i = 0; i < route.segments.length; i++) {
        const r = route.segments[i];
        const s = segs[i];
        if (r.kind === 'lit') {
          if (r.value !== s) { match = false; break; }
        } else {
          params[r.value] = decodeURIComponent(s);
        }
      }
      if (match) {
        return route.handler(req, env, ctx, params);
      }
    }
    return new Response(
      JSON.stringify({ ok: false, data: null, error: { code: 'NOT_FOUND', message: `no route for ${req.method} ${path}` } }),
      { status: 404, headers: { 'Content-Type': 'application/json' } },
    );
  }
}
