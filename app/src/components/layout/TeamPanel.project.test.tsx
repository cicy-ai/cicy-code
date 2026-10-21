// SPDX-License-Identifier: Apache-2.0
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const api = vi.hoisted(() => ({
  listGroups: vi.fn(),
  addGroupPane: vi.fn(),
  removeGroupPane: vi.fn(),
  getAgentCurrentReplyBatch: vi.fn(),
}));

vi.mock('../../services/api', () => ({
  default: new Proxy(api as Record<string, any>, {
    // Any API the panel touches but this test doesn't care about resolves empty.
    get: (target, key: string) => target[key] ?? vi.fn().mockResolvedValue({ data: {} }),
  }),
}));

vi.mock('../../contexts/AppContext', () => ({ useApp: () => ({ chatWsConnected: false, agentTypeOptions: [] }) }));

import TeamPanel from './TeamPanel';

const panes = [
  { pane_id: 'w-1001:main.0', title: '知识专员', agent_type: 'cicy' },
  { pane_id: 'w-1073:main.0', title: 'cat-im', agent_type: 'claude' },
];

describe('<TeamPanel /> 加入项目', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getAgentCurrentReplyBatch.mockResolvedValue({ data: {} });
    api.listGroups.mockResolvedValue({ data: { groups: [
      { id: 7, name: 'tg', pane_ids: ['w-1073:main.0'] },
      { id: 9, name: 'cicy-code', pane_ids: [] },
    ] } });
    api.addGroupPane.mockResolvedValue({ data: { success: true } });
    api.removeGroupPane.mockResolvedValue({ data: { success: true } });
  });

  it('lists projects with current membership and adds / removes the agent', async () => {
    const changed = vi.fn();
    window.addEventListener('cicy:project-groups-changed', changed);
    render(<TeamPanel paneId="w-1001" panes={panes as any} bindings={[]} onRefreshPanes={vi.fn()} onRefreshPoll={vi.fn()} />);

    const card = await screen.findByText('cat-im');
    const worker = card.closest('[data-id="team-panel-worker-w-1073"]') as HTMLElement;
    fireEvent.click(worker.querySelector('[data-id="team-panel-worker-menu-button"]')!);
    fireEvent.click(worker.querySelector('[data-id="team-panel-worker-menu-project"]')!);

    const tg = await waitFor(() => {
      const node = worker.querySelector('[data-id="team-panel-worker-menu-project-7"]');
      if (!node) throw new Error('project list not loaded');
      return node as HTMLElement;
    });
    const cicyCode = worker.querySelector('[data-id="team-panel-worker-menu-project-9"]') as HTMLElement;
    expect(tg).toHaveAttribute('aria-checked', 'true');
    expect(cicyCode).toHaveAttribute('aria-checked', 'false');

    fireEvent.click(cicyCode);
    await waitFor(() => expect(api.addGroupPane).toHaveBeenCalledWith(9, 'w-1073:main.0', 'add'));
    await waitFor(() => expect(cicyCode).toHaveAttribute('aria-checked', 'true'));

    fireEvent.click(tg);
    await waitFor(() => expect(api.removeGroupPane).toHaveBeenCalledWith(7, 'w-1073:main.0'));
    await waitFor(() => expect(tg).toHaveAttribute('aria-checked', 'false'));
    expect(changed).toHaveBeenCalledTimes(2);
    window.removeEventListener('cicy:project-groups-changed', changed);
  });
});
