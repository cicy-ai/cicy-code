import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';

const api = vi.hoisted(() => ({
  getAgentProgress: vi.fn(),
  refreshAgentProgress: vi.fn(),
  listTodos: vi.fn(),
  updateTodo: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));
const send = vi.hoisted(() => ({ sendToAgent: vi.fn() }));
vi.mock('../../services/agentSend', () => send);
vi.mock('../chat/CurrentHistoryView', () => ({
  default: ({ paneId, agentType }: { paneId: string; agentType: string }) => <div data-testid="term">{`${agentType}:${paneId}`}</div>,
}));

import AgentProgressPanel from './AgentProgressPanel';


describe('AgentProgressPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    api.listTodos.mockResolvedValue({ data: { todos: [
      { id: '1', title: '卡片新增进度 tab', status: 'todo', updated_at: '2026-10-22T03:00:00Z' },
      { id: '2', title: '修复技能页遮挡', status: 'test', body: '已部署', updated_at: '2026-10-22T02:00:00Z' },
      { id: '3', title: '放弃的任务', status: 'dropped' },
      { id: '4', title: '已验收的任务', status: 'done', updated_at: '2026-10-22T01:00:00Z' },
    ] } });
    api.refreshAgentProgress.mockResolvedValue({ data: { success: true, helper: 'w-1090' } });
  });

  it('creates the helper on first open, groups todos and keeps its log folded', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: '' } });
    render(<AgentProgressPanel mainId="w-1075:main.0" mainBusy={false} />);
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledWith('w-1075', false));
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-doing-w-1075"]')).not.toBeNull());
    // the helper's working log is noise: folded until asked for
    expect(screen.queryByTestId('term')).not.toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="agent-progress-log-toggle-w-1075"]') as HTMLElement);
    await waitFor(() => expect(screen.getByTestId('term')).toHaveTextContent('cicy:w-1090'));
    expect(document.querySelector('[data-id="agent-progress-doing-w-1075"]')).toHaveTextContent('卡片新增进度 tab');
    expect(document.querySelector('[data-id="agent-progress-review-w-1075"]')).toHaveTextContent('修复技能页遮挡');
    // confirmed group is collapsed by default
    expect(screen.queryByText('已验收的任务')).not.toBeInTheDocument();
    expect(screen.queryByText('放弃的任务')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('修复技能页遮挡'));
    expect(screen.getByText('已部署')).toBeInTheDocument();
  });

  it('catches up on open, when the main agent goes idle, and on click', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090', busy: false, updated_at: '2026-10-22T03:00:00Z' } });
    const { rerender } = render(<AgentProgressPanel mainId="w-1075" mainBusy />);
    // opening the tab asks for an automatic catch-up (the server skips it when nothing is new)
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledTimes(1));
    expect(api.refreshAgentProgress).toHaveBeenLastCalledWith('w-1075', true);
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-status-w-1075"]')).toHaveTextContent('更新于'));
    rerender(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledTimes(2));
    expect(api.refreshAgentProgress).toHaveBeenLastCalledWith('w-1075', true);
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-refresh-w-1075"]')).not.toBeDisabled(), { timeout: 3000 });
    fireEvent.click(document.querySelector('[data-id="agent-progress-refresh-w-1075"]') as HTMLElement);
    await waitFor(() => expect(api.refreshAgentProgress).toHaveBeenCalledTimes(3));
    expect(api.refreshAgentProgress).toHaveBeenLastCalledWith('w-1075', false);
  });

  it('says so while the helper works, and does not pretend to be working when idle and empty', async () => {
    api.listTodos.mockResolvedValue({ data: { todos: [] } });
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    api.refreshAgentProgress.mockResolvedValue({ data: { success: true, helper: 'w-1090', busy: true } });
    const view = render(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-status-w-1075"]')).toHaveTextContent('进度助手正在处理'));
    expect(document.querySelector('[data-id="agent-progress-empty-w-1075"]')).toHaveTextContent('正在整理对话');
    expect(document.querySelector('[data-id="agent-progress-refresh-w-1075"]')).toBeDisabled();
    view.unmount();

    api.refreshAgentProgress.mockResolvedValue({ data: { success: true, helper: 'w-1090', busy: false, up_to_date: true } });
    render(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-empty-w-1075"]')).toHaveTextContent('还没有任务记录'), { timeout: 3000 });
  });

  it('reports a failed list load instead of an empty list', async () => {
    api.listTodos.mockRejectedValue(new Error('boom'));
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    render(<AgentProgressPanel mainId="w-1075" mainBusy={false} />);
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-load-error-w-1075"]')).toHaveTextContent('加载失败'));
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

  it('sends what is typed to the helper, not the main agent, and opens its log', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    send.sendToAgent.mockResolvedValue(true);
    render(<AgentProgressPanel mainId="w-1075:main.0" mainBusy={false} />);
    const input = document.querySelector('[data-id="agent-progress-input-w-1075"]') as HTMLTextAreaElement;
    const button = document.querySelector('[data-id="agent-progress-send-w-1075"]') as HTMLButtonElement;
    await waitFor(() => expect(input).not.toBeDisabled());
    expect(button).toBeDisabled();
    fireEvent.change(input, { target: { value: '第 2 条做完了吗' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(send.sendToAgent).toHaveBeenCalledTimes(1));
    const [target, text, opts] = send.sendToAgent.mock.calls[0];
    expect(target).toBe('w-1090');
    expect(text).toContain('第 2 条做完了吗');
    expect(text).toContain('--helper w-1090');
    expect(opts).toMatchObject({ submit: true, agentType: 'cicy', fromComposer: true });
    await waitFor(() => expect(input).toHaveValue(''));
    // the reply shows up in the helper's conversation, so it is opened
    expect(screen.getByTestId('term')).toBeInTheDocument();
    // while the helper works, a second message is held back
    fireEvent.change(input, { target: { value: '再问一句' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(send.sendToAgent).toHaveBeenCalledTimes(1);
  });

  it('keeps the text and says why when sending to the helper fails', async () => {
    api.getAgentProgress.mockResolvedValue({ data: { helper: 'w-1090' } });
    send.sendToAgent.mockRejectedValue(new Error('boom'));
    render(<AgentProgressPanel mainId="w-1075:main.0" mainBusy={false} />);
    const input = document.querySelector('[data-id="agent-progress-input-w-1075"]') as HTMLTextAreaElement;
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.change(input, { target: { value: '你好' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(document.querySelector('[data-id="agent-progress-send-error-w-1075"]')).toHaveTextContent('boom'));
    expect(input).toHaveValue('你好');
  });
});
