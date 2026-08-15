import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getGroup: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  deleteGroup: vi.fn(),
  addGroupPane: vi.fn(),
  removeGroupPane: vi.fn(),
  updateGroupPaneLayout: vi.fn(),
  getAgentCurrentReply: vi.fn(),
}));
const agentSend = vi.hoisted(() => ({ sendToAgent: vi.fn() }));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => agentSend);
vi.mock('../AgentAvatar', () => ({ default: () => <span data-testid="agent-avatar" /> }));

import ProjectsPanel from './ProjectsPanel';

const defaultGroups = [
  { id: 1, name: 'Default project', description: '', is_default: true, pane_ids: [], pane_count: 0 },
  { id: 2, name: 'Existing', description: '', is_default: false, pane_ids: [], pane_count: 0 },
];

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
  localStorage.clear();
  api.listGroups.mockResolvedValue({ data: { groups: defaultGroups } });
  api.getGroup.mockResolvedValue({ data: { panes: [] } });
  api.createGroup.mockResolvedValue({ data: { id: 3 } });
  api.getAgentCurrentReply.mockResolvedValue({ data: { question: '', items: [], status: 'completed' } });
});

describe('<ProjectsPanel /> floating action button', () => {
  it('hides while the bottom dock is open and returns collapsed after it closes', async () => {
    const { rerender } = render(<ProjectsPanel agents={[]} dockOpen={false} onOpenAgent={vi.fn()} />);
    await screen.findByText('projectDefault');

    fireEvent.click(document.querySelector('[data-id="project-add-agent"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-fab-menu"]')).toHaveClass('pointer-events-auto');

    rerender(<ProjectsPanel agents={[]} dockOpen onOpenAgent={vi.fn()} />);
    expect(document.querySelector('[data-id="project-fab-wrap"]')).not.toBeInTheDocument();

    rerender(<ProjectsPanel agents={[]} dockOpen={false} onOpenAgent={vi.fn()} />);
    expect(document.querySelector('[data-id="project-fab-wrap"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-fab-menu"]')).toHaveClass('pointer-events-none');
  });
});

describe('<ProjectsPanel /> project view cache', () => {
  it('restores zoom and card layout before the background layout request resolves', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    api.getGroup.mockReturnValue(new Promise(() => {}));
    localStorage.setItem('cicy_project_view:default', JSON.stringify({
      zoom: 1.25,
      pan: { x: 18, y: 24 },
      layouts: { 'w-101': { x: 120, y: 80, z: 3, width: 420, height: 360 } },
    }));

    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} onOpenAgent={vi.fn()} />);

    const node = await waitFor(() => {
      const value = document.querySelector<HTMLElement>('[data-id="project-canvas-node-w-101"]');
      if (!value) throw new Error('cached agent card did not render');
      return value;
    });
    expect(node.style.left).toBe('120px');
    expect(node.style.top).toBe('80px');
    expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toHaveStyle({ width: '420px', height: '360px' });
    expect(document.querySelector('[data-id="project-canvas-zoom-value"]')).toHaveTextContent('125%');
  });
});

async function openCreateModal() {
  render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
  await screen.findByText('projectDefault');
  fireEvent.click(screen.getByRole('button', { name: 'projectCreate' }));
  return await waitFor(() => {
    const input = document.querySelector('[data-id="project-create-name"]');
    if (!input) throw new Error('project name input did not open');
    return input as HTMLInputElement;
  });
}

describe('<ProjectsPanel /> project creation', () => {
  it('does not submit when Enter only confirms a Chinese IME candidate', async () => {
    const input = await openCreateModal();
    fireEvent.change(input, { target: { value: '中文项目' } });
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true });

    expect(api.createGroup).not.toHaveBeenCalled();
    expect(document.querySelector('[data-id="project-create-modal"]')).toBeInTheDocument();

    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(api.createGroup).toHaveBeenCalledWith({ name: '中文项目', description: '' }));
  });

  it('keeps the modal open and explains duplicate names', async () => {
    const input = await openCreateModal();
    fireEvent.change(input, { target: { value: ' existing ' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    expect(api.createGroup).not.toHaveBeenCalled();
    expect(await screen.findByText('projectNameExists')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-create-modal"]')).toBeInTheDocument();
  });

  it('preserves the name and shows an inline error when creation fails', async () => {
    api.createGroup.mockRejectedValueOnce(new Error('network unavailable'));
    const input = await openCreateModal();
    fireEvent.change(input, { target: { value: 'New project' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    expect(await screen.findByText('network unavailable')).toBeInTheDocument();
    expect(input).toHaveValue('New project');
    expect(document.querySelector('[data-id="project-create-modal"]')).toBeInTheDocument();
  });
});

describe('<ProjectsPanel /> agent prompt footer', () => {
  it('opens the selected agent guidance document from the header action', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    const onOpenGuidance = vi.fn();
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} onOpenAgent={vi.fn()} onOpenGuidance={onOpenGuidance} />);

    const button = await screen.findByRole('button', { name: '人设' });
    fireEvent.click(button);

    expect(onOpenGuidance).toHaveBeenCalledWith('w-101:main.0');
  });

  it('stays hidden until the card is selected and ignores IME confirmation Enter', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    agentSend.sendToAgent.mockResolvedValue(undefined);
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
    const canvasNode = card.closest('[data-id="project-canvas-node-w-101"]') as HTMLElement;
    fireEvent.pointerDown(canvasNode, { button: 0, pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.pointerUp(canvasNode, { pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.click(document.querySelector('[data-id="project-agent-card-live-body"]') as HTMLElement);

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('agent prompt footer did not open');
      return node as HTMLInputElement;
    });
    fireEvent.click(card);
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument();

    fireEvent.change(input, { target: { value: '中文任务' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true });
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();

    input.blur();
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('中文任务')).toBeInTheDocument();
    expect(input).toHaveValue('');
    await waitFor(() => expect(input).toHaveFocus());
  });
});
