import { describe, expect, it, vi } from 'vitest';
import { createInflightCoalescer } from './inflight';

describe('createInflightCoalescer', () => {
  it('shares one request for concurrent reads with the same key', async () => {
    let resolve!: (value: string) => void;
    const request = vi.fn(() => new Promise<string>((done) => { resolve = done; }));
    const run = createInflightCoalescer();

    const first = run('same', request);
    const second = run('same', request);

    expect(request).toHaveBeenCalledTimes(1);
    resolve('ok');
    await expect(Promise.all([first, second])).resolves.toEqual(['ok', 'ok']);
  });

  it('starts a fresh request after the prior request settles', async () => {
    const request = vi.fn(async () => 'ok');
    const run = createInflightCoalescer();

    await run('same', request);
    await run('same', request);

    expect(request).toHaveBeenCalledTimes(2);
  });
});
