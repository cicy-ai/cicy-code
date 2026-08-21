import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', async () => {
  const React = await import('react');
  return {
    initReactI18next: { type: '3rdParty', init: vi.fn() },
    useTranslation: () => ({
      t: (key: string, options?: { defaultValue?: string; title?: string }) => (
        key === 'confirmRestart'
          ? `重启 <strong>${options?.title || ''}</strong>？`
          : options?.defaultValue || key
      ),
    }),
    Trans: ({ values, components }: { values?: { title?: string }; components?: { strong?: React.ReactElement } }) => (
      React.createElement(
        React.Fragment,
        null,
        '重启 ',
        components?.strong ? React.cloneElement(components.strong, {}, values?.title || '') : values?.title || '',
        '？',
      )
    ),
  };
});

const api = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getGroup: vi.fn(),
  getPane: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  deleteGroup: vi.fn(),
  addGroupPane: vi.fn(),
  removeGroupPane: vi.fn(),
  updateGroupPaneLayout: vi.fn(),
  getAgentCurrentReply: vi.fn(),
  getAgentHistoryIDs: vi.fn(),
  getAgentCurrentHistory: vi.fn(),
  getAgentGreeting: vi.fn(),
  getIMAccounts: vi.fn(),
  getCiCyCloudInstances: vi.fn(),
  getCiCyCloudAgents: vi.fn(),
  sendCiCyCloudMessage: vi.fn(),
  getCiCyCloudMessageStatus: vi.fn(),
  uploadAssetFile: vi.fn(),
  getMemoryTemplate: vi.fn(),
  saveMemoryTemplate: vi.fn(),
  restartPane: vi.fn(),
  deletePane: vi.fn(),
}));
const agentSend = vi.hoisted(() => ({ sendToAgent: vi.fn() }));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => agentSend);
vi.mock('../AgentAvatar', () => ({ default: () => <span data-testid="agent-avatar" /> }));
vi.mock('../terminal/TerminalView', () => ({ default: ({ ttydSrc }: { ttydSrc: string }) => <div data-id="mock-project-terminal">{ttydSrc}</div> }));
vi.mock('../layout/UpdateAgentModal', () => ({
  default: ({ paneId, title, onClose }: { paneId: string; title: string; onClose: () => void }) => (
    <div data-id="mock-update-agent-modal">{paneId}:{title}<button data-id="mock-update-agent-close" onClick={onClose}>close</button></div>
  ),
}));
vi.mock('../layout/WechatBindModal', () => ({
  default: ({ paneId, title, platform = 'wechat', onClose }: { paneId: string; title: string; platform?: string; onClose: () => void }) => (
    <div data-id={`mock-${platform}-bind-modal`}>{paneId}:{title}<button data-id={`mock-${platform}-bind-close`} onClick={onClose}>close</button></div>
  ),
}));
vi.mock('../layout/ForkConfirmModal', () => ({
  default: ({ sourcePaneId, masterPaneId, projectId, onClose }: { sourcePaneId: string; masterPaneId: string; projectId?: number | string; onClose: () => void }) => (
    <div data-id="mock-fork-confirm-modal">{sourcePaneId}:{masterPaneId}:{projectId}<button data-id="mock-fork-confirm-close" onClick={onClose}>close</button></div>
  ),
}));
vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value, onChange, extensions: _extensions, basicSetup: _basicSetup, theme: _theme, height: _height, ...props }: any) => (
    <textarea {...props} value={value} onChange={(event) => onChange(event.target.value)} />
  ),
}));

import ProjectsPanel from './ProjectsPanel';
import { clearAgentSendTarget, getAgentSendTarget } from '../../services/agentSendTarget';

const defaultGroups = [
  { id: 1, name: 'Default project', description: '', is_default: true, pane_ids: [], pane_count: 0 },
  { id: 2, name: 'Existing', description: '', is_default: false, pane_ids: [], pane_count: 0 },
];

beforeEach(() => {
  vi.clearAllMocks();
  clearAgentSendTarget();
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
  vi.stubGlobal('IntersectionObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  window.history.replaceState(null, '', '#/project/default');
  localStorage.clear();
  api.listGroups.mockResolvedValue({ data: { groups: defaultGroups } });
  api.getGroup.mockResolvedValue({ data: { panes: [] } });
  api.getPane.mockResolvedValue({ data: { agent_type: 'codex', role_template: '' } });
  api.createGroup.mockResolvedValue({ data: { id: 3 } });
  api.getAgentCurrentReply.mockResolvedValue({ data: { question: '', items: [], status: 'completed' } });
  api.getAgentHistoryIDs.mockResolvedValue({ data: { conversation_id: '', id: 0, model: '', prompts: [] } });
  api.getAgentCurrentHistory.mockResolvedValue({ data: { items: [] } });
  api.getAgentGreeting.mockResolvedValue({ data: { greeting: '' } });
  api.getIMAccounts.mockResolvedValue({ data: { accounts: [{ platform: 'cicy_cloud', config: { team_id: 'local_team' } }] } });
  api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [] } });
  api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [] } });
  api.addGroupPane.mockResolvedValue({ data: { success: true } });
  api.removeGroupPane.mockResolvedValue({ data: { success: true } });
  api.updateGroupPaneLayout.mockResolvedValue({ data: { success: true } });
  api.uploadAssetFile.mockResolvedValue({ data: { file: { file_ref: '/home/cicy/cicy-ai/assets/queued.png' } } });
  api.getMemoryTemplate.mockResolvedValue({ data: { content: '# Global', path: '/home/cicy/cicy-ai/memory/global.md' } });
  api.saveMemoryTemplate.mockResolvedValue({ data: { saved: true } });
  api.restartPane.mockResolvedValue({ data: { success: true } });
  api.deletePane.mockResolvedValue({ data: { success: true } });
  vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:queued-image'), revokeObjectURL: vi.fn() });
});

describe('<ProjectsPanel /> floating action button', () => {
  it('passes the current Project identity when creating an Agent from the canvas', async () => {
    const onCreateAgent = vi.fn();
    api.listGroups.mockResolvedValue({ data: { groups: [
      defaultGroups[0],
      { ...defaultGroups[1], project_template: 'existing-project' },
    ] } });
    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} onCreateAgent={onCreateAgent} />);

    const project = await waitFor(() => {
      const node = document.querySelector('[data-id="project-list-item-2"]');
      if (!node) throw new Error('custom Project did not render');
      return node as HTMLElement;
    });
    fireEvent.click(project);
    fireEvent.click(document.querySelector('[data-id="project-add-agent"]') as HTMLElement);
    fireEvent.click(document.querySelector('[data-id="project-fab-create-agent"]') as HTMLElement);

    expect(onCreateAgent).toHaveBeenCalledWith(expect.objectContaining({
      projectId: 2,
      projectTemplate: 'existing-project',
      onCreated: expect.any(Function),
    }));
    const request = onCreateAgent.mock.calls[0][0];
    await request.onCreated('w-999:main.0');
    expect(api.addGroupPane).toHaveBeenCalledWith(2, 'w-999:main.0');
  }, 15_000);

  it('lists searchable Cloud Agents by Instance and persists a qualified project member id', async () => {
    const lastSeenAt = new Date().toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, '');
    api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [{ instanceId: 'code-remote', teamId: 'mac_local', status: 'online', lastSeenAt }] } });
    api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [
      { instanceId: 'code-remote', teamId: 'mac_local', agentId: 'w-200', title: 'Remote Builder', agentType: 'codex' },
      { instanceId: 'code-remote', teamId: 'mac_local', agentId: 'w-201', title: 'Remote Reviewer', agentType: 'claude' },
    ] } });
    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);

    fireEvent.click(await waitFor(() => document.querySelector('[data-id="project-add-agent"]') as HTMLElement));
    fireEvent.click(document.querySelector('[data-id="project-fab-add-existing"]') as HTMLElement);
    fireEvent.click(await waitFor(() => document.querySelector('[data-id="project-add-agent-instance-code-remote"]') as HTMLElement));
    const codexFilter = await waitFor(() => document.querySelector('[data-id="project-add-agent-type-codex"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-add-agent-type-claude"]')).toHaveTextContent('1');
    fireEvent.click(codexFilter);
    expect(document.querySelector('[data-id="project-add-agent-mac_local.w-201"]')).not.toBeInTheDocument();
    fireEvent.change(document.querySelector('[data-id="project-add-agent-search"]') as HTMLInputElement, { target: { value: 'Builder' } });
    fireEvent.click(await waitFor(() => document.querySelector('[data-id="project-add-agent-mac_local.w-200"]') as HTMLElement));
    fireEvent.click(document.querySelector('[data-id="project-add-agent-confirm"]') as HTMLElement);

    await waitFor(() => expect(api.addGroupPane).toHaveBeenCalledWith(1, 'mac_local.w-200'));
    await waitFor(() => expect(api.updateGroupPaneLayout).toHaveBeenCalledWith(1, 'mac_local.w-200', expect.objectContaining({ width: 600 })));
  });

  it('shows dot-only Instance status and prevents adding Agents from an offline Instance', async () => {
    api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [{ instanceId: 'code-offline', teamId: 'old_mac', status: 'offline', lastSeenAt: '2026-01-01 00:00:00' }] } });
    api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [{ instanceId: 'code-offline', teamId: 'old_mac', agentId: 'w-201', title: 'Offline Builder', agentType: 'codex' }] } });
    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);

    fireEvent.click(await waitFor(() => document.querySelector('[data-id="project-add-agent"]') as HTMLElement));
    fireEvent.click(document.querySelector('[data-id="project-fab-add-existing"]') as HTMLElement);
    const instance = await waitFor(() => document.querySelector('[data-id="project-add-agent-instance-code-offline"]') as HTMLElement);
    expect(instance).not.toHaveTextContent('在线');
    expect(instance).not.toHaveTextContent('离线');
    fireEvent.click(instance);
    const agent = await waitFor(() => document.querySelector('[data-id="project-add-agent-old_mac.w-201"]') as HTMLButtonElement);
    expect(agent).toBeDisabled();
    fireEvent.click(agent);
    expect(document.querySelector('[data-id="project-add-agent-confirm"]')).toBeDisabled();
    expect(api.addGroupPane).not.toHaveBeenCalled();
  });

  it('edits global.md and the project definition with the shared file editor', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], project_file: '/home/cicy/cicy-ai/memory/projects/default.md', project_rules: '# Project' }] } });
    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
    const open = await waitFor(() => {
      const node = document.querySelector('[data-id="project-definition-edit-1"]');
      if (!node) throw new Error('definition button did not render');
      return node as HTMLElement;
    });
    fireEvent.click(open);

    expect(await screen.findByText('global.md')).toBeInTheDocument();
    expect(screen.getByText('default.md')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-definition-file-project"]')).toHaveAttribute('aria-selected', 'true');
    expect(document.querySelector('[data-id="project-definition-file-tabs"]')).toHaveAttribute('role', 'tablist');
    expect(document.querySelector('[data-id="project-definition-tab-help-project"]')).toHaveTextContent('只对当前项目内的 Agent 生效');
    expect(document.querySelector('[data-id="markdown-file-editor"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-definition-tips"]')).toHaveTextContent('global.md → Project 定义 → Agent 角色');
    fireEvent.click(document.querySelector('[data-id="project-definition-file-global"]') as HTMLElement);
    expect(await screen.findByText('/home/cicy/cicy-ai/memory/global.md')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-definition-tab-help-global"]')).toHaveTextContent('对所有项目和 Agent 生效');
    fireEvent.click(document.querySelector('[data-id="project-definition-save"]') as HTMLElement);
    await waitFor(() => expect(api.saveMemoryTemplate).toHaveBeenCalledWith('global', '', '# Global'));
    expect(api.updateGroup).toHaveBeenCalledWith(1, { project_rules: '# Project' });
  });

  it('hides while the bottom dock is open and returns collapsed after it closes', async () => {
    const { rerender } = render(<ProjectsPanel agents={[]} dockOpen={false} onOpenAgent={vi.fn()} />);
    await screen.findByText('projectDefault');

    fireEvent.click(document.querySelector('[data-id="project-add-agent"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-fab-menu"]')).toHaveClass('pointer-events-auto');

    rerender(<ProjectsPanel agents={[]} dockOpen onOpenAgent={vi.fn()} />);
    expect(document.querySelector('[data-id="project-fab-wrap"]')).not.toBeInTheDocument();

    rerender(<ProjectsPanel agents={[]} dockOpen={false} onOpenAgent={vi.fn()} />);
    expect(document.querySelector('[data-id="project-fab-wrap"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="projects-panel"]')).toHaveClass('relative');
    expect(document.querySelector('[data-id="project-fab-wrap"]')).toHaveClass('absolute', 'bottom-16', 'right-5');
    expect(document.querySelector('[data-id="project-fab-wrap"]')).not.toHaveClass('fixed');
    expect(document.querySelector('[data-id="project-fab-menu"]')).toHaveClass('pointer-events-none');
  });

  it('collapses the project list and remembers the preference', async () => {
    const { unmount } = render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
    fireEvent.click(await waitFor(() => document.querySelector('[data-id="projects-list-collapse"]') as HTMLElement));
    expect(document.querySelector('[data-id="projects-list"]')).toHaveClass('hidden');
    expect(localStorage.getItem('cicy_projects_list_collapsed')).toBe('1');
    unmount();

    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
    const expand = await waitFor(() => document.querySelector('[data-id="projects-list-expand"]') as HTMLElement);
    fireEvent.click(expand);
    expect(document.querySelector('[data-id="projects-list"]')).not.toHaveClass('hidden');
    expect(localStorage.getItem('cicy_projects_list_collapsed')).toBe('0');
  });
});

describe('<ProjectsPanel /> project view cache', () => {
  it('polls live replies only for the selected agent card', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-10265:main.0', 'w-10281:main.0'], pane_count: 2 }] },
    });
    render(<ProjectsPanel
      agents={[
        { paneId: 'w-10265:main.0', title: 'Selected', agentType: 'codex' },
        { paneId: 'w-10281:main.0', title: 'Inactive', agentType: 'codex' },
      ]}
      activeAgentId="w-10265"
      onOpenAgent={vi.fn()}
    />);

    await waitFor(() => expect(api.getAgentCurrentReply).toHaveBeenCalledWith('w-10265'));

    const polledAgentIds = new Set(api.getAgentCurrentReply.mock.calls.map(([paneId]) => paneId));
    expect(polledAgentIds).toEqual(new Set(['w-10265']));
  });

  it('only zooms with the mouse wheel while hand mode is active', async () => {
    render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
    await screen.findByText('projectDefault');

    const canvas = document.querySelector('[data-id="project-infinite-canvas"]') as HTMLElement;
    const zoom = document.querySelector('[data-id="project-canvas-zoom-value"]') as HTMLElement;
    const hand = document.querySelector('[data-id="project-canvas-wheel-zoom-toggle"]') as HTMLButtonElement;

    expect(hand).toHaveAttribute('aria-pressed', 'false');
    fireEvent.wheel(canvas, { clientX: 120, clientY: 100, deltaY: -100 });
    expect(zoom).toHaveTextContent('100%');

    fireEvent.click(hand);
    expect(hand).toHaveAttribute('aria-pressed', 'true');
    fireEvent.wheel(canvas, { clientX: 120, clientY: 100, deltaY: -100 });
    expect(zoom).toHaveTextContent('102%');

    fireEvent.click(hand);
    expect(hand).toHaveAttribute('aria-pressed', 'false');
    fireEvent.wheel(canvas, { clientX: 120, clientY: 100, deltaY: -100 });
    expect(zoom).toHaveTextContent('102%');
  });

  it('renders duplicate roster records for one pane only once', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-1010:main.0'], pane_count: 1 }] } });
    render(<ProjectsPanel agents={[
      { paneId: 'w-1010:main.0', title: 'cicy-code', agentType: 'codex' },
      { paneId: 'w-1010:main.0', title: 'cicy-code', agentType: 'codex', defaultModel: 'gpt-5.6-sol' },
      { paneId: 'w-1010:main.0', title: 'cicy-code', agentType: 'codex', status: 'working' },
    ]} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelectorAll('[data-id="project-canvas-node-w-1010"]')).toHaveLength(1));
    expect(document.querySelectorAll('[data-id="project-agent-card-w-1010"]')).toHaveLength(1);
  });

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
    expect(document.querySelector('[data-id="project-list-item-default"] [data-id="project-list-item-agent-count"]')).toHaveTextContent('1');
    expect(document.querySelector('[data-id="project-agent-card-metrics"]')).not.toHaveClass('border-b');
    fireEvent.click(document.querySelector('[data-id="project-agent-card-w-101"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-question-fixed"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-history-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toHaveStyle({ width: '420px', height: '360px' });
    expect(document.querySelector('[data-id="project-canvas-zoom-value"]')).toHaveTextContent('125%');
  });

  it('reserves stable header metric slots before async metrics arrive', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    const agent = { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' };
    const { rerender } = render(<ProjectsPanel agents={[agent]} statuses={{}} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toBeInTheDocument());
    const modelSlot = document.querySelector('[data-id="project-agent-card-model-slot"]');
    const contextSlot = document.querySelector('[data-id="project-agent-card-context-slot"]');
    const costSlot = document.querySelector('[data-id="project-agent-card-cost-slot"]');
    expect(modelSlot).toHaveClass('w-24', 'shrink-0');
    expect(contextSlot).toHaveClass('w-3', 'shrink-0');
    expect(costSlot).toHaveClass('w-16', 'shrink-0');
    expect(modelSlot).toBeEmptyDOMElement();
    expect(contextSlot).toBeEmptyDOMElement();
    expect(costSlot).toBeEmptyDOMElement();

    rerender(<ProjectsPanel agents={[agent]} statuses={{
      'w-101:main.0': { status: 'completed', model: 'gpt-5.6-sol', context_used_pct: 42, context_window_size: 256000, cost_credit: 1.14 },
    }} onOpenAgent={vi.fn()} />);

    expect(document.querySelector('[data-id="project-agent-card-model-slot"]')).toBe(modelSlot);
    expect(document.querySelector('[data-id="project-agent-card-context-slot"]')).toBe(contextSlot);
    expect(document.querySelector('[data-id="project-agent-card-cost-slot"]')).toBe(costSlot);
    expect(modelSlot).toHaveTextContent('gpt-5.6-sol');
    expect(contextSlot?.querySelector('[data-id="project-agent-card-context"]')).toBeInTheDocument();
    expect(costSlot).toHaveTextContent('$1.14');
  });

  it('brings default-project cards into view when agents arrive after the project', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    localStorage.setItem('cicy_project_view:default', JSON.stringify({
      zoom: 1,
      pan: { x: 60, y: 60 },
      layouts: { 'w-101': { x: 4000, y: 3000, z: 1, width: 300, height: 320 } },
    }));
    const { rerender } = render(<ProjectsPanel agents={[]} onOpenAgent={vi.fn()} />);
    await screen.findByText('projectDefault');
    const canvas = document.querySelector('[data-id="project-infinite-canvas"]') as HTMLElement;
    Object.defineProperty(canvas, 'clientWidth', { configurable: true, value: 1000 });
    Object.defineProperty(canvas, 'clientHeight', { configurable: true, value: 700 });

    rerender(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toBeInTheDocument());
    await waitFor(() => {
      const cached = JSON.parse(localStorage.getItem('cicy_project_view:default') || '{}');
      expect(cached.pan.x).not.toBe(60);
    });
  });

  it('keeps painted cards stable while a late server layout fills uncached cards', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0', 'w-102:main.0'], pane_count: 2 }] },
    });
    let resolveLayout: (value: any) => void = () => {};
    api.getGroup.mockReturnValue(new Promise((resolve) => { resolveLayout = resolve; }));
    localStorage.setItem('cicy_project_view:default', JSON.stringify({
      zoom: 1,
      pan: { x: 60, y: 60 },
      layouts: { 'w-101': { x: 40, y: 40, z: 1, width: 300, height: 320 } },
    }));

    render(<ProjectsPanel agents={[
      { paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' },
      { paneId: 'w-102:main.0', title: '测试', agentType: 'codex' },
    ]} onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-canvas-node-w-101"]')).toHaveStyle({ left: '40px', top: '40px' }));

    act(() => resolveLayout({ data: { panes: [
      { pane_id: 'w-101:main.0', pos_x: -1600, pos_y: -1000, width: 300, height: 320, z_index: 1 },
      { pane_id: 'w-102:main.0', pos_x: 760, pos_y: 500, width: 360, height: 340, z_index: 2 },
    ] } }));

    await waitFor(() => expect(document.querySelector('[data-id="project-canvas-node-w-102"]')).toHaveStyle({ left: '760px', top: '500px' }));
    expect(document.querySelector('[data-id="project-canvas-node-w-101"]')).toHaveStyle({ left: '40px', top: '40px' });
    expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toHaveStyle({ width: '300px', height: '320px' });
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
  it('clears the pending button when a newer authoritative status is completed', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    agentSend.sendToAgent.mockResolvedValue(undefined);
    const agent = { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'idle' };
    const view = render(
      <ProjectsPanel
        agents={[agent]}
        statuses={{ 'w-101:main.0': { status: 'completed', updated_at: '2026-08-21T00:00:00Z', latest_question: '上一轮' } }}
        activeAgentId="w-101"
        onOpenAgent={vi.fn()}
      />,
    );

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('project agent prompt did not render');
      return node as HTMLTextAreaElement;
    });
    fireEvent.change(input, { target: { value: '本轮已经完成' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-prompt-cancel-w-101"]')).toBeInTheDocument());

    view.rerender(
      <ProjectsPanel
        agents={[agent]}
        statuses={{ 'w-101:main.0': { status: 'completed', updated_at: '2026-08-21T00:01:00Z', latest_question: '本轮已经完成' } }}
        activeAgentId="w-101"
        onOpenAgent={vi.fn()}
      />,
    );

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-prompt-cancel-w-101"]')).not.toBeInTheDocument());
    expect(document.querySelector('[data-id="project-agent-prompt-send-w-101"]')).toBeInTheDocument();
  });

  it('keeps the complete Q&A history after sending a new project-card prompt', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    api.getAgentHistoryIDs.mockResolvedValue({ data: { conversation_id: 'conv-trim', id: 6, model: 'gpt-5', prompts: [] } });
    api.getAgentCurrentHistory.mockResolvedValue({ data: { items: [
      { history_id: 1, conversation_id: 'conv-trim', role: 'user', content: '第一问保留' },
      { history_id: 2, conversation_id: 'conv-trim', role: 'assistant', content: '第一答保留' },
      { history_id: 3, conversation_id: 'conv-trim', role: 'user', content: '第二问保留' },
      { history_id: 4, conversation_id: 'conv-trim', role: 'assistant', content: '第二答保留' },
      { history_id: 5, conversation_id: 'conv-trim', role: 'user', content: '第三问保留' },
      { history_id: 6, conversation_id: 'conv-trim', role: 'assistant', content: '第三答保留' },
    ] } });
    api.getAgentCurrentReply.mockResolvedValue({ data: {
      conversation_id: 'conv-trim',
      history_id: 0,
      complete: true,
      status: 'completed',
      answer: '',
      thinking: '',
      items: [],
    } });
    agentSend.sendToAgent.mockResolvedValue(undefined);

    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} activeAgentId="w-101" onOpenAgent={vi.fn()} />);

    expect(await screen.findByText('第一问保留', undefined, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByText('第二问保留')).toBeInTheDocument();
    expect(screen.getByText('第三问保留')).toBeInTheDocument();

    const input = document.querySelector('[data-id="project-agent-prompt-input-w-101"]') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: '第四问正在发送' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('第四问正在发送')).toBeInTheDocument();
    expect(screen.getByText('第一问保留')).toBeInTheDocument();
    expect(screen.getByText('第一答保留')).toBeInTheDocument();
    expect(screen.getByText('第二问保留')).toBeInTheDocument();
    expect(screen.getByText('第二答保留')).toBeInTheDocument();
    expect(screen.getByText('第三问保留')).toBeInTheDocument();
    expect(screen.getByText('第三答保留')).toBeInTheDocument();
  });

  it('always shows card body tabs and renders every Q&A with the shared history view', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    api.getAgentHistoryIDs.mockResolvedValue({ data: { conversation_id: 'conv-all', id: 4, model: 'gpt-5', prompts: [] } });
    api.getAgentCurrentHistory.mockResolvedValue({ data: { items: [
      { history_id: 1, conversation_id: 'conv-all', role: 'user', content: '第一问' },
      { history_id: 2, conversation_id: 'conv-all', role: 'assistant', content: '第一答' },
      { history_id: 3, conversation_id: 'conv-all', role: 'user', content: '第二问' },
      { history_id: 4, conversation_id: 'conv-all', role: 'assistant', content: '第二答' },
    ] } });
    api.getAgentCurrentReply.mockResolvedValue({ data: {
      conversation_id: 'conv-all',
      history_id: 0,
      complete: true,
      status: 'completed',
      answer: '',
      thinking: '',
      items: [],
    } });

    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);

    const tabs = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-tabs-w-101"]');
      if (!node) throw new Error('agent card tabs did not render');
      return node as HTMLElement;
    });
    expect(tabs).not.toHaveAttribute('aria-hidden', 'true');
    expect(tabs).not.toHaveClass('invisible', 'pointer-events-none');
    expect(screen.getByRole('tab', { name: '会话' })).toHaveAttribute('aria-selected', 'true');
    expect(await screen.findByText('第一问')).toBeInTheDocument();
    expect(screen.getByText('第一答')).toBeInTheDocument();
    expect(screen.getByText('第二问')).toBeInTheDocument();
    expect(screen.getByText('第二答')).toBeInTheDocument();
    expect(document.querySelector('[data-id="current-history-view"]')).toBeInTheDocument();
  });

  it('reloads the committed window when compaction shrinks the same conversation history', async () => {
    const checkpointPrompt = 'You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.';
    let servedOldWindow = false;
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-901:main.0'], pane_count: 1 }] } });
    api.getAgentHistoryIDs.mockImplementation(() => Promise.resolve({ data: {
      conversation_id: 'conv-compact',
      id: servedOldWindow ? 5 : 10,
      model: 'gpt-5',
      prompts: [],
    } }));
    api.getAgentCurrentHistory
      .mockImplementationOnce(() => {
        servedOldWindow = true;
        return Promise.resolve({ data: { items: [
          { history_id: 9, conversation_id: 'conv-compact', role: 'user', content: checkpointPrompt },
          { history_id: 10, conversation_id: 'conv-compact', role: 'assistant', content: '旧压缩摘要' },
        ] } });
      })
      .mockResolvedValue({ data: { items: [
        { history_id: 4, conversation_id: 'conv-compact', role: 'assistant', content: '压缩后的上下文' },
        { history_id: 5, conversation_id: 'conv-compact', role: 'user', content: '压缩后的真实问题' },
      ] } });
    api.getAgentCurrentReply.mockResolvedValue({ data: {
      conversation_id: 'conv-compact',
      reply_conversation_id: 'conv-compact',
      history_id: 6,
      complete: false,
      status: 'thinking',
      answer: '',
      thinking: '',
      items: [],
    } });

    render(<ProjectsPanel agents={[{ paneId: 'w-901:main.0', title: '压缩测试', agentType: 'codex' }]} activeAgentId="w-901" onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(screen.getByText('压缩后的真实问题')).toBeInTheDocument());
    expect(screen.queryByText(checkpointPrompt)).not.toBeInTheDocument();
    expect(api.getAgentCurrentHistory).toHaveBeenCalledTimes(2);
  });

  it('switches card body tabs while keeping the shared history inline', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toBeInTheDocument());
    const historyTab = await screen.findByRole('tab', { name: '会话' });
    expect(historyTab).toHaveAttribute('aria-selected', 'true');
    const historyBody = document.querySelector('[data-id="project-agent-card-history-body-w-101"]');
    expect(historyBody).toBeInTheDocument();
    expect(historyBody).toHaveClass('-mb-4');
    expect(historyBody?.querySelector('[data-id="current-history-view"]')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '角色' }));
    expect(document.querySelector('[data-id="project-agent-card-history-body-w-101"]')).not.toBeInTheDocument();
    fireEvent.click(historyTab);
    expect(document.querySelector('[data-id="project-agent-card-history-body-w-101"]')).toBeInTheDocument();
  });

  it('toggles an inline terminal for non-cicy agents only', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0', 'w-102:main.0'], pane_count: 2 }] },
    });
    render(<ProjectsPanel agents={[
      { paneId: 'w-101:main.0', title: 'Codex', agentType: 'codex', ttydSrc: '/ttyd/w-101/?token=test' },
      { paneId: 'w-102:main.0', title: 'CiCy', agentType: 'cicy', ttydSrc: '/ttyd/w-102/?token=test' },
    ]} activeAgentId="w-101" onOpenAgent={vi.fn()} />);

    const toggle = await screen.findByRole('tab', { name: 'Terminal' });
    expect(document.querySelector('[data-id="project-agent-card-tab-terminal-w-102"]')).not.toBeInTheDocument();
    const sharedFooter = document.querySelector('[data-id="project-agent-card-footer-w-101"]');
    expect(sharedFooter).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBe(sharedFooter);
    expect(document.querySelector('[data-id="mock-project-terminal"]')).toHaveTextContent('/ttyd/w-101/');
    fireEvent.click(toggle);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-tab-history-w-101"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument();
  });

  it('restores each agent card body tab from localStorage after a page remount', async () => {
    const agents = [
      { paneId: 'w-101:main.0', title: 'Codex', agentType: 'codex', ttydSrc: '/ttyd/w-101/?token=test' },
      { paneId: 'w-102:main.0', title: 'Claude', agentType: 'claude', ttydSrc: '/ttyd/w-102/?token=test' },
    ];
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: agents.map((agent) => agent.paneId), pane_count: 2 }] },
    });

    const view = render(<ProjectsPanel agents={agents} activeAgentId="w-101" onOpenAgent={vi.fn()} />);
    const firstTerminalTab = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-tab-terminal-w-101"]');
      if (!node) throw new Error('first Agent Terminal tab did not render');
      return node as HTMLButtonElement;
    });
    fireEvent.click(firstTerminalTab);
    await waitFor(() => expect(JSON.parse(localStorage.getItem('cicy_project_agent_body_tabs:v1') || '{}')).toEqual({
      'local:w-101': 'terminal',
    }));

    view.rerender(<ProjectsPanel agents={agents} activeAgentId="w-102" onOpenAgent={vi.fn()} />);
    const secondRoleTab = await waitFor(() => document.querySelector('[data-id="project-agent-card-tab-role-w-102"]') as HTMLButtonElement);
    fireEvent.click(secondRoleTab);
    await waitFor(() => expect(JSON.parse(localStorage.getItem('cicy_project_agent_body_tabs:v1') || '{}')).toEqual({
      'local:w-101': 'terminal',
      'local:w-102': 'role',
    }));

    view.unmount();
    const restored = render(<ProjectsPanel agents={agents} activeAgentId="w-101" onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-tab-terminal-w-101"]')).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument());

    restored.rerender(<ProjectsPanel agents={agents} activeAgentId="w-102" onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-tab-role-w-102"]')).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-role-body-w-102"]')).toBeInTheDocument());
  });

  it('keeps a restored Terminal open when switching projects resets parent state', async () => {
    localStorage.setItem('cicy_project_agent_body_tabs:v1', JSON.stringify({ 'local:w-101': 'terminal' }));
    const agent = { paneId: 'w-101:main.0', title: 'Codex', agentType: 'codex', ttydSrc: '/ttyd/w-101/?token=test' };
    api.listGroups.mockResolvedValue({ data: { groups: [
      { ...defaultGroups[0], pane_ids: [agent.paneId], pane_count: 1 },
      { ...defaultGroups[1], pane_ids: [agent.paneId], pane_count: 1 },
    ] } });

    render(<ProjectsPanel agents={[agent]} activeAgentId="w-101" onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument());

    fireEvent.click(document.querySelector('[data-id="project-list-item-2"]') as HTMLElement);
    const card = await waitFor(() => document.querySelector('[data-id="project-agent-card-w-101"]') as HTMLElement);
    fireEvent.click(card);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-tab-terminal-w-101"]')).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument());
    expect(document.querySelector('[data-id="project-agent-card-live-body-wrap"]')).not.toBeInTheDocument();
  });

  it('keeps inactive cards on their saved Terminal or role body', async () => {
    localStorage.setItem('cicy_project_agent_body_tabs:v1', JSON.stringify({
      'local:w-102': 'terminal',
      'local:w-103': 'role',
    }));
    const agents = [
      { paneId: 'w-101:main.0', title: 'Active', agentType: 'codex', ttydSrc: '/ttyd/w-101/?token=test' },
      { paneId: 'w-102:main.0', title: 'Terminal', agentType: 'codex', ttydSrc: '/ttyd/w-102/?token=test' },
      { paneId: 'w-103:main.0', title: 'Role', agentType: 'codex', ttydSrc: '/ttyd/w-103/?token=test' },
    ];
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: agents.map((agent) => agent.paneId), pane_count: agents.length }] },
    });

    render(<ProjectsPanel agents={agents} activeAgentId="w-101" onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-102"]')).toBeInTheDocument());
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-role-body-w-103"]')).toBeInTheDocument());
    expect(document.querySelector('[data-id="project-agent-card-w-102"] [data-id="project-agent-card-live-body-wrap"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-w-103"] [data-id="project-agent-card-live-body-wrap"]')).not.toBeInTheDocument();
  });

  it('hides the Terminal tab for remote Agents', async () => {
    const lastSeenAt = new Date().toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, '');
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['mac_local.w-200'], pane_count: 1 }] } });
    api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [{ instanceId: 'code-remote', teamId: 'mac_local', status: 'online', lastSeenAt }] } });
    api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [{ instanceId: 'code-remote', teamId: 'mac_local', agentId: 'w-200', title: 'Remote Codex', agentType: 'codex' }] } });

    render(<ProjectsPanel agents={[]} activeAgentId="mac_local.w-200" onOpenAgent={vi.fn()} />);

    expect(await screen.findByRole('tab', { name: '角色' })).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Terminal' })).not.toBeInTheDocument();
  });

  it('shows the agent role editor without a redundant toolbar and isolates its scrolling', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} activeAgentId="w-101" onOpenAgent={vi.fn()} />);

    fireEvent.click(await screen.findByRole('tab', { name: '角色' }));
    const roleBody = document.querySelector('[data-id="project-agent-card-role-body-w-101"]') as HTMLElement;
    expect(roleBody).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-role-toolbar"]')).not.toBeInTheDocument();
    const zoom = document.querySelector('[data-id="project-canvas-zoom-value"]');
    const zoomBeforeWheel = zoom?.textContent;
    fireEvent.wheel(roleBody, { deltaY: 100 });
    expect(zoom).toHaveTextContent(String(zoomBeforeWheel));
  });

  it('loads and saves a remote Agent persona through Cloud RPC', async () => {
    const lastSeenAt = new Date().toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, '');
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['mac_local.w-200'], pane_count: 1 }] } });
    api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [{ instanceId: 'code-remote', teamId: 'mac_local', status: 'online', lastSeenAt }] } });
    api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [{ instanceId: 'code-remote', teamId: 'mac_local', agentId: 'w-200', title: 'Remote Agent', agentType: 'cicy' }] } });
    api.sendCiCyCloudMessage.mockImplementation((_instanceId: string, _agentId: string, _senderId: string, text: string) => {
      const { op } = JSON.parse(text);
      return Promise.resolve({ data: { message: { id: `rpc-${op}` } } });
    });
    api.getCiCyCloudMessageStatus.mockImplementation((messageId: string) => Promise.resolve({ data: { reply: { text: JSON.stringify({
      ok: true,
      data: messageId === 'rpc-persona_save'
        ? { filename: 'AGENTS.md', guidance: '# Updated role', systemPrompt: 'Remote system', meta: 'name: remote' }
        : { filename: 'AGENTS.md', guidance: '# Remote role', systemPrompt: 'Remote system', meta: 'name: remote' },
    }) } } }));

    render(<ProjectsPanel agents={[]} activeAgentId="mac_local.w-200" onOpenAgent={vi.fn()} />);

    const roleTab = await screen.findByRole('tab', { name: '角色' });
    expect(roleTab).toBeEnabled();
    fireEvent.click(roleTab);
    const editor = await screen.findByLabelText('AGENTS.md');
    expect(editor).toHaveValue('# Remote role');
    expect(localStorage.getItem('cicy_remote_agent_persona:v1:code-remote:w-200')).toContain('# Remote role');
    expect(api.sendCiCyCloudMessage).toHaveBeenCalledWith('code-remote', 'w-200', '', JSON.stringify({ op: 'persona' }), 'rpc_request');

    fireEvent.change(editor, { target: { value: '# Updated role' } });
    fireEvent.click(document.querySelector('[data-id="remote-agent-role-save"]') as HTMLElement);
    await waitFor(() => expect(api.sendCiCyCloudMessage).toHaveBeenCalledWith('code-remote', 'w-200', '', JSON.stringify({ op: 'persona_save', guidance: '# Updated role' }), 'rpc_request'));
    await waitFor(() => expect(document.querySelector('[data-id="remote-agent-role-save"]')).toHaveTextContent('已保存'));

    fireEvent.click(document.querySelector('[data-id="remote-agent-role-tab-systemPrompt"]') as HTMLElement);
    const systemEditor = screen.getByLabelText('system.md');
    expect(systemEditor).toHaveValue('Remote system');
    fireEvent.change(systemEditor, { target: { value: 'Updated system' } });
    fireEvent.click(document.querySelector('[data-id="remote-agent-role-save"]') as HTMLElement);
    await waitFor(() => expect(api.sendCiCyCloudMessage).toHaveBeenCalledWith('code-remote', 'w-200', '', JSON.stringify({ op: 'persona_save', systemPrompt: 'Updated system' }), 'rpc_request'));

    fireEvent.click(document.querySelector('[data-id="remote-agent-role-tab-meta"]') as HTMLElement);
    const metaEditor = screen.getByLabelText('meta.yaml');
    expect(metaEditor).toHaveValue('name: remote');
    fireEvent.change(metaEditor, { target: { value: 'name: updated' } });
    fireEvent.click(document.querySelector('[data-id="remote-agent-role-save"]') as HTMLElement);
    await waitFor(() => expect(api.sendCiCyCloudMessage).toHaveBeenCalledWith('code-remote', 'w-200', '', JSON.stringify({ op: 'persona_save', meta: 'name: updated' }), 'rpc_request'));
  });

  it('shows card controls only when active and ignores IME confirmation Enter', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    agentSend.sendToAgent.mockResolvedValue(undefined);
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} statuses={{ 'w-101:main.0': { status: 'completed', latest_response: '上一轮回答' } }} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    const inactiveTabs = document.querySelector('[data-id="project-agent-card-tabs-w-101"]');
    const inactiveFooterSlot = document.querySelector('[data-id="project-agent-card-footer-slot-w-101"]');
    expect(inactiveTabs).toBeInTheDocument();
    expect(inactiveTabs).not.toHaveClass('invisible', 'pointer-events-none');
    expect(inactiveTabs).not.toHaveAttribute('aria-hidden', 'true');
    expect(inactiveFooterSlot).toBeInTheDocument();
    expect(inactiveFooterSlot).not.toHaveClass('invisible');
    expect(inactiveFooterSlot).toHaveClass('pointer-events-none', '[&>footer]:invisible');
    expect(inactiveFooterSlot).toHaveClass('bg-[#15161b]');
    expect(inactiveFooterSlot).toHaveAttribute('aria-hidden', 'true');
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-question-fixed"]')).not.toBeInTheDocument();
    const canvasNode = card.closest('[data-id="project-canvas-node-w-101"]') as HTMLElement;
    fireEvent.pointerDown(canvasNode, { button: 0, pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.pointerUp(canvasNode, { pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.click(card);

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('agent prompt footer did not open');
      return node as HTMLTextAreaElement;
    });
    expect(input.tagName).toBe('TEXTAREA');
    expect(document.querySelector('[data-id="project-agent-card-tabs-w-101"]')).not.toHaveClass('invisible');
    expect(document.querySelector('[data-id="project-agent-card-footer-slot-w-101"]')).not.toHaveClass('invisible');
    expect(document.querySelector('[data-id="project-agent-card-history-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-question-fixed"]')).not.toBeInTheDocument();
    fireEvent.click(card);
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument();

    fireEvent.change(input, { target: { value: '中文任务' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true });
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', shiftKey: true });
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();

    input.blur();
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('中文任务')).toBeInTheDocument();
    expect(screen.queryByText('上一轮回答')).not.toBeInTheDocument();
    expect(input).toHaveValue('');
    // sendToAgent resolving is only transport acknowledgement. The optimistic
    // pending reply must keep the three dots mounted until a terminal snapshot
    // arrives, otherwise the loading indicator disappears and reappears during
    // the handoff to server-side working status.
    expect(document.querySelector('[data-id="current-history-stream-loading"]')).toBeInTheDocument();
    expect(document.querySelectorAll('[data-id^="current-history-view-pending-placeholder-dot-"]')).toHaveLength(3);
    await waitFor(() => expect(input).toHaveFocus());
  });

  it('keeps the shared history body mounted when card selection changes', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    const historyBody = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-history-body-w-101"]');
      if (!node) throw new Error('shared history body did not render');
      return node;
    });
    expect(historyBody.querySelector('[data-id="current-history-view"]')).toBeInTheDocument();

    fireEvent.click(card);

    expect(document.querySelector('[data-id="project-agent-card-history-body-w-101"]')).toBe(historyBody);
  });

  it('keeps the loading stop control visible on an inactive card', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} statuses={{ 'w-101:main.0': { status: 'working' } }} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument());
    expect(document.querySelector('[data-id="project-agent-prompt-cancel-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-inactive-loading-w-101"]')).not.toBeInTheDocument();
  });

  it('offers separate move-to and add-to actions from the card More menu', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [
      { ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 },
      defaultGroups[1],
    ] } });
    const toast = vi.fn();
    window.addEventListener('show-toast', toast);
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);

    const menu = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-menu-w-101"]');
      if (!node) throw new Error('agent card menu did not render');
      return node as HTMLElement;
    });
    fireEvent.click(menu);
    expect(document.querySelector('[data-id="project-agent-card-move-to"]')).toHaveTextContent('移动到');
    expect(document.querySelector('[data-id="project-agent-card-add-to"]')).toHaveTextContent('添加到');
    expect(document.querySelector('[data-id="project-agent-card-move-submenu"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-add-submenu"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-add-project-2"]')).not.toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-add-to"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-add-submenu"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-submenu-label"]')).toHaveTextContent('添加到');
    fireEvent.mouseEnter(document.querySelector('[data-id="project-agent-card-move-to"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-move-submenu"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-submenu-label"]')).toHaveTextContent('移动到');
    fireEvent.click(document.querySelector('[data-id="project-agent-card-move-to"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-move-submenu"]')).toBeInTheDocument();
    fireEvent.mouseEnter(document.querySelector('[data-id="project-agent-card-add-to"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-add-submenu"]')).toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-add-project-2"]') as HTMLElement);
    expect(api.removeGroupPane).not.toHaveBeenCalled();
    await waitFor(() => expect(api.addGroupPane).toHaveBeenCalledWith(2, 'w-101:main.0', 'add'));
    await waitFor(() => expect(toast).toHaveBeenCalled());
    expect((toast.mock.calls[0][0] as CustomEvent).detail).toBe('已添加到「Existing」');
    expect(document.querySelector('[data-id="project-list-item-default"] [data-id="project-list-item-agent-count"]')).toHaveTextContent('1');
    expect(document.querySelector('[data-id="project-list-item-2"] [data-id="project-list-item-agent-count"]')).toHaveTextContent('1');
    window.removeEventListener('show-toast', toast);
  });

  it('reuses the existing Agent lifecycle and IM modals from the card More menu', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [
      { ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 },
    ] } });
    const onAgentsRefresh = vi.fn();
    render(
      <ProjectsPanel
        agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]}
        masterPaneId="w-1001"
        onAgentsRefresh={onAgentsRefresh}
        onOpenAgentFile={vi.fn()}
        onOpenAgent={vi.fn()}
      />,
    );

    const openMenu = async () => {
      const button = await waitFor(() => {
        const node = document.querySelector('[data-id="project-agent-card-menu-w-101"]');
        if (!node) throw new Error('project Agent More menu did not render');
        return node as HTMLElement;
      });
      fireEvent.click(button);
    };

    await openMenu();
    expect(document.querySelector('[data-id="project-agent-card-action-restart-w-101"]')).toHaveTextContent('重启');
    expect(document.querySelector('[data-id="project-agent-card-action-update-w-101"]')).toHaveTextContent('更新');
    expect(document.querySelector('[data-id="project-agent-card-action-wechat-w-101"]')).toHaveTextContent('绑定微信');
    expect(document.querySelector('[data-id="project-agent-card-action-feishu-w-101"]')).toHaveTextContent('飞书会话');
    expect(document.querySelector('[data-id="project-agent-card-action-fork-w-101"]')).toHaveTextContent('Fork');

    fireEvent.click(document.querySelector('[data-id="project-agent-card-action-restart-w-101"]') as HTMLElement);
    const restartBody = await waitFor(() => document.querySelector('[data-id="modal-body"]') as HTMLElement);
    expect(restartBody).toHaveTextContent('重启 架构师？');
    expect(restartBody).not.toHaveTextContent('<strong>');
    expect(restartBody.querySelector('span')).toHaveTextContent('架构师');
    fireEvent.click(await waitFor(() => document.querySelector('[data-id="modal-confirm"]') as HTMLElement));
    await waitFor(() => expect(api.restartPane).toHaveBeenCalledWith('w-101'));
    await waitFor(() => expect(onAgentsRefresh).toHaveBeenCalled());

    await openMenu();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-action-update-w-101"]') as HTMLElement);
    expect(await waitFor(() => document.querySelector('[data-id="mock-update-agent-modal"]'))).toHaveTextContent('w-101:架构师');
    fireEvent.click(document.querySelector('[data-id="mock-update-agent-close"]') as HTMLElement);

    await openMenu();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-action-wechat-w-101"]') as HTMLElement);
    expect(await waitFor(() => document.querySelector('[data-id="mock-wechat-bind-modal"]'))).toHaveTextContent('w-101:架构师');
    fireEvent.click(document.querySelector('[data-id="mock-wechat-bind-close"]') as HTMLElement);

    await openMenu();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-action-feishu-w-101"]') as HTMLElement);
    expect(await waitFor(() => document.querySelector('[data-id="mock-feishu-bind-modal"]'))).toHaveTextContent('w-101:架构师');
    fireEvent.click(document.querySelector('[data-id="mock-feishu-bind-close"]') as HTMLElement);

    await openMenu();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-action-fork-w-101"]') as HTMLElement);
    expect(await waitFor(() => document.querySelector('[data-id="mock-fork-confirm-modal"]'))).toHaveTextContent('w-101:w-1001:1');
  });

  it('deletes a local non-master Agent from the Project card after confirmation', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [
      { ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 },
    ] } });
    const onAgentsRefresh = vi.fn();
    render(
      <ProjectsPanel
        agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]}
        masterPaneId="w-1001"
        onAgentsRefresh={onAgentsRefresh}
        onOpenAgent={vi.fn()}
      />,
    );

    fireEvent.click(await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-menu-w-101"]');
      if (!node) throw new Error('project Agent More menu did not render');
      return node as HTMLElement;
    }));
    const deleteButton = document.querySelector('[data-id="project-agent-card-delete"]') as HTMLElement;
    expect(deleteButton).toHaveTextContent('删除');
    fireEvent.click(deleteButton);
    fireEvent.click(await waitFor(() => document.querySelector('[data-id="modal-confirm"]') as HTMLElement));

    await waitFor(() => expect(api.deletePane).toHaveBeenCalledWith('w-101'));
    expect(api.removeGroupPane).not.toHaveBeenCalled();
    await waitFor(() => expect(onAgentsRefresh).toHaveBeenCalled());
  });

  it('matches TeamPanel action eligibility for a local cicy Agent', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: 'CiCy', agentType: 'cicy' }]} onOpenAgent={vi.fn()} />);
    fireEvent.click(await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-menu-w-101"]');
      if (!node) throw new Error('local project Agent More menu did not render');
      return node as HTMLElement;
    }));
    expect(document.querySelector('[data-id="project-agent-card-action-restart-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-action-update-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-action-wechat-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-action-feishu-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-action-fork-w-101"]')).toBeInTheDocument();
  });

  it('hides local-only TeamPanel actions for a Cloud Agent', async () => {
    const lastSeenAt = new Date().toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, '');
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['mac_local.w-200'], pane_count: 1 }] } });
    api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [{ instanceId: 'code-remote', teamId: 'mac_local', status: 'online', lastSeenAt }] } });
    api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [{ instanceId: 'code-remote', teamId: 'mac_local', agentId: 'w-200', title: 'Remote', agentType: 'codex' }] } });
    render(<ProjectsPanel agents={[]} activeAgentId="mac_local.w-200" onOpenAgent={vi.fn()} />);
    fireEvent.click(await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-menu-mac_local.w-200"]');
      if (!node) throw new Error('remote project Agent More menu did not render');
      return node as HTMLElement;
    }));
    expect(document.querySelector('[data-id^="project-agent-card-action-"]')).not.toBeInTheDocument();
  });

  it('keeps project-card selection synchronized with the shared active agent', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0', 'w-102:main.0'], pane_count: 2 }] },
    });
    const onActiveAgentChange = vi.fn();
    const { rerender } = render(
      <ProjectsPanel
        agents={[
          { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' },
          { paneId: 'w-102:main.0', title: '测试', agentType: 'codex' },
        ]}
        activeAgentId="w-101"
        onActiveAgentChange={onActiveAgentChange}
        onOpenAgent={vi.fn()}
      />,
    );
    const first = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('first project agent card did not render');
      return node as HTMLElement;
    });
    const second = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-102"]');
      if (!node) throw new Error('second project agent card did not render');
      return node as HTMLElement;
    });
    expect(first.className).toContain('border-blue-500');
    fireEvent.click(second);
    expect(onActiveAgentChange).toHaveBeenCalledWith('w-102');
    expect(getAgentSendTarget()).toEqual({ source: 'project', paneId: 'w-102:main.0' });

    rerender(
      <ProjectsPanel
        agents={[
          { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' },
          { paneId: 'w-102:main.0', title: '测试', agentType: 'codex' },
        ]}
        activeAgentId="w-102"
        onActiveAgentChange={onActiveAgentChange}
        onOpenAgent={vi.fn()}
      />,
    );
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-card-w-102"]')?.className).toContain('border-blue-500'));
    const routed = new CustomEvent('cicy:route-agent-prompt', {
      cancelable: true,
      detail: { paneId: 'w-102', text: '请先检查这个任务' },
    });
    window.dispatchEvent(routed);
    expect(routed.defaultPrevented).toBe(true);
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-prompt-input-w-102"]')).toHaveValue('请先检查这个任务'));
  });

  it('fills a targeted Project Agent prompt without submitting it', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    render(
      <ProjectsPanel
        agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]}
        activeAgentId="w-101"
        onOpenAgent={vi.fn()}
      />,
    );
    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('project agent prompt did not render');
      return node as HTMLTextAreaElement;
    });

    act(() => {
      window.dispatchEvent(new CustomEvent('cicy:fill-project-composer', {
        detail: { paneId: 'w-101:main.0', text: '请先检查这个任务' },
      }));
    });

    await waitFor(() => expect(input).toHaveValue('请先检查这个任务'));
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();
  });

  it('blocks contextual sends and shows a toast when no project agent is selected', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);
    await waitFor(() => {
      if (!document.querySelector('[data-id="project-agent-card-w-101"]')) throw new Error('project agent card did not render');
    });
    const toast = vi.fn();
    window.addEventListener('show-toast', toast);
    const routed = new CustomEvent('cicy:route-agent-prompt', {
      cancelable: true,
      detail: { paneId: 'w-101', text: '不应直接发送' },
    });
    window.dispatchEvent(routed);
    window.removeEventListener('show-toast', toast);

    expect(routed.defaultPrevented).toBe(true);
    expect(toast).toHaveBeenCalled();
    expect(document.querySelector('[data-id="project-agent-prompt-input-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-slot-w-101"]')).toHaveClass('invisible', 'pointer-events-none');
  });

  it('queues multiple prompts while thinking and sends them together when idle', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    agentSend.sendToAgent.mockResolvedValue(undefined);
    const thinkingAgent = { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'thinking' };
    const { rerender } = render(<ProjectsPanel agents={[thinkingAgent]} statuses={{ 'w-101:main.0': { status: 'thinking', updated_at: '1' } }} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    fireEvent.click(card);
    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('prompt did not open');
      return node as HTMLInputElement;
    });

    expect(document.querySelector('[data-id="project-agent-prompt-cancel-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-prompt-send-w-101"]')).not.toBeInTheDocument();

    fireEvent.change(input, { target: { value: '第一条' } });
    expect(document.querySelector('[data-id="project-agent-prompt-cancel-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-prompt-send-w-101"]')).toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    fireEvent.change(input, { target: { value: '第二条' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    expect(agentSend.sendToAgent).not.toHaveBeenCalled();
    expect(document.querySelectorAll('[data-id="project-agent-message-queue-item"]')).toHaveLength(1);
    expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).toHaveTextContent('第一条 第二条');
    expect(document.querySelector('[data-id="project-agent-message-queue-text"]')).toHaveStyle({ userSelect: 'text' });

    // The pane list can lag and still say thinking. The fresher status snapshot
    // reaching completed must release the queue.
    rerender(<ProjectsPanel agents={[thinkingAgent]} statuses={{ 'w-101:main.0': { status: 'completed', updated_at: '2' } }} onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledWith(
      'w-101:main.0',
      '第一条\n\n第二条',
      { submit: true, agentType: 'codex', fromComposer: true },
    ));
    expect(document.querySelectorAll('[data-id="project-agent-message-queue-item"]')).toHaveLength(0);
    expect(await screen.findByText(/第一条/)).toBeInTheDocument();
  });

  it('restores a queued prompt after the project panel reloads', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    agentSend.sendToAgent.mockResolvedValue(undefined);
    const thinkingAgent = { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'thinking' };
    const liveThinking = { 'w-101:main.0': { status: 'thinking', updated_at: '1' } };
    const first = render(<ProjectsPanel agents={[thinkingAgent]} statuses={liveThinking} onOpenAgent={vi.fn()} />);

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('project agent prompt did not render');
      return node as HTMLInputElement;
    });
    fireEvent.change(input, { target: { value: '刷新后继续发送' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).toHaveTextContent('刷新后继续发送'));
    await waitFor(() => expect(localStorage.getItem('cicy_project_agent_queue:v1')).toContain('刷新后继续发送'));
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();

    first.rerender(<ProjectsPanel key="after-reload" agents={[thinkingAgent]} statuses={liveThinking} onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).toHaveTextContent('刷新后继续发送'));
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();

    first.rerender(<ProjectsPanel key="after-reload" agents={[thinkingAgent]} statuses={{ 'w-101:main.0': { status: 'completed', updated_at: '2' } }} onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(agentSend.sendToAgent).toHaveBeenCalledWith(
      'w-101:main.0',
      '刷新后继续发送',
      { submit: true, agentType: 'codex', fromComposer: true },
    ));
  });

  it('keeps a queued image visible when another text message is queued', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    const thinkingAgent = { paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'thinking' };
    render(<ProjectsPanel agents={[thinkingAgent]} statuses={{ 'w-101:main.0': { status: 'thinking' } }} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    fireEvent.click(card);
    const fileInput = document.querySelector('[data-id="project-agent-prompt-file-input-w-101"]') as HTMLInputElement;
    fireEvent.change(fileInput, { target: { files: [new File(['png'], 'queued.png', { type: 'image/png' })] } });
    await waitFor(() => expect(document.querySelector('[data-id^="project-agent-card-attachment-"]')).toBeInTheDocument());
    await waitFor(() => expect((document.querySelector('[data-id="project-agent-prompt-send-w-101"]') as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(document.querySelector('[data-id="project-agent-prompt-send-w-101"]') as HTMLElement);

    await waitFor(() => expect(document.querySelector('[data-id^="project-agent-message-queue-attachment-"]')).toBeInTheDocument());
    const input = document.querySelector('[data-id="project-agent-prompt-input-w-101"]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '继续检查图片' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    expect(document.querySelector('[data-id^="project-agent-message-queue-attachment-"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).toHaveTextContent('继续检查图片');
    expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).not.toHaveTextContent('/home/cicy/cicy-ai/assets/queued.png');
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();

    fireEvent.click(document.querySelector('[data-id="project-agent-message-queue-edit-w-101"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).not.toBeInTheDocument();
    expect(input).toHaveValue('继续检查图片');
    expect(document.querySelector('[data-id="project-agent-card-attachment-media"]')).toBeInTheDocument();

    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).toBeInTheDocument());
    fireEvent.click(document.querySelector('[data-id="project-agent-message-queue-delete-w-101"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-message-queue-item"]')).not.toBeInTheDocument();
    expect(agentSend.sendToAgent).not.toHaveBeenCalled();
  });

  it('clears an idle-send image immediately while delivery is still pending', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    let finishSend!: () => void;
    agentSend.sendToAgent.mockReturnValue(new Promise<void>((resolve) => { finishSend = resolve; }));
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'idle' }]} onOpenAgent={vi.fn()} />);

    const card = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-w-101"]');
      if (!node) throw new Error('agent card did not render');
      return node as HTMLElement;
    });
    fireEvent.click(card);
    const fileInput = document.querySelector('[data-id="project-agent-prompt-file-input-w-101"]') as HTMLInputElement;
    fireEvent.change(fileInput, { target: { files: [new File(['png'], 'idle.png', { type: 'image/png' })] } });
    await waitFor(() => expect((document.querySelector('[data-id="project-agent-prompt-send-w-101"]') as HTMLButtonElement).disabled).toBe(false));
    const input = document.querySelector('[data-id="project-agent-prompt-input-w-101"]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '检查图片' } });
    fireEvent.click(document.querySelector('[data-id="project-agent-prompt-send-w-101"]') as HTMLElement);

    expect(input).toHaveValue('');
    expect(document.querySelector('[data-id="project-agent-card-attachments"]')).not.toBeInTheDocument();
    expect(await screen.findByText('检查图片')).toBeInTheDocument();
    expect(document.querySelector('[data-id="current-history-optimistic-q"] img')).toBeInTheDocument();
    finishSend();
  });
});
