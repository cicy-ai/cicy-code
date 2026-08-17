import { describe, expect, it } from 'vitest';
import { normalizeRawHistoryItem } from './normalizeItem';

describe('normalizeRawHistoryItem transport failures', () => {
  const failure = '⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC';

  it('drops committed assistant transport diagnostics', () => {
    expect(normalizeRawHistoryItem({ role: 'assistant', content: failure })).toBeNull();
  });

  it('keeps the same text when the user quotes it in a question', () => {
    expect(normalizeRawHistoryItem({ role: 'user', content: failure })).toMatchObject({
      role: 'user',
      q: failure,
    });
  });
});
