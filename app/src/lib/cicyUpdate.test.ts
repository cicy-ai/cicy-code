// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { interpretCicyUpdateResponse } from './cicyUpdate';

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

  it('keeps polling for the detached container updater', () => {
    expect(interpretCicyUpdateResponse({ started: true, target: '2.3.556' }, 'Update failed')).toEqual({
      kind: 'poll',
    });
  });
});
