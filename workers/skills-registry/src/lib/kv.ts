// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// src/lib/kv.ts
//
// KV layout:
//   catalog                   string[]   all skill names
//   skill:<name>:latest       string     latest non-yanked version
//   skill:<name>:versions     VersionEntry[]
//   skill:<name>:<version>    Manifest (with publish field)
//   categories                Record<string, string[]>  category -> [name]

import type { Env, Manifest, VersionEntry } from '../types';
import { compareSemver } from './semver';

const KEY_CATALOG = 'catalog';
const KEY_CATEGORIES = 'categories';

export async function getCatalog(env: Env): Promise<string[]> {
  return (await env.SKILLS_KV.get<string[]>(KEY_CATALOG, 'json')) || [];
}

export async function setCatalog(env: Env, names: string[]): Promise<void> {
  await env.SKILLS_KV.put(KEY_CATALOG, JSON.stringify(names));
}

export async function getCategories(env: Env): Promise<Record<string, string[]>> {
  return (await env.SKILLS_KV.get<Record<string, string[]>>(KEY_CATEGORIES, 'json')) || {};
}

export async function setCategories(
  env: Env,
  cats: Record<string, string[]>,
): Promise<void> {
  await env.SKILLS_KV.put(KEY_CATEGORIES, JSON.stringify(cats));
}

export async function getLatestVersion(env: Env, name: string): Promise<string | null> {
  return env.SKILLS_KV.get(`skill:${name}:latest`);
}

export async function setLatestVersion(
  env: Env,
  name: string,
  version: string,
): Promise<void> {
  await env.SKILLS_KV.put(`skill:${name}:latest`, version);
}

export async function getVersions(env: Env, name: string): Promise<VersionEntry[]> {
  return (await env.SKILLS_KV.get<VersionEntry[]>(`skill:${name}:versions`, 'json')) || [];
}

export async function setVersions(
  env: Env,
  name: string,
  versions: VersionEntry[],
): Promise<void> {
  await env.SKILLS_KV.put(`skill:${name}:versions`, JSON.stringify(versions));
}

export async function getManifest(
  env: Env,
  name: string,
  version: string,
): Promise<Manifest | null> {
  return env.SKILLS_KV.get<Manifest>(`skill:${name}:${version}`, 'json');
}

export async function putManifest(
  env: Env,
  manifest: Manifest,
): Promise<void> {
  await env.SKILLS_KV.put(
    `skill:${manifest.name}:${manifest.version}`,
    JSON.stringify(manifest),
  );
}

export async function getLatestManifest(
  env: Env,
  name: string,
): Promise<Manifest | null> {
  const v = await getLatestVersion(env, name);
  if (!v) return null;
  return getManifest(env, name, v);
}

/**
 * Re-derive `latest` by scanning versions and picking highest non-yanked.
 */
export async function recomputeLatest(env: Env, name: string): Promise<string | null> {
  const versions = await getVersions(env, name);
  const candidates = versions.filter((v) => !v.yanked).map((v) => v.version);
  if (candidates.length === 0) return null;
  candidates.sort(compareSemver);
  const top = candidates[candidates.length - 1];
  await setLatestVersion(env, name, top);
  return top;
}

/**
 * Add or update a skill in the catalog and category map.
 */
export async function indexSkill(
  env: Env,
  manifest: Manifest,
): Promise<void> {
  const catalog = await getCatalog(env);
  if (!catalog.includes(manifest.name)) {
    catalog.push(manifest.name);
    catalog.sort();
    await setCatalog(env, catalog);
  }

  const cats = await getCategories(env);
  for (const c of Object.keys(cats)) {
    cats[c] = cats[c].filter((n) => n !== manifest.name);
  }
  if (!cats[manifest.category]) cats[manifest.category] = [];
  if (!cats[manifest.category].includes(manifest.name)) {
    cats[manifest.category].push(manifest.name);
    cats[manifest.category].sort();
  }
  // prune empty
  for (const c of Object.keys(cats)) {
    if (cats[c].length === 0) delete cats[c];
  }
  await setCategories(env, cats);
}

export async function yankVersion(
  env: Env,
  name: string,
  version: string,
): Promise<boolean> {
  const m = await getManifest(env, name, version);
  if (!m) return false;
  m.yanked = true;
  await putManifest(env, m);
  const versions = await getVersions(env, name);
  const target = versions.find((v) => v.version === version);
  if (target) {
    target.yanked = true;
    await setVersions(env, name, versions);
  }
  await recomputeLatest(env, name);
  return true;
}
