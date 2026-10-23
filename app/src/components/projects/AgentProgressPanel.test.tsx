import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';

const api = vi.hoisted(() => ({
  getAgentProgress: vi.fn(),
  refreshAgentProgress: vi.fn(),
  listTodos: vi.fn(),
  updateTodo: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../chat/CurrentHistoryView', () => ({
  default: ({ paneId, agentType }: { paneId: string; agentType: string }) => <div data-testid="term">{`${agentType}:${paneId}`}</div>,
}));

import AgentProgressPanel from './AgentProgressPanel';


describe('AgentProgressPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listTodos.mockResolvedValue({ data: { todos: [
      { id: '1', title: '卡片新增进度 tab', status: 'todo', updated_at: '2026-10-22T03:00:00Z' },
      { id: '2', title: '修复技能页遮挡', status: 'test', body: '已部署', updated_at: '2026-10-22T02:00:00Z' },
      { id: '3', title: '放弃的任务', status: 'dropped' },
      { id: '4', title: '已验收的任务', status: 'done', updated_at: '2026-10-22T01:00:00Z' },
    ] } });
    api.refreshAgentProgress.mockResolvedValue({ data: { success: true, helper: 'w-1090' } });
  });

  it('creates the helper on first open, groups todos and shows its terminal', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: '' } });
    render(<AgentProgressPanel mainId="w-1075:main.0" mainBusy={false} />);
    await waitFor(() => expect(screen.getByTestId('term')).toHaveTextContent('cicy:w-1090'));
    expect(api.refreshAgentProgress).toHaveBeenCalledWith('w-1075');
    expect(document.querySelector('[data-id="agent-progress-doing-w-1075"]')).toHaveTextContent('卡片新增进度 tab');
    expect(document.querySelector('[data-id="agent-progress-review-w-1075"]')).toHaveTextContent('修复技能页遮挡');
    // confirmed group is collapsed by default
    expect(screen.queryByText('已验收的任务')).not.toBeInTheDocument();
    expect(screen.queryByText('放弃的任务')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('修复技能页遮挡'));
    expect(screen.getByText('已部署')).toBeInTheDocument();
  });

  it('refreshes when the main agent goes idle and on click', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    const { rerender } = render(<AgentProgressPanel mainId="w-1075" mainBusy />);
    await waitFor(() => expect(screen.getByTestId('term')).toBeInTheDocument());
    expect(api.refreshAgentProgress).not.toHaveBeenCalled();
    rerender(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-refresh-w-1075"]')).not.toBeDisabled(), { timeout: 3000 });
    fireEvent.click(document.querySelector('[data-id="agent-progress-refresh-w-1075"]') as HTMLElement);
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledTimes(2));
  });

  it('confirms a task manually and can undo it', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    api.updateTodo.mockResolvedValue({ data: {} });
    render(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    const btn = await waitFor(() => { const el = document.querySelector('[data-id="agent-progress-confirm-2"]'); expect(el).not.toBeNull(); return el as HTMLElement; });
    fireEvent.click(btn);
    expect(api.updateTodo).toHaveBeenCalledWith('w-1075', '2', { status: 'done' });
    // in-progress tasks can be confirmed directly too
    fireEvent.click(document.querySelector('[data-id="agent-progress-confirm-1"]') as HTMLElement);
    await waitFor(() => expect(api.updateTodo).toHaveBeenCalledWith('w-1075', '1', { status: 'done' }));
    fireEvent.click(screen.getByText(/已确认 \d/, { selector: 'button' }));
    fireEvent.click(document.querySelector('[data-id="agent-progress-undo-4"]') as HTMLElement);
    await waitFor(() => expect(api.updateTodo).toHaveBeenCalledWith('w-1075', '4', { status: 'test' }));
  });
});
