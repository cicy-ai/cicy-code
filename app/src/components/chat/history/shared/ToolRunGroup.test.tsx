import { createRef } from 'react';
import { fireEvent, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../../../AgentAvatar', () => ({
  default: () => <span data-testid="agent-avatar" />,
}));

import { HistoryList } from '../HistoryList';
import type { HistoryTurn } from '../types';
import { AssistantTurnView } from './AssistantTurnView';

function tool(id: string, command: string) {
  return {
    name: 'exec_command',
    tool_id: id,
    arg: JSON.stringify({ command }),
    result: `${command} complete`,
  };
}

function turnWithContinuousTools(tools: ReturnType<typeof tool>[], historyId = 42): HistoryTurn {
  return {
    history_id: historyId,
    q: '',
    role: 'assistant',
    status: 'completed',
    steps: [
      { type: 'text', text: 'before tools' },
      { type: 'tool', tools: tools.slice(0, 2) },
      { type: 'tool', tools: tools.slice(2) },
    ],
  };
}

function turnWithSeparatedToolRuns(tools: ReturnType<typeof tool>[], historyId = 42): HistoryTurn {
  return {
    ...turnWithContinuousTools(tools, historyId),
    steps: [
      { type: 'text', text: 'before tools' },
      { type: 'tool', tools: tools.slice(0, 2) },
      { type: 'text', text: 'between the first two runs' },
      { type: 'tool', tools: tools.slice(2, 3) },
      { type: 'thinking', text: 'between the final two runs' },
      { type: 'tool', tools: tools.slice(3) },
    ],
  };
}

function toolOnlyTurn(entry: ReturnType<typeof tool>, historyId: number): HistoryTurn {
  return {
    history_id: historyId,
    q: '',
    role: 'assistant',
    status: 'completed',
    steps: [{ type: 'tool', tools: [entry] }],
  };
}

function assistantTextTurn(text: string, historyId: number, type: 'text' | 'thinking' = 'text'): HistoryTurn {
  return {
    history_id: historyId,
    q: '',
    role: 'assistant',
    status: 'completed',
    steps: [{ type, text }],
  };
}

function renderCommittedHistory(displayItems: HistoryTurn[]) {
  const scrollRef = createRef<HTMLDivElement>();
  const loadMoreRef = createRef<HTMLDivElement>();
  return render(<HistoryList {...({
    items: displayItems,
    liveTurn: null,
    replyPending: false,
    optimisticQ: null,
    compacting: false,
    displayItems,
    committedMaxId: Number(displayItems.at(-1)?.history_id || 0),
    loading: false,
    loadingMore: false,
    conversationId: 'conversation-committed-tools',
    pendingUrl: '',
    setPendingUrl: vi.fn(),
    retryingKey: '',
    recapResponses: new Set(),
    handleOutcomeRetry: vi.fn(),
    loadMore: vi.fn(),
    canLoadMore: false,
    scrollRef,
    loadMoreRef,
    optimisticBaselineUserIdRef: { current: 0 },
    paneId: 'w-1',
    agentType: 'codex',
    promptsOnly: false,
    hideTools: false,
    fullWidth: true,
    leftAlignQuestions: false,
    greeting: '',
  } as any)} />);
}

function toolCards(container: HTMLElement) {
  return Array.from(container.querySelectorAll<HTMLElement>('[data-id="current-history-tool-card"]'));
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
});

describe('tool run grouping', () => {
  it('collapses adjacent tool-only assistant turns into one historical run', () => {
    const view = renderCommittedHistory([
      toolOnlyTurn(tool('cross-turn-first', 'one'), 101),
      toolOnlyTurn(tool('cross-turn-second', 'two'), 102),
      toolOnlyTurn(tool('cross-turn-third', 'three'), 103),
    ]);

    const groups = view.container.querySelectorAll('[data-id="current-history-tool-run-group"]');
    expect(groups).toHaveLength(1);
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveTextContent('3');
    expect(toolCards(view.container)).toHaveLength(1);
    expect(toolCards(view.container)[0]).toHaveTextContent('three');

    fireEvent.click(view.container.querySelector('[data-id="current-history-tool-run-toggle"]') as HTMLButtonElement);
    expect(toolCards(view.container)).toHaveLength(3);
  });

  it('starts a new historical tool run after text or thinking between assistant turns', () => {
    const view = renderCommittedHistory([
      toolOnlyTurn(tool('before-text-first', 'one'), 201),
      toolOnlyTurn(tool('before-text-second', 'two'), 202),
      assistantTextTurn('visible answer between runs', 203),
      toolOnlyTurn(tool('before-thinking-first', 'three'), 204),
      toolOnlyTurn(tool('before-thinking-second', 'four'), 205),
      assistantTextTurn('visible thinking between runs', 206, 'thinking'),
      toolOnlyTurn(tool('after-thinking-first', 'five'), 207),
      toolOnlyTurn(tool('after-thinking-second', 'six'), 208),
    ]);

    const groups = Array.from(view.container.querySelectorAll<HTMLElement>('[data-id="current-history-tool-run-group"]'));
    expect(groups).toHaveLength(3);
    expect(groups.map((group) => group.querySelector('[data-id="current-history-tool-run-count"]')?.textContent)).toEqual(['×2', '×2', '×2']);
    expect(toolCards(view.container)).toHaveLength(3);
    expect(toolCards(view.container).map((card) => card.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('two'),
      expect.stringContaining('four'),
      expect.stringContaining('six'),
    ]));
  });

  it('keeps the newest tool and its toggle in place while expanding and collapsing a run', () => {
    const tools = [tool('first', 'first'), tool('second', 'second'), tool('third', 'third')];
    const view = render(
      <AssistantTurnView
        turn={turnWithContinuousTools(tools)}
        turnKey={42}
        isLatestTurn
        showAvatar
        agentType="codex"
        paneId="w-1"
        hideTools={false}
      />,
    );

    expect(toolCards(view.container)).toHaveLength(1);
    expect(toolCards(view.container)[0]).toHaveAttribute('data-tool-id', 'tool:third');
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveTextContent('3');
    expect(view.container.querySelector('[data-id="current-history-tool-run-toggle"]')).toBeInTheDocument();
    expect(view.container.querySelector('[data-id="current-history-tool-toggle-expand"]')).not.toBeInTheDocument();

    fireEvent.click(view.container.querySelector('[data-id="current-history-tool-run-toggle"]') as HTMLButtonElement);

    expect(toolCards(view.container)).toHaveLength(3);
    expect(toolCards(view.container).map((card) => card.dataset.toolId)).toEqual([
      'tool:third',
      'tool:first',
      'tool:second',
    ]);
    const firstVisibleCard = toolCards(view.container)[0];
    const collapse = firstVisibleCard.querySelector('[data-id="current-history-tool-run-toggle"]') as HTMLButtonElement;
    expect(collapse).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(collapse);

    expect(toolCards(view.container)).toHaveLength(1);
    expect(toolCards(view.container)[0]).toHaveAttribute('data-tool-id', 'tool:third');
  });

  it('splits committed tool runs whenever text or thinking appears between them', () => {
    const tools = [
      tool('first-run-a', 'one'),
      tool('first-run-b', 'two'),
      tool('second-run', 'three'),
      tool('third-run', 'four'),
    ];
    const view = render(
      <AssistantTurnView
        turn={turnWithSeparatedToolRuns(tools)}
        turnKey={42}
        isLatestTurn
        showAvatar
        agentType="codex"
        paneId="w-1"
        hideTools={false}
      />,
    );

    const groups = Array.from(view.container.querySelectorAll<HTMLElement>('[data-id="current-history-tool-run-group"]'));
    expect(groups).toHaveLength(3);
    expect(groups.map((group) => group.querySelector('[data-id="current-history-tool-run-count"]')?.textContent || '')).toEqual(['×2', '×1', '×1']);
    expect(toolCards(view.container).map((card) => card.dataset.toolId)).toEqual(['tool:first-run-b', 'tool:second-run', 'tool:third-run']);

    const body = view.container.querySelector('[data-id="current-history-turn-assistant-body-42"]') as HTMLElement;
    expect(Array.from(body.children).map((node) => node.getAttribute('data-id'))).toEqual([
      'current-history-turn-step-text-42-0',
      'current-history-tool-run-group',
      'current-history-turn-step-text-42-2',
      'current-history-tool-run-group',
      'current-history-turn-step-thinking-42-4',
      'current-history-tool-run-group',
    ]);
  });

  it('uses the same collapsed tool run for the live reply tail', () => {
    const tools = [tool('live-first', 'one'), tool('live-second', 'two'), tool('live-third', 'three')];
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const optimisticBaselineUserIdRef = { current: 0 };
    const view = render(<HistoryList {...({
      items: [],
      liveTurn: turnWithContinuousTools(tools, 84),
      replyPending: false,
      optimisticQ: null,
      compacting: false,
      displayItems: [],
      committedMaxId: 83,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-1',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef,
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: false,
      greeting: '',
    } as any)} />);

    expect(toolCards(view.container)).toHaveLength(1);
    expect(toolCards(view.container)[0]).toHaveAttribute('data-tool-id', 'tool:live-third');
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveTextContent('3');
  });

  it('keeps a live tool run expanded when tool continuations advance its history slot', () => {
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const optimisticBaselineUserIdRef = { current: 0 };
    const propsFor = (historyId: number, tools: ReturnType<typeof tool>[]) => ({
      items: [],
      liveTurn: { ...turnWithContinuousTools(tools, historyId), turn_id: 'stable-live-turn' },
      replyPending: true,
      optimisticQ: null,
      compacting: false,
      displayItems: [],
      committedMaxId: historyId - 1,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-1',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef,
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: false,
      greeting: '',
    } as any);
    const first = tool('stable-first', 'one');
    const second = tool('stable-second', 'two');
    const third = tool('stable-third', 'three');
    const fourth = tool('stable-fourth', 'four');
    const view = render(<HistoryList {...propsFor(200, [first, second, third])} />);

    expect(view.container.querySelector('[data-id="current-history-tool-run-group"]')).toHaveAttribute('data-tool-run-id', 'tool-run:stable-live-turn');
    fireEvent.click(view.container.querySelector('[data-id="current-history-tool-run-toggle"]') as HTMLButtonElement);
    expect(toolCards(view.container)).toHaveLength(3);

    view.rerender(<HistoryList {...propsFor(208, [first, second, third, fourth])} />);

    expect(view.container.querySelector('[data-id="current-history-tool-run-group"]')).toHaveAttribute('data-expanded', 'true');
    expect(toolCards(view.container)).toHaveLength(4);
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveTextContent('4');
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveClass('tool-run-count-pop');
  });

  it('updates to the newest tool and animates the count when a live run grows', () => {
    const first = tool('first-growing', 'first');
    const second = tool('second-growing', 'second');
    const third = tool('third-growing', 'third');
    const view = render(
      <AssistantTurnView
        turn={turnWithContinuousTools([first, second], 142)}
        turnKey="live-142"
        isLatestTurn
        showAvatar
        agentType="codex"
        paneId="w-1"
        hideTools={false}
      />,
    );

    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).not.toHaveClass('tool-run-count-pop');

    view.rerender(
      <AssistantTurnView
        turn={turnWithContinuousTools([first, second, third], 142)}
        turnKey="live-142"
        isLatestTurn
        showAvatar
        agentType="codex"
        paneId="w-1"
        hideTools={false}
      />,
    );

    expect(toolCards(view.container)).toHaveLength(1);
    expect(toolCards(view.container)[0]).toHaveAttribute('data-tool-id', 'tool:third-growing');
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveTextContent('3');
    expect(view.container.querySelector('[data-id="current-history-tool-run-count"]')).toHaveClass('tool-run-count-pop');
    expect(view.container.querySelector('[data-id="current-history-tool-run-latest"]')).toHaveClass('tool-run-latest-in');
  });
});
