import { useCallback, useEffect, useMemo, useRef, useState, type ComponentType } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { X, RefreshCw, FileText, Wrench, Cloud, ShieldCheck, Pencil, Search, MoreVertical, Trash2, RotateCcw } from 'lucide-react';
import apiService from '../../services/api';
import { normalizeAgentType } from '../../lib/agentType';
import { useDialogs } from '../ui/Modal';
import AgentAvatar from '../AgentAvatar';
import TipBelow from '../ui/TipBelow';

const shortId = (id: string) => (id || '').replace(/:.*$/, '');

// The doc filename an agent reads its role from: claude → CLAUDE.md, everyone
// else (codex / cicy / opencode / …) → AGENTS.md.
function agentDocFile(agentType?: string): string {
  return normalizeAgentType(agentType) === 'claude' ? 'CLAUDE.md' : 'AGENTS.md';
}

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
  onOpenAgentFile: (paneId: string, relPath: string) => void;
  onOpenRoleTools: (paneId: string) => void;
  onRenameTitle: (paneId: string, title: string) => void;
  ModelPicker: ModelPickerComponent;
}

type RosterFilter = 'all' | 'bound' | 'unbound';

// The team roster (花名册): every agent on the host — bound and unbound — in one
// table. Per row: agent type, gateway-vs-official, inline-editable title, a model
// picker (gateway only), and shortcuts to the agent's role doc (CLAUDE.md /
// AGENTS.md) and its role+tools (the request/tools inspector view). Opens as a
// top-level view inside the cli-tab content area.
export default function TeamRosterPanel({
  panes, bindings, masterPaneId, onClose, onRefresh,
  onOpenAgentFile, onOpenRoleTools, onRenameTitle, ModelPicker,
}: Props) {
  const { t } = useTranslation('workspace');
  const { confirm, node: dialogsNode } = useDialogs();
  const [filter, setFilter] = useState<RosterFilter>('all');
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

  // The AI provider an agent routes through (gateway agents only).
  const providerOf = useCallback((sid: string) => {
    const d = details[sid];
    return String(d?.runtime_ai?.provider_name || d?.runtime_ai_default?.provider_name || '').trim();
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

  return (
    <div data-id="team-roster-panel" className="absolute inset-0 z-30 flex flex-col bg-[#0b0b0d]">
      {/* Fills the mid-panel (left of the cli-content drawer / right-panel), so the
          cli-content-resize-handle stays exposed and draggable to its right. */}
      {/* header */}
      <div data-id="team-roster-header" className="flex h-11 shrink-0 items-center justify-between border-b border-[var(--vsc-border)] px-3">
        <div className="flex items-center gap-2">
          <span data-id="team-roster-title" className="text-[13px] font-medium text-zinc-200">{t('rosterTitle', { defaultValue: '团队花名册' })}</span>
          <span data-id="team-roster-total" className="text-[11px] text-zinc-500">{t('rosterCount', { defaultValue: '{{n}} 个', n: counts.all })}</span>
        </div>
        <div className="flex items-center gap-1">
          {onRefresh ? (
            <button data-id="team-roster-refresh" type="button" onClick={onRefresh} title={t('rosterRefresh', { defaultValue: '刷新' })}
              className="inline-flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200">
              <RefreshCw className="h-4 w-4" />
            </button>
          ) : null}
          <button data-id="team-roster-close" type="button" onClick={onClose} title={t('rosterClose', { defaultValue: '关闭' })}
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200">
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

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
                const doc = agentDocFile(r.agent_type);
                const isEditing = editing === r.sid;
                return (
                  <tr key={r.sid} data-id={`team-roster-row-${r.sid}`} className="border-b border-white/[0.04] hover:bg-white/[0.02]">
                    {/* agent: avatar + inline-editable title + id + bound dot */}
                    <td className="px-3 py-3 align-middle">
                      <div className="flex items-center gap-2.5">
                        <span title={r.bound ? t('rosterBound', { defaultValue: '已绑定' }) : t('rosterUnbound', { defaultValue: '未绑定' })}
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
                              <button
                                data-id={`team-roster-title-${r.sid}`}
                                type="button"
                                onClick={() => startEdit(r.sid, r.title || '')}
                                title={t('rosterEditTitle', { defaultValue: '点击改名' })}
                                className="group flex h-6 items-center gap-1 truncate text-left text-[12px] text-zinc-200 hover:text-white"
                              >
                                <span className="truncate">{r.title || r.sid}</span>
                                <Pencil className="h-3 w-3 shrink-0 text-zinc-600 opacity-0 group-hover:opacity-100" />
                              </button>
                            )}
                          </div>
                          <div className="truncate text-[10px] text-zinc-600">{r.sid}</div>
                        </div>
                      </div>
                    </td>
                    {/* type + access (gateway / official) stacked in one cell */}
                    <td className="px-2 py-3 align-middle">
                      <div className="flex flex-col items-start gap-1">
                        <span className="text-[12px] text-zinc-300">{type}</span>
                        {r.use_custom_gateway ? (
                          <span data-id={`team-roster-gateway-${r.sid}`} className="inline-flex items-center gap-1 rounded bg-violet-500/10 px-1.5 py-0.5 text-[10px] text-violet-300">
                            <Cloud className="h-3 w-3" /> {t('rosterGateway', { defaultValue: '网关' })}
                          </span>
                        ) : (
                          <span data-id={`team-roster-official-${r.sid}`} className="inline-flex items-center gap-1 rounded bg-white/[0.05] px-1.5 py-0.5 text-[10px] text-zinc-400">
                            <ShieldCheck className="h-3 w-3" /> {t('rosterOfficial', { defaultValue: '官方' })}
                          </span>
                        )}
                      </div>
                    </td>
                    {/* model on top, provider below */}
                    <td className="px-2 py-3 align-middle">
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
                          <span className="text-[12px] text-zinc-300">{modelOf(r.sid) || '—'}</span>
                        )}
                        {providerOf(r.sid) ? (
                          <span data-id={`team-roster-provider-${r.sid}`} className="text-[10px] text-zinc-500">{providerOf(r.sid)}</span>
                        ) : null}
                      </div>
                    </td>
                    {/* actions: role doc (icon-only) + role/tools + more */}
                    <td className="px-3 py-3 align-middle">
                      <div className="flex items-center justify-end gap-1">
                        <TipBelow label={t('rosterOpenDoc', { defaultValue: '查看 {{doc}}', doc })}>
                          <button
                            data-id={`team-roster-doc-${r.sid}`}
                            type="button"
                            onClick={() => onOpenAgentFile(r.pane_id, doc)}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-white/[0.08] text-zinc-300 hover:bg-white/[0.06] hover:text-zinc-100"
                          >
                            <FileText className="h-3.5 w-3.5" />
                          </button>
                        </TipBelow>
                        <TipBelow label={t('rosterOpenRoleTools', { defaultValue: '查看 role + tools' })}>
                          <button
                            data-id={`team-roster-tools-${r.sid}`}
                            type="button"
                            onClick={() => onOpenRoleTools(r.pane_id)}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-white/[0.08] text-zinc-300 hover:bg-white/[0.06] hover:text-zinc-100"
                          >
                            <Wrench className="h-3.5 w-3.5" />
                          </button>
                        </TipBelow>
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
    </div>
  );
}
