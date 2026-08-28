import { describe, expect, it } from 'vitest';
import { cloudInstanceOnline } from './ProjectsPanel';

describe('cloudInstanceOnline', () => {
  const now = new Date();
  it('accepts the cloud format (UTC without zone)', () => {
    const cloud = now.toISOString().slice(0, 19).replace('T', ' ');
    expect(cloudInstanceOnline({ status: 'online', lastSeenAt: cloud })).toBe(true);
  });
  it('accepts the hub format (RFC3339 with Z) — regression: was parsed as NaN', () => {
    expect(cloudInstanceOnline({ status: 'online', lastSeenAt: now.toISOString() })).toBe(true);
  });
  it('is offline when stale or not reported online', () => {
    const old = new Date(now.getTime() - 10 * 60_000).toISOString();
    expect(cloudInstanceOnline({ status: 'online', lastSeenAt: old })).toBe(false);
    expect(cloudInstanceOnline({ status: 'offline', lastSeenAt: now.toISOString() })).toBe(false);
    expect(cloudInstanceOnline({ status: 'online', lastSeenAt: '' })).toBe(false);
  });
});
