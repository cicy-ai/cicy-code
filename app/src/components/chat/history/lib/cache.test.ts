import { afterEach, describe, expect, it, vi } from 'vitest';
import { openCurrentHistoryToolDB } from './cache';

describe('openCurrentHistoryToolDB', () => {
  const originalIndexedDB = Object.getOwnPropertyDescriptor(window, 'indexedDB');

  afterEach(() => {
    vi.useRealTimers();
    if (originalIndexedDB) Object.defineProperty(window, 'indexedDB', originalIndexedDB);
    else delete (window as Window & { indexedDB?: IDBFactory }).indexedDB;
  });

  it('falls back when IndexedDB open never reports completion', async () => {
    vi.useFakeTimers();
    const stalledRequest = {} as IDBOpenDBRequest;
    Object.defineProperty(window, 'indexedDB', {
      configurable: true,
      value: { open: vi.fn(() => stalledRequest) },
    });

    const result = openCurrentHistoryToolDB();
    let settled = false;
    void result.finally(() => { settled = true; });

    await vi.advanceTimersByTimeAsync(2_000);

    expect(settled).toBe(true);
    await expect(result).resolves.toBeNull();
  });
});
