// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { interpretCicyUpdateResponse, shouldAutoUpdate } from './cicyUpdate';

describe('interpretCicyUpdateResponse', () => {
  it('preserves the backend error when the update did not start', () => {
    expect(interpretCicyUpdateResponse({ started: false, error: 'checksum failed' }, 'Update failed')).toEqual({
      kind: 'failed',
      message: 'checksum failed',
    });
  });

  it('uses the localized fallback when the backend omitted an error', () => {
    expect(interpretCicyUpdateResponse({ started: false }, 'Update failed')).toEqual({
      kind: 'failed',
      message: 'Update failed',
    });
  });

  it('identifies a completed local-bin install that needs a manual restart', () => {
    expect(interpretCicyUpdateResponse({
      started: true,
      completed: true,
      restart_required: true,
      target: '2.3.556',
    }, 'Update failed')).toEqual({
      kind: 'restart-required',
      target: '2.3.556',
    });
  });

  it('keeps the exact target while polling a restarting updater', () => {
    expect(interpretCicyUpdateResponse({ started: true, target: '2.3.556' }, 'Update failed')).toEqual({
      kind: 'poll',
      target: '2.3.556',
    });
  });

  it('rejects a started response that omits the exact target version', () => {
    expect(interpretCicyUpdateResponse({ started: true }, 'Update failed')).toEqual({
      kind: 'failed',
      message: 'Update failed',
    });
  });
});

describe('shouldAutoUpdate', () => {
  const base = { enabled: true, hasUpdate: true, updating: false, target: '2.3.593', attempted: '' };

  it('fires for a newly published version when auto-update is on', () => {
    expect(shouldAutoUpdate(base)).toBe(true);
  });

  it('stays off until the user opts in', () => {
    expect(shouldAutoUpdate({ ...base, enabled: false })).toBe(false);
  });

  it('waits for a published update', () => {
    expect(shouldAutoUpdate({ ...base, hasUpdate: false })).toBe(false);
  });

  it('does not stack a second install on a running one', () => {
    expect(shouldAutoUpdate({ ...base, updating: true })).toBe(false);
  });

  // The badge is still lit after a failed install, so without this the effect
  // would reinstall the same version on every render.
  it('attempts a given version only once', () => {
    expect(shouldAutoUpdate({ ...base, attempted: '2.3.593' })).toBe(false);
  });

  it('re-arms when a newer version is published', () => {
    expect(shouldAutoUpdate({ ...base, target: '2.3.594', attempted: '2.3.593' })).toBe(true);
  });

  it('does nothing until the version check reports a target', () => {
    expect(shouldAutoUpdate({ ...base, target: '' })).toBe(false);
    expect(shouldAutoUpdate({ ...base, target: '   ' })).toBe(false);
  });
});
