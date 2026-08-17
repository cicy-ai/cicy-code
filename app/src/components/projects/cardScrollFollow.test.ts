// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { resolveCardScrollFollow } from './cardScrollFollow';

describe('resolveCardScrollFollow', () => {
  it('preserves detached state when a programmatic layout event sees loading', () => {
    expect(resolveCardScrollFollow(false, true, false)).toBe(false);
  });

  it('follows when user scrolling leaves loading inside the viewport', () => {
    expect(resolveCardScrollFollow(false, true, true)).toBe(true);
  });

  it('detaches when user scrolling leaves loading outside the viewport', () => {
    expect(resolveCardScrollFollow(true, false, true)).toBe(false);
  });
});
