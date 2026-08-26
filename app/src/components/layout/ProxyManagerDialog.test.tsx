import { fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const api = vi.hoisted(() => ({
  getProxyStatus: vi.fn(),
  getProxyList: vi.fn(),
  getProxyBindMode: vi.fn(),
  getProxyShellGlobal: vi.fn(),
  setProxyShellGlobal: vi.fn(),
}));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => ({ sendToAgent: vi.fn() }));
vi.mock('../../contexts/AppContext', () => ({
  useApp: () => ({ activeAgentId: 'w-1001' }),
}));

import { ProxyManagerDialog } from './ProxyManagerDialog';

beforeEach(() => {
  vi.clearAllMocks();
  api.getProxyStatus.mockResolvedValue({ data: { success: true, running: true, pid: '123' } });
  api.getProxyList.mockResolvedValue({ data: { groups: [], nodes: [] } });
  api.getProxyBindMode.mockResolvedValue({ data: { allow_lan: false } });
  api.getProxyShellGlobal.mockResolvedValue({
    data: { success: true, enabled: false, path: '/home/cicy/.bashrc', proxy_url: 'http://127.0.0.1:9001' },
  });
  api.setProxyShellGlobal.mockImplementation(async (enabled: boolean) => ({
    data: { success: true, enabled, changed: true, immediate: true, path: '/home/cicy/.bashrc', proxy_url: 'http://127.0.0.1:9001' },
  }));
});

describe('<ProxyManagerDialog /> global Bash proxy', () => {
  it('sets and cancels the managed bashrc proxy with immediate feedback', async () => {
    render(<ProxyManagerDialog open onClose={vi.fn()} paneId="w-1001" />);

    const button = await waitFor(() => {
      const node = document.querySelector('[data-id="proxy-manager-drawer-shell-global"]');
      if (!node) throw new Error('global proxy button missing');
      return node as HTMLButtonElement;
    });
    expect(button).toHaveTextContent('proxyManagerShellGlobalEnable');

    fireEvent.click(button);
    await waitFor(() => expect(api.setProxyShellGlobal).toHaveBeenCalledWith(true));
    await waitFor(() => expect(button).toHaveTextContent('proxyManagerShellGlobalDisable'));
    expect(document.querySelector('[data-id="proxy-manager-drawer-shell-global-result"]')).toHaveTextContent('proxyManagerShellGlobalEnabled');

    fireEvent.click(button);
    await waitFor(() => expect(api.setProxyShellGlobal).toHaveBeenLastCalledWith(false));
    await waitFor(() => expect(button).toHaveTextContent('proxyManagerShellGlobalEnable'));
    expect(document.querySelector('[data-id="proxy-manager-drawer-shell-global-result"]')).toHaveTextContent('proxyManagerShellGlobalDisabled');
  });
});
