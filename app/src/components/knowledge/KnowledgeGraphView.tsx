// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import ForceGraph2D from 'react-force-graph-2d';
import { Loader2, RefreshCw, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';

// Knowledge graph: a BIPARTITE force graph over ~/cicy-ai/knowledge — entry nodes
// (colored by governance status) linked to their tag nodes (hubs). The store has
// no [[wikilinks]], so tags + domains ARE the structure. Search, domain/status
// filters, hover-neighborhood highlight, and entry selection live here. The
// parent decides where the selected Markdown file opens.

interface KnEntry { id: string; title: string; tags?: string; status?: string; domain?: string; summary?: string; path?: string }
interface GNode { id: string; kind: 'entry' | 'tag'; label: string; status?: string; domain?: string; val: number; color: string; x?: number; y?: number }

const STATUS_COLOR: Record<string, string> = {
  canon: '#34d399', pending: '#fbbf24', draft: '#94a3b8', deprecated: '#c084fc', rejected: '#fb7185',
};
const TAG_COLOR = '#60a5fa';
const ALL_STATUS = ['canon', 'pending', 'draft', 'deprecated', 'rejected'];

const parseTags = (t?: string): string[] =>
  (t || '').split(/[\s,]+/).map((s) => s.trim().toLowerCase()).filter(Boolean);
const normStatus = (s?: string) => (s || 'canon').toLowerCase();

interface Props { className?: string; onOpenEntry?: (entry: KnEntry) => void }

export default function KnowledgeGraphView({ className, onOpenEntry }: Props) {
  const { t } = useTranslation('workspace');
  const stLabel = (s?: string) => { const k = normStatus(s); return t('st' + k.charAt(0).toUpperCase() + k.slice(1)); };
  const [entries, setEntries] = useState<KnEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [offDomains, setOffDomains] = useState<Set<string>>(new Set());
  const [offStatus, setOffStatus] = useState<Set<string>>(new Set());
  const [hoverId, setHoverId] = useState<string | null>(null);

  const wrapRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const fgRef = useRef<any>(null);

  const load = useCallback(async () => {
    setLoading(true); setError('');
    try {
      const { data } = await apiService.listKnowledge({ view: 'index' });
      const rows: KnEntry[] = Array.isArray(data?.knowledge) ? data.knowledge : [];
      setEntries(rows.filter((r) => r.id && r.title));
    } catch (e: any) { setError(e?.message || 'load failed'); } finally { setLoading(false); }
  }, []);
  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    const el = wrapRef.current; if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el); setSize({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  const allDomains = useMemo(() => Array.from(new Set(entries.map((e) => e.domain).filter(Boolean))).sort() as string[], [entries]);

  // Entries passing the domain + status filters.
  const shownEntries = useMemo(() => entries.filter((e) =>
    !offDomains.has(e.domain || '') && !offStatus.has(normStatus(e.status))), [entries, offDomains, offStatus]);

  // Build the bipartite graph + a neighbor map (captured as id strings BEFORE
  // force-graph mutates link endpoints into node refs).
  const { graph, neighbors } = useMemo(() => {
    const nodes: GNode[] = [];
    const links: { source: string; target: string }[] = [];
    const nb = new Map<string, Set<string>>();
    const addNb = (a: string, b: string) => { (nb.get(a) || nb.set(a, new Set()).get(a))!.add(b); (nb.get(b) || nb.set(b, new Set()).get(b))!.add(a); };
    const tagCount = new Map<string, number>();
    shownEntries.forEach((e) => parseTags(e.tags).forEach((tg) => tagCount.set(tg, (tagCount.get(tg) || 0) + 1)));
    const keepTag = (tg: string) => { const c = tagCount.get(tg) || 0; return c >= 2 && c <= 40; };
    const tagIds = new Set<string>();
    shownEntries.forEach((e) => {
      const status = normStatus(e.status);
      nodes.push({ id: `e:${e.id}`, kind: 'entry', label: e.title, status, domain: e.domain, val: 2, color: STATUS_COLOR[status] || '#a1a1aa' });
      parseTags(e.tags).filter(keepTag).forEach((tg) => {
        const tid = `t:${tg}`;
        if (!tagIds.has(tid)) { tagIds.add(tid); nodes.push({ id: tid, kind: 'tag', label: `#${tg}`, val: Math.min(9, tagCount.get(tg) || 2), color: TAG_COLOR }); }
        links.push({ source: `e:${e.id}`, target: tid }); addNb(`e:${e.id}`, tid);
      });
    });
    return { graph: { nodes, links }, neighbors: nb };
  }, [shownEntries]);

  // Search → matched entry ids (dim the rest).
  const matched = useMemo(() => {
    const q = query.trim().toLowerCase(); if (!q) return null;
    const s = new Set<string>();
    shownEntries.forEach((e) => { if (`${e.title} ${e.tags || ''} ${e.domain || ''}`.toLowerCase().includes(q)) s.add(`e:${e.id}`); });
    return s;
  }, [query, shownEntries]);

  // Hover → node + its neighbors highlighted.
  const hiSet = useMemo(() => {
    if (!hoverId) return null;
    const s = new Set<string>([hoverId]); (neighbors.get(hoverId) || new Set()).forEach((x) => s.add(x)); return s;
  }, [hoverId, neighbors]);

  const openEntry = useCallback((id: string) => {
    const entry = entries.find((item) => item.id === id);
    if (!entry) return;
    setSelectedId(id);
    onOpenEntry?.(entry);
  }, [entries, onOpenEntry]);

  const onNodeClick = useCallback((node: any) => { if (node?.kind === 'entry') openEntry(String(node.id).replace(/^e:/, '')); }, [openEntry]);

  const selId = selectedId ? `e:${selectedId}` : null;
  const chip = (active: boolean, onClick: () => void, key: string, label: string, dot?: string) => (
    <button key={key} type="button" onClick={onClick}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] transition-colors ${active ? 'border-white/[0.14] bg-white/[0.06] text-zinc-200' : 'border-white/[0.05] text-zinc-600 line-through'}`}>
      {dot ? <span className="h-2 w-2 rounded-full" style={{ background: dot, opacity: active ? 1 : 0.4 }} /> : null}{label}
    </button>
  );

  return (
    <div data-id="knowledge-graph-view" className={`flex h-full w-full flex-col ${className || ''}`}>
      {/* toolbar: search + status chips + domain chips */}
      <div data-id="knowledge-graph-toolbar" className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-[var(--vsc-border)] bg-[#0b0b0d] px-3 py-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-zinc-600" />
          <input data-id="knowledge-graph-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('knSearchPlaceholder')}
            className="w-56 rounded-md border border-white/[0.08] bg-black/30 py-1 pl-7 pr-2 text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-blue-500/40" />
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {ALL_STATUS.map((s) => chip(!offStatus.has(s), () => setOffStatus((p) => { const n = new Set(p); n.has(s) ? n.delete(s) : n.add(s); return n; }), s, stLabel(s), STATUS_COLOR[s]))}
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {allDomains.map((d) => chip(!offDomains.has(d), () => setOffDomains((p) => { const n = new Set(p); n.has(d) ? n.delete(d) : n.add(d); return n; }), d, d))}
        </div>
        <span className="ml-auto text-[10px] text-zinc-600">{t('knCount', { n: shownEntries.length, m: allDomains.length })}</span>
      </div>

      <div className="flex min-h-0 flex-1">
        <div ref={wrapRef} data-id="knowledge-graph-canvas" className="relative min-w-0 flex-1 overflow-hidden bg-[#08080a]">
          <button data-id="knowledge-graph-refresh" type="button" onClick={load} title={t('knRefresh')}
            className="absolute right-3 top-3 z-10 inline-flex h-7 w-7 items-center justify-center rounded-md border border-white/[0.08] bg-black/40 text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200">
            <RefreshCw className="h-3.5 w-3.5" />
          </button>
          {loading ? (
            <div className="flex h-full items-center justify-center text-[12px] text-zinc-600"><Loader2 className="mr-2 h-4 w-4 animate-spin" /> {t('knBuilding')}</div>
          ) : error ? (
            <div className="flex h-full items-center justify-center text-[12px] text-rose-400">{error}</div>
          ) : (
            <ForceGraph2D
              ref={fgRef}
              width={size.w}
              height={size.h}
              graphData={graph}
              backgroundColor="#08080a"
              nodeRelSize={4}
              d3VelocityDecay={0.28}
              cooldownTicks={140}
              onNodeHover={(n: any) => setHoverId(n ? n.id : null)}
              onNodeClick={onNodeClick}
              nodeLabel={(n: any) => (n.kind === 'entry' ? `${n.label}${n.domain ? `  ·  ${n.domain}` : ''}  [${stLabel(n.status)}]` : `${n.label}`)}
              linkColor={(l: any) => {
                const a = typeof l.source === 'object' ? l.source.id : l.source, b = typeof l.target === 'object' ? l.target.id : l.target;
                if (hiSet && hiSet.has(a) && hiSet.has(b)) return 'rgba(96,165,250,0.55)';
                return hiSet ? 'rgba(255,255,255,0.03)' : 'rgba(255,255,255,0.07)';
              }}
              linkWidth={(l: any) => { const a = typeof l.source === 'object' ? l.source.id : l.source, b = typeof l.target === 'object' ? l.target.id : l.target; return hiSet && hiSet.has(a) && hiSet.has(b) ? 1.4 : 0.5; }}
              linkDirectionalParticles={(l: any) => { const a = typeof l.source === 'object' ? l.source.id : l.source, b = typeof l.target === 'object' ? l.target.id : l.target; return hiSet && hiSet.has(a) && hiSet.has(b) ? 2 : 0; }}
              linkDirectionalParticleWidth={2}
              linkDirectionalParticleColor={() => '#93c5fd'}
              nodeCanvasObjectMode={() => 'replace'}
              nodePointerAreaPaint={(node: any, color: string, ctx: any) => {
                const r = Math.sqrt(node.val) * 2 + (node.kind === 'tag' ? 1.5 : 2.5);
                ctx.fillStyle = color; ctx.beginPath(); ctx.arc(node.x, node.y, r + 2, 0, 2 * Math.PI); ctx.fill();
              }}
              nodeCanvasObject={(node: any, ctx: any, scale: number) => {
                const isSel = node.id === selId;
                const isHover = node.id === hoverId;
                const dim = (hiSet && !hiSet.has(node.id)) || (matched && node.kind === 'entry' && !matched.has(node.id) && !(hiSet && hiSet.has(node.id)));
                const r = Math.sqrt(node.val) * 2 + (node.kind === 'tag' ? 1.5 : 2.5);
                ctx.globalAlpha = dim ? 0.1 : 1;
                // soft glow for hover/selected/matched
                if (isSel || isHover || (matched && matched.has(node.id))) { ctx.shadowColor = node.color; ctx.shadowBlur = 14; }
                ctx.beginPath(); ctx.arc(node.x, node.y, r, 0, 2 * Math.PI);
                if (node.kind === 'tag') { ctx.fillStyle = 'rgba(96,165,250,0.16)'; ctx.fill(); ctx.lineWidth = 1 / scale; ctx.strokeStyle = TAG_COLOR; ctx.stroke(); }
                else { ctx.fillStyle = node.color; ctx.fill(); }
                ctx.shadowBlur = 0;
                if (isSel || isHover) { ctx.lineWidth = 1.6 / scale; ctx.strokeStyle = '#ffffff'; ctx.stroke(); }
                // label: tags when zoomed; entries when hover/selected/neighbor/matched
                const showLabel = !dim && (node.kind === 'tag'
                  ? scale > 1.15
                  : (isHover || isSel || (hiSet && hiSet.has(node.id)) || (matched && matched.has(node.id) && scale > 0.9)));
                if (showLabel) {
                  const fs = Math.max(3, 11 / scale);
                  ctx.font = `${fs}px ui-sans-serif, system-ui, sans-serif`;
                  ctx.textAlign = 'center'; ctx.textBaseline = 'bottom';
                  const txt = String(node.label).slice(0, 30);
                  const w = ctx.measureText(txt).width;
                  ctx.fillStyle = 'rgba(8,8,10,0.72)'; ctx.fillRect(node.x - w / 2 - 2, node.y - r - 4 - fs, w + 4, fs + 2);
                  ctx.fillStyle = node.kind === 'tag' ? '#93c5fd' : '#e4e4e7';
                  ctx.fillText(txt, node.x, node.y - r - 3);
                }
                ctx.globalAlpha = 1;
              }}
            />
          )}
        </div>

      </div>
    </div>
  );
}
