// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { formatToolResult, normalizeToolForDisplay, toolHeadline } from './toolFormat';

describe('Codex tool display normalization', () => {
  it('shows the real command instead of the JavaScript orchestration wrapper', () => {
    const tool = normalizeToolForDisplay({
      name: 'exec',
      arg: 'const r = await tools.exec_command({cmd:"rg -n \\\"hello\\\" app/src",workdir:"/tmp"}); text(r.output);',
      result: 'Script completed\nWall time 0.1 seconds\nOutput:\napp/src/a.ts:1:hello',
    });
    expect(tool?.name).toBe('exec_command');
    expect(toolHeadline(tool)).toBe('rg -n "hello" app/src');
    expect(formatToolResult(tool)).toBe('app/src/a.ts:1:hello');
  });

  it('omits wait and empty write_stdin polling calls', () => {
    expect(normalizeToolForDisplay({ name: 'wait', arg: '{"cell_id":"12"}' })).toBeNull();
    expect(normalizeToolForDisplay({
      name: 'exec',
      arg: 'const r = await tools.write_stdin({session_id:7,chars:"",yield_time_ms:1000}); text(r.output);',
    })).toBeNull();
  });

  it('does not render a result body for a successful command with no stdout', () => {
    expect(formatToolResult({
      name: 'exec_command',
      result: 'Script completed\nWall time 0.1 seconds\nProcess exited with code 0\nFinal output:',
    })).toBe('');
  });
});
