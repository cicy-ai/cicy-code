import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { advanceStatus, statusClasses, humanTime, shortId } from './TodoPanel';
import type { TodoStatus } from './TodoPanel';

// ── i18n + api mocks ────────────────────────────────────────────────────────
// The component only needs `t` to return a stable string. We echo the key (and
// interpolate {{id}}/{{title}}) so assertions can match on the key names.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => {
      if (!opts) return key;
      return Object.keys(opts).reduce(
        (acc, k) => acc.replace(new RegExp(`{{${k}}}`, 'g'), String(opts[k])),
        key,
      );
    },
  }),
}));

// vi.hoisted so the object exists before the hoisted vi.mock factory runs.
const api = vi.hoisted(() => ({
  listTodos: vi.fn(),
  getTodoCounts: vi.fn(),
  getPanes: vi.fn(),
  addTodo: vi.fn(),
  sendCommand: vi.fn(),
  updateTodo: vi.fn(),
  deleteTodo: vi.fn(),
}));
vi.mock('../services/api', () => ({ default: api }));

import TodoPanel from './TodoPanel';

const mkTodo = (over: Partial<any> = {}) => ({
  id: '1', title: 'sample', status: 'todo',
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  pane_id: 'w-1001', ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  api.getPanes.mockResolvedValue({ data: [] });
  api.getTodoCounts.mockResolvedValue({ data: { all: 0, todo: 0, test: 0, done: 0, dropped: 0 } });
  api.listTodos.mockResolvedValue({ data: { todos: [] } });
});

// ── pure helpers ─────────────────────────────────────────────────────────────
describe('advanceStatus', () => {
  it('walks the todo → test → done lifecycle', () => {
    expect(advanceStatus('todo')).toBe('test');
    expect(advanceStatus('test')).toBe('done');
  });
  it('wraps done/dropped back to todo', () => {
    expect(advanceStatus('done')).toBe('todo');
    expect(advanceStatus('dropped')).toBe('todo');
  });
});

describe('shortId', () => {
  it('strips the tmux suffix to the short pane id', () => {
    expect(shortId('w-1001:main.0')).toBe('w-1001');
  });
  it('leaves a bare id untouched', () => {
    expect(shortId('w-10025')).toBe('w-10025');
  });
});

describe('statusClasses', () => {
  it('returns a distinct stripe colour for every status incl. test', () => {
    const stripes = (['todo', 'test', 'done', 'dropped'] as TodoStatus[]).map(
      (s) => statusClasses(s).stripe,
    );
    expect(new Set(stripes).size).toBe(4); // all distinct
    expect(statusClasses('test').stripe).toBe('bg-cyan-400');
  });
});

describe('humanTime', () => {
  const tr = (key: string, opts?: Record<string, unknown>) =>
    opts ? `${key}:${JSON.stringify(opts)}` : key;
  it('returns a dash for an empty timestamp', () => {
    expect(humanTime('', tr)).toBe('-');
  });
  it('buckets recent times as "just now"', () => {
    expect(humanTime(new Date().toISOString(), tr)).toBe('timeJustNow');
  });
  it('buckets minutes / hours ago with the right count', () => {
    const fiveMin = new Date(Date.now() - 5 * 60 * 1000).toISOString();
    expect(humanTime(fiveMin, tr)).toBe('timeMinutesAgo:{"n":5}');
    const twoHr = new Date(Date.now() - 2 * 3600 * 1000).toISOString();
    expect(humanTime(twoHr, tr)).toBe('timeHoursAgo:{"n":2}');
  });
});

// ── component ────────────────────────────────────────────────────────────────
describe('<TodoPanel />', () => {
  it('renders all four status columns including the test lane', async () => {
    render(<TodoPanel paneId="w-1001" active isMaster />);
    await waitFor(() => expect(api.listTodos).toHaveBeenCalled());
    for (const key of ['status.todo', 'status.test', 'status.done', 'status.dropped']) {
      expect(screen.getByText(key)).toBeInTheDocument();
    }
  });

  it('buckets a server todo into its status column', async () => {
    api.listTodos.mockResolvedValue({ data: { todos: [mkTodo({ id: '7', title: 'wire up auth', status: 'test' })] } });
    render(<TodoPanel paneId="w-1001" active isMaster />);
    expect(await screen.findByText('wire up auth')).toBeInTheDocument();
    // its stable id is shown on the card
    expect(await screen.findByText('7')).toBeInTheDocument();
  });

  it('optimistically shows a new todo before the server round-trip resolves', async () => {
    let resolveAdd: (v: unknown) => void = () => {};
    api.addTodo.mockReturnValue(new Promise((r) => { resolveAdd = r; }));
    render(<TodoPanel paneId="w-1001" active isMaster />);
    await waitFor(() => expect(api.listTodos).toHaveBeenCalled());

    const input = screen.getByPlaceholderText('quickAddPlaceholder');
    fireEvent.change(input, { target: { value: 'new optimistic task' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    // Card appears immediately — addTodo has NOT resolved yet.
    expect(await screen.findByText('new optimistic task')).toBeInTheDocument();
    expect(api.addTodo).toHaveBeenCalled();
    resolveAdd({});
  });
});
