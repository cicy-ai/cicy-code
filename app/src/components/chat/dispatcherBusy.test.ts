import { describe, expect, it } from 'vitest';
import { nextDispatcherBusy } from './dispatcherBusy';

describe('nextDispatcherBusy', () => {
  it('enters busy from the shared history poll', () => {
    expect(nextDispatcherBusy(false, { busy: true })).toBe(true);
  });

  it('only clears busy for an authoritative terminal snapshot', () => {
    expect(nextDispatcherBusy(true, { busy: false })).toBe(true);
    expect(nextDispatcherBusy(true, { busy: false, terminal: true })).toBe(false);
  });
});
