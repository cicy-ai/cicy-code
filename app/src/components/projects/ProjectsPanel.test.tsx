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
  getIMAccounts: vi.fn(),
  getCiCyCloudInstances: vi.fn(),
  getCiCyCloudAgents: vi.fn(),
  sendCiCyCloudMessage: vi.fn(),
  getCiCyCloudMessageStatus: vi.fn(),
  uploadAssetFile: vi.fn(),
  getMemoryTemplate: vi.fn(),
  saveMemoryTemplate: vi.fn(),
}));
const agentSend = vi.hoisted(() => ({ sendToAgent: vi.fn() }));

vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => agentSend);
vi.mock('../AgentAvatar', () => ({ default: () => <span data-testid="agent-avatar" /> }));
vi.mock('../terminal/TerminalView', () => ({ default: ({ ttydSrc }: { ttydSrc: string }) => <div data-id="mock-project-terminal">{ttydSrc}</div> }));

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
  api.getIMAccounts.mockResolvedValue({ data: { accounts: [{ platform: 'cicy_cloud', config: { team_id: 'local_team' } }] } });
  api.getCiCyCloudInstances.mockResolvedValue({ data: { instances: [] } });
  api.getCiCyCloudAgents.mockResolvedValue({ data: { agents: [] } });
  api.addGroupPane.mockResolvedValue({ data: { success: true } });
  api.updateGroupPaneLayout.mockResolvedValue({ data: { success: true } });
  api.uploadAssetFile.mockResolvedValue({ data: { file: { file_ref: '/home/cicy/cicy-ai/assets/queued.png' } } });
  api.getMemoryTemplate.mockResolvedValue({ data: { content: '# Global', path: '/home/cicy/cicy-ai/memory/global.md' } });
  api.saveMemoryTemplate.mockResolvedValue({ data: { saved: true } });
  vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:queued-image'), revokeObjectURL: vi.fn() });
});

describe('<ProjectsPanel /> floating action button', () => {
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
    expect(document.querySelector('[data-id="project-agent-card-w-101"]')).toHaveStyle({ width: '420px', height: '360px' });
    expect(document.querySelector('[data-id="project-canvas-zoom-value"]')).toHaveTextContent('125%');
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

  it('rechecks visibility when a late server layout replaces visible cached cards', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    let resolveLayout: (value: any) => void = () => {};
    api.getGroup.mockReturnValue(new Promise((resolve) => { resolveLayout = resolve; }));
    localStorage.setItem('cicy_project_view:default', JSON.stringify({
      zoom: 1,
      pan: { x: 60, y: 60 },
      layouts: { 'w-101': { x: 40, y: 40, z: 1, width: 300, height: 320 } },
    }));

    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'claude' }]} onOpenAgent={vi.fn()} />);
    await waitFor(() => expect(document.querySelector('[data-id="project-canvas-node-w-101"]')).toHaveStyle({ left: '40px', top: '40px' }));
    const canvas = document.querySelector('[data-id="project-infinite-canvas"]') as HTMLElement;
    Object.defineProperty(canvas, 'clientWidth', { configurable: true, value: 1000 });
    Object.defineProperty(canvas, 'clientHeight', { configurable: true, value: 700 });

    resolveLayout({ data: { panes: [{ pane_id: 'w-101:main.0', pos_x: -1600, pos_y: -1000, width: 300, height: 320, z_index: 1 }] } });

    await waitFor(() => expect(document.querySelector('[data-id="project-canvas-node-w-101"]')).toHaveStyle({ left: '-1600px', top: '-1000px' }));
    await waitFor(() => {
      const cached = JSON.parse(localStorage.getItem('cicy_project_view:default') || '{}');
      expect(cached.pan).not.toEqual({ x: 60, y: 60 });
      expect(cached.pan.x).toBeGreaterThan(1000);
      expect(cached.pan.y).toBeGreaterThan(700);
    });
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
  it('switches card body tabs and opens the full history in the right panel', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    const onOpenHistory = vi.fn();
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} onOpenHistory={onOpenHistory} />);

    const historyTab = await screen.findByRole('tab', { name: '会话' });
    expect(historyTab).toHaveAttribute('aria-selected', 'true');
    expect(document.querySelector('[data-id="project-agent-card-history-sentinel"]')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '完整历史' }));
    expect(onOpenHistory).toHaveBeenCalledWith('w-101:main.0');
    expect(document.querySelector('[data-id="project-agent-card-history-body-w-101"]')).not.toBeInTheDocument();
  });

  it('toggles an inline terminal for non-cicy agents only', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0', 'w-102:main.0'], pane_count: 2 }] },
    });
    render(<ProjectsPanel agents={[
      { paneId: 'w-101:main.0', title: 'Codex', agentType: 'codex', ttydSrc: '/ttyd/w-101/?token=test' },
      { paneId: 'w-102:main.0', title: 'CiCy', agentType: 'cicy', ttydSrc: '/ttyd/w-102/?token=test' },
    ]} onOpenAgent={vi.fn()} />);

    const toggle = await screen.findByRole('tab', { name: 'Terminal' });
    expect(document.querySelector('[data-id="project-agent-card-tab-terminal-w-102"]')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="mock-project-terminal"]')).toHaveTextContent('/ttyd/w-101/');
    fireEvent.click(toggle);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="project-agent-card-tab-history-w-101"]') as HTMLElement);
    expect(document.querySelector('[data-id="project-agent-card-terminal-body-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).toBeInTheDocument();
  });

  it('shows the agent role editor without a redundant toolbar and isolates its scrolling', async () => {
    api.listGroups.mockResolvedValue({ data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] } });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} />);

    fireEvent.click(await screen.findByRole('tab', { name: '角色' }));
    const roleBody = document.querySelector('[data-id="project-agent-card-role-body-w-101"]') as HTMLElement;
    expect(roleBody).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-role-toolbar"]')).not.toBeInTheDocument();
    const zoom = document.querySelector('[data-id="project-canvas-zoom-value"]');
    expect(zoom).toHaveTextContent('100%');
    fireEvent.wheel(roleBody, { deltaY: 100 });
    expect(zoom).toHaveTextContent('100%');
  });

  it('shows the realtime latest question instead of a stale reply snapshot', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    api.getAgentCurrentReply.mockResolvedValue({ data: { question: '上一条消息', items: [], status: 'thinking' } });
    render(<ProjectsPanel
      agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex', status: 'thinking' }]}
      statuses={{ 'w-101:main.0': { status: 'thinking', latest_question: '最新消息', updated_at: '2' } }}
      onOpenAgent={vi.fn()}
    />);

    expect(await screen.findByText('最新消息')).toBeInTheDocument();
    expect(screen.queryByText('上一条消息')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-output-loading"]')).toHaveTextContent('Working· 0s');
    expect(document.querySelector('[data-id="project-agent-card-live-body"] [data-id="project-agent-card-stream-loading"]')).toBeInTheDocument();
    expect(document.querySelectorAll('[data-id="project-agent-card-stream-loading-dot"]')).toHaveLength(3);
  });

  it('renders reply markdown and unwraps Codex exec tool calls', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    api.getAgentCurrentReply.mockResolvedValue({
      data: {
        question: '检查输出\n\n[image.png](/home/cicy/cicy-ai/assets/2026/08/15/example.png)',
        items: [
          { type: 'text', text: '**结论**\n\n- 第一项\n- 第二项' },
          { type: 'text', text: '⚠️ 生成失败（HTTP 502）\n\nlocal error: tls: bad record MAC' },
          { type: 'tool_use', name: 'read', input: '{"path":"/tmp/old.txt"}', output: '旧工具结果' },
          {
            type: 'tool_use',
            name: 'exec',
            input: 'const r = await tools.exec_command({cmd:"cicy-knowledge recall \\\"tool use\\\"",workdir:"/tmp"}); text(r.output);',
            output: 'Script completed\nWall time 0.1 seconds\nOutput:\n- 命中一\n- 命中二',
          },
          { type: 'tool_use', name: 'wait', input: '{"cell_id":"117","yield_time_ms":30000}' },
          { type: 'thinking', thinking: '继续检查另一部分' },
          { type: 'tool_use', name: 'search', input: '{"query":"new"}' },
        ],
        status: 'completed',
        started_at: '2026-08-17T04:00:00Z',
        updated_at: '2026-08-17T04:00:05Z',
      },
    });

    const onOpenHistory = vi.fn();
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} onOpenAgent={vi.fn()} onOpenHistory={onOpenHistory} />);

    const response = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-card-latest-response"]');
      if (!node) throw new Error('reply did not render');
      return node as HTMLElement;
    });
    expect(response.querySelector('strong')).toHaveTextContent('结论');
    expect(await screen.findByText('检查输出')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-latest-question"] img')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-latest-question"]')).not.toHaveTextContent('image.png');
    expect(document.querySelector('[data-id="project-agent-card-question-attachment"]')).not.toHaveTextContent('/home/cicy/cicy-ai/assets/');
    expect(response.querySelectorAll('li')).toHaveLength(2);
    expect(await screen.findByText('exec_command')).toBeInTheDocument();
    expect(document.querySelectorAll('[data-id="project-agent-card-reply-tool"]')).toHaveLength(2);
    expect(Array.from(document.querySelectorAll('[data-id="project-agent-card-tool-count"]')).map((node) => node.textContent)).toEqual(['2', '1']);
    expect(document.querySelector('[data-id="project-agent-card-output-loading"]')).toHaveTextContent('Workedfor 5s');
    expect(document.querySelector('[data-id="project-agent-card-live-body"]')).not.toContainElement(document.querySelector('[data-id="project-agent-card-latest-question"]'));
    expect(document.querySelector('[data-id="project-agent-card-live-body"]')).not.toContainElement(document.querySelector('[data-id="project-agent-card-output-loading"]'));
    expect(screen.queryByText('read')).not.toBeInTheDocument();
    expect(screen.queryByText('旧工具结果')).not.toBeInTheDocument();
    expect(screen.queryByText('wait')).not.toBeInTheDocument();
    expect(screen.queryByText(/cell_id/)).not.toBeInTheDocument();
    expect(screen.queryByText(/bad record MAC/)).not.toBeInTheDocument();
    expect(screen.queryByText(/const r = await tools\.exec_command/)).not.toBeInTheDocument();

    fireEvent.click(document.querySelector('[data-id="project-agent-card-reply-tool"]') as HTMLElement);
    expect(onOpenHistory).toHaveBeenCalledWith('w-101:main.0');
    expect(document.querySelector('[data-id="project-agent-card-tool-result-markdown"]')).not.toBeInTheDocument();
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
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-tabs-w-101"]')).not.toBeInTheDocument();
    const canvasNode = card.closest('[data-id="project-canvas-node-w-101"]') as HTMLElement;
    fireEvent.pointerDown(canvasNode, { button: 0, pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.pointerUp(canvasNode, { pointerId: 1, clientX: 120, clientY: 120 });
    fireEvent.click(document.querySelector('[data-id="project-agent-card-live-body"]') as HTMLElement);

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="project-agent-prompt-input-w-101"]');
      if (!node) throw new Error('agent prompt footer did not open');
      return node as HTMLTextAreaElement;
    });
    expect(input.tagName).toBe('TEXTAREA');
    expect(document.querySelector('[data-id="project-agent-card-tabs-w-101"]')).toBeInTheDocument();
    expect(document.querySelector('[data-id="project-agent-card-history-w-101"]')).toBeInTheDocument();
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
    expect(document.querySelector('[data-id="project-agent-card-stream-loading"]')).toBeInTheDocument();
    expect(document.querySelectorAll('[data-id="project-agent-card-stream-loading-dot"]')).toHaveLength(3);
    await waitFor(() => expect(input).toHaveFocus());
  });

  it('keeps the loading stop control visible on an inactive card', async () => {
    api.listGroups.mockResolvedValue({
      data: { groups: [{ ...defaultGroups[0], pane_ids: ['w-101:main.0'], pane_count: 1 }] },
    });
    render(<ProjectsPanel agents={[{ paneId: 'w-101:main.0', title: '架构师', agentType: 'codex' }]} statuses={{ 'w-101:main.0': { status: 'working' } }} onOpenAgent={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('[data-id="project-agent-inactive-cancel-w-101"]')).toBeInTheDocument());
    expect(document.querySelector('[data-id="project-agent-card-footer-w-101"]')).not.toBeInTheDocument();
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
    expect(document.querySelector('[data-id="project-agent-prompt-input-w-101"]')).toHaveValue('');
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
    expect(document.querySelector('[data-id="project-agent-card-question-attachment"] img')).toBeInTheDocument();
    finishSend();
  });
});
