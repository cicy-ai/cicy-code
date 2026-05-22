// src/handlers/publish.ts
//
// POST /v1/admin/publish
// Authorization: Bearer <admin_token>
// Content-Type: application/json
//
// Body:
// {
//   "manifest": { ...fully-formed manifest with publish.{sha256,size,download_url,source} },
//   "files":    { "skill_md": "...", "help_md": "...", "tools_md": "...", "readme": "..." }, // optional
//   "verify":   { "download_url": "...", "sha256": "...", "size": ... }
// }
//
// Worker:
//   1. validates admin token
//   2. validates manifest shape + verify match
//   3. (optionally) HEADs download_url to confirm reachability
//   4. writes KV: manifest, versions, latest, catalog/categories, files
//   5. returns success

import type { Env, Manifest, PublishRequest } from '../types';
import { ok, err } from '../lib/response';
import { requireAdmin } from '../lib/auth';
import { isValidSemver, compareSemver } from '../lib/semver';
import {
  getManifest,
  putManifest,
  getVersions,
  setVersions,
  getLatestVersion,
  setLatestVersion,
  indexSkill,
} from '../lib/kv';

const VALID_CATEGORIES = new Set([
  'network', 'cloud', 'ai', 'dev', 'system', 'productivity', 'agent', 'infra', 'other',
]);

export async function publish(req: Request, env: Env): Promise<Response> {
  const authErr = requireAdmin(req, env);
  if (authErr) return authErr;

  let body: PublishRequest & { files?: Record<string, string> };
  try {
    body = await req.json();
  } catch (e) {
    return err('BAD_JSON', `invalid JSON body: ${(e as Error).message}`, 400);
  }

  const { manifest, verify, files } = body;

  // ── validate manifest ────────────────────────────────────────────────
  const mErr = validateManifest(manifest);
  if (mErr) return err('BAD_MANIFEST', mErr, 422);

  // verify integrity fields match
  if (!verify || typeof verify !== 'object') {
    return err('BAD_VERIFY', 'verify object required', 422);
  }
  if (!manifest.publish) {
    return err('BAD_MANIFEST', 'manifest.publish is required', 422);
  }
  if (manifest.publish.download_url !== verify.download_url) {
    return err('VERIFY_MISMATCH', 'manifest.publish.download_url != verify.download_url', 422);
  }
  if (manifest.publish.sha256 !== verify.sha256) {
    return err('VERIFY_MISMATCH', 'manifest.publish.sha256 != verify.sha256', 422);
  }
  if (manifest.publish.size !== verify.size) {
    return err('VERIFY_MISMATCH', 'manifest.publish.size != verify.size', 422);
  }

  // ── (optional) HEAD the download URL ─────────────────────────────────
  if (new URL(req.url).searchParams.get('skip_head') !== '1') {
    try {
      const head = await fetch(manifest.publish.download_url, {
        method: 'HEAD',
        redirect: 'follow',
      });
      if (!head.ok) {
        return err(
          'DOWNLOAD_UNREACHABLE',
          `HEAD ${manifest.publish.download_url} returned ${head.status}`,
          422,
        );
      }
      const remoteSize = Number(head.headers.get('content-length') || 0);
      if (remoteSize > 0 && remoteSize !== manifest.publish.size) {
        return err(
          'SIZE_MISMATCH',
          `remote size ${remoteSize} != manifest size ${manifest.publish.size}`,
          422,
        );
      }
    } catch (e) {
      return err('HEAD_FAILED', `HEAD failed: ${(e as Error).message}`, 422);
    }
  }

  // ── idempotency: same name+version+sha256 → 200 OK ──────────────────
  const existing = await getManifest(env, manifest.name, manifest.version);
  if (existing) {
    if (existing.publish?.sha256 === manifest.publish.sha256) {
      return ok({
        name: manifest.name,
        version: manifest.version,
        idempotent: true,
        manifest_url: `https://${new URL(req.url).host}/v1/skills/${manifest.name}/${manifest.version}`,
        download_url: manifest.publish.download_url,
      });
    }
    return err(
      'CONFLICT',
      `${manifest.name}@${manifest.version} already published with different sha256`,
      409,
    );
  }

  // ── write KV ─────────────────────────────────────────────────────────
  await putManifest(env, manifest);

  if (files && typeof files === 'object') {
    await env.SKILLS_KV.put(
      `skill:${manifest.name}:${manifest.version}:files`,
      JSON.stringify(files),
    );
  }

  const list = await getVersions(env, manifest.name);
  list.push({
    version: manifest.version,
    published_at: manifest.publish.published_at,
    size: manifest.publish.size,
  });
  await setVersions(env, manifest.name, list);

  // update latest if this is newer
  const currentLatest = await getLatestVersion(env, manifest.name);
  if (!currentLatest || compareSemver(manifest.version, currentLatest) > 0) {
    await setLatestVersion(env, manifest.name, manifest.version);
  }

  await indexSkill(env, manifest);

  return ok({
    name: manifest.name,
    version: manifest.version,
    manifest_url: `https://${new URL(req.url).host}/v1/skills/${manifest.name}/${manifest.version}`,
    download_url: manifest.publish.download_url,
  });
}

// ── validation ────────────────────────────────────────────────────────

function validateManifest(m: Manifest): string | null {
  if (!m || typeof m !== 'object') return 'manifest must be an object';
  if (typeof m.name !== 'string' || !/^[a-z][a-z0-9_-]*$/.test(m.name)) {
    return `invalid name: ${m.name}`;
  }
  if (m.name.length > 64) return 'name too long';
  if (typeof m.version !== 'string' || !isValidSemver(m.version)) {
    return `invalid version: ${m.version}`;
  }
  if (typeof m.title !== 'string' || !m.title || m.title.length > 100) {
    return 'invalid title';
  }
  if (typeof m.description !== 'string' || !m.description || m.description.length > 200) {
    return 'invalid description';
  }
  if (!VALID_CATEGORIES.has(m.category)) return `invalid category: ${m.category}`;
  if (typeof m.author !== 'string' || !m.author) return 'invalid author';
  if (typeof m.license !== 'string' || !m.license) return 'invalid license';
  if (!m.runtime || typeof m.runtime.node !== 'string') return 'invalid runtime.node';
  if (typeof m.entry !== 'string' || !m.entry.startsWith('bin/')) {
    return 'entry must start with "bin/"';
  }
  if (!m.publish || typeof m.publish !== 'object') return 'publish required';
  if (!/^[a-f0-9]{64}$/.test(m.publish.sha256)) return 'invalid publish.sha256';
  if (typeof m.publish.size !== 'number' || m.publish.size < 0) {
    return 'invalid publish.size';
  }
  if (typeof m.publish.download_url !== 'string' || !m.publish.download_url.startsWith('https://')) {
    return 'invalid publish.download_url';
  }
  if (m.config) {
    if (typeof m.config.path !== 'string' || !m.config.path.startsWith('~/cicy-ai/db/')) {
      return 'config.path must start with ~/cicy-ai/db/';
    }
  }
  return null;
}
