// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// src/lib/response.ts

import type { ApiResponse } from '../types';

const CORS_HEADERS = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Methods': 'GET, POST, DELETE, OPTIONS',
  'Access-Control-Allow-Headers': 'Authorization, Content-Type',
  'Access-Control-Max-Age': '86400',
};

export function ok<T>(data: T, init?: ResponseInit): Response {
  const body: ApiResponse<T> = { ok: true, data, error: null };
  return json(body, init);
}

export function err(
  code: string,
  message: string,
  status = 400,
  extra?: ResponseInit,
): Response {
  const body: ApiResponse<never> = {
    ok: false,
    data: null,
    error: { code, message },
  };
  return json(body, { status, ...extra });
}

export function json<T>(body: T, init: ResponseInit = {}): Response {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json; charset=utf-8');
  for (const [k, v] of Object.entries(CORS_HEADERS)) headers.set(k, v);
  if (!headers.has('Cache-Control')) {
    headers.set('Cache-Control', 'public, max-age=60');
  }
  return new Response(JSON.stringify(body), { ...init, headers });
}

export function preflight(): Response {
  return new Response(null, { status: 204, headers: CORS_HEADERS });
}

export function redirect(url: string, status: 301 | 302 = 302): Response {
  const headers = new Headers({
    Location: url,
    'Cache-Control': 'public, max-age=300',
  });
  for (const [k, v] of Object.entries(CORS_HEADERS)) headers.set(k, v);
  return new Response(null, { status, headers });
}

export function text(body: string, init: ResponseInit = {}): Response {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'text/plain; charset=utf-8');
  for (const [k, v] of Object.entries(CORS_HEADERS)) headers.set(k, v);
  return new Response(body, { ...init, headers });
}
