import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  Trans: ({ children }: { children?: React.ReactNode }) => children,
  initReactI18next: { type: '3rdParty', init: vi.fn() },
}));

const api = vi.hoisted(() => ({
  getPanes: vi.fn(),
  sendCommand: vi.fn(),
}));
const tmux = vi.hoisted(() => ({ sendCommandToTmux: vi.fn() }));

vi.mock('../services/api', () => ({ default: api }));
vi.mock('../services/mockApi', () => tmux);

import { NetworkSignal } from './Workspace';
import { sendToAgent } from '../services/agentSend';
import { clearAgentSendTarget, setAgentSendTarget } from '../services/agentSendTarget';

const clientId = 'web-w-1001-e2e-client';
const prompt = `My browser page clientId: ${clientId}.`;

function SendFlow({ projectSink = false }: { projectSink?: boolean }) {
  const [projectPrompt, setProjectPrompt] = useState('');
  useEffect(() => {
    if (!projectSink) return;
    const fill = (event: Event) => {
      setProjectPrompt(String((event as CustomEvent).detail?.text || ''));
    };
    window.addEventListener('cicy:fill-project-composer', fill);
    return () => window.removeEventListener('cicy:fill-project-composer', fill);
  }, [projectSink]);
  return (
    <>
      <NetworkSignal
        latency={1}
        connected
        clientId={clientId}
        onSendClientId={() => sendToAgent('w-fallback:main.0', prompt, { submit: true })}
      />
      {projectSink ? <textarea data-id="e2e-project-prompt" readOnly value={projectPrompt} /> : null}
    </>
  );
}

function clickSend() {
  fireEvent.click(document.querySelector('[data-id="network-signal-trigger"]') as HTMLElement);
  clickSendButton();
}

function clickSendButton() {
  fireEvent.click(document.querySelector('[data-id="network-signal-send-client-id"]') as HTMLElement);
}

describe('send-to-Agent UI E2E', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearAgentSendTarget();
    api.getPanes.mockResolvedValue({ data: [] });
    api.sendCommand.mockImplementation((_pane: string, _text: string, _submit: boolean, options?: { deferUntilReady?: boolean }) => (
      options?.deferUntilReady
        ? Promise.resolve({ data: { success: true, queued: true } })
        : new Promise(() => {})
    ));
  });

  it('Team selection queues /api/tmux/send and always releases the sending button', async () => {
    setAgentSendTarget({ source: 'team', paneId: 'w-team:main.0' });
    render(<SendFlow />);

    clickSend();

    await waitFor(() => expect(document.querySelector('[data-id="network-signal-send-client-id-label"]')).toHaveTextContent('networkClientIdSent'));
    expect(api.sendCommand).toHaveBeenCalledWith(
      'w-team:main.0',
      prompt,
      true,
      { deferUntilReady: true },
    );
    expect(document.querySelector('[data-id="network-signal-send-client-id"]')).toBeDisabled();

    clickSendButton();
    expect(api.sendCommand).toHaveBeenCalledTimes(1);

    act(() => setAgentSendTarget({ source: 'team', paneId: 'w-team-2:main.0' }));
    await waitFor(() => expect(document.querySelector('[data-id="network-signal-send-client-id"]')).toBeEnabled());
    clickSendButton();
    await waitFor(() => expect(api.sendCommand).toHaveBeenCalledTimes(2));
    expect(api.sendCommand).toHaveBeenLastCalledWith(
      'w-team-2:main.0',
      prompt,
      true,
      { deferUntilReady: true },
    );
  });

  it('Project selection fills its prompt and never touches the transport', async () => {
    setAgentSendTarget({ source: 'project', paneId: 'w-project:main.0' });
    render(<SendFlow projectSink />);

    clickSend();

    await waitFor(() => expect(document.querySelector('[data-id="e2e-project-prompt"]')).toHaveValue(prompt));
    expect(api.sendCommand).not.toHaveBeenCalled();
    expect(document.querySelector('[data-id="network-signal-send-client-id-label"]')).toHaveTextContent('networkClientIdFilled');
    expect(document.querySelector('[data-id="network-signal-send-client-id"]')).toBeDisabled();
  });

  it('missing selection shows the toast and releases the sending button', async () => {
    const toast = vi.fn();
    window.addEventListener('show-toast', toast);
    render(<SendFlow />);

    clickSend();

    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ detail: '未选中 Agent' })));
    expect(api.sendCommand).not.toHaveBeenCalled();
    expect(document.querySelector('[data-id="network-signal-send-client-id-label"]')).toHaveTextContent('networkSendClientId');
    window.removeEventListener('show-toast', toast);
  });
});
