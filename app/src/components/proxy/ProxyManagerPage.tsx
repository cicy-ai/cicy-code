import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Activity, Check, ChevronDown, ChevronRight, Globe, Layers, Loader2, Pencil, Plus, RefreshCw, RotateCw, Search, Server, Trash2, X, Zap,
} from 'lucide-react';
import apiService from '../../services/api';
import Select from '../ui/Select';

/**
 * #/proxy — standalone proxy management page (opened in its own tab from the
 * status-bar "管理节点" button). Two sections:
 *   · Groups: which node each proxy-group currently selects, its member list
 *     (editable, order kept), switch live via the mihomo controller.
 *   · Nodes: every `proxies:` entry of mihomo.yaml with add / edit (yaml) /
 *     delete, latency probes and exit IP. New nodes join every group by
 *     default; deleting a node removes it from every group.
 */

type FileNode = { name: string; type: string; server: string; port: string; yaml: string };
type FileGroup = { name: string; type: string; proxies: string[]; use: string[] };
type LiveEntry = { name: string; type: string; last_delay_ms: number; now?: string; members?: string[] };
type DelayRow = { url: string; ok: boolean; delay_ms?: number; error?: string };
type IPResult = { ok: boolean; ip?: string; country?: string; cc?: string; error?: string };
type TestResult = { results: DelayRow[]; ip?: IPResult; ipDirect?: IPResult; running?: boolean };
type MihomoStatus = { running: boolean; version: string; pid: string; config: string };

const BUILTIN = ['DIRECT', 'REJECT'];
const PROBES: Array<{ url: string; short: string }> = [
  { url: 'https://api.anthropic.com', short: 'anthropic' },
  { url: 'https://chatgpt.com', short: 'chatgpt' },
  { url: 'https://www.cloudflare.com', short: 'cloudflare' },
];

const NODE_TEMPLATE = `name: my-node
type: ss            # ss | vmess | vless | trojan | hysteria2 | socks5 | http …
server: 1.2.3.4
port: 443
cipher: aes-128-gcm
password: secret
udp: true
`;

function delayTone(ms?: number): string {
  if (!ms || ms <= 0) return 'text-zinc-500';
  if (ms < 400) return 'text-emerald-400';
  if (ms < 1200) return 'text-amber-300';
  return 'text-red-400';
}

function errMessage(e: any): string {
  return String(e?.response?.data?.detail || e?.response?.data?.error || e?.message || e || 'error');
}

export default function ProxyManagerPage() {
  const { t } = useTranslation('workspace');
  const tp = useCallback((k: string, fallback: string, opts?: Record<string, unknown>) => t(`proxyPage.${k}`, { defaultValue: fallback, ...opts }) as string, [t]);

  const [tab, setTab] = useState<'groups' | 'nodes'>(() => (window.location.hash.includes('/nodes') ? 'nodes' : 'groups'));
  const [fileNodes, setFileNodes] = useState<FileNode[]>([]);
  const [fileGroups, setFileGroups] = useState<FileGroup[]>([]);
  const [live, setLive] = useState<Record<string, LiveEntry>>({});
  const [status, setStatus] = useState<MihomoStatus | null>(null);
  const [exit, setExit] = useState<{ proxy?: IPResult; direct?: IPResult; match?: boolean } | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [tests, setTests] = useState<Record<string, TestResult>>({});
  const [testingAll, setTestingAll] = useState(false);
  const [query, setQuery] = useState('');
  const [editor, setEditor] = useState<{ mode: 'add' | 'edit'; name?: string; yaml: string; groups: string[] } | null>(null);
  const [editorBusy, setEditorBusy] = useState(false);
  const [editorErr, setEditorErr] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string>('');
  const [deleting, setDeleting] = useState(false);
  const [reloading, setReloading] = useState(false);

  useEffect(() => { document.title = `${tp('title', '代理管理')} · CiCy Code`; }, [tp]);
  useEffect(() => {
    const want = tab === 'nodes' ? '#/proxy/nodes' : '#/proxy';
    if (window.location.hash !== want) window.history.replaceState(null, '', want);
  }, [tab]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [fileRes, liveRes, statusRes] = await Promise.all([
        apiService.getProxyNodes(),
        apiService.getProxyList().catch(() => null),
        apiService.getProxyStatus().catch(() => null),
      ]);
      setFileNodes(((fileRes?.data?.nodes || []) as FileNode[]).map((n) => ({ ...n, server: n.server || '', port: n.port || '' })));
      setFileGroups(((fileRes?.data?.groups || []) as FileGroup[]).map((g) => ({ ...g, proxies: g.proxies || [], use: g.use || [] })));
      const map: Record<string, LiveEntry> = {};
      for (const e of [...(liveRes?.data?.groups || []), ...(liveRes?.data?.nodes || [])] as LiveEntry[]) map[e.name] = e;
      setLive(map);
      if (statusRes?.data) setStatus(statusRes.data as MihomoStatus);
    } catch (e: any) {
      setError(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadExit = useCallback(async () => {
    try {
      const res = await apiService.getProxyExitInfo();
      const groups = (res?.data?.groups || []) as Array<IPResult & { via?: string }>;
      const proxy = groups.find((g) => g.via === 'proxy' || g.via === 'both');
      const direct = groups.find((g) => g.via === 'direct' || g.via === 'both');
      setExit({ proxy, direct, match: !!res?.data?.match });
    } catch { /* optional */ }
  }, []);

  useEffect(() => { void load(); void loadExit(); }, [load, loadExit]);

  const flash = (msg: string) => { setNotice(msg); window.setTimeout(() => setNotice(''), 3500); };

  /* ---- probes ---- */
  const testOne = useCallback(async (name: string) => {
    setTests((prev) => ({ ...prev, [name]: { ...(prev[name] || { results: [] }), running: true } }));
    try {
      const res = await apiService.testProxy(name);
      setTests((prev) => ({ ...prev, [name]: { ...(res?.data || { results: [] }), running: false } }));
    } catch (e: any) {
      setTests((prev) => ({ ...prev, [name]: { results: [{ url: '', ok: false, error: errMessage(e) }], running: false } }));
    }
  }, []);

  const testAll = useCallback(async () => {
    setTestingAll(true);
    const names = fileNodes.map((n) => n.name);
    const queue = [...names];
    const workers = Array.from({ length: Math.min(4, queue.length) }, async () => {
      while (queue.length) {
        const n = queue.shift();
        if (n) await testOne(n);
      }
    });
    await Promise.all(workers);
    setTestingAll(false);
    void load();
  }, [fileNodes, testOne, load]);

  /* ---- groups ---- */
  const selectMember = async (group: string, member: string) => {
    try {
      await apiService.selectProxy(member, group);
      setLive((prev) => ({ ...prev, [group]: { ...(prev[group] || { name: group, type: 'Selector', last_delay_ms: 0 }), now: member } }));
      flash(tp('switched', '{{group}} → {{member}}', { group, member }));
      void loadExit();
    } catch (e: any) {
      setError(errMessage(e));
    }
  };

  const saveMembers = async (group: string, proxies: string[]) => {
    try {
      await apiService.setProxyGroupMembers(group, proxies);
      flash(tp('membersSaved', '已更新 {{group}} 的成员', { group }));
      await load();
    } catch (e: any) {
      setError(errMessage(e));
    }
  };

  /* ---- nodes ---- */
  const openAdd = () => { setEditorErr(''); setEditor({ mode: 'add', yaml: NODE_TEMPLATE, groups: fileGroups.map((g) => g.name) }); };
  const openEdit = (n: FileNode) => { setEditorErr(''); setEditor({ mode: 'edit', name: n.name, yaml: n.yaml, groups: [] }); };

  const submitEditor = async () => {
    if (!editor) return;
    setEditorBusy(true);
    setEditorErr('');
    try {
      if (editor.mode === 'add') {
        const res = await apiService.createProxyNode(editor.yaml, editor.groups);
        flash(tp('nodeAdded', '已添加 {{name}}，加入 {{n}} 个组', { name: res?.data?.node?.name, n: (res?.data?.groups || []).length }));
      } else {
        const res = await apiService.updateProxyNode(editor.name || '', editor.yaml);
        flash(tp('nodeSaved', '已保存 {{name}}', { name: res?.data?.node?.name }));
      }
      setEditor(null);
      await load();
    } catch (e: any) {
      setEditorErr(errMessage(e));
    } finally {
      setEditorBusy(false);
    }
  };

  const doDelete = async () => {
    if (!confirmDelete) return;
    setDeleting(true);
    try {
      const res = await apiService.deleteProxyNode(confirmDelete);
      flash(tp('nodeDeleted', '已删除 {{name}}（从 {{n}} 个组移除）', { name: confirmDelete, n: (res?.data?.left_groups || []).length }));
      setConfirmDelete('');
      await load();
    } catch (e: any) {
      setError(errMessage(e));
      setConfirmDelete('');
    } finally {
      setDeleting(false);
    }
  };

  const reload = async () => {
    setReloading(true);
    try {
      await apiService.proxyLifecycle('reload');
      flash(tp('reloaded', 'mihomo 已重新加载'));
      await load();
    } catch (e: any) {
      setError(errMessage(e));
    } finally {
      setReloading(false);
    }
  };

  /* ---- derived ---- */
  const groupsOf = useMemo(() => {
    const m: Record<string, string[]> = {};
    for (const g of fileGroups) for (const p of g.proxies) (m[p] ||= []).push(g.name);
    return m;
  }, [fileGroups]);
  const filteredNodes = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return fileNodes;
    return fileNodes.filter((n) => [n.name, n.type, n.server, n.port].some((v) => String(v || '').toLowerCase().includes(q)));
  }, [fileNodes, query]);
  const memberOptions = useMemo(() => [...fileNodes.map((n) => n.name), ...fileGroups.map((g) => g.name), ...BUILTIN], [fileNodes, fileGroups]);

  return (
    <div data-id="proxy-page" className="flex h-screen flex-col bg-[#0b0b0d] text-zinc-200">
      {/* header */}
      <header data-id="proxy-page-header" className="flex items-center justify-between gap-4 border-b border-white/[0.06] px-6 py-3.5">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-white/[0.08] bg-white/[0.04]">
            <Globe size={17} className="text-sky-300" />
          </div>
          <div className="min-w-0">
            <h1 className="text-[15px] font-semibold text-white">{tp('title', '代理管理')}</h1>
            <p className="truncate font-mono text-[11px] text-zinc-600">{status?.config || '~/cicy-ai/db/mihomo.yaml'}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <StatusPill running={!!status?.running} version={status?.version || ''} t={tp} />
          {exit ? (
            <div data-id="proxy-page-exit" className="hidden items-center gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-1.5 text-[11px] md:flex">
              <span className="flex items-center gap-1.5"><Zap size={11} className="text-emerald-400" /><span className="text-zinc-500">{tp('exitProxy', '代理出口')}</span><span className="font-mono text-zinc-200">{exit.proxy?.ip || '—'}</span>{exit.proxy?.cc ? <span className="text-zinc-500">{exit.proxy.cc}</span> : null}</span>
              <span className="h-3 w-px bg-white/[0.08]" />
              <span className="flex items-center gap-1.5"><Globe size={11} className="text-zinc-500" /><span className="text-zinc-500">{tp('exitDirect', '直连出口')}</span><span className="font-mono text-zinc-200">{exit.direct?.ip || '—'}</span>{exit.direct?.cc ? <span className="text-zinc-500">{exit.direct.cc}</span> : null}</span>
            </div>
          ) : null}
          <button type="button" data-id="proxy-page-test-all" onClick={() => void testAll()} disabled={testingAll || loading || !fileNodes.length} className={BTN}>
            {testingAll ? <Loader2 size={13} className="animate-spin" /> : <Activity size={13} />}{testingAll ? tp('testingAll', '测速中…') : tp('testAll', '全部测速')}
          </button>
          <button type="button" data-id="proxy-page-reload" onClick={() => void reload()} disabled={reloading} className={BTN} title={tp('reloadTitle', '重新加载 mihomo 配置')}>
            {reloading ? <Loader2 size={13} className="animate-spin" /> : <RotateCw size={13} />}{tp('reload', '重载')}
          </button>
          <button type="button" data-id="proxy-page-refresh" onClick={() => { void load(); void loadExit(); }} disabled={loading} className={BTN_ICON} title={tp('refresh', '刷新')}>
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </header>

      {/* tabs */}
      <div className="flex items-center justify-between border-b border-white/[0.06] px-6">
        <nav className="flex gap-1">
          {([['groups', tp('tabGroups', '代理组'), Layers, fileGroups.length], ['nodes', tp('tabNodes', '节点'), Server, fileNodes.length]] as const).map(([id, label, Icon, count]) => (
            <button
              key={id}
              type="button"
              data-id={`proxy-page-tab-${id}`}
              onClick={() => setTab(id)}
              className={`relative flex items-center gap-2 px-3 py-3 text-[13px] transition-colors ${tab === id ? 'text-white' : 'text-zinc-500 hover:text-zinc-300'}`}
            >
              <Icon size={14} />{label}
              <span className={`rounded-full px-1.5 text-[10px] ${tab === id ? 'bg-white/[0.1] text-zinc-200' : 'bg-white/[0.04] text-zinc-500'}`}>{count}</span>
              {tab === id ? <span className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-sky-400" /> : null}
            </button>
          ))}
        </nav>
        {tab === 'nodes' ? (
          <div className="flex items-center gap-2">
            <label className="flex h-8 items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 text-[12px] text-zinc-400 focus-within:border-sky-500/50">
              <Search size={13} />
              <input data-id="proxy-page-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={tp('search', '搜索节点…')} className="w-44 bg-transparent text-zinc-200 outline-none placeholder:text-zinc-600" />
              {query ? <button type="button" onClick={() => setQuery('')} className="text-zinc-500 hover:text-zinc-300"><X size={12} /></button> : null}
            </label>
            <button type="button" data-id="proxy-page-add-node" onClick={openAdd} className="flex h-8 items-center gap-1.5 rounded-lg bg-sky-500/90 px-3 text-[12px] font-medium text-white transition-colors hover:bg-sky-400">
              <Plus size={14} />{tp('addNode', '添加节点')}
            </button>
          </div>
        ) : null}
      </div>

      {(error || notice) ? (
        <div className={`px-6 py-2 text-[12px] ${error ? 'bg-red-950/40 text-red-300' : 'bg-emerald-950/30 text-emerald-300'}`}>
          <span className="flex items-center gap-2">{error ? <X size={12} /> : <Check size={12} />}{error || notice}{error ? <button type="button" onClick={() => setError('')} className="ml-auto text-red-400/70 hover:text-red-200">{tp('dismiss', '关闭')}</button> : null}</span>
        </div>
      ) : null}

      <main className="flex-1 overflow-auto px-6 py-5">
        {loading && !fileNodes.length ? (
          <div className="flex h-40 items-center justify-center gap-2 text-[13px] text-zinc-500"><Loader2 size={16} className="animate-spin" />{tp('loading', '加载中…')}</div>
        ) : tab === 'groups' ? (
          <div className="grid gap-4 xl:grid-cols-2">
            {fileGroups.map((g) => (
              <GroupCard
                key={g.name}
                group={g}
                live={live[g.name]}
                delays={live}
                options={memberOptions.filter((m) => m !== g.name)}
                onSelect={(m) => void selectMember(g.name, m)}
                onSave={(p) => void saveMembers(g.name, p)}
                t={tp}
              />
            ))}
            {!fileGroups.length ? <EmptyHint text={tp('noGroups', 'mihomo.yaml 里没有 proxy-groups')} /> : null}
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-white/[0.07] bg-white/[0.015]">
            <table data-id="proxy-page-nodes" className="w-full table-fixed border-separate border-spacing-0 text-[12px]">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wide text-zinc-500">
                  <th className={TH + ' w-[15%]'}>{tp('colName', '名称')}</th>
                  <th className={TH + ' w-[7%]'}>{tp('colType', '类型')}</th>
                  <th className={TH + ' w-[16%]'}>{tp('colServer', '服务器')}</th>
                  <th className={TH + ' w-[6%] text-right'}>{tp('colDelay', '延迟')}</th>
                  {PROBES.map((p) => <th key={p.url} className={TH + ' w-[6%] text-right'}>{p.short}</th>)}
                  <th className={TH + ' w-[10%]'}>{tp('colExit', '出口')}</th>
                  <th className={TH}>{tp('colGroups', '所属组')}</th>
                  <th className={TH + ' w-[104px] text-right'} />
                </tr>
              </thead>
              <tbody>
                {filteredNodes.map((n) => {
                  const tr = tests[n.name];
                  const liveDelay = live[n.name]?.last_delay_ms;
                  return (
                    <tr key={n.name} data-id={`proxy-node-${n.name}`} className="group/row transition-colors hover:bg-white/[0.03]">
                      <td className={TD}><div className="truncate font-medium text-zinc-100" title={n.name}>{n.name}</div></td>
                      <td className={TD}><span className="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[10px] text-zinc-300">{n.type}</span></td>
                      <td className={TD}><span className="truncate font-mono text-[11px] text-zinc-400" title={`${n.server}:${n.port}`}>{n.server}{n.port ? `:${n.port}` : ''}</span></td>
                      <td className={TD + ' text-right font-mono ' + delayTone(liveDelay)}>{liveDelay ? `${liveDelay}` : '—'}</td>
                      {PROBES.map((p) => {
                        const row = tr?.results?.find((r) => r.url === p.url);
                        return (
                          <td key={p.url} className={TD + ' text-right font-mono'}>
                            {tr?.running ? <Loader2 size={11} className="ml-auto animate-spin text-zinc-500" /> : row ? (row.ok ? <span className={delayTone(row.delay_ms)}>{row.delay_ms}</span> : <span className="text-red-400" title={row.error}>✕</span>) : <span className="text-zinc-700">·</span>}
                          </td>
                        );
                      })}
                      <td className={TD}>
                        {tr?.ip?.ok ? <span className="font-mono text-[11px] text-zinc-300" title={tr.ip.country}>{tr.ip.ip}{tr.ip.cc ? <span className="ml-1 text-zinc-500">{tr.ip.cc}</span> : null}</span> : tr?.ip?.error ? <span className="text-[11px] text-red-400" title={tr.ip.error}>{tp('exitFail', '失败')}</span> : <span className="text-zinc-700">·</span>}
                      </td>
                      <td className={TD}>
                        <div className="flex flex-wrap gap-1">
                          {(groupsOf[n.name] || []).map((g) => (
                            <span key={g} className={`whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] ${live[g]?.now === n.name ? 'bg-emerald-500/15 text-emerald-300' : 'bg-white/[0.05] text-zinc-400'}`} title={live[g]?.now === n.name ? tp('activeIn', '当前被该组选中') : ''}>{g}</span>
                          ))}
                          {!(groupsOf[n.name] || []).length ? <span className="whitespace-nowrap text-[11px] text-amber-300/80">{tp('noGroup', '未加入任何组')}</span> : null}
                        </div>
                      </td>
                      <td className={TD + ' text-right'}>
                        <div className="flex justify-end gap-1 opacity-70 transition-opacity group-hover/row:opacity-100">
                          <IconBtn dataId={`proxy-node-test-${n.name}`} title={tp('test', '测速')} onClick={() => void testOne(n.name)} disabled={!!tr?.running}><Activity size={13} /></IconBtn>
                          <IconBtn dataId={`proxy-node-edit-${n.name}`} title={tp('edit', '编辑')} onClick={() => openEdit(n)}><Pencil size={13} /></IconBtn>
                          <IconBtn dataId={`proxy-node-delete-${n.name}`} title={tp('delete', '删除')} onClick={() => setConfirmDelete(n.name)} danger><Trash2 size={13} /></IconBtn>
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {!filteredNodes.length ? (
                  <tr><td colSpan={9 + PROBES.length} className="px-4 py-10 text-center text-[13px] text-zinc-500">{fileNodes.length ? tp('noMatch', '没有匹配的节点') : tp('noNodes', '还没有节点，点右上角「添加节点」')}</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        )}
      </main>

      {/* editor */}
      {editor ? (
        <Modal onClose={() => !editorBusy && setEditor(null)} title={editor.mode === 'add' ? tp('addNode', '添加节点') : tp('editNode', '编辑节点 {{name}}', { name: editor.name })} width="720px">
          <p className="mb-2 text-[12px] text-zinc-500">{tp('editorHint', '粘贴一个 mihomo 节点（YAML mapping）。写入前会用 mihomo -t 校验，失败不会改动配置。')}</p>
          <textarea
            data-id="proxy-node-editor"
            value={editor.yaml}
            onChange={(e) => setEditor({ ...editor, yaml: e.target.value })}
            spellCheck={false}
            className="h-72 w-full resize-y rounded-lg border border-white/[0.09] bg-black/40 p-3 font-mono text-[12px] leading-5 text-zinc-100 outline-none focus:border-sky-500/50"
          />
          {editor.mode === 'add' && fileGroups.length ? (
            <div className="mt-3">
              <div className="mb-1.5 text-[11px] text-zinc-500">{tp('joinGroups', '加入代理组（默认全部）')}</div>
              <div className="flex flex-wrap gap-1.5">
                {fileGroups.map((g) => {
                  const on = editor.groups.includes(g.name);
                  return (
                    <button key={g.name} type="button" onClick={() => setEditor({ ...editor, groups: on ? editor.groups.filter((x) => x !== g.name) : [...editor.groups, g.name] })} className={`flex items-center gap-1 rounded-md border px-2 py-1 text-[11px] ${on ? 'border-sky-500/50 bg-sky-500/15 text-sky-200' : 'border-white/[0.08] text-zinc-500 hover:text-zinc-300'}`}>
                      {on ? <Check size={11} /> : null}{g.name}
                    </button>
                  );
                })}
              </div>
            </div>
          ) : null}
          {editorErr ? <pre className="mt-3 max-h-32 overflow-auto whitespace-pre-wrap rounded-md bg-red-950/40 p-2 font-mono text-[11px] text-red-300">{editorErr}</pre> : null}
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={() => setEditor(null)} disabled={editorBusy} className={BTN}>{tp('cancel', '取消')}</button>
            <button type="button" data-id="proxy-node-editor-save" onClick={() => void submitEditor()} disabled={editorBusy || !editor.yaml.trim()} className="flex h-8 items-center gap-1.5 rounded-lg bg-sky-500/90 px-3 text-[12px] font-medium text-white hover:bg-sky-400 disabled:opacity-50">
              {editorBusy ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}{editor.mode === 'add' ? tp('add', '添加') : tp('save', '保存')}
            </button>
          </div>
        </Modal>
      ) : null}

      {confirmDelete ? (
        <Modal onClose={() => !deleting && setConfirmDelete('')} title={tp('deleteTitle', '删除节点')} width="420px">
          <p className="text-[13px] text-zinc-300">{tp('deleteBody', '删除 {{name}}？它会同时从 {{n}} 个代理组中移除。', { name: confirmDelete, n: (groupsOf[confirmDelete] || []).length })}</p>
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={() => setConfirmDelete('')} disabled={deleting} className={BTN}>{tp('cancel', '取消')}</button>
            <button type="button" data-id="proxy-node-delete-confirm" onClick={() => void doDelete()} disabled={deleting} className="flex h-8 items-center gap-1.5 rounded-lg bg-red-600/90 px-3 text-[12px] font-medium text-white hover:bg-red-500 disabled:opacity-50">
              {deleting ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}{tp('delete', '删除')}
            </button>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

/* ---------- pieces ---------- */

const BTN = 'flex h-8 items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.07] hover:text-zinc-100 disabled:opacity-40';
const BTN_ICON = 'flex h-8 w-8 items-center justify-center rounded-lg border border-white/[0.08] bg-white/[0.03] text-zinc-400 transition-colors hover:bg-white/[0.07] hover:text-zinc-100 disabled:opacity-40';
const TH = 'border-b border-white/[0.07] bg-white/[0.02] px-3 py-2.5 font-medium';
const TD = 'border-b border-white/[0.05] px-3 py-2 align-middle';

function IconBtn({ children, onClick, title, dataId, disabled, danger }: { children: React.ReactNode; onClick: () => void; title: string; dataId: string; disabled?: boolean; danger?: boolean }) {
  return (
    <button type="button" data-id={dataId} title={title} onClick={onClick} disabled={disabled} className={`flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 transition-colors disabled:opacity-40 ${danger ? 'hover:bg-red-500/15 hover:text-red-300' : 'hover:bg-white/[0.08] hover:text-zinc-100'}`}>
      {children}
    </button>
  );
}

function StatusPill({ running, version, t }: { running: boolean; version: string; t: (k: string, f: string) => string }) {
  return (
    <span data-id="proxy-page-status" className={`flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[11px] ${running ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300' : 'border-red-500/30 bg-red-500/10 text-red-300'}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${running ? 'bg-emerald-400' : 'bg-red-400'}`} />
      mihomo {running ? t('running', '运行中') : t('stopped', '未运行')}{version ? <span className="font-mono text-zinc-500">{version}</span> : null}
    </span>
  );
}

function EmptyHint({ text }: { text: string }) {
  return <div className="col-span-full rounded-xl border border-dashed border-white/[0.08] px-4 py-10 text-center text-[13px] text-zinc-500">{text}</div>;
}

function Modal({ children, onClose, title, width }: { children: React.ReactNode; onClose: () => void; title: string; width: string }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" onClick={onClose}>
      <div className="w-full rounded-2xl border border-white/[0.1] bg-[#121215] p-5 shadow-2xl" style={{ maxWidth: width }} onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-[14px] font-semibold text-white">{title}</h2>
          <button type="button" onClick={onClose} className="rounded-md p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"><X size={15} /></button>
        </div>
        {children}
      </div>
    </div>
  );
}

function GroupCard({ group, live, delays, options, onSelect, onSave, t }: {
  group: FileGroup;
  live?: LiveEntry;
  delays: Record<string, LiveEntry>;
  options: string[];
  onSelect: (member: string) => void;
  onSave: (proxies: string[]) => void;
  t: (k: string, f: string, o?: Record<string, unknown>) => string;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<string[]>(group.proxies);
  useEffect(() => { if (!editing) setDraft(group.proxies); }, [group.proxies, editing]);
  const now = live?.now || '';
  const dirty = editing && (draft.length !== group.proxies.length || draft.some((p, i) => p !== group.proxies[i]));
  const toggle = (m: string) => setDraft((d) => (d.includes(m) ? d.filter((x) => x !== m) : [...d, m]));
  const move = (m: string, dir: -1 | 1) => setDraft((d) => {
    const i = d.indexOf(m); const j = i + dir;
    if (i < 0 || j < 0 || j >= d.length) return d;
    const c = [...d]; [c[i], c[j]] = [c[j], c[i]]; return c;
  });

  return (
    <section data-id={`proxy-group-${group.name}`} className="rounded-xl border border-white/[0.07] bg-white/[0.015]">
      <header className="flex items-center justify-between gap-3 border-b border-white/[0.06] px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-[14px] font-semibold text-zinc-100">{group.name}</h2>
            <span className="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[10px] text-zinc-400">{group.type}</span>
            {group.use.length ? <span className="rounded bg-violet-500/15 px-1.5 py-0.5 text-[10px] text-violet-300" title={group.use.join(', ')}>{t('usesProviders', '含订阅 {{n}}', { n: group.use.length })}</span> : null}
          </div>
          <div className="mt-0.5 text-[11px] text-zinc-500">{t('members', '{{n}} 个成员', { n: group.proxies.length })}</div>
        </div>
        <div className="flex items-center gap-2">
          <div className="w-52">
            <Select
              dataId={`proxy-group-select-${group.name}`}
              compact
              value={now}
              onChange={(v) => onSelect(v)}
              options={group.proxies.map((m) => ({ value: m, label: `${m}${delays[m]?.last_delay_ms ? `  ${delays[m].last_delay_ms}ms` : ''}` }))}
              placeholder={t('pickMember', '选择出口')}
            />
          </div>
          <button type="button" data-id={`proxy-group-edit-${group.name}`} onClick={() => { setEditing((e) => !e); setDraft(group.proxies); }} className={BTN}>
            {editing ? <X size={12} /> : <Pencil size={12} />}{editing ? t('cancel', '取消') : t('editMembers', '编辑成员')}
          </button>
        </div>
      </header>
      <div className="px-4 py-3">
        {!editing ? (
          <div className="flex flex-wrap gap-1.5">
            {group.proxies.map((m) => {
              const active = m === now;
              const d = delays[m]?.last_delay_ms;
              return (
                <button key={m} type="button" onClick={() => onSelect(m)} title={t('clickToSelect', '点击切换到此出口')} className={`flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] transition-colors ${active ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200' : 'border-white/[0.07] bg-white/[0.02] text-zinc-300 hover:border-white/[0.16]'}`}>
                  {active ? <Check size={11} /> : null}{m}{d ? <span className={`font-mono ${delayTone(d)}`}>{d}</span> : null}
                </button>
              );
            })}
          </div>
        ) : (
          <div>
            <div className="mb-2 text-[11px] text-zinc-500">{t('editMembersHint', '勾选成员；顺序即列表顺序（url-test / fallback 组按顺序优先）。')}</div>
            <div className="max-h-64 space-y-0.5 overflow-auto rounded-lg border border-white/[0.06] p-1">
              {[...draft, ...options.filter((o) => !draft.includes(o))].map((m) => {
                const on = draft.includes(m);
                const idx = draft.indexOf(m);
                return (
                  <div key={m} className={`flex items-center gap-2 rounded-md px-2 py-1 text-[12px] ${on ? 'bg-white/[0.04] text-zinc-100' : 'text-zinc-500'}`}>
                    <button type="button" onClick={() => toggle(m)} className={`flex h-4 w-4 items-center justify-center rounded border ${on ? 'border-sky-400 bg-sky-500/80 text-white' : 'border-white/[0.2]'}`}>{on ? <Check size={10} /> : null}</button>
                    <span className="flex-1 truncate">{m}{BUILTIN.includes(m) ? <span className="ml-1 text-[10px] text-zinc-600">built-in</span> : null}</span>
                    {on ? (
                      <span className="flex gap-0.5">
                        <button type="button" onClick={() => move(m, -1)} disabled={idx <= 0} className="rounded p-0.5 text-zinc-500 hover:text-zinc-200 disabled:opacity-30"><ChevronDown size={12} className="rotate-180" /></button>
                        <button type="button" onClick={() => move(m, 1)} disabled={idx >= draft.length - 1} className="rounded p-0.5 text-zinc-500 hover:text-zinc-200 disabled:opacity-30"><ChevronDown size={12} /></button>
                      </span>
                    ) : <ChevronRight size={12} className="text-zinc-700" />}
                  </div>
                );
              })}
            </div>
            <div className="mt-3 flex justify-end gap-2">
              <button type="button" onClick={() => { setEditing(false); setDraft(group.proxies); }} className={BTN}>{t('cancel', '取消')}</button>
              <button type="button" data-id={`proxy-group-save-${group.name}`} disabled={!dirty || !draft.length} onClick={() => { onSave(draft); setEditing(false); }} className="flex h-8 items-center gap-1.5 rounded-lg bg-sky-500/90 px-3 text-[12px] font-medium text-white hover:bg-sky-400 disabled:opacity-50">
                <Check size={13} />{t('save', '保存')}
              </button>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
