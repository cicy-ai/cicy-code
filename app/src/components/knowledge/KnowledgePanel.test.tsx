import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  getKnowledgeSpecialist: vi.fn(),
  setKnowledgeSpecialist: vi.fn(),
  getKnowledgeConfig: vi.fn(),
  saveKnowledgeConfig: vi.fn(),
}));
const agentSend = vi.hoisted(() => ({ sendToAgent: vi.fn() }));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../files/FilesView', () => ({ default: () => <div data-id="mock-files-view" /> }));
vi.mock('./KnowledgeGraphView', () => ({ default: () => <div data-id="mock-knowledge-graph" /> }));
vi.mock('../chat/DispatcherChat', () => ({ default: () => <div data-id="mock-dispatcher-chat" /> }));
vi.mock('../../services/agentSend', () => agentSend);

import KnowledgePanel from './KnowledgePanel';

beforeEach(() => {
  vi.clearAllMocks();
  api.getKnowledgeSpecialist.mockResolvedValue({ data: { pane: 'w-101:main.0' } });
  api.setKnowledgeSpecialist.mockImplementation((pane: string) => Promise.resolve({ data: { pane } }));
  agentSend.sendToAgent.mockResolvedValue(undefined);
});

describe('<KnowledgePanel /> specialist selection', () => {
  it('persists the selected specialist as the knowledge governance Agent', async () => {
    render(
      <KnowledgePanel
        open
        onClose={vi.fn()}
        agentId="w-1001"
        workspaceFolder="/tmp/workspace"
        agents={[
          { paneId: 'w-101:main.0', title: '知识专员 A', roleTemplate: 'knowledge-specialist', agentType: 'codex' },
          { paneId: 'w-102:main.0', title: '知识专员 B', roleTemplate: 'knowledge-specialist', agentType: 'claude' },
        ]}
      />,
    );

    fireEvent.click(document.querySelector('[data-id="knowledge-agent-chat-open"]') as HTMLElement);
    const select = await waitFor(() => document.querySelector('[data-id="knowledge-agent-chat-select"]') as HTMLSelectElement);
    fireEvent.change(select, { target: { value: 'w-102:main.0' } });

    await waitFor(() => expect(api.setKnowledgeSpecialist).toHaveBeenCalledWith('w-102:main.0'));
    expect(select).toHaveValue('w-102:main.0');
  });

  it('requests an unlocked Agent type and no Project by default for a new specialist', async () => {
    let detail: Record<string, unknown> | null = null;
    const listener = (event: Event) => { detail = (event as CustomEvent).detail; };
    window.addEventListener('cicy:request-create-agent', listener);

    render(
      <KnowledgePanel
        open
        onClose={vi.fn()}
        agentId="w-1001"
        workspaceFolder="/tmp/workspace"
        agents={[]}
      />,
    );
    fireEvent.click(document.querySelector('[data-id="knowledge-agent-chat-open"]') as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: '创建知识专员' }));

    expect(detail).toEqual(expect.objectContaining({
      roleTemplate: 'knowledge-specialist',
      roleTemplateLocked: true,
      agentTypeLocked: false,
      projectTemplate: '',
    }));
    window.removeEventListener('cicy:request-create-agent', listener);
  });

  it('fills pending governance into the selected Agent prompt without submitting', async () => {
    let fillDetail: Record<string, unknown> | null = null;
    const onFill = (event: Event) => { fillDetail = (event as CustomEvent).detail; };
    window.addEventListener('cicy:fill-composer', onFill);
    render(
      <KnowledgePanel
        open
        onClose={vi.fn()}
        agentId="w-1001"
        workspaceFolder="/tmp/workspace"
        pendingCount={6}
        agents={[
          { paneId: 'w-101:main.0', title: '知识专员', roleTemplate: 'knowledge-specialist', agentType: 'codex' },
        ]}
      />,
    );

    fireEvent.click(document.querySelector('[data-id="knowledge-agent-chat-open"]') as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: '待治理' }));

    expect(fillDetail).toEqual({ paneId: 'w-101:main.0', text: '请处理所有的待治理条目' });
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '待治理' })).toBeEnabled();
    window.removeEventListener('cicy:fill-composer', onFill);
  });
});
