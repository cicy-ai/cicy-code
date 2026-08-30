import { describe, expect, it } from 'vitest';
import { parseAppHash } from './appRoute';

describe('parseAppHash', () => {
  it('opens the default project for a first visit with no hash', () => {
    expect(parseAppHash('')).toEqual({
      view: 'workspace',
      agentId: 'w-1001',
      canonicalHash: '#/project/default',
    });
  });

  it('routes the standalone proxy manager', () => {
    expect(parseAppHash('#/proxy')).toEqual({ view: 'proxy', agentId: '' });
    expect(parseAppHash('#/proxy/nodes')).toEqual({ view: 'proxy', agentId: '' });
  });

  it('preserves explicit agent and project routes', () => {
    expect(parseAppHash('#/agent/w-1010')).toEqual({ view: 'workspace', agentId: 'w-1010' });
    expect(parseAppHash('#/project/music')).toEqual({ view: 'workspace', agentId: 'w-1001' });
  });
});
