import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
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
}));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => ({ sendToAgent: vi.fn() }));
vi.mock('../AgentAvatar', () => ({ default: () => <span data-testid="agent-avatar" /> }));

import ProjectsPanel from './ProjectsPanel';

const defaultGroups = [
  { id: 1, name: 'Default project', description: '', is_default: true, pane_ids: [], pane_count: 0 },
  { id: 2, name: 'Existing', description: '', is_default: false, pane_ids: [], pane_count: 0 },
];

beforeEach(() => {
  vi.clearAllMocks();
  api.listGroups.mockResolvedValue({ data: { groups: defaultGroups } });
  api.getGroup.mockResolvedValue({ data: { panes: [] } });
  api.createGroup.mockResolvedValue({ data: { id: 3 } });
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
