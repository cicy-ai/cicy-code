import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const dataAccess = vi.hoisted(() => ({
  getHistoryIDs: vi.fn(),
  loadWindowItems: vi.fn(),
}));
const api = vi.hoisted(() => ({
  getAgentCurrentReply: vi.fn(),
  retryCicyReply: vi.fn(),
}));
const cache = vi.hoisted(() => ({
  historyMemCache: vi.fn(() => new Map()),
  setHistoryMemCache: vi.fn(),
}));

vi.mock('./lib/dataAccess', () => dataAccess);
vi.mock('../../../services/api', () => ({ default: api }));
vi.mock('./lib/cache', () => cache);

import { useCurrentHistory } from './useCurrentHistory';

function HookHarness({ consumeWsDeltas = false }: { consumeWsDeltas?: boolean }) {
  const state = useCurrentHistory({
    paneId: 'w-1',
    open: true,
    promptsOnly: false,
    hideTools: false,
    agentType: 'codex',
    consumeWsDeltas,
  });
  return (
    <div>
      <span data-testid="live-answer">{String(state.liveTurn?.a || '')}</span>
      <span data-testid="pending">{String(state.replyPending)}</span>
    </div>
  );
}

function currentReply(overrides: Record<string, unknown> = {}) {
  return {
    conversation_id: 'conversation-1',
    reply_conversation_id: 'conversation-1',
    history_id: 2,
    turn_id: 'turn-1',
    status: 'completed',
    complete: true,
    answer: '最终回答',
    thinking: '',
    items: [{ type: 'text', text: '最终回答' }],
    updated_at: '2026-08-21T00:00:00Z',
    ...overrides,
  };
}

beforeEach(() => {
  dataAccess.getHistoryIDs.mockReset();
  dataAccess.loadWindowItems.mockReset();
  api.getAgentCurrentReply.mockReset();
  api.retryCicyReply.mockReset();
  cache.setHistoryMemCache.mockReset();
  cache.historyMemCache.mockReturnValue(new Map());
  dataAccess.getHistoryIDs.mockResolvedValue({
    conversation_id: 'conversation-1',
    id: 1,
    model: 'gpt-5',
    prompts: [{ id: 1, ts: '2026-08-21T00:00:00Z', content: '问题' }],
  });
  dataAccess.loadWindowItems.mockResolvedValue({
    lo: 1,
    items: [{
      history_id: 1,
      conversation_id: 'conversation-1',
      role: 'user',
      content: '问题',
    }],
  });
});

describe('useCurrentHistory reply.json synchronization', () => {
  it('re-reads reply.json immediately when a non-cicy agent reaches a terminal status', async () => {
    api.getAgentCurrentReply
      .mockResolvedValueOnce({
        data: currentReply({
          status: 'working',
          complete: false,
          answer: '',
          items: [{ type: 'tool_use', name: 'exec_command', input: { cmd: 'test' } }],
        }),
      })
      .mockResolvedValue({ data: currentReply() });
    render(<HookHarness />);

    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('live-answer')).toHaveTextContent('');
    expect(screen.getByTestId('pending')).toHaveTextContent('true');
    await act(async () => {
      window.dispatchEvent(new CustomEvent('agent-status-change', {
        detail: { agent_id: 'w-1', turn_id: 'turn-1', status: 'completed' },
      }));
    });

    await waitFor(
      () => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(2),
      { timeout: 700 },
    );
    await waitFor(() => expect(screen.getByTestId('live-answer')).toHaveTextContent('最终回答'));
    expect(screen.getByTestId('pending')).toHaveTextContent('false');
  });

  it('re-reads the canonical files when current_updated is forwarded', async () => {
    api.getAgentCurrentReply.mockResolvedValue({ data: currentReply() });
    render(<HookHarness />);

    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(1));
    await act(async () => {
      window.dispatchEvent(new CustomEvent('cicy:current-history-updated', {
        detail: { agent_id: 'w-1', conversation_id: 'conversation-1', turn_id: 'turn-1' },
      }));
    });

    await waitFor(
      () => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(2),
      { timeout: 700 },
    );
  });

  it('does not lose a synchronization event that arrives while reply.json is being read', async () => {
    let resolveSecond!: (value: unknown) => void;
    api.getAgentCurrentReply
      .mockResolvedValueOnce({ data: currentReply() })
      .mockImplementationOnce(() => new Promise((resolve) => { resolveSecond = resolve; }))
      .mockResolvedValue({ data: currentReply({ updated_at: '2026-08-21T00:00:02Z' }) });
    render(<HookHarness />);

    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(1));
    await act(async () => {
      window.dispatchEvent(new CustomEvent('agent-status-change', {
        detail: { agent_id: 'w-1', turn_id: 'turn-1', status: 'working' },
      }));
    });
    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(2));

    await act(async () => {
      window.dispatchEvent(new CustomEvent('agent-status-change', {
        detail: { agent_id: 'w-1', turn_id: 'turn-1', status: 'completed' },
      }));
      await new Promise((resolve) => window.setTimeout(resolve, 220));
      resolveSecond({ data: currentReply({ status: 'working', complete: false }) });
    });

    await waitFor(
      () => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(3),
      { timeout: 700 },
    );
  });

  it('never attaches a reply from an older committed boundary', async () => {
    dataAccess.getHistoryIDs.mockResolvedValue({
      conversation_id: 'conversation-1',
      id: 3,
      model: 'gpt-5',
      prompts: [{ id: 3, ts: '2026-08-21T00:00:00Z', content: '新问题' }],
    });
    dataAccess.loadWindowItems.mockResolvedValue({
      lo: 1,
      items: [{
        history_id: 3,
        conversation_id: 'conversation-1',
        role: 'user',
        content: '新问题',
      }],
    });
    api.getAgentCurrentReply.mockResolvedValue({
      data: currentReply({ history_id: 2, turn_id: 'old-turn', answer: '旧回答' }),
    });
    render(<HookHarness />);

    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('live-answer')).toHaveTextContent('');
    expect(screen.getByTestId('pending')).toHaveTextContent('false');
  });
});
