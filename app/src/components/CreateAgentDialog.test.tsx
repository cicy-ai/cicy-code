import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, fallback?: string) => ({
      templateProjectNone: '不加入 Project',
      title: '新建员工',
      submit: '创建',
    }[key] || fallback || key),
    i18n: { language: 'zh-CN' },
  }),
}));

const appState = vi.hoisted(() => ({
  agentTypeOptions: [
    { value: 'codex', label: 'Codex', description: 'Codex agent' },
    { value: 'cicy', label: 'CiCy', description: 'CiCy agent' },
  ],
}));

vi.mock('../contexts/AppContext', () => ({ useApp: () => appState }));

const api = vi.hoisted(() => ({
  listProjects: vi.fn(),
  listMemoryTemplates: vi.fn(),
  listCustomAgents: vi.fn(),
}));

vi.mock('../services/api', () => ({ default: api }));

import CreateAgentDialog from './CreateAgentDialog';

beforeEach(() => {
  vi.clearAllMocks();
  api.listProjects.mockResolvedValue({ data: { projects: [{ slug: 'default', name: '默认项目' }] } });
  api.listMemoryTemplates.mockResolvedValue({ data: { roles: ['assistant', 'knowledge-specialist'] } });
  api.listCustomAgents.mockResolvedValue({ data: { agents: [] } });
});

describe('<CreateAgentDialog /> project assignment', () => {
  it('lets a general employee be created without joining a Project', async () => {
    const onSubmit = vi.fn();
    render(<CreateAgentDialog open onClose={vi.fn()} onSubmit={onSubmit} />);

    fireEvent.change(screen.getByPlaceholderText('namePlaceholder'), { target: { value: '独立员工' } });
    const projectSelect = document.querySelector('[data-id="create-agent-dialog-project-template-select"] [data-id="select-trigger"]') as HTMLButtonElement;
    fireEvent.click(projectSelect);
    fireEvent.click(await screen.findByText('不加入 Project'));
    fireEvent.click(screen.getByRole('button', { name: '创建' }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      title: '独立员工',
      project_template: '',
    })));
  }, 15_000);

  it('shows a locked empty Project as not joined', async () => {
    render(
      <CreateAgentDialog
        open
        onClose={vi.fn()}
        onSubmit={vi.fn()}
        initialValues={{ project_template: '' }}
        projectTemplateLocked
      />,
    );

    const projectSelect = document.querySelector('[data-id="create-agent-dialog-project-template-select"] [data-id="select-trigger"]') as HTMLButtonElement;
    await waitFor(() => expect(projectSelect).toHaveTextContent('不加入 Project'));
    expect(projectSelect).toBeDisabled();
  });
});
