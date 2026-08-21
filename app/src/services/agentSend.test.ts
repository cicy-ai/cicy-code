import { beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ getPanes: vi.fn(), sendCommand: vi.fn() }));
const tmux = vi.hoisted(() => ({ sendCommandToTmux: vi.fn() }));

vi.mock('./api', () => ({ default: api }));
vi.mock('./mockApi', () => tmux);

import { sendToAgent } from './agentSend';
import { clearAgentSendTarget, setAgentSendTarget } from './agentSendTarget';

describe('sendToAgent', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearAgentSendTarget();
    api.getPanes.mockResolvedValue({ data: [] });
    api.sendCommand.mockResolvedValue({ data: { success: true } });
  });

  it('blocks contextual sends and shows a toast when no Agent is selected', async () => {
    const toast = vi.fn();
    window.addEventListener('show-toast', toast);

    const outcome = await sendToAgent('w-102:main.0', '检查任务', { submit: true });

    window.removeEventListener('show-toast', toast);
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ detail: '未选中 Agent' }));
    expect(outcome).toBe(false);
    expect(api.sendCommand).not.toHaveBeenCalled();
    expect(tmux.sendCommandToTmux).not.toHaveBeenCalled();
  });

  it('sends contextual prompts directly through /api/tmux/send for a Team selection', async () => {
    setAgentSendTarget({ source: 'team', paneId: 'w-201:main.0' });

    const outcome = await sendToAgent('w-102:main.0', '检查任务', { submit: false });

    expect(outcome).toBe(true);
    expect(api.sendCommand).toHaveBeenCalledWith(
      'w-201:main.0',
      '检查任务',
      true,
      { deferUntilReady: true },
    );
    expect(tmux.sendCommandToTmux).not.toHaveBeenCalled();
  });

  it('fills the active Project Agent prompt without transport', async () => {
    setAgentSendTarget({ source: 'project', paneId: 'w-202:main.0' });
    const fill = vi.fn();
    window.addEventListener('cicy:fill-project-composer', fill);

    const outcome = await sendToAgent('w-102:main.0', '检查任务', { submit: true });

    window.removeEventListener('cicy:fill-project-composer', fill);
    expect(fill).toHaveBeenCalledWith(expect.objectContaining({
      detail: { paneId: 'w-202:main.0', text: '检查任务' },
    }));
    expect(outcome).toBe(true);
    expect(api.sendCommand).not.toHaveBeenCalled();
    expect(tmux.sendCommandToTmux).not.toHaveBeenCalled();
  });

  it('allows a composer send to bypass prompt routing and submit', async () => {
    api.getPanes.mockResolvedValue({ data: [{ pane_id: 'w-103', agent_type: 'codex' }] });
    tmux.sendCommandToTmux.mockResolvedValue({ success: true, message: '' });

    await sendToAgent('w-103', '正式发送', { submit: true, fromComposer: true });

    expect(tmux.sendCommandToTmux).toHaveBeenCalledWith('正式发送', 'w-103', true);
  });

  it('allows an explicit worker target to bypass the global selection', async () => {
    setAgentSendTarget({ source: 'project', paneId: 'w-202:main.0' });
    tmux.sendCommandToTmux.mockResolvedValue({ success: true, message: '' });

    await sendToAgent('w-301:main.0', '处理分配任务', {
      submit: true,
      agentType: 'codex',
      routing: 'explicit',
    });

    expect(tmux.sendCommandToTmux).toHaveBeenCalledWith('处理分配任务', 'w-301:main.0', true);
    expect(api.sendCommand).not.toHaveBeenCalled();
  });

  it('marks a terminal prompt for deferred delivery while the Agent is starting', async () => {
    tmux.sendCommandToTmux.mockResolvedValue({ success: true, message: '' });

    await sendToAgent('w-104:main.0', '请处理所有的待治理条目', {
      submit: true,
      agentType: 'codex',
      fromComposer: true,
      deferUntilReady: true,
    });

    expect(tmux.sendCommandToTmux).toHaveBeenCalledWith(
      '请处理所有的待治理条目',
      'w-104:main.0',
      true,
      { deferUntilReady: true },
    );
  });
});
