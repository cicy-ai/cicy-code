import { describe, expect, it } from 'vitest';
import { prepareRenderTurns } from './renderTurns';
import type { HistoryTurn } from '../types';

const tool = (id: number, name = 'Bash'): HistoryTurn => ({ history_id: id, role: 'assistant', q: '', a: '', steps: [{ type: 'tool', tools: [{ name, arg: 'x', result: 'ok' }] }] });
const sys = (id: number, text: string): HistoryTurn => ({ history_id: id, role: 'system', q: '', text, a: '', steps: [] });
const user = (id: number, q: string): HistoryTurn => ({ history_id: id, role: 'user', q, text: q, a: '', steps: [] });

describe('prepareRenderTurns', () => {
  it('drops noise, merges tool rounds across interleaved reminders, and coalesces the rest after the run', () => {
    const out = prepareRenderTurns([
      user(1, 'hi'),
      tool(2),
      sys(3, '<total_tokens>9 tokens left</total_tokens>'),
      tool(4),
      sys(5, "Only you see that command's output — the user's terminal shows at most a few lines of it."),
      tool(6, 'Read'),
      sys(7, "As you answer the user's questions, you can use the following context:\n# claudeMd\nstuff"),
      tool(8),
      sys(9, 'Some other instruction.'),
      { history_id: 10, role: 'assistant', q: '', a: 'done', steps: [{ type: 'text', text: 'done' }] },
    ]);
    expect(out.map((t) => t.role)).toEqual(['user', 'assistant', 'system', 'assistant']);
    expect(out[1].steps).toHaveLength(4);
    expect(out[2].notices?.map((n) => n.kind)).toEqual(['context', 'notice']);
  });

  it('surfaces a mid-turn user message as a user turn tagged steer', () => {
    const out = prepareRenderTurns([
      tool(1),
      sys(2, 'The user sent a new message while you were working:\n他真的是个垃圾\n\nThis is how Claude Code surfaces messages the user sends mid-turn — within the running turn.'),
      tool(3),
    ]);
    expect(out.map((t) => t.role)).toEqual(['assistant', 'user', 'assistant']);
    expect(out[1].q).toBe('他真的是个垃圾');
    expect(out[1].steer).toBe(true);
  });

  it('leaves cicy outcome records and compaction summaries untouched', () => {
    const outcome: HistoryTurn = { history_id: 1, role: 'assistant', q: '', a: '已停止生成', text: '已停止生成', steps: [], outcome: 'cancelled' };
    const out = prepareRenderTurns([outcome]);
    expect(out[0]).toBe(outcome);
  });
});

describe('prepareRenderTurns interrupt echo', () => {
  it("renders Claude's interrupt echo as a cancelled outcome instead of a question", () => {
    const out = prepareRenderTurns([
      { history_id: 1, role: 'user', q: '跑测试', text: '跑测试', a: '', steps: [] },
      { history_id: 2, role: 'user', q: '[Request interrupted by user]', text: '[Request interrupted by user]', a: '', steps: [] },
    ]);
    expect(out[1].role).toBe('assistant');
    expect(out[1].outcome).toBe('cancelled');
  });
});
