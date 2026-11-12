import { describe, expect, it } from 'vitest';
import { buildTurnsFromRawItems } from './turns';

describe('buildTurnsFromRawItems tool pairing', () => {
  // A command that printed nothing still has a result record. It used to be
  // drawn as a second, blank tool card ("shell ×2" with no command shown).
  it('folds an empty tool result into its call instead of drawing a blank card', () => {
    const turns = buildTurnsFromRawItems([
      { id: 1, role: 'user', content: [{ type: 'text', text: 'go' }] },
      { id: 2, role: 'assistant', content: [{ type: 'tool_use', id: 't1', name: 'shell', input: { command: 'git add -A' } }] },
      { id: 3, role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: '', is_error: false }] },
    ] as any);
    const tools = turns.flatMap((turn) => (turn.steps || []).filter((step: any) => step.type === 'tool').flatMap((step: any) => step.tools));
    expect(tools).toHaveLength(1);
    expect(tools[0].arg).toContain('git add -A');
  });
});
