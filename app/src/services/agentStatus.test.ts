// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { mergeAgentStatusPush } from './agentStatus';

describe('mergeAgentStatusPush', () => {
  it('releases a polled working status immediately when the gateway pushes completed', () => {
    const current = {
      'w-101:main.0': { status: 'working', model: 'gpt-5', contextUsage: 42 },
    };

    expect(mergeAgentStatusPush(current, {
      agent_id: 'w-101',
      status: 'completed',
      updated_at: '2026-08-21T09:00:00Z',
    })).toEqual({
      'w-101:main.0': {
        status: 'completed',
        model: 'gpt-5',
        contextUsage: 42,
        agent_id: 'w-101',
        updated_at: '2026-08-21T09:00:00Z',
      },
    });
  });

  it('uses the active agent for legacy idle pushes without agent_id', () => {
    expect(mergeAgentStatusPush({}, { status: 'idle' }, 'w-102:main.0')).toEqual({
      'w-102:main.0': { status: 'idle' },
    });
  });
});
