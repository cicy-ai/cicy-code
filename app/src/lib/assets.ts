// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

const publicAssetPrefix = (import.meta.env.VITE_PUBLIC_ASSET_PREFIX || '').replace(/\/+$/, '');

export function assetUrl(path: string) {
  if (!path) return path;
  if (/^(?:[a-z]+:)?\/\//i.test(path) || path.startsWith('data:')) {
    return path;
  }

  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return publicAssetPrefix ? `${publicAssetPrefix}${normalizedPath}` : normalizedPath;
}
