import { createRef } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const dataAccess = vi.hoisted(() => ({
  getCurrentHistory: vi.fn(),
}));
const api = vi.hoisted(() => ({
  getAgentCurrentReply: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../../AgentAvatar', () => ({
  default: () => <span data-testid="agent-avatar" />,
}));
vi.mock('./lib/dataAccess', () => dataAccess);
vi.mock('../../../services/api', () => ({ default: api }));

import { HistoryList } from './HistoryList';

beforeEach(() => {
  dataAccess.getCurrentHistory.mockReset();
  api.getAgentCurrentReply.mockReset();
  api.getAgentCurrentReply.mockResolvedValue({ data: {} });
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
});

describe('HistoryList pending reply placement', () => {
  it('does not offer the historical lazy-answer toggle for the latest question', () => {
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const turns = [
      { history_id: 9, q: '最后一个问题', text: '最后一个问题', role: 'user' },
    ];

    render(<HistoryList {...({
      items: turns,
      liveTurn: null,
      replyPending: false,
      optimisticQ: null,
      compacting: false,
      displayItems: turns,
      committedMaxId: 9,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-latest-question',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef: { current: 9 },
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: true,
      greeting: '',
    } as any)} />);

    expect(document.querySelector('[data-id="current-history-q-toggle-9"]')).not.toBeInTheDocument();
    expect(screen.getByText('最后一个问题')).toBeInTheDocument();
    // h-5 (20px) clears fade-scroll-y's 16px bottom mask; h-2 left the newest bubble inside it.
    expect(document.querySelector('[data-id="current-history-final-answer-placeholder"]')).toHaveClass('h-5');
  });

  it('does not offer a lazy-answer toggle between adjacent consecutive questions', () => {
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const turns = [
      { history_id: 14, q: '连续补充一', text: '连续补充一', role: 'user' },
      { history_id: 15, q: '连续补充二', text: '连续补充二', role: 'user' },
      { history_id: 16, q: '连续补充三', text: '连续补充三', role: 'user' },
    ];

    render(<HistoryList {...({
      items: turns,
      liveTurn: null,
      replyPending: false,
      optimisticQ: null,
      compacting: false,
      displayItems: turns,
      committedMaxId: 16,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-consecutive-questions',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef: { current: 16 },
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: true,
      greeting: '',
    } as any)} />);

    expect(document.querySelector('[data-id="current-history-q-toggle-14"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="current-history-q-toggle-15"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="current-history-q-toggle-16"]')).not.toBeInTheDocument();
  });

  it('offers a lazy answer toggle for an orphan question in full history', async () => {
    let resolveHistory!: (value: any) => void;
    dataAccess.getCurrentHistory.mockImplementation(() => new Promise((resolve) => { resolveHistory = resolve; }));
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const turns = [
      { history_id: 1, q: '只有问题', text: '只有问题', role: 'user' },
      { history_id: 3, q: '完整问题', text: '完整问题', role: 'user' },
      { history_id: 4, a: '完整回答', text: '完整回答', role: 'assistant' },
    ];

    render(<HistoryList {...({
      items: turns,
      liveTurn: null,
      replyPending: false,
      optimisticQ: null,
      compacting: false,
      displayItems: turns,
      committedMaxId: 4,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-orphan',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef: { current: 3 },
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: true,
      greeting: '',
    } as any)} />);

    const toggle = document.querySelector('[data-id="current-history-q-toggle-1"]') as HTMLElement;
    expect(toggle).toBeInTheDocument();
    expect(document.querySelector('[data-id="current-history-q-toggle-3"]')).not.toBeInTheDocument();

    fireEvent.click(toggle);
    expect(screen.getByText('加载回复…')).toBeInTheDocument();
    await act(async () => {
      resolveHistory({ items: [{ history_id: 2, conversation_id: 'conversation-orphan', role: 'assistant', content: '按需加载的回答' }] });
    });
    await waitFor(() => expect(screen.getByText('按需加载的回答')).toBeInTheDocument());
    expect(dataAccess.getCurrentHistory).toHaveBeenCalledWith('w-1', {
      before: 3,
      limit: 16,
      conversation_id: 'conversation-orphan',
    });
  });

  it('positions a newly opened full history at the latest Q&A', () => {
    const scrollHeight = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(900);
    const clientHeight = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(300);
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const turns = [
      { history_id: 1, q: '最新问题', text: '最新问题', role: 'user' },
      { history_id: 2, a: '最新回答', text: '最新回答', role: 'assistant' },
    ];

    render(<HistoryList {...({
      items: turns,
      liveTurn: null,
      replyPending: false,
      optimisticQ: null,
      compacting: false,
      displayItems: turns,
      committedMaxId: 2,
      loading: false,
      loadingMore: false,
      conversationId: 'conversation-latest',
      pendingUrl: '',
      setPendingUrl: vi.fn(),
      retryingKey: '',
      recapResponses: new Set(),
      handleOutcomeRetry: vi.fn(),
      loadMore: vi.fn(),
      canLoadMore: false,
      scrollRef,
      loadMoreRef,
      optimisticBaselineUserIdRef: { current: 1 },
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: true,
      greeting: '',
    } as any)} />);

    expect(scrollRef.current?.scrollTop).toBe(900);
    clientHeight.mockRestore();
    scrollHeight.mockRestore();
  });

  it('puts the new reply placeholder after the optimistic prompt instead of under the previous completed answer', () => {
    const scrollRef = createRef<HTMLDivElement>();
    const loadMoreRef = createRef<HTMLDivElement>();
    const view = render(<HistoryList {...({
      items: [{ history_id: 1, q: 'previous question', text: 'previous question', role: 'user' }],
      liveTurn: {
        history_id: 2,
        turn_id: 'previous-completed-turn',
        q: '',
        role: 'assistant',
        status: 'completed',
        steps: [{ type: 'text', text: 'previous answer' }],
      },
      replyPending: true,
      optimisticQ: { text: 'new question', ts: Date.now() },
      compacting: false,
      displayItems: [{ history_id: 1, q: 'previous question', text: 'previous question', role: 'user' }],
      committedMaxId: 1,
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
      optimisticBaselineUserIdRef: { current: 1 },
      paneId: 'w-1',
      agentType: 'codex',
      promptsOnly: false,
      hideTools: false,
      fullWidth: true,
      leftAlignQuestions: true,
      greeting: '',
    } as any)} />);

    const previousAnswer = view.container.querySelector('[data-id="current-history-live-turn"]') as HTMLElement;
    const newQuestion = view.container.querySelector('[data-id="current-history-optimistic-q"]') as HTMLElement;
    const newAnswerPlaceholder = view.container.querySelector('[data-id="current-history-optimistic-a"]') as HTMLElement;

    expect(previousAnswer).toBeInTheDocument();
    expect(previousAnswer.querySelector('[data-id="current-history-stream-loading"]')).not.toBeInTheDocument();
    expect(newAnswerPlaceholder).toBeInTheDocument();
    expect(newAnswerPlaceholder.querySelector('[data-id="current-history-stream-loading"]')).toBeInTheDocument();
    expect(view.container.querySelector('[data-id="current-history-final-answer-placeholder"]')).toHaveClass('h-10');
    expect(previousAnswer.compareDocumentPosition(newQuestion) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(newQuestion.compareDocumentPosition(newAnswerPlaceholder) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
