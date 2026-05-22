// src/lib/semver.ts
//
// Minimal semver comparator. Handles MAJOR.MINOR.PATCH and -prerelease
// (alphanumeric). Enough for our use case; not a full spec implementation.

export function parseSemver(v: string): {
  major: number;
  minor: number;
  patch: number;
  pre: string;
} | null {
  const m = v.match(/^(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z0-9.-]+))?(?:\+[A-Za-z0-9.-]+)?$/);
  if (!m) return null;
  return {
    major: parseInt(m[1], 10),
    minor: parseInt(m[2], 10),
    patch: parseInt(m[3], 10),
    pre: m[4] || '',
  };
}

export function compareSemver(a: string, b: string): number {
  const pa = parseSemver(a);
  const pb = parseSemver(b);
  if (!pa || !pb) return a.localeCompare(b);
  if (pa.major !== pb.major) return pa.major - pb.major;
  if (pa.minor !== pb.minor) return pa.minor - pb.minor;
  if (pa.patch !== pb.patch) return pa.patch - pb.patch;
  // a release version > prerelease (semver §11.4)
  if (!pa.pre && pb.pre) return 1;
  if (pa.pre && !pb.pre) return -1;
  if (!pa.pre && !pb.pre) return 0;
  return pa.pre.localeCompare(pb.pre);
}

export function isValidSemver(v: string): boolean {
  return parseSemver(v) !== null;
}
