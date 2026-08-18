import { beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ getPanes: vi.fn(), sendCommand: vi.fn() }));
const tmux = vi.hoisted(() => ({ sendCommandToTmux: vi.fn() }));

vi.mock('./api', () => ({ default: api }));
vi.mock('./mockApi', () => tmux);

import { sendToAgent } from './agentSend';

describe('sendToAgent', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getPanes.mockResolvedValue({ data: [] });
  });

  it('lets the selected project prompt consume contextual sends without transport', async () => {
    const handler = (event: Event) => event.preventDefault();
    window.addEventListener('cicy:route-agent-prompt', handler);
    await sendToAgent('w-102:main.0', '检查任务', { submit: true });
    window.removeEventListener('cicy:route-agent-prompt', handler);

    expect(api.sendCommand).not.toHaveBeenCalled();
    expect(tmux.sendCommandToTmux).not.toHaveBeenCalled();
  });

  it('allows a composer send to bypass prompt routing and submit', async () => {
    api.getPanes.mockResolvedValue({ data: [{ pane_id: 'w-103', agent_type: 'codex' }] });
    tmux.sendCommandToTmux.mockResolvedValue({ success: true, message: '' });

    await sendToAgent('w-103', '正式发送', { submit: true, fromComposer: true });

    expect(tmux.sendCommandToTmux).toHaveBeenCalledWith('正式发送', 'w-103', true);
  });
});
