import { useCallback, useEffect, useMemo, useRef, useState, lazy, Suspense, type ComponentType } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { X, ShieldCheck, Pencil, Search, MoreVertical, Trash2, RotateCcw, SquarePen } from 'lucide-react';
import apiService from '../../services/api';
import { normalizeAgentType } from '../../lib/agentType';
import { useDialogs } from '../ui/Modal';
import AgentAvatar from '../AgentAvatar';

// Lazy: the per-agent 3-tab editor (doc / meta.yaml / system.md) — keeps CodeMirror
// out of the roster's initial chunk; loads when a row is clicked.
const AgentDocRoleEditor = lazy(() => import('./AgentDocRoleEditor'));

const shortId = (id: string) => (id || '').replace(/:.*$/, '');

interface RosterAgent {
  pane_id: string;
  agent_type?: string;
  title?: string;
  use_custom_gateway?: boolean;
}
interface RosterBinding {
  name: string;
  title?: string;
  agent_type?: string;
}

type ModelPickerComponent = ComponentType<{
  paneId: string;
  agentDetail: any;
  onUpdated: (patch: any) => void;
  onOpen?: () => void;
}>;

interface Props {
  panes: RosterAgent[];
  bindings: RosterBinding[];
  masterPaneId: string;
  onClose: () => void;
  onRefresh?: () => void;
  onRenameTitle: (paneId: string, title: string) => void;
  ModelPicker: ModelPickerComponent;
}

type RosterFilter = 'all' | 'bound' | 'unbound';

// The team roster (花名册): a FULL-SCREEN modal listing every agent (bound +
// unbound). Clicking a row slides out a right-hand DRAWER with that agent's 3-tab
// editor — [CLAUDE.md/AGENTS.md] [meta.yaml] [system.md] — so you can quickly edit
// an agent's guidance doc and its role template's system prompt + meta without
// leaving the list. Clicking another row re-points the drawer.
export default function TeamRosterPanel({
  panes, bindings, masterPaneId, onClose, onRefresh,
  onRenameTitle, ModelPicker,
}: Props) {
  const { t } = useTranslation('workspace');
  const { confirm, node: dialogsNode } = useDialogs();
  // The agent whose editor drawer is open (pane_id), or null = list-only.
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  // Drawer width. On open, default to a 6:4 list:drawer split (drawer = 2/5 of the
  // viewport); the drag handle still lets you fine-tune from there.
  const [drawerWidth, setDrawerWidth] = useState(() => Math.round(window.innerWidth * 2 / 5));
  const drawerWasOpen = useRef(false);
  useEffect(() => {
    const open = !!selectedAgent;
    if (open && !drawerWasOpen.current) setDrawerWidth(Math.round(window.innerWidth * 2 / 5));
    drawerWasOpen.current = open;
  }, [selectedAgent]);
  const [filter, setFilter] = useState<RosterFilter>('all');

  // Drag the drawer's left edge to resize it (width = viewport-right − mouseX).
  const startDrawerDrag = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const onMove = (ev: MouseEvent) => {
      const w = Math.min(Math.max(window.innerWidth - ev.clientX, 380), window.innerWidth - 320);
      setDrawerWidth(w);
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, []);
  const [search, setSearch] = useState('');
  const [modelFilter, setModelFilter] = useState('');
  // Per-row "more" menu: which row, and the trigger rect (portal-positioned so
  // the menu escapes the table's overflow clipping).
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [menuRect, setMenuRect] = useState<DOMRect | null>(null);
  // Per-pane full detail (runtime_ai_provider_options etc.) for the gateway model
  // picker — fetched lazily for the gateway agents currently shown.
  const [details, setDetails] = useState<Record<string, any>>({});
  const [editing, setEditing] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState('');
  const editInputRef = useRef<HTMLInputElement>(null);

  const boundIds = useMemo(() => new Set(bindings.map((b) => shortId(b.name))), [bindings]);

  // Rows: every pane except the master itself, tagged bound/unbound, then filtered.
  const rows = useMemo(() => {
    const list = panes
      .filter((p) => shortId(p.pane_id) !== masterPaneId)
      .map((p) => ({ ...p, sid: shortId(p.pane_id), bound: boundIds.has(shortId(p.pane_id)) }));
    list.sort((a, b) => (a.bound === b.bound ? a.sid.localeCompare(b.sid) : a.bound ? -1 : 1));
    if (filter === 'bound') return list.filter((r) => r.bound);
    if (filter === 'unbound') return list.filter((r) => !r.bound);
    return list;
  }, [panes, boundIds, masterPaneId, filter]);

  const counts = useMemo(() => {
    const all = panes.filter((p) => shortId(p.pane_id) !== masterPaneId);
    const bound = all.filter((p) => boundIds.has(shortId(p.pane_id))).length;
    return { all: all.length, bound, unbound: all.length - bound };
  }, [panes, boundIds, masterPaneId]);

  // Lazily fetch detail for the rows shown: gateway rows need
  // runtime_ai_provider_options (the model picker), and every row's default_model
  // powers the model column + the "filter by model" control. Only the GET pane
  // detail carries these, so fetch once per pane.
  useEffect(() => {
    let cancelled = false;
    const need = rows.filter((r) => !details[r.sid]);
    need.forEach(async (r) => {
      try {
        const { data } = await apiService.getPane(r.pane_id);
        if (cancelled || !data) return;
        setDetails((prev) => ({ ...prev, [r.sid]: { ...(prev[r.sid] || {}), ...data } }));
      } catch { /* leave row detail empty on failure */ }
    });
    return () => { cancelled = true; };
  }, [rows, details]);

  // The model an agent runs: gateway → its chosen route, others → default_model.
  const modelOf = useCallback((sid: string) => {
    const d = details[sid];
    return String(d?.runtime_ai?.model || d?.default_model || '').trim();
  }, [details]);

  // Distinct models present, for the model filter dropdown.
  const models = useMemo(() => {
    const set = new Set<string>();
    rows.forEach((r) => { const m = modelOf(r.sid); if (m) set.add(m); });
    return Array.from(set).sort();
  }, [rows, modelOf]);

  // Apply the title search + model filter on top of the bound/unbound rows.
  const visibleRows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return rows.filter((r) => {
      if (q && !(`${r.title || ''} ${r.sid}`.toLowerCase().includes(q))) return false;
      if (modelFilter && modelOf(r.sid) !== modelFilter) return false;
      return true;
    });
  }, [rows, search, modelFilter, modelOf]);

  const startEdit = (sid: string, current: string) => {
    setEditing(sid);
    setDraftTitle(current);
    setTimeout(() => editInputRef.current?.select(), 0);
  };
  const commitEdit = (paneId: string) => {
    const next = draftTitle.trim();
    const sid = shortId(paneId);
    const cur = (rows.find((r) => r.sid === sid)?.title || '').trim();
    if (next && next !== cur) onRenameTitle(paneId, next);
    setEditing(null);
  };

  const toast = (msg: string) => window.dispatchEvent(new CustomEvent('show-toast', { detail: msg }));

  const openMenu = (sid: string, e: React.MouseEvent) => {
    e.stopPropagation(); // don't let the row click open the editor
    setMenuRect((e.currentTarget as HTMLElement).getBoundingClientRect());
    setMenuFor((prev) => (prev === sid ? null : sid));
  };

  const restartRow = async (sid: string, title: string) => {
    setMenuFor(null);
    const ok = await confirm({ body: t('rosterRestartConfirm', { defaultValue: '重启 {{name}}?', name: title }) });
    if (!ok) return;
    try {
      await apiService.restartPane(sid);
      toast(t('rosterRestarting', { defaultValue: '{{name}} 重启中…', name: title }));
      onRefresh?.();
    } catch { toast(t('rosterRestartFailed', { defaultValue: '重启失败' })); }
  };

  const deleteRow = async (sid: string, title: string) => {
    setMenuFor(null);
    const ok = await confirm({ body: t('rosterDeleteConfirm', { defaultValue: '删除 {{name}}?此操作不可撤销。', name: title }), danger: true });
    if (!ok) return;
    try {
      await apiService.deletePane(sid);
      toast(t('rosterDeleted', { defaultValue: '已删除 {{name}}', name: title }));
      onRefresh?.();
    } catch { toast(t('rosterDeleteFailed', { defaultValue: '删除失败' })); }
  };

  const FILTERS: { key: RosterFilter; label: string; n: number }[] = [
    { key: 'all', label: t('rosterFilterAll', { defaultValue: '全部' }), n: counts.all },
    { key: 'bound', label: t('rosterFilterBound', { defaultValue: '已绑定' }), n: counts.bound },
    { key: 'unbound', label: t('rosterFilterUnbound', { defaultValue: '未绑定' }), n: counts.unbound },
  ];

  // Portal'd to <body> so the full-screen modal escapes any ancestor stacking
  // context (cli-agent-stack etc.) — otherwise the app's left panel (z-130) and the
  // resize mask (z-140) paint OVER it. z-[190] sits above all app chrome but below
  // this modal's own menu/select portals (z-200) and confirm dialogs (z-9999999).
  return createPortal(
    <div data-id="team-roster-panel" className="fixed inset-0 z-[190] flex flex-col bg-[#0b0b0d]">
      {/* Full-screen modal: agent list on the left, a per-agent editor drawer on the
          right (opens when a row is clicked). */}
      {/* header */}
      <div data-id="team-roster-header" className="flex h-11 shrink-0 items-center justify-between border-b border-[var(--vsc-border)] px-3">
        <div className="flex items-center gap-2">
          <span data-id="team-roster-title" className="text-[13px] font-medium text-zinc-200">{t('rosterTitle', { defaultValue: '团队花名册' })}</span>
          <span data-id="team-roster-total" className="text-[11px] text-zinc-500">{t('rosterCount', { defaultValue: '{{n}} 个', n: counts.all })}</span>
        </div>
        <div className="flex items-center gap-1">
          <button data-id="team-roster-close" type="button" onClick={onClose} title={t('rosterClose', { defaultValue: '关闭' })}
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200">
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* body: agent list (left) + editor drawer (right) */}
      <div data-id="team-roster-body" className="flex min-h-0 flex-1 overflow-hidden">
      <div data-id="team-roster-left" className="flex min-w-0 flex-1 flex-col">
      {/* filter tabs */}
      <div data-id="team-roster-filters" className="flex shrink-0 items-center gap-1 border-b border-[var(--vsc-border)] px-3 py-1.5">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            data-id={`team-roster-filter-${f.key}`}
            type="button"
            onClick={() => setFilter(f.key)}
            className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors ${
              filter === f.key ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
            }`}
          >
            {f.label}
            <span className={`rounded px-1 text-[10px] ${filter === f.key ? 'bg-white/10 text-zinc-300' : 'bg-white/[0.04] text-zinc-500'}`}>{f.n}</span>
          </button>
        ))}
      </div>

      {/* search by title + filter by model */}
      <div data-id="team-roster-toolbar" className="flex shrink-0 items-center gap-2 border-b border-[var(--vsc-border)] px-3 py-1.5">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-zinc-600" />
          <input
            data-id="team-roster-search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('rosterSearchPlaceholder', { defaultValue: '按标题搜索…' })}
            className="w-full rounded-md border border-white/[0.08] bg-black/30 py-1 pl-7 pr-2 text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-blue-500/40"
          />
        </div>
        <select
          data-id="team-roster-model-filter"
          value={modelFilter}
          onChange={(e) => setModelFilter(e.target.value)}
          className="max-w-[200px] rounded-md border border-white/[0.08] bg-black/30 px-2 py-1 text-[12px] text-zinc-300 outline-none focus:border-blue-500/40"
        >
          <option value="">{t('rosterModelAll', { defaultValue: '全部模型' })}</option>
          {models.map((m) => <option key={m} value={m}>{m}</option>)}
        </select>
      </div>

      {/* table — horizontal scroll, no text wrapping */}
      <div data-id="team-roster-list" className="min-h-0 flex-1 overflow-x-auto overflow-y-auto">
        {visibleRows.length === 0 ? (
          <div data-id="team-roster-empty" className="flex h-full items-center justify-center text-[12px] text-zinc-600">
            {t('rosterEmpty', { defaultValue: '没有匹配的 agent' })}
          </div>
        ) : (
          <table data-id="team-roster-table" className="w-max min-w-full border-collapse whitespace-nowrap text-[12px]">
            <thead>
              <tr className="sticky top-0 z-10 bg-[#0b0b0d] text-left text-[11px] text-zinc-500">
                <th className="px-3 py-2 font-medium">{t('rosterColAgent', { defaultValue: 'Agent' })}</th>
                <th className="px-2 py-2 font-medium">{t('rosterColTypeAccess', { defaultValue: '类型 / 接入' })}</th>
                <th className="px-2 py-2 font-medium">{t('rosterColModelProvider', { defaultValue: '模型 / 供应商' })}</th>
                <th className="px-3 py-2 text-right font-medium">{t('rosterColActions', { defaultValue: '操作' })}</th>
              </tr>
            </thead>
            <tbody>
              {visibleRows.map((r) => {
                const type = normalizeAgentType(r.agent_type) || '—';
                const isEditing = editing === r.sid;
                return (
                  <tr
                    key={r.sid}
                    data-id={`team-roster-row-${r.sid}`}
                    className={`h-[72px] border-b border-white/[0.04] ${
                      shortId(selectedAgent || '') === r.sid ? 'bg-blue-500/[0.12]' : 'hover:bg-white/[0.02]'
                    }`}
                  >
                    {/* agent: avatar + inline-editable title + id + bound dot */}
                    <td data-id={`team-roster-cell-agent-${r.sid}`} className="px-3 py-1 align-middle">
                      <div className="flex items-center gap-2.5">
                        <span data-id={`team-roster-bound-${r.sid}`} title={r.bound ? t('rosterBound', { defaultValue: '已绑定' }) : t('rosterUnbound', { defaultValue: '未绑定' })}
                          className={`h-1.5 w-1.5 shrink-0 rounded-full ${r.bound ? 'bg-emerald-400/80' : 'bg-zinc-600'}`} />
                        <AgentAvatar agentType={r.agent_type} title={r.title || r.sid} variant="stack" />
                        <div className="min-w-0">
                          {/* fixed-height row so toggling edit mode never shifts the layout */}
                          <div className="flex h-6 items-center">
                            {isEditing ? (
                              <input
                                ref={editInputRef}
                                data-id={`team-roster-title-input-${r.sid}`}
                                value={draftTitle}
                                onClick={(e) => e.stopPropagation()}
                                onChange={(e) => setDraftTitle(e.target.value)}
                                onBlur={() => commitEdit(r.pane_id)}
                                onKeyDown={(e) => {
                                  e.stopPropagation();
                                  if (e.key === 'Enter') { e.preventDefault(); commitEdit(r.pane_id); }
                                  if (e.key === 'Escape') { e.preventDefault(); setEditing(null); }
                                }}
                                className="h-6 w-40 rounded border border-blue-500/40 bg-black/40 px-1.5 text-[12px] text-zinc-100 outline-none"
                              />
                            ) : (
                              // Plain title (display only). Switching agents is the
                              // dedicated ✏️ edit button; rename is the hover pencil.
                              <div data-id={`team-roster-title-${r.sid}`} className="group flex h-6 items-center gap-1 text-[12px] text-zinc-200">
                                <span className="truncate">{r.title || r.sid}</span>
                                <button
                                  data-id={`team-roster-rename-${r.sid}`}
                                  type="button"
                                  onClick={(e) => { e.stopPropagation(); startEdit(r.sid, r.title || ''); }}
                                  title={t('rosterEditTitle', { defaultValue: '改名' })}
                                  className="shrink-0 rounded p-0.5 text-zinc-600 opacity-0 hover:bg-white/[0.08] hover:text-zinc-300 group-hover:opacity-100"
                                >
                                  <Pencil className="h-3 w-3" />
                                </button>
                              </div>
                            )}
                          </div>
                          <div data-id={`team-roster-sid-${r.sid}`} className="truncate text-[10px] text-zinc-600">{r.sid}</div>
                        </div>
                      </div>
                    </td>
                    {/* agent type (gateway-vs-official now lives in the model column) */}
                    <td data-id={`team-roster-cell-type-${r.sid}`} className="px-2 py-1 align-middle">
                      <span data-id={`team-roster-type-${r.sid}`} className="text-[12px] text-zinc-300">{type}</span>
                    </td>
                    {/* model on top, provider below */}
                    <td data-id={`team-roster-cell-model-${r.sid}`} className="px-2 py-1 align-middle" onClick={(e) => e.stopPropagation()}>
                      <div className="flex flex-col items-start gap-1">
                        {r.use_custom_gateway ? (
                          <ModelPicker
                            paneId={r.pane_id}
                            agentDetail={details[r.sid] || null}
                            onUpdated={(patch) => setDetails((prev) => ({ ...prev, [r.sid]: { ...(prev[r.sid] || {}), ...patch } }))}
                            onOpen={async () => {
                              try {
                                const { data } = await apiService.getPane(r.pane_id);
                                if (data) setDetails((prev) => ({ ...prev, [r.sid]: { ...(prev[r.sid] || {}), ...data } }));
                              } catch { /* ignore */ }
                            }}
                          />
                        ) : (
                          // Official (login) agents run on their login's own model — the
                          // DB default_model / runtime_ai is the GATEWAY routing config,
                          // which doesn't apply here. Showing it would be misleading, so
                          // just mark 官方.
                          <span data-id={`team-roster-model-${r.sid}`} className="inline-flex items-center gap-1 text-[12px] text-zinc-400">
                            <ShieldCheck className="h-3 w-3 text-zinc-500" /> {t('rosterOfficial', { defaultValue: '官方' })}
                          </span>
                        )}
                      </div>
                    </td>
                    {/* actions: edit (opens doc/role drawer) + more menu (restart/delete) */}
                    <td data-id={`team-roster-cell-actions-${r.sid}`} className="px-3 py-1 align-middle">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          data-id={`team-roster-edit-${r.sid}`}
                          type="button"
                          onClick={() => setSelectedAgent(r.pane_id)}
                          title={t('rosterEditAgent', { defaultValue: '编辑 doc / role' })}
                          className={`inline-flex h-7 w-7 items-center justify-center rounded-md border ${
                            shortId(selectedAgent || '') === r.sid
                              ? 'border-blue-500/40 bg-blue-500/15 text-blue-300'
                              : 'border-white/[0.08] text-zinc-300 hover:bg-white/[0.06] hover:text-zinc-100'
                          }`}
                        >
                          <SquarePen className="h-3.5 w-3.5" />
                        </button>
                        <button
                          data-id={`team-roster-more-${r.sid}`}
                          type="button"
                          onClick={(e) => openMenu(r.sid, e)}
                          title={t('rosterMore', { defaultValue: '更多' })}
                          className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-white/[0.08] text-zinc-300 hover:bg-white/[0.06] hover:text-zinc-100"
                        >
                          <MoreVertical className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      </div>{/* /team-roster-left */}

      {/* editor drawer — opens on row click; drag the left edge to resize */}
      {selectedAgent ? (
        <>
          <div
            data-id="team-roster-drawer-resize"
            onMouseDown={startDrawerDrag}
            className="w-1 shrink-0 cursor-col-resize bg-[var(--vsc-border)] transition-colors hover:bg-blue-400/70"
            title={t('rosterDrawerResize', { defaultValue: '拖动调整宽度' })}
          />
          <div data-id="team-roster-drawer" className="flex shrink-0 flex-col" style={{ width: drawerWidth }}>
            <div data-id="team-roster-drawer-header" className="flex h-9 shrink-0 items-center border-b border-[var(--vsc-border)] px-3">
              <span className="truncate text-[12px] font-medium text-zinc-200">
                {(rows.find((r) => r.sid === shortId(selectedAgent))?.title) || shortId(selectedAgent)}
                <span className="ml-1.5 text-[11px] text-zinc-500">{shortId(selectedAgent)}</span>
              </span>
            </div>
            <div className="min-h-0 flex-1">
              <Suspense fallback={<div className="flex h-full items-center justify-center text-[12px] text-zinc-600">加载编辑器…</div>}>
                <AgentDocRoleEditor paneId={selectedAgent} className="h-full w-full" />
              </Suspense>
            </div>
          </div>
        </>
      ) : null}
      </div>{/* /team-roster-body */}

      {/* per-row "more" menu — portal'd to escape the table's overflow clipping */}
      {menuFor && menuRect ? createPortal(
        <div data-id="team-roster-menu-portal">
          <div className="fixed inset-0 z-[200]" onClick={() => setMenuFor(null)} />
          <div
            data-id="team-roster-menu"
            className="fixed z-[201] min-w-[128px] overflow-hidden rounded-md border border-white/10 bg-[#16171b] py-1 shadow-xl shadow-black/40"
            style={{ top: menuRect.bottom + 4, left: Math.max(8, menuRect.right - 128) }}
          >
            {(() => {
              const row = rows.find((r) => r.sid === menuFor);
              if (!row) return null;
              const title = row.title || row.sid;
              return (
                <>
                  <button
                    data-id={`team-roster-menu-restart-${row.sid}`}
                    type="button"
                    onClick={() => restartRow(row.sid, title)}
                    className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06]"
                  >
                    <RotateCcw className="h-3.5 w-3.5" /> {t('rosterRestart', { defaultValue: '重启' })}
                  </button>
                  <button
                    data-id={`team-roster-menu-delete-${row.sid}`}
                    type="button"
                    onClick={() => deleteRow(row.sid, title)}
                    className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12px] text-rose-300 hover:bg-rose-500/10"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> {t('rosterDelete', { defaultValue: '删除' })}
                  </button>
                </>
              );
            })()}
          </div>
        </div>, document.body) : null}
      {dialogsNode}
    </div>,
    document.body,
  );
}
