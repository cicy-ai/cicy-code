import React, { useState, useEffect, useRef } from 'react';
import { Loader2, Home, Folder } from 'lucide-react';
import { urls } from '../config';
import apiService from '../services/api';
import { WebFrame } from './WebFrame';
import { SettingsView } from './SettingsView';
import { EditPaneData } from './EditPaneDialog';
import { useApp } from '../contexts/AppContext';
import { usePane } from '../contexts/PaneContext';
import { useDialog } from '../contexts/DialogContext';
import { QueueView } from './QueueView';
import { TrafficChart } from './TrafficChart';

interface RightSidePanelProps {
  ttydWidth: number;
  isDragging: boolean;
  setBoundAgents: (agents: string[]) => void;
  boundAgents?: string[];
  leftWidth: number;
  onCloseDrawer?: () => void;
}

const RightSidePanel: React.FC<RightSidePanelProps> = ({ ttydWidth, isDragging, setBoundAgents, boundAgents = [], leftWidth, onCloseDrawer }) => {
  const { paneDetail, api, setPaneDetail, updatePane, globalVar, loadGlobalVar, updateGlobalVar } = useApp();
  const { displayPaneId, token, hasPermission, activeTab, setActiveTab, previewTab, setPreviewTab, toast, setToast, isInteracting } = usePane();

  const [paneWorkspace, setPaneWorkspace] = useState<string>(() => {
    const cached = localStorage.getItem(`codeserver_folder_${displayPaneId}`);
    return cached || paneDetail?.workspace || '/home/w3c_offical';
  });
  const [showFavorDirs, setShowFavorDirs] = useState(false);
  const [favorDirs, setFavorDirs] = useState<string[]>([]);
  const [tempPaneData, setTempPaneData] = useState<EditPaneData | null>(null);

  const navigateToPath = async (path: string, forceRefresh = false) => {
    if (!path) return;
    const frame = document.querySelector('.code-server-iframe') as HTMLIFrameElement | HTMLElement;
    if (!frame) return;
    if (!forceRefresh) {
      try {
        const currentSrc = (frame as any).src || frame.getAttribute('src');
        if (currentSrc && new URL(currentSrc).searchParams.get('folder') === path) return;
      } catch {}
    }
    try {
      const { data } = await apiService.fileExists(path);
      if (!data.exists) { setToast(`Path not found: ${path}`); setTimeout(() => setToast(null), 3000); return; }
      const newUrl = urls.codeServer(path);
      if ((frame as any).stop) (frame as any).stop();
      if ((frame as any).src !== undefined) (frame as any).src = newUrl;
      else frame.setAttribute('src', newUrl);
      setPaneWorkspace(path);
      localStorage.setItem(`codeserver_folder_${displayPaneId}`, path);
    } catch {
      setToast('Failed to check path');
      setTimeout(() => setToast(null), 3000);
    }
  };
  const [isSavingPane, setIsSavingPane] = useState(false);

  useEffect(() => {
    if (globalVar?.favor?.dir) setFavorDirs(globalVar.favor.dir);
  }, [globalVar]);

  useEffect(() => {
    if (paneDetail && !isSavingPane) {
      setTempPaneData({
        target: paneDetail.pane_id || displayPaneId,
        title: paneDetail.title || '',
        workspace: paneDetail.workspace || '/home/w3c_offical',
        agent_duty: paneDetail.agent_duty || '',
        agent_type: paneDetail.agent_type || '',
        init_script: paneDetail.init_script || '',
        config: paneDetail.config || '{}',
        tg_token: paneDetail.tg_token || '',
        tg_chat_id: paneDetail.tg_chat_id || '',
        tg_enable: paneDetail.tg_enable || false,
        active: paneDetail.active !== 0,
        ttyd_preview: paneDetail.ttyd_preview || '',
        role: paneDetail.role || '',
        default_model: paneDetail.default_model || '',
        trust_level: paneDetail.trust_level || '',
      });
    }
  }, [paneDetail, displayPaneId, isSavingPane]);

  useEffect(() => { setTempPaneData(null); }, [displayPaneId]);

  useEffect(() => {
    if (activeTab === 'Settings' && !paneDetail && api && displayPaneId) {
      api.getPane(displayPaneId).then(({ data }) => setPaneDetail(data)).catch(console.error);
    }
    if (activeTab === 'Global') loadGlobalVar();
  }, [activeTab, displayPaneId, api, loadGlobalVar]);

  // Load workspace from paneDetail only if no cached folder for this pane
  useEffect(() => {
    if (paneDetail?.workspace) {
      const cached = localStorage.getItem(`codeserver_folder_${displayPaneId}`);
      if (!cached) {
        setPaneWorkspace(paneDetail.workspace);
        localStorage.setItem(`codeserver_folder_${displayPaneId}`, paneDetail.workspace);
      }
    }
  }, [paneDetail]);

  return (
    <div id="right-side" className="w-full h-full bg-vsc-bg">
      <div id="right-side-top" className="absolute top-0 left-0 right-0 h-10 bg-vsc-bg-titlebar border-b border-vsc-border flex items-center gap-1 px-2 z-10">
        {([ 'Workers', 'Code', 'Traffic', 'Board', 'Preview', 'Settings'] as const).map(tab => (
          <button
            key={tab}
            onClick={() => {
              setActiveTab(tab);
              localStorage.setItem('activeTab', tab);
            }}
            className={`px-4 py-1 rounded text-sm ${activeTab === tab ? 'bg-vsc-bg text-vsc-text' : 'text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover'}`}
          >
            {tab}
          </button>
        ))}
      </div>
      {isInteracting && <div className="absolute inset-0 z-20"></div>}
        {paneWorkspace && (
          <div id="right-side-inner" className="absolute inset-0 flex flex-col" style={{marginTop: '40px', display: activeTab === 'Code' ? 'flex' : 'none'}}>
            <div  id="right-side-code-server"  className="w-full flex-1 overflow-hidden flex flex-col">
              <div className=".bg-vsc-bg h-8 border-b border-vsc-border flex items-center px-2 gap-2 flex-shrink-0">
                <button 
                  onClick={() => navigateToPath(paneDetail?.workspace || paneWorkspace, true)}
                  className="p-1 text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover rounded"
                  title="Home"
                >
                  <Home size={16} />
                </button>
                <div className="relative">
                  <button 
                    onClick={() => setShowFavorDirs(!showFavorDirs)}
                    className="p-1 text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover rounded"
                    title="Favorite Directories"
                  >
                    <Folder size={16} />
                  </button>
                  {showFavorDirs && (
                    <>
                      <div className="fixed inset-0 z-10" onClick={() => setShowFavorDirs(false)}></div>
                      <div className="absolute top-full left-0 mt-1 bg-vsc-bg-secondary border border-vsc-border rounded shadow-lg z-20 min-w-[400px] max-w-[600px]">
                        {favorDirs.length === 0 ? (
                          <div className="px-3 py-2 text-xs text-vsc-text-secondary">No favorite directories</div>
                        ) : (
                          favorDirs.map((dir, idx) => (
                            <button
                              key={idx}
                              onClick={() => {
                                navigateToPath(dir);
                                setShowFavorDirs(false);
                              }}
                              className="w-full text-left px-3 py-2 text-sm text-vsc-text hover:bg-vsc-bg-hover"
                            >
                              {dir}
                            </button>
                          ))
                        )}
                      </div>
                    </>
                  )}
                </div>
                <input 
                  className="flex-1 bg-vsc-bg text-vsc-text px-2 py-1 text-sm border border-vsc-border rounded" 
                  value={paneWorkspace} 
                  onChange={(e) => setPaneWorkspace(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') navigateToPath(paneWorkspace);
                  }}
                />
              </div>
              <WebFrame codeServer loading="lazy" src={urls.codeServer(paneWorkspace)} className="code-server-iframe w-full flex-1" />
            </div>
            {isDragging && <div className="absolute inset-0 z-20"></div>}
          </div>
        )}
      {activeTab === 'Traffic' && (
        <div className="absolute inset-0" style={{marginTop: '40px'}}>
          <TrafficChart />
        </div>
      )}
      {activeTab === 'Board' && (
        <div className="absolute inset-0" style={{marginTop: '40px'}}>
          <WebFrame src="https://github.com/users/cicy-dev/projects/4" className="w-full h-full border-0" codeServer />
        </div>
      )}
      {activeTab === 'Preview' && (
        <>
          {!globalVar?.favor?.previewUrls || globalVar.favor.previewUrls.filter((item: any) => item.enable).length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full bg-vsc-bg" style={{marginTop: '40px'}}>
              <svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="text-vsc-text-muted mb-4">
                <circle cx="11" cy="11" r="8"/>
                <path d="m21 21-4.35-4.35"/>
              </svg>
              <p className="text-vsc-text-muted text-sm">No preview URLs found</p>
            </div>
          ) : (
            <>
              <div style={{position: 'absolute', top: '40px', left: 0, right: 0, height: '32px', background: '#2a2d2e', borderBottom: '1px solid #474747', display: 'flex', gap: '4px', padding: '4px'}}>
                {globalVar.favor.previewUrls.filter((item: any) => item.enable).map((item: any, idx: number) => (
                  <button
                    key={idx}
                    onClick={() => {
                      setPreviewTab(idx);
                      localStorage.setItem(`${displayPaneId}_previewTab`, idx.toString());
                    }}
                    style={{
                      padding: '4px 12px',
                      fontSize: '13px',
                      background: previewTab === idx ? '#474747' : 'transparent',
                      color: previewTab === idx ? '#fff' : '#858585',
                      border: 'none',
                      borderRadius: '4px',
                      cursor: 'pointer'
                    }}
                  >
                    {item.name || `URL ${idx + 1}`}
                  </button>
                ))}
              </div>
              {globalVar.favor.previewUrls.filter((item: any) => item.enable).map((item: any, idx: number) => (
                <div key={idx} className="absolute inset-0" style={{marginTop: '72px', display: previewTab === idx ? 'block' : 'none'}}>
                  <WebFrame src={item.url || item} className="w-full h-full" />
                  {isDragging && <div className="absolute inset-0 z-20"></div>}
                </div>
              ))}
            </>
          )}
        </>
      )}
      {activeTab === 'Workers' && (
        <BindedAgentsTab paneId={displayPaneId} token={token} isDragging={isDragging} setBoundAgents={setBoundAgents} />
      )}
      {activeTab === 'Settings' && (
        <SettingsTabWithSub
          tempPaneData={tempPaneData} setTempPaneData={setTempPaneData}
          isSavingPane={isSavingPane} setIsSavingPane={setIsSavingPane}
          globalVar={globalVar} updateGlobalVar={updateGlobalVar}
        />
      )}
      {isDragging && activeTab !== 'Settings' && activeTab !== 'Agents' && <div className="absolute inset-0 z-20"></div>}
    </div>
  );
};

const SettingsTabWithSub: React.FC<{
  tempPaneData: any, setTempPaneData: (v: any) => void,
  isSavingPane: boolean, setIsSavingPane: (v: boolean) => void,
  globalVar: any, updateGlobalVar: (v: any) => Promise<void>,
}> = ({ tempPaneData, setTempPaneData, isSavingPane, setIsSavingPane, globalVar, updateGlobalVar }) => {
  const { paneDetail, api, setPaneDetail, updatePane } = useApp();
  const { displayPaneId, setToast } = usePane();
  const [sub, setSub] = useState<'Agent'|'Global'|'Tokens'>('Agent');

  return (
    <div style={{marginTop: '40px', height: 'calc(100% - 40px)', display: 'flex'}}>
      {/* Vertical sub-nav */}
      <div className="w-20 flex-shrink-0 border-r border-vsc-border bg-vsc-bg-secondary flex flex-col py-2 gap-1">
        {(['Agent', 'Global', 'Tokens'] as const).map(t => (
          <button key={t} onClick={() => setSub(t)}
            className={`mx-1 px-2 py-1.5 text-xs rounded text-left ${sub === t ? 'bg-vsc-bg text-vsc-text font-medium' : 'text-vsc-text-secondary hover:text-vsc-text hover:bg-vsc-bg-hover'}`}
          >{t}</button>
        ))}
      </div>
      {/* Content */}
      <div className="flex-1 min-w-0 relative">
        {sub === 'Agent' && (
          !tempPaneData ? (
            <div className="absolute inset-0 flex items-center justify-center"><Loader2 className="w-8 h-8 text-vsc-text-secondary animate-spin" /></div>
          ) : (
            <SettingsView
              pane={tempPaneData}
              onChange={setTempPaneData}
              onSave={async () => {
                if (!tempPaneData?.target) return;
                setIsSavingPane(true);
                try {
                  const { target, ...d } = tempPaneData;
                  await apiService.updatePane(target, d);
                  if (api) { const { data: u } = await api.getPane(target); setPaneDetail(u); }
                  updatePane(target, { title: tempPaneData.title, workspace: tempPaneData.workspace, agent_type: tempPaneData.agent_type, agent_duty: tempPaneData.agent_duty, role: tempPaneData.role, default_model: tempPaneData.default_model });
                } catch (err) { console.error(err); }
                finally { setIsSavingPane(false); }
              }}
              isSaving={isSavingPane}
            />
          )
        )}
        {sub === 'Global' && (
          <div className="h-full flex flex-col p-4">
            <label className="block text-xs text-vsc-text-secondary mb-2">Global Settings (JSON)</label>
            <textarea
              id="global-settings-textarea"
              defaultValue={JSON.stringify(globalVar, null, 2)}
              className="flex-1 w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-3 py-2 focus:outline-none focus:border-vsc-accent resize-none"
            />
            <button
              onClick={async () => {
                try {
                  const ta = document.getElementById('global-settings-textarea') as HTMLTextAreaElement;
                  await updateGlobalVar(JSON.parse(ta.value));
                  setToast('Saved'); setTimeout(() => setToast(null), 2000);
                } catch { setToast('Invalid JSON'); setTimeout(() => setToast(null), 2000); }
              }}
              className="mt-3 w-full bg-vsc-button hover:bg-vsc-button-hover text-white text-sm font-medium py-2 rounded"
            >Save</button>
          </div>
        )}
        {sub === 'Tokens' && <TokensSubTab />}
      </div>
    </div>
  );
};

const ALL_PERMS = ['api_full', 'ttyd_read', 'prompt', 'app_manage', 'agent_manage', 'desktop_manage'];

const TokensSubTab: React.FC = () => {
  const { setToast } = usePane();
  const [tokens, setTokens] = useState<any[]>([]);
  const [groups, setGroups] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [formGroupId, setFormGroupId] = useState('');
  const [formPerms, setFormPerms] = useState<string[]>(['ttyd_read', 'prompt']);
  const [formNote, setFormNote] = useState('');
  const [creating, setCreating] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([apiService.listTokens(), apiService.listGroups()])
      .then(([t, g]) => { setTokens(t.data.tokens || []); setGroups(g.data.groups || []); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const handleCreate = async () => {
    if (formPerms.length === 0) return;
    setCreating(true);
    try {
      const { data } = await apiService.createToken({
        group_id: formGroupId ? parseInt(formGroupId) : null,
        perms: formPerms,
        note: formNote || undefined,
      });
      setNewToken(data.token);
      const { data: t } = await apiService.listTokens();
      setTokens(t.tokens || []);
      setShowForm(false);
      setFormGroupId(''); setFormPerms(['ttyd_read', 'prompt']); setFormNote('');
    } catch (err) { setToast(`Create failed: ${err}`); setTimeout(() => setToast(null), 3000); }
    finally { setCreating(false); }
  };

  const handleDelete = async (id: number) => {
    if (!confirm(`Delete token #${id}?`)) return;
    await apiService.deleteToken(id);
    setTokens(t => t.filter(x => x.id !== id));
  };

  const doCopy = (text: string, key: string) => {
    navigator.clipboard.writeText(text);
    setCopied(key);
    setTimeout(() => setCopied(null), 1500);
  };

  const groupName = (id: number | null) => {
    if (id === null) return '—';
    const g = groups.find((x: any) => x.id === id);
    return g ? g.name : `#${id}`;
  };

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-vsc-border flex-shrink-0">
        <span className="text-xs font-semibold text-vsc-text">🔑 API Tokens</span>
        <div className="flex-1" />
        <button onClick={() => setShowForm(f => !f)} className="px-2 py-0.5 text-[11px] bg-vsc-button hover:bg-vsc-button-hover text-white rounded">+ New</button>
      </div>

      {/* Create form */}
      {showForm && (
        <div className="px-3 py-2 border-b border-vsc-border bg-vsc-bg-secondary space-y-2">
          <div className="flex gap-2">
            <select value={formGroupId} onChange={e => setFormGroupId(e.target.value)} className="flex-1 bg-vsc-bg border border-vsc-border text-vsc-text text-xs rounded px-2 py-1">
              <option value="">All groups</option>
              {groups.map((g: any) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
            <input value={formNote} onChange={e => setFormNote(e.target.value)} placeholder="Note..." className="flex-1 bg-vsc-bg border border-vsc-border text-vsc-text text-xs rounded px-2 py-1" />
          </div>
          <div className="flex flex-wrap gap-2">
            {ALL_PERMS.map(p => (
              <label key={p} className="flex items-center gap-1 text-[11px] text-vsc-text-secondary cursor-pointer">
                <input type="checkbox" checked={formPerms.includes(p)} onChange={e => setFormPerms(prev => e.target.checked ? [...prev, p] : prev.filter(x => x !== p))} className="accent-blue-500" />
                {p}
              </label>
            ))}
          </div>
          <button onClick={handleCreate} disabled={creating || formPerms.length === 0} className="px-3 py-1 text-xs bg-[#238636] hover:bg-[#2ea043] disabled:opacity-50 text-white rounded">
            {creating ? '...' : 'Create'}
          </button>
        </div>
      )}

      {/* New token display */}
      {newToken && (
        <div className="px-3 py-2 border-b border-vsc-border bg-green-900/20">
          <div className="flex items-center gap-2">
            <code className="flex-1 text-[11px] text-green-300 font-mono bg-black/30 px-2 py-1 rounded break-all">{newToken}</code>
            <button onClick={() => doCopy(newToken, 'new')} className="text-green-400 hover:text-green-200 text-xs">{copied === 'new' ? '✓' : 'Copy'}</button>
            <button onClick={() => setNewToken(null)} className="text-vsc-text-secondary hover:text-vsc-text text-xs">✕</button>
          </div>
        </div>
      )}

      {/* Token list */}
      <div className="flex-1 overflow-auto">
        {loading ? (
          <div className="flex items-center justify-center py-8"><Loader2 className="animate-spin text-vsc-text-secondary" size={20} /></div>
        ) : tokens.length === 0 ? (
          <div className="text-center py-8 text-vsc-text-secondary text-xs">No tokens</div>
        ) : (
          <table className="w-full text-[11px]">
            <thead className="sticky top-0 bg-vsc-bg-secondary">
              <tr className="text-vsc-text-secondary border-b border-vsc-border">
                <th className="px-2 py-1.5 text-left">ID</th>
                <th className="px-2 py-1.5 text-left">Prefix</th>
                <th className="px-2 py-1.5 text-left">Group</th>
                <th className="px-2 py-1.5 text-left">Perms</th>
                <th className="px-2 py-1.5 text-left">Note</th>
                <th className="px-2 py-1.5 text-left">Date</th>
                <th className="px-2 py-1.5"></th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((t: any) => (
                <tr key={t.id} className="border-b border-vsc-border hover:bg-vsc-bg-hover">
                  <td className="px-2 py-1.5 text-vsc-text-secondary">#{t.id}</td>
                  <td className="px-2 py-1.5 text-vsc-text font-mono">{t.token_prefix}</td>
                  <td className="px-2 py-1.5 text-blue-400">{groupName(t.group_id)}</td>
                  <td className="px-2 py-1.5">
                    <div className="flex flex-wrap gap-0.5">
                      {(t.perms || '').split(',').map((p: string) => (
                        <span key={p} className="px-1 bg-vsc-bg-secondary rounded text-vsc-text-secondary">{p}</span>
                      ))}
                    </div>
                  </td>
                  <td className="px-2 py-1.5 text-vsc-text-secondary truncate max-w-[80px]">{t.note || '-'}</td>
                  <td className="px-2 py-1.5 text-vsc-text-secondary">{(t.created_at || '').slice(0, 10)}</td>
                  <td className="px-2 py-1.5">
                    <button onClick={() => handleDelete(t.id)} className="text-vsc-text-secondary hover:text-red-400" title="Delete">✕</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
};

const SearchSelect: React.FC<{value: string, onChange: (v: string) => void, options: {value: string, label: string, role?: string}[], placeholder?: string}> = ({value, onChange, options, placeholder = 'Select...'}) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [roleTag, setRoleTag] = useState<'all' | 'master' | 'worker'>('all');
  const ref = React.useRef<HTMLDivElement>(null);
  const selectedLabel = options.find(o => o.value === value)?.label || '';

  React.useEffect(() => {
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener('mousedown', h);
    return () => document.removeEventListener('mousedown', h);
  }, []);

  const filtered = options.filter(o => {
    if (!o.label.toLowerCase().includes(search.toLowerCase())) return false;
    if (roleTag === 'master' && o.role !== 'master') return false;
    if (roleTag === 'worker' && o.role !== 'worker') return false;
    return true;
  });

  return (
    <div ref={ref} className="relative w-32">
      <button type="button" onClick={() => { setOpen(!open); setSearch(''); }} className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-[11px] rounded px-1.5 py-0.5 text-left truncate">
        {selectedLabel || placeholder}
      </button>
      {open && (
        <div className="absolute top-full left-0 mt-1 w-48 bg-vsc-bg-secondary border border-vsc-border rounded shadow-lg z-50">
          <input autoFocus value={search} onChange={e => setSearch(e.target.value)} placeholder="Search..." className="w-full bg-vsc-bg border-b border-vsc-border text-vsc-text text-[11px] px-2 py-1 outline-none" />
          <div className="flex gap-0.5 px-2 py-1 border-b border-vsc-border">
            {(['all', 'master', 'worker'] as const).map(f => (
              <button key={f} type="button" onClick={() => setRoleTag(f)}
                className={`px-1.5 py-0.5 text-[10px] rounded ${roleTag === f ? 'bg-vsc-button text-white' : 'text-vsc-text-secondary hover:text-vsc-text'}`}>
                {f === 'all' ? 'All' : f === 'master' ? '📋 Master' : '🔧 Worker'}
              </button>
            ))}
          </div>
          <div className="max-h-48 overflow-auto">
            {filtered.length === 0 ? (
              <div className="px-2 py-1 text-[11px] text-vsc-text-secondary">No results</div>
            ) : filtered.map(o => (
              <button key={o.value} type="button" onClick={() => { onChange(o.value); setOpen(false); }} className={`w-full text-left px-2 py-1 text-[11px] hover:bg-vsc-bg-hover ${o.value === value ? 'text-vsc-accent' : 'text-vsc-text'}`}>
                {o.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

const BindedAgentsTab: React.FC<{paneId: string, token: string | null, isDragging: boolean, setBoundAgents: (a: string[]) => void}> = ({paneId, token, isDragging, setBoundAgents}) => {
  const { allPanes } = useApp();
  const { setToast } = usePane();
  const { confirm: confirmDialog } = useDialog();
  const [agents, setAgents] = useState<any[]>([]);
  const [allBindings, setAllBindings] = useState<any[]>([]);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerW, setContainerW] = useState(400);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [heights, setHeights] = useState<Record<number, number>>({});
  const dragRef = useRef<{id: number, startY: number, startH: number} | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;
    const ro = new ResizeObserver(entries => { for (const e of entries) setContainerW(e.contentRect.width); });
    ro.observe(containerRef.current);
    return () => ro.disconnect();
  }, []);

  const narrow = containerW < 320;
  const stretch = containerW < 800;

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragRef.current) return;
      const delta = e.clientY - dragRef.current.startY;
      setHeights(h => ({ ...h, [dragRef.current!.id]: Math.max(120, dragRef.current!.startH + delta) }));
    };
    const onUp = () => { dragRef.current = null; };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
  }, []);

  const fetchAgents = async (incremental = false) => {
    if (!incremental) setLoading(true);
    try {
      const [{ data }, { data: allB }] = await Promise.all([
        apiService.getAgentsByPane(paneId),
        apiService.getAgentsByPane('all'),
      ]);
      setAllBindings(allB);
      const withTitles = data.map((a: any) => {
        const info = allPanes.find((p: any) => p.pane_id === a.name);
        return { ...a, title: info?.title || a.name, role: info?.role || '', model: info?.default_model || '', trust: info?.trust_level || '', status: info?.status || '' };
      });
      setAgents(prev => {
        if (!incremental) return withTitles;
        const prevIds = new Set(prev.map((a: any) => a.id));
        const newIds = new Set(withTitles.map((a: any) => a.id));
        return [...prev.filter((a: any) => newIds.has(a.id)), ...withTitles.filter((a: any) => !prevIds.has(a.id))];
      });
      setBoundAgents(data.map((a: any) => a.name));
    } catch {} finally { setLoading(false); }
  };

  useEffect(() => { fetchAgents(); }, [paneId]);
  useEffect(() => {
    const handler = (e: Event) => {
      const msg = (e as CustomEvent).detail;
      if (msg?.action === 'refresh_agents') fetchAgents(true);
    };
    window.addEventListener('notify', handler);
    return () => window.removeEventListener('notify', handler);
  }, [paneId]);
  useEffect(() => {
    setAgents(prev => prev.map(a => {
      const info = allPanes.find((p: any) => p.pane_id === a.name);
      return info ? { ...a, title: info.title || a.name, status: info.status || '', model: info.default_model || '', trust: info.trust_level || '' } : a;
    }));
  }, [allPanes]);

  const allBoundNames = new Set(allBindings.map((b: any) => b.name));
  const unbindable = allPanes.filter((p: any) => p.pane_id !== paneId && !allBoundNames.has(p.pane_id));

  const handleBind = async () => {
    if (!selectedAgent) return;
    try { await apiService.bindAgent({ pane_id: paneId, agent_name: selectedAgent }); fetchAgents(true); setSelectedAgent(''); } catch (err: any) { setToast(`Bind failed: ${err.message || err}`); setTimeout(() => setToast(null), 3000); }
  };

  const handleCreateAndBind = async () => {
    setCreating(true);
    try {
      const { data } = await apiService.createPane({ title: `worker-${Date.now().toString(36)}`, role: 'worker' });
      const newPaneId = data.pane_id || data.name;
      if (newPaneId) {
        await apiService.bindAgent({ pane_id: paneId, agent_name: newPaneId });
        fetchAgents(true);
        window.dispatchEvent(new CustomEvent('refresh-panes'));
        setToast('Worker created & bound');
        setTimeout(() => setToast(null), 2000);
      }
    } catch (err) { setToast(`Create failed: ${err}`); setTimeout(() => setToast(null), 3000); }
    finally { setCreating(false); }
  };

  const handleUnbind = (agentId: number, agentTitle: string) => {
    confirmDialog(
      <span>Unbind <strong>{agentTitle}</strong>?</span>,
      async () => {
        setAgents(prev => prev.filter(a => a.id !== agentId));
        if (expandedId === agentId) setExpandedId(null);
        try { await apiService.unbindAgent(agentId); fetchAgents(true); } catch {}
      }
    );
  };

  const statusColor = (s: string) => s === 'thinking' ? '#b08800' : s === 'idle' ? '#1a6b2a' : '#b62324';
  const statusDot = (s: string) => <span style={{display:'inline-block',width:6,height:6,borderRadius:'50%',background:statusColor(s),marginRight:3}} title={s || 'offline'} />;

  const counts = agents.reduce((acc, a) => { const s = a.status || 'offline'; acc[s] = (acc[s] || 0) + 1; return acc; }, {} as Record<string, number>);

  const renderCard = (agent: any, isStretch = false) => (
    <div
      key={agent.id}
      className="flex flex-col bg-vsc-bg border border-vsc-border rounded overflow-hidden"
      style={{
        ...(isStretch ? { width: '100%', height: '100%' } : { width: 420, height: 300, flexShrink: 0, flexGrow: 0 }),
        ...(agent.status === 'offline' ? { opacity: 0.5 } : {}),
      }}
    >
      {/* Card header */}
      <div className="flex items-center gap-1.5 px-2 py-1 flex-shrink-0" style={{background: statusColor(agent.status), borderBottom: `1px solid ${statusColor(agent.status)}`}}>
        {statusDot(agent.status)}
        <span className="text-[10px] text-vsc-text-secondary opacity-70">{agent.name.replace(':main.0','')}</span>
        <span className="text-[11px] text-vsc-text font-medium truncate flex-1">{(agent.title || agent.name).replace(':main.0','')}</span>
        {!narrow && agent.model && <span className="text-[10px] text-vsc-text-secondary opacity-60 truncate max-w-[100px]">{agent.model}</span>}
        <span className="text-[10px] text-vsc-text-secondary">{agent.status || 'offline'}</span>
      </div>
      {/* WebFrame terminal */}
      <div className="flex-1 relative min-h-0">
        <WebFrame src={urls.ttyd(agent.name, token || '', 1)} className="w-full h-full" />
        {isDragging && <div className="absolute inset-0 z-20" />}
      </div>
      {/* Card footer */}
      <div className="flex items-center gap-1 px-2 py-0.5 bg-vsc-bg-secondary flex-shrink-0">
        <button onClick={() => setExpandedId(agent.id)} className="text-[10px] px-1 text-vsc-text-secondary hover:text-vsc-text" title="Expand">⛶</button>
        <button onClick={() => window.open(urls.ttydOpen(agent.name, token || ''), '_blank')} className="text-[10px] px-1 text-vsc-text-secondary hover:text-vsc-text" title="Open in new tab">↗</button>
        <div className="flex-1" />
        <button onClick={() => handleUnbind(agent.id, agent.title || agent.name)} className="text-[10px] px-1 text-vsc-text-secondary hover:text-red-400" title="Unbind">✕</button>
      </div>
    </div>
  );

  // Expanded modal
  const expandedAgent = agents.find(a => a.id === expandedId);

  return (
    <div ref={containerRef} style={{marginTop: '40px', height: 'calc(100% - 40px)', display: 'flex', flexDirection: 'column'}}>
      {/* Top bar */}
      <div className="flex items-center gap-1 px-2 py-1.5 border-b border-vsc-border flex-shrink-0">
        <SearchSelect
          value={selectedAgent}
          onChange={setSelectedAgent}
          options={unbindable.map((p: any) => ({ value: p.pane_id, label: `${p.role === 'master' ? '📋' : '🔧'} ${(p.title || p.pane_id).replace(':main.0','')}`, role: p.role || '' }))}
          placeholder="Select..."
        />
        <button onClick={handleBind} disabled={!selectedAgent} className="px-1.5 py-0.5 text-[11px] bg-vsc-button hover:bg-vsc-button-hover disabled:opacity-40 text-white rounded" title="Bind">{narrow ? '+' : '+Bind'}</button>
        <button onClick={handleCreateAndBind} disabled={creating} className="px-1.5 py-0.5 text-[11px] bg-[#238636] hover:bg-[#2ea043] disabled:opacity-50 text-white rounded" title="Create new worker & bind">{creating ? '...' : narrow ? '+N' : '+New'}</button>
        <div className="flex-1" />
        <span className="text-[10px] text-vsc-text-secondary">{Object.entries(counts).map(([s, n]) => <span key={s} className="mr-2">{statusDot(s)}{n}</span>)}</span>
      </div>
      {/* Cards grid */}
      {loading ? (
        <div className="flex-1 flex items-center justify-center"><Loader2 className="animate-spin text-vsc-text-secondary" size={20} /></div>
      ) : agents.length === 0 ? (
        <div className="flex-1 flex items-center justify-center text-vsc-text-secondary text-xs">No workers bound</div>
      ) : stretch ? (
        <div className="flex-1 overflow-y-auto flex flex-col items-center gap-0 p-2">
          {agents.map((a, i) => (
            <React.Fragment key={a.id}>
              <div style={{width: '95%', height: heights[a.id] || 300, flexShrink: 0}}>{renderCard(a, true)}</div>
              {i < agents.length - 1 && (
                <div
                  style={{width: '95%', height: 6, cursor: 'row-resize', display: 'flex', alignItems: 'center', justifyContent: 'center'}}
                  onMouseDown={e => {
                    const el = (e.target as HTMLElement).previousElementSibling as HTMLElement;
                    dragRef.current = { id: a.id, startY: e.clientY, startH: el?.offsetHeight || heights[a.id] || 300 };
                  }}
                >
                  <div style={{width: 40, height: 2, background: '#555', borderRadius: 1}} />
                </div>
              )}
            </React.Fragment>
          ))}
        </div>
      ) : (
        <div className="flex-1 overflow-y-auto" style={{display: 'flex', flexWrap: 'wrap', gap: 8, padding: 8, alignContent: 'flex-start'}}>
          {agents.map(a => renderCard(a, false))}
        </div>
      )}
      {/* Expanded modal */}
      {expandedAgent && (
        <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4" onClick={() => setExpandedId(null)}>
          <div className="bg-vsc-bg border border-vsc-border rounded-lg flex flex-col" style={{width: '90%', height: '85%', maxWidth: 1200}} onClick={e => e.stopPropagation()}>
            <div className="flex items-center gap-2 px-3 py-2 bg-vsc-bg-secondary rounded-t-lg flex-shrink-0">
              {statusDot(expandedAgent.status)}
              <span className="text-sm text-vsc-text font-medium">{(expandedAgent.title || expandedAgent.name).replace(':main.0','')}</span>
              {expandedAgent.model && <span className="text-xs text-vsc-text-secondary">{expandedAgent.model}</span>}
              <span className="text-xs text-vsc-text-secondary">{expandedAgent.status || 'offline'}</span>
              <div className="flex-1" />
              <button onClick={() => window.open(urls.ttydOpen(expandedAgent.name, token || ''), '_blank')} className="text-xs px-2 py-0.5 text-vsc-text-secondary hover:text-vsc-text">↗ New Tab</button>
              <button onClick={() => setExpandedId(null)} className="text-xs px-2 py-0.5 text-vsc-text-secondary hover:text-red-400">✕</button>
            </div>
            <div className="flex-1 relative min-h-0">
              <WebFrame src={urls.ttyd(expandedAgent.name, token || '', 1)} className="w-full h-full" />
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

const PromptTab: React.FC = () => {
  const { globalVar, updateGlobalVar } = useApp();
  const { setToast } = usePane();
  const [text, setText] = useState(globalVar?.common_prompts || '');

  useEffect(() => { setText(globalVar?.common_prompts || ''); }, [globalVar?.common_prompts]);

  return (
    <div style={{marginTop: '40px', height: 'calc(100% - 40px)', display: 'flex', flexDirection: 'column', padding: '12px'}}>
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Enter common prompts here... Shared across all agents."
        className="flex-1 w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm font-mono rounded px-3 py-2 focus:outline-none focus:border-vsc-accent resize-none"
      />
      <button
        onClick={async () => {
          try {
            await updateGlobalVar({ ...globalVar, common_prompts: text });
            setToast('Prompt saved');
            setTimeout(() => setToast(null), 2000);
          } catch {
            setToast('Failed to save');
            setTimeout(() => setToast(null), 2000);
          }
        }}
        className="mt-2 w-full bg-vsc-button hover:bg-vsc-button-hover text-white text-sm font-medium py-2 rounded"
      >
        Save Prompt
      </button>
    </div>
  );
};

export default RightSidePanel;
