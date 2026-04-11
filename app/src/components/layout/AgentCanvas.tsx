import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Check, Copy, Folder, Pencil, Settings } from 'lucide-react';
import { assetUrl } from '../../lib/assets';
import config from '../../config';
import { lockPointer, unlockPointer } from '../../lib/pointerLock';
import { useDevRegister } from '../../lib/devStore';
import { emitWebFrameMaskEvent } from '../../lib/webFrameMask';
import { WebFrame } from '../WebFrame';

export interface AgentCanvasItem {
  paneId: string;
  title: string;
  agentType?: string;
  status?: string;
  contextUsage?: number | null;
  machineLabel?: string;
  ttydSrc?: string;
  workspace?: string;
  isPrimary?: boolean;
  isApiOnly?: boolean;
}

interface AgentCanvasProps {
  scopeId: string;
  items: AgentCanvasItem[];
  activePaneId: string;
  onActivePaneIdChange: (paneId: string) => void;
  onOpenCodeServicePane?: (paneId: string, workspace?: string) => void;
  onOpenOpenClaw?: () => void;
  onRenamePaneTitle?: (paneId: string, title: string) => Promise<void>;
  onOpenSettingsPane?: (paneId: string) => void;
  locateRequest?: { paneId: string; nonce: number; zoomToActual?: boolean } | null;
}

interface ViewportState {
  x: number;
  y: number;
  zoom: number;
}

interface WindowLayout {
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  minimized?: boolean;
}

const WORLD_WIDTH = 5200;
const WORLD_HEIGHT = 3200;
const MIN_WIDTH = 420;
const MIN_HEIGHT = 320;
const MAX_WIDTH = 1600;
const MAX_HEIGHT = 1200;
const MIN_ZOOM = 0.45;
const MAX_ZOOM = 1.6;
const DEFAULT_WINDOW_WIDTH = 760;
const DEFAULT_WINDOW_HEIGHT = 760;
const WINDOW_GAP = 96;
const WINDOW_MARGIN = 96;
const LOCATE_PADDING = 48;
const INITIAL_VISIBLE_RATIO = 0.58;
const MIN_VISIBLE_WINDOWS = 2;
const AGENT_CANVAS_LAYOUT_VERSION = 'v3';

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? JSON.parse(raw) : fallback;
  } catch {
    return fallback;
  }
}

function statusTone(status?: string) {
  switch ((status || '').trim().toLowerCase()) {
    case 'thinking':
    case 'tool_use':
      return {
        dot: 'bg-amber-400',
        ring: 'border-amber-400/40 shadow-[0_0_0_1px_rgba(251,191,36,0.18)]',
        label: status === 'tool_use' ? 'tool' : 'thinking',
      };
    case 'restarting':
      return {
        dot: 'bg-sky-400',
        ring: 'border-sky-400/35 shadow-[0_0_0_1px_rgba(56,189,248,0.14)]',
        label: 'restarting',
      };
    case 'idle':
    case 'text':
    default:
      return {
        dot: 'bg-emerald-400',
        ring: 'border-white/[0.08]',
        label: status || 'idle',
      };
  }
}

function blurActiveEditableElement() {
  const active = document.activeElement as HTMLElement | null;
  if (!active) return;
  const tagName = active.tagName;
  const isEditable = active.isContentEditable || tagName === 'TEXTAREA' || tagName === 'INPUT';
  if (!isEditable) return;
  try {
    active.blur();
  } catch {}
}

function normalizeAgentType(agentType?: string) {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw';
    case 'codex':
    case 'openai':
    case 'kiro-cli':
    case 'kiro-cli chat':
    case 'gemini':
    case 'copilot':
      return 'codex';
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude';
    case 'cicy':
      return 'cicy';
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode';
    default:
      return '';
  }
}

function AgentWindowAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType);
  const baseClassName = 'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border shadow-sm';

  if (!normalizedAgentType) {
    return (
      <div
        data-id="agent-window-avatar"
        className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}
        title={title}
      >
        <span data-id="agent-window-avatar-fallback" className="text-[11px] font-semibold uppercase">{title.slice(0, 1) || '?'}</span>
      </div>
    );
  }

  if (normalizedAgentType === 'openclaw') {
    return (
      <div
        data-id="agent-window-avatar"
        className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}
        title="OpenClaw"
      >
        <span data-id="agent-window-avatar-openclaw" className="text-[18px] leading-none" aria-label="OpenClaw">🦞</span>
      </div>
    );
  }

  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
    claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
    cicy: { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
    opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg'), className: 'h-6 w-6' },
  };
  const icon = iconMap[normalizedAgentType];
  if (!icon) return null;

  return (
    <div
      data-id="agent-window-avatar"
      className={`${baseClassName} border-zinc-500/40 bg-zinc-300`}
      title={icon.label}
    >
      <img data-id="agent-window-avatar-image" src={icon.src} alt={icon.label} className={`${icon.className || 'h-5 w-5'} object-contain`} />
    </div>
  );
}

function defaultWindowLayout(index: number, _isPrimary: boolean, zIndex: number, total: number): WindowLayout {
  if (total <= 1) {
    return { x: 180, y: 140, width: DEFAULT_WINDOW_WIDTH, height: DEFAULT_WINDOW_HEIGHT, zIndex };
  }

  const cols = total <= 2 ? total : total <= 4 ? 2 : 3;
  const rows = Math.ceil(total / cols);
  const row = Math.floor(index / cols);
  const col = index % cols;
  const itemsInRow = row === rows - 1 ? total - row * cols : cols;
  const safeItemsInRow = Math.max(1, itemsInRow);
  const maxRowWidth = cols * DEFAULT_WINDOW_WIDTH + (cols - 1) * WINDOW_GAP;
  const rowWidth = safeItemsInRow * DEFAULT_WINDOW_WIDTH + (safeItemsInRow - 1) * WINDOW_GAP;
  const rowOffset = (maxRowWidth - rowWidth) / 2;

  return {
    x: WINDOW_MARGIN + rowOffset + col * (DEFAULT_WINDOW_WIDTH + WINDOW_GAP),
    y: WINDOW_MARGIN + row * (DEFAULT_WINDOW_HEIGHT + WINDOW_GAP),
    width: DEFAULT_WINDOW_WIDTH,
    height: DEFAULT_WINDOW_HEIGHT,
    zIndex,
  };
}

function layoutsOverlap(a: WindowLayout, b: WindowLayout, gap = 24) {
  return !(
    a.x + a.width + gap <= b.x ||
    b.x + b.width + gap <= a.x ||
    a.y + a.height + gap <= b.y ||
    b.y + b.height + gap <= a.y
  );
}

function findAvailableWindowLayout(
  existingLayouts: WindowLayout[],
  index: number,
  isPrimary: boolean,
  zIndex: number,
  total: number,
): WindowLayout {
  const preferred = defaultWindowLayout(index, isPrimary, zIndex, total);
  const cols = Math.max(1, Math.floor((WORLD_WIDTH - WINDOW_MARGIN * 2 + WINDOW_GAP) / (DEFAULT_WINDOW_WIDTH + WINDOW_GAP)));
  const rows = Math.max(1, Math.floor((WORLD_HEIGHT - WINDOW_MARGIN * 2 + WINDOW_GAP) / (DEFAULT_WINDOW_HEIGHT + WINDOW_GAP)));
  const preferredCol = Math.max(0, Math.min(cols - 1, Math.round((preferred.x - WINDOW_MARGIN) / (DEFAULT_WINDOW_WIDTH + WINDOW_GAP))));
  const preferredRow = Math.max(0, Math.min(rows - 1, Math.round((preferred.y - WINDOW_MARGIN) / (DEFAULT_WINDOW_HEIGHT + WINDOW_GAP))));
  const tried = new Set<string>();
  const candidates: WindowLayout[] = [preferred];

  for (let distance = 0; distance < cols + rows; distance += 1) {
    for (let row = 0; row < rows; row += 1) {
      for (let col = 0; col < cols; col += 1) {
        const manhattan = Math.abs(row - preferredRow) + Math.abs(col - preferredCol);
        if (manhattan !== distance) continue;
        const key = `${col}:${row}`;
        if (tried.has(key)) continue;
        tried.add(key);
        candidates.push({
          x: WINDOW_MARGIN + col * (DEFAULT_WINDOW_WIDTH + WINDOW_GAP),
          y: WINDOW_MARGIN + row * (DEFAULT_WINDOW_HEIGHT + WINDOW_GAP),
          width: DEFAULT_WINDOW_WIDTH,
          height: DEFAULT_WINDOW_HEIGHT,
          zIndex,
        });
      }
    }
  }

  const available = candidates.find((candidate) => (
    existingLayouts.every((layout) => !layoutsOverlap(candidate, layout))
  ));
  if (available) return available;

  const fallbackOffset = Math.min(existingLayouts.length * 36, 240);
  return {
    ...preferred,
    x: Math.max(24, Math.min(WORLD_WIDTH - preferred.width - 24, preferred.x + fallbackOffset)),
    y: Math.max(24, Math.min(WORLD_HEIGHT - preferred.height - 24, preferred.y + fallbackOffset)),
  };
}

function getVisibleRatio(rect: DOMRect, layout: WindowLayout, viewport: ViewportState) {
  const left = layout.x * viewport.zoom + viewport.x;
  const top = layout.y * viewport.zoom + viewport.y;
  const right = (layout.x + layout.width) * viewport.zoom + viewport.x;
  const bottom = (layout.y + layout.height) * viewport.zoom + viewport.y;
  const visibleWidth = Math.max(0, Math.min(rect.width, right) - Math.max(0, left));
  const visibleHeight = Math.max(0, Math.min(rect.height, bottom) - Math.max(0, top));
  const visibleArea = visibleWidth * visibleHeight;
  const totalArea = Math.max(1, (right - left) * (bottom - top));
  return visibleArea / totalArea;
}

export default function AgentCanvas({
  scopeId,
  items,
  activePaneId,
  onActivePaneIdChange,
  onOpenCodeServicePane,
  onOpenOpenClaw,
  onRenamePaneTitle,
  onOpenSettingsPane,
  locateRequest,
}: AgentCanvasProps) {
  const viewportKey = `agent_canvas:${scopeId}:viewport:${AGENT_CANVAS_LAYOUT_VERSION}`;
  const layoutsKey = `agent_canvas:${scopeId}:layouts:${AGENT_CANVAS_LAYOUT_VERSION}`;
  const [viewport, setViewport] = useState<ViewportState>(() => readJSON(viewportKey, { x: 120, y: 90, zoom: 0.82 }));
  const [layouts, setLayouts] = useState<Record<string, WindowLayout>>(() => readJSON(layoutsKey, {}));
  const [mountedPaneIds, setMountedPaneIds] = useState<string[]>(() => (
    items
      .filter((item) => item.isPrimary && item.ttydSrc && !item.isApiOnly)
      .map((item) => item.paneId)
      .slice(0, config.maxLiveTtydWindows)
  ));
  const stageRef = useRef<HTMLDivElement>(null);
  const hadSavedLayoutsRef = useRef(Object.keys(readJSON<Record<string, WindowLayout>>(layoutsKey, {})).length > 0);
  const didInitialFitRef = useRef(false);
  const lastFitItemCountRef = useRef(0);
  const handledLocateNonceRef = useRef<number | null>(null);
  const locateFrameRef = useRef<number | null>(null);
  const stageScrollResetFrameRef = useRef<number | null>(null);
  const zoomMaskTimerRef = useRef<number | null>(null);

  useEffect(() => {
    localStorage.setItem(viewportKey, JSON.stringify(viewport));
  }, [viewport, viewportKey]);

  useEffect(() => {
    localStorage.setItem(layoutsKey, JSON.stringify(layouts));
  }, [layouts, layoutsKey]);

  const retainMountedPaneIds = useCallback((
    currentMountedPaneIds: string[],
    preferredPaneIds: string[] = [],
  ) => {
    const livePaneIds = new Set(
      items
        .filter((item) => item.ttydSrc && !item.isApiOnly)
        .map((item) => item.paneId),
    );
    const ordered = currentMountedPaneIds.filter((paneId) => livePaneIds.has(paneId));
    const pinnedPaneIds = preferredPaneIds.filter((paneId) => livePaneIds.has(paneId));

    pinnedPaneIds.forEach((paneId) => {
      const index = ordered.indexOf(paneId);
      if (index >= 0) ordered.splice(index, 1);
      ordered.push(paneId);
    });

    while (ordered.length > config.maxLiveTtydWindows) {
      const removableIndex = ordered.findIndex((paneId) => !pinnedPaneIds.includes(paneId));
      if (removableIndex >= 0) {
        ordered.splice(removableIndex, 1);
        continue;
      }
      ordered.splice(0, ordered.length - config.maxLiveTtydWindows);
    }

    return ordered;
  }, [items]);

  const ensurePaneMounted = useCallback((paneId: string) => {
    setMountedPaneIds((prev) => retainMountedPaneIds(prev, [
      ...items.filter((item) => item.isPrimary).map((item) => item.paneId),
      paneId,
    ]));
  }, [items, retainMountedPaneIds]);

  useEffect(() => () => {
    if (locateFrameRef.current !== null) {
      window.cancelAnimationFrame(locateFrameRef.current);
    }
    if (stageScrollResetFrameRef.current !== null) {
      window.cancelAnimationFrame(stageScrollResetFrameRef.current);
    }
    if (zoomMaskTimerRef.current !== null) {
      window.clearTimeout(zoomMaskTimerRef.current);
    }
    emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${scopeId}:canvas-drag`, reason: 'canvas-drag' });
    emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${scopeId}:canvas-zoom`, reason: 'canvas-zoom' });
  }, []);

  const resetStageScroll = useCallback(() => {
    const stage = stageRef.current;
    if (!stage) return;
    if (stage.scrollTop !== 0) stage.scrollTop = 0;
    if (stage.scrollLeft !== 0) stage.scrollLeft = 0;
  }, []);

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    resetStageScroll();
    const handleScroll = () => {
      if (stageScrollResetFrameRef.current !== null) {
        window.cancelAnimationFrame(stageScrollResetFrameRef.current);
      }
      stageScrollResetFrameRef.current = window.requestAnimationFrame(() => {
        stageScrollResetFrameRef.current = null;
        resetStageScroll();
      });
    };
    stage.addEventListener('scroll', handleScroll, { passive: true });
    return () => {
      stage.removeEventListener('scroll', handleScroll);
      if (stageScrollResetFrameRef.current !== null) {
        window.cancelAnimationFrame(stageScrollResetFrameRef.current);
        stageScrollResetFrameRef.current = null;
      }
    };
  }, [resetStageScroll]);

  useEffect(() => {
    resetStageScroll();
  }, [activePaneId, resetStageScroll, viewport]);

  useEffect(() => {
    setLayouts((prev) => {
      if (!hadSavedLayoutsRef.current) {
        const next: Record<string, WindowLayout> = {};
        let changed = Object.keys(prev).length !== items.length;
        items.forEach((item, index) => {
          const layout = defaultWindowLayout(index, !!item.isPrimary, index + 1, items.length);
          next[item.paneId] = layout;
          const prevLayout = prev[item.paneId];
          if (
            !prevLayout ||
            prevLayout.x !== layout.x ||
            prevLayout.y !== layout.y ||
            prevLayout.width !== layout.width ||
            prevLayout.height !== layout.height ||
            prevLayout.zIndex !== layout.zIndex
          ) {
            changed = true;
          }
        });
        return changed ? next : prev;
      }

      const next: Record<string, WindowLayout> = {};
      let changed = false;
      let maxZ = 0;
      Object.values(prev).forEach((layout) => {
        if (layout.zIndex > maxZ) maxZ = layout.zIndex;
      });
      items.forEach((item, index) => {
        if (prev[item.paneId]) {
          next[item.paneId] = prev[item.paneId];
          return;
        }
        changed = true;
        maxZ += 1;
        next[item.paneId] = findAvailableWindowLayout(
          Object.values(next),
          index,
          !!item.isPrimary,
          maxZ,
          items.length,
        );
      });
      Object.keys(prev).forEach((paneId) => {
        if (!next[paneId]) {
          changed = true;
        }
      });
      return changed ? next : prev;
    });
  }, [items]);

  useEffect(() => {
    setMountedPaneIds((prev) => retainMountedPaneIds(prev, [
      ...items.filter((item) => item.isPrimary).map((item) => item.paneId),
      activePaneId,
    ]));
  }, [activePaneId, items, retainMountedPaneIds]);

  const focusPane = useCallback((paneId: string) => {
    ensurePaneMounted(paneId);
    onActivePaneIdChange(paneId);
    setLayouts((prev) => {
      const current = prev[paneId];
      if (!current) return prev;
      const maxZ = Math.max(...Object.values(prev).map((layout) => layout.zIndex), 0);
      if (current.zIndex >= maxZ) return prev;
      return {
        ...prev,
        [paneId]: {
          ...current,
          zIndex: maxZ + 1,
        },
      };
    });
  }, [ensurePaneMounted, onActivePaneIdChange]);

  const updateLayout = useCallback((paneId: string, patch: Partial<WindowLayout>) => {
    setLayouts((prev) => {
      const current = prev[paneId];
      if (!current) return prev;
      return {
        ...prev,
        [paneId]: { ...current, ...patch },
      };
    });
  }, []);

  const fitAll = useCallback(() => {
    const stage = stageRef.current;
    if (!stage || items.length === 0) return;
    const rect = stage.getBoundingClientRect();
    const visibleLayouts = items
      .map((item) => layouts[item.paneId])
      .filter((layout): layout is WindowLayout => !!layout);
    if (visibleLayouts.length === 0) return;
    const minX = Math.min(...visibleLayouts.map((layout) => layout.x));
    const minY = Math.min(...visibleLayouts.map((layout) => layout.y));
    const maxX = Math.max(...visibleLayouts.map((layout) => layout.x + layout.width));
    const maxY = Math.max(...visibleLayouts.map((layout) => layout.y + layout.height));
    const boundsWidth = Math.max(1, maxX - minX);
    const boundsHeight = Math.max(1, maxY - minY);
    const padding = 140;
    const zoom = Math.max(
      MIN_ZOOM,
      Math.min(
        MAX_ZOOM,
        Math.min(rect.width / (boundsWidth + padding * 2), rect.height / (boundsHeight + padding * 2), 1),
      ),
    );
    setViewport({
      zoom,
      x: rect.width / 2 - (minX + boundsWidth / 2) * zoom,
      y: rect.height / 2 - (minY + boundsHeight / 2) * zoom,
    });
  }, [items, layouts]);

  const resetZoomToActual = useCallback(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const rect = stage.getBoundingClientRect();
    setViewport((prev) => {
      const centerWorldX = (rect.width / 2 - prev.x) / prev.zoom;
      const centerWorldY = (rect.height / 2 - prev.y) / prev.zoom;
      return {
        zoom: 1,
        x: rect.width / 2 - centerWorldX,
        y: rect.height / 2 - centerWorldY,
      };
    });
  }, []);

  const getCenteredViewportForPane = useCallback((
    rect: DOMRect,
    layout: WindowLayout,
    currentZoom: number,
    zoomToActual = false,
  ) => {
    const maxUsableWidth = Math.max(1, rect.width - LOCATE_PADDING * 2);
    const maxUsableHeight = Math.max(1, rect.height - LOCATE_PADDING * 2);
    const fitZoom = Math.min(
      MAX_ZOOM,
      Math.max(
        MIN_ZOOM,
        Math.min(maxUsableWidth / layout.width, maxUsableHeight / layout.height, 1),
      ),
    );
    const zoom = zoomToActual ? 1 : Math.min(currentZoom, fitZoom);
    const x = rect.width / 2 - (layout.x + layout.width / 2) * zoom;
    const y = rect.height / 2 - (layout.y + layout.height / 2) * zoom;
    return { x, y, zoom };
  }, []);

  const isPaneFullyVisible = useCallback((rect: DOMRect, layout: WindowLayout, nextViewport: ViewportState) => {
    const left = layout.x * nextViewport.zoom + nextViewport.x;
    const top = layout.y * nextViewport.zoom + nextViewport.y;
    const right = (layout.x + layout.width) * nextViewport.zoom + nextViewport.x;
    const bottom = (layout.y + layout.height) * nextViewport.zoom + nextViewport.y;
    return (
      left >= LOCATE_PADDING &&
      top >= LOCATE_PADDING &&
      right <= rect.width - LOCATE_PADDING &&
      bottom <= rect.height - LOCATE_PADDING
    );
  }, []);

  useEffect(() => {
    if (items.length === 0) return;
    const stage = stageRef.current;
    if (!stage) return;
    const hasAllLayouts = items.every((item) => !!layouts[item.paneId]);
    if (!hasAllLayouts) return;

    const shouldRunFit = !didInitialFitRef.current || (!hadSavedLayoutsRef.current && lastFitItemCountRef.current !== items.length);
    if (!shouldRunFit) return;

    const shouldReframeSavedViewport = () => {
      if (!hadSavedLayoutsRef.current) return true;
      const rect = stage.getBoundingClientRect();
      const activeLayout = layouts[activePaneId];
      const activeVisibleRatio = activeLayout ? getVisibleRatio(rect, activeLayout, viewport) : 0;
      const visibleWindowCount = items.reduce((count, item) => {
        const layout = layouts[item.paneId];
        if (!layout) return count;
        return count + (getVisibleRatio(rect, layout, viewport) >= 0.35 ? 1 : 0);
      }, 0);
      return activeVisibleRatio < INITIAL_VISIBLE_RATIO || visibleWindowCount < Math.min(MIN_VISIBLE_WINDOWS, items.length);
    };

    const frame = window.requestAnimationFrame(() => {
      if (shouldReframeSavedViewport()) {
        fitAll();
      }
      didInitialFitRef.current = true;
      lastFitItemCountRef.current = items.length;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [activePaneId, fitAll, items, layouts, viewport]);

  const centerPane = useCallback((paneId: string, zoomToActual = false) => {
    const stage = stageRef.current;
    const layout = layouts[paneId];
    if (!stage || !layout) return null;
    resetStageScroll();
    const rect = stage.getBoundingClientRect();
    const nextViewport = getCenteredViewportForPane(rect, layout, viewport.zoom, zoomToActual);
    setViewport(nextViewport);
    focusPane(paneId);
    if (stageScrollResetFrameRef.current !== null) {
      window.cancelAnimationFrame(stageScrollResetFrameRef.current);
    }
    stageScrollResetFrameRef.current = window.requestAnimationFrame(() => {
      stageScrollResetFrameRef.current = null;
      resetStageScroll();
    });
    return { layout, rect, viewport: nextViewport };
  }, [focusPane, getCenteredViewportForPane, layouts, resetStageScroll, viewport.zoom]);

  useEffect(() => {
    if (!locateRequest?.paneId) return;
    if (locateRequest.nonce === handledLocateNonceRef.current) return;
    if (!items.some((item) => item.paneId === locateRequest.paneId)) return;
    if (!layouts[locateRequest.paneId]) return;
    let cancelled = false;
    let attempts = 0;

    const runLocate = () => {
      if (cancelled) return;
      attempts += 1;
      const result = centerPane(locateRequest.paneId, !!locateRequest.zoomToActual);
      if (!result) return;
      const { layout, rect, viewport: nextViewport } = result;
      if (locateRequest.zoomToActual || isPaneFullyVisible(rect, layout, nextViewport) || attempts >= 4) {
        handledLocateNonceRef.current = locateRequest.nonce;
        locateFrameRef.current = null;
        return;
      }
      locateFrameRef.current = window.requestAnimationFrame(runLocate);
    };

    if (locateFrameRef.current !== null) {
      window.cancelAnimationFrame(locateFrameRef.current);
    }
    locateFrameRef.current = window.requestAnimationFrame(runLocate);
    return () => {
      cancelled = true;
      if (locateFrameRef.current !== null) {
        window.cancelAnimationFrame(locateFrameRef.current);
        locateFrameRef.current = null;
      }
    };
  }, [centerPane, isPaneFullyVisible, items, layouts, locateRequest]);
  useDevRegister(`AgentCanvas:${scopeId}`, {
    scopeId,
    itemCount: items.length,
    activePaneId,
    viewport,
    layoutCount: Object.keys(layouts).length,
    mountedPaneIds,
    locatePaneId: locateRequest?.paneId || '',
  });

  const handleWheel = useCallback((event: React.WheelEvent<HTMLDivElement>) => {
    const stage = stageRef.current;
    if (!stage) return;
    event.preventDefault();
    emitWebFrameMaskEvent({ action: 'start', key: `agent-canvas:${scopeId}:canvas-zoom`, reason: 'canvas-zoom' });
    if (zoomMaskTimerRef.current !== null) {
      window.clearTimeout(zoomMaskTimerRef.current);
    }
    zoomMaskTimerRef.current = window.setTimeout(() => {
      zoomMaskTimerRef.current = null;
      emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${scopeId}:canvas-zoom`, reason: 'canvas-zoom' });
    }, 140);
    const rect = stage.getBoundingClientRect();
    const cursorX = event.clientX - rect.left;
    const cursorY = event.clientY - rect.top;
    setViewport((prev) => {
      const delta = event.deltaY > 0 ? 0.92 : 1.08;
      const nextZoom = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, Number((prev.zoom * delta).toFixed(3))));
      const worldX = (cursorX - prev.x) / prev.zoom;
      const worldY = (cursorY - prev.y) / prev.zoom;
      return {
        zoom: nextZoom,
        x: cursorX - worldX * nextZoom,
        y: cursorY - worldY * nextZoom,
      };
    });
  }, [scopeId]);

  const handleStageMouseDown = useCallback((event: React.MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    if ((event.target as HTMLElement).closest('[data-agent-canvas-window="true"]')) return;
    const startX = event.clientX;
    const startY = event.clientY;
    const startViewport = viewport;
    emitWebFrameMaskEvent({ action: 'start', key: `agent-canvas:${scopeId}:canvas-drag`, reason: 'canvas-drag' });
    lockPointer();

    const onMove = (moveEvent: MouseEvent) => {
      setViewport((prev) => ({
        ...prev,
        x: startViewport.x + moveEvent.clientX - startX,
        y: startViewport.y + moveEvent.clientY - startY,
      }));
    };
    const onUp = () => {
      emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${scopeId}:canvas-drag`, reason: 'canvas-drag' });
      unlockPointer();
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    event.preventDefault();
  }, [scopeId, viewport]);

  const worldStyle = useMemo(() => ({
    width: WORLD_WIDTH,
    height: WORLD_HEIGHT,
    transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})`,
    transformOrigin: '0 0',
  }), [viewport]);

  return (
    <div
      data-id="agent-canvas-root"
      ref={stageRef}
      className="absolute inset-0 overflow-hidden bg-[#060709]"
      onWheel={handleWheel}
      onMouseDown={handleStageMouseDown}
    >
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top,rgba(59,130,246,0.09),transparent_36%),linear-gradient(180deg,rgba(255,255,255,0.02),transparent_22%)]" />
      <div
        data-id="agent-canvas-viewport-controls"
        className="fixed right-4 top-[62px] z-[260] flex items-center gap-2 rounded-full border border-white/[0.08] bg-black/70 px-2 py-1 shadow-[0_12px_28px_rgba(0,0,0,0.35)] backdrop-blur"
      >
        <button
          type="button"
          data-id="agent-canvas-zoom-actual"
          onClick={resetZoomToActual}
          className="rounded px-2 py-1 text-xs text-zinc-300 transition-colors hover:bg-white/[0.06]"
        >
          1:1
        </button>
        <span data-id="agent-canvas-zoom" className="min-w-14 text-center text-[11px] font-mono text-zinc-500">
          {Math.round(viewport.zoom * 100)}%
        </span>
      </div>

      <div
        data-id="agent-canvas-world"
        className="absolute left-0 top-0"
        style={worldStyle}
      >
        <div
          data-id="agent-canvas-grid"
          className="absolute inset-0 opacity-[0.18]"
          style={{
            backgroundImage: 'linear-gradient(rgba(148,163,184,0.12) 1px, transparent 1px), linear-gradient(90deg, rgba(148,163,184,0.12) 1px, transparent 1px)',
            backgroundSize: '32px 32px',
            backgroundPosition: '0 0',
          }}
        />

        {items.map((item) => {
          const layout = layouts[item.paneId];
          if (!layout) return null;
          return (
            <AgentCanvasWindow
              key={item.paneId}
              item={item}
              layout={layout}
              active={activePaneId === item.paneId}
              liveMounted={mountedPaneIds.includes(item.paneId)}
              zoom={viewport.zoom}
              onFocus={() => focusPane(item.paneId)}
              onLayoutChange={(patch) => updateLayout(item.paneId, patch)}
              onOpenCodeService={onOpenCodeServicePane ? () => onOpenCodeServicePane(item.paneId, item.workspace) : undefined}
              onOpenOpenClaw={normalizeAgentType(item.agentType) === 'openclaw' ? onOpenOpenClaw : undefined}
              onRenameTitle={onRenamePaneTitle ? (title) => onRenamePaneTitle(item.paneId, title) : undefined}
              onOpenSettings={onOpenSettingsPane ? () => onOpenSettingsPane(item.paneId) : undefined}
              maskScopeId={scopeId}
            />
          );
        })}
      </div>
    </div>
  );
}

function AgentCanvasWindow({
  item,
  layout,
  active,
  liveMounted,
  zoom,
  onFocus,
  onLayoutChange,
  onOpenCodeService,
  onOpenOpenClaw,
  onRenameTitle,
  onOpenSettings,
  maskScopeId,
}: {
  item: AgentCanvasItem;
  layout: WindowLayout;
  active: boolean;
  liveMounted: boolean;
  zoom: number;
  onFocus: () => void;
  onLayoutChange: (patch: Partial<WindowLayout>) => void;
  onOpenCodeService?: () => void;
  onOpenOpenClaw?: () => void;
  onRenameTitle?: (title: string) => Promise<void>;
  onOpenSettings?: () => void;
  maskScopeId: string;
}) {
  const dragState = useRef<{ startX: number; startY: number; x: number; y: number } | null>(null);
  const resizeState = useRef<{ startX: number; startY: number; width: number; height: number } | null>(null);
  const tone = statusTone(item.status);
  const shouldRenderLiveView = !layout.minimized && !item.isApiOnly && !!item.ttydSrc && liveMounted;
  const showOverview = layout.minimized || item.isApiOnly || !shouldRenderLiveView;
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState(item.title || item.paneId);
  const [savingTitle, setSavingTitle] = useState(false);
  const [copiedPaneId, setCopiedPaneId] = useState(false);
  const titleInputRef = useRef<HTMLInputElement>(null);
  const copiedPaneTimerRef = useRef<number | null>(null);
  const titleCommitInFlightRef = useRef(false);

  useEffect(() => {
    if (!isEditingTitle) {
      setTitleDraft(item.title || item.paneId);
    }
  }, [isEditingTitle, item.paneId, item.title]);
  useEffect(() => () => {
    if (copiedPaneTimerRef.current !== null) {
      window.clearTimeout(copiedPaneTimerRef.current);
    }
  }, []);
  useDevRegister(`AgentCanvasWindow:${item.paneId}`, {
    paneId: item.paneId,
    title: item.title || item.paneId,
    isEditingTitle,
    titleDraft,
    savingTitle,
    copiedPaneId,
    active,
    liveMounted,
    shouldRenderLiveView,
  });

  useEffect(() => {
    if (!isEditingTitle) return;
    requestAnimationFrame(() => {
      titleInputRef.current?.focus();
      titleInputRef.current?.select();
    });
  }, [isEditingTitle]);

  const startDrag = useCallback((event: React.MouseEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest('button, input, [data-agent-window-no-drag="true"]')) return;
    dragState.current = {
      startX: event.clientX,
      startY: event.clientY,
      x: layout.x,
      y: layout.y,
    };
    onFocus();
    emitWebFrameMaskEvent({ action: 'start', key: `agent-canvas:${maskScopeId}:window-drag:${item.paneId}`, reason: 'window-drag' });
    lockPointer();
    const onMove = (moveEvent: MouseEvent) => {
      if (!dragState.current) return;
      onLayoutChange({
        x: Math.max(24, Math.min(WORLD_WIDTH - layout.width - 24, dragState.current.x + (moveEvent.clientX - dragState.current.startX) / zoom)),
        y: Math.max(24, Math.min(WORLD_HEIGHT - layout.height - 24, dragState.current.y + (moveEvent.clientY - dragState.current.startY) / zoom)),
      });
    };
    const onUp = () => {
      dragState.current = null;
      emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${maskScopeId}:window-drag:${item.paneId}`, reason: 'window-drag' });
      unlockPointer();
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    event.preventDefault();
  }, [item.paneId, layout.height, layout.width, layout.x, layout.y, maskScopeId, onFocus, onLayoutChange, zoom]);

  const startTitleEdit = useCallback((event?: React.SyntheticEvent) => {
    event?.preventDefault();
    event?.stopPropagation();
    if (!onRenameTitle || savingTitle) return;
    onFocus();
    setTitleDraft(item.title || item.paneId);
    setIsEditingTitle(true);
  }, [item.paneId, item.title, onFocus, onRenameTitle, savingTitle]);

  const cancelTitleEdit = useCallback(() => {
    if (savingTitle || titleCommitInFlightRef.current) return;
    setTitleDraft(item.title || item.paneId);
    setIsEditingTitle(false);
  }, [item.paneId, item.title, savingTitle]);

  const handlePaneIdCopied = useCallback(() => {
    setCopiedPaneId(true);
    if (copiedPaneTimerRef.current !== null) {
      window.clearTimeout(copiedPaneTimerRef.current);
    }
    copiedPaneTimerRef.current = window.setTimeout(() => {
      setCopiedPaneId(false);
      copiedPaneTimerRef.current = null;
    }, 1200);
  }, []);

  const copyPaneId = useCallback(async () => {
    const value = item.paneId;
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        await navigator.clipboard.writeText(value);
        handlePaneIdCopied();
        return;
      }
    } catch {}

    try {
      const textarea = document.createElement('textarea');
      textarea.value = value;
      textarea.setAttribute('readonly', 'true');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      textarea.style.pointerEvents = 'none';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      const ok = document.execCommand('copy');
      document.body.removeChild(textarea);
      if (ok) {
        handlePaneIdCopied();
        return;
      }
    } catch {}

    window.dispatchEvent(new CustomEvent('show-toast', { detail: `复制失败：${value}` }));
  }, [handlePaneIdCopied, item.paneId]);

  const commitTitleEdit = useCallback(async () => {
    if (!isEditingTitle || !onRenameTitle || savingTitle || titleCommitInFlightRef.current) return;
    const nextTitle = titleDraft.trim();
    const currentTitle = (item.title || item.paneId).trim();
    if (!nextTitle || nextTitle === currentTitle) {
      setTitleDraft(item.title || item.paneId);
      setIsEditingTitle(false);
      return;
    }
    titleCommitInFlightRef.current = true;
    setSavingTitle(true);
    try {
      await onRenameTitle(nextTitle);
      setIsEditingTitle(false);
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '错误：标题修改失败' }));
      setTitleDraft(item.title || item.paneId);
    } finally {
      titleCommitInFlightRef.current = false;
      setSavingTitle(false);
    }
  }, [isEditingTitle, item.paneId, item.title, onRenameTitle, savingTitle, titleDraft]);

  const startResize = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    resizeState.current = {
      startX: event.clientX,
      startY: event.clientY,
      width: layout.width,
      height: layout.height,
    };
    onFocus();
    emitWebFrameMaskEvent({ action: 'start', key: `agent-canvas:${maskScopeId}:window-resize:${item.paneId}`, reason: 'window-resize' });
    lockPointer();
    const onMove = (moveEvent: MouseEvent) => {
      if (!resizeState.current) return;
      const maxWidth = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, WORLD_WIDTH - layout.x - 24));
      const maxHeight = Math.max(MIN_HEIGHT, Math.min(MAX_HEIGHT, WORLD_HEIGHT - layout.y - 24));
      onLayoutChange({
        width: Math.max(MIN_WIDTH, Math.min(maxWidth, resizeState.current.width + (moveEvent.clientX - resizeState.current.startX) / zoom)),
        height: Math.max(MIN_HEIGHT, Math.min(maxHeight, resizeState.current.height + (moveEvent.clientY - resizeState.current.startY) / zoom)),
      });
    };
    const onUp = () => {
      resizeState.current = null;
      emitWebFrameMaskEvent({ action: 'end', key: `agent-canvas:${maskScopeId}:window-resize:${item.paneId}`, reason: 'window-resize' });
      unlockPointer();
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [item.paneId, layout.height, layout.width, layout.x, layout.y, maskScopeId, onFocus, onLayoutChange, zoom]);

  return (
    <div
      data-agent-canvas-window="true"
      data-id={`agent-window-${item.paneId}`}
      className={`absolute overflow-hidden rounded-2xl border bg-[#0c0d10]/92 shadow-[0_22px_70px_rgba(0,0,0,0.42)] backdrop-blur ${active ? 'border-blue-400/45 shadow-[0_28px_90px_rgba(37,99,235,0.22)]' : tone.ring}`}
      style={{
        left: layout.x,
        top: layout.y,
        width: layout.width,
        height: layout.height,
        zIndex: layout.zIndex,
      }}
      onMouseDown={(event) => {
        if (!(event.target as HTMLElement).closest('input, textarea, button, [contenteditable="true"]')) {
          blurActiveEditableElement();
        }
        onFocus();
      }}
    >
      <div
        data-id="agent-window-header"
        className="flex h-12 items-center border-b border-white/[0.07] bg-[linear-gradient(180deg,rgba(255,255,255,0.06),rgba(255,255,255,0.02))] px-3"
      >
        <div data-id="agent-window-header-left" className="flex items-center gap-3">
          <AgentWindowAvatar agentType={item.agentType} title={item.title || item.paneId} />
          <div data-id="agent-window-header-left-body">
            <div data-id="agent-window-title-row" className="group/title flex items-center gap-2">
              <div data-id="agent-window-title-wrap" className="relative min-w-0 flex-1">
                {isEditingTitle ? (
                  <input
                    ref={titleInputRef}
                    data-id="agent-window-title-input"
                    data-agent-window-no-drag="true"
                    value={titleDraft}
                    onChange={(event) => setTitleDraft(event.target.value)}
                    onMouseDown={(event) => event.stopPropagation()}
                    onDoubleClick={(event) => event.stopPropagation()}
                    onBlur={() => {
                      if (titleCommitInFlightRef.current) return;
                      const nextTitle = titleDraft.trim();
                      const currentTitle = (item.title || item.paneId).trim();
                      if (nextTitle && nextTitle !== currentTitle) {
                        void commitTitleEdit();
                        return;
                      }
                      cancelTitleEdit();
                    }}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        void commitTitleEdit();
                      } else if (event.key === 'Escape') {
                        event.preventDefault();
                        cancelTitleEdit();
                      }
                    }}
                    disabled={savingTitle}
                    className="block h-7 w-full rounded border border-blue-500/40 bg-white/[0.08] px-2 pr-7 text-sm font-medium leading-[1.75rem] text-zinc-100 outline-none"
                  />
                ) : (
                  <span
                    data-id="agent-window-title"
                    onDoubleClick={startTitleEdit}
                    className="block h-7 w-full cursor-text truncate px-2 pr-7 text-sm font-medium leading-[1.75rem] text-zinc-100"
                    title="双击重命名"
                  >
                    {item.title || item.paneId}
                  </span>
                )}
                {onRenameTitle ? (
                  <button
                    type="button"
                    data-id="agent-window-title-edit"
                    data-agent-window-no-drag="true"
                    onClick={startTitleEdit}
                    className={`absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-zinc-500 transition-opacity hover:text-zinc-200 ${isEditingTitle ? 'opacity-0 pointer-events-none' : 'opacity-0 group-hover/title:opacity-100'}`}
                    title="编辑标题"
                  >
                    <Pencil className="h-3 w-3" />
                  </button>
                ) : null}
              </div>
              {item.isPrimary && (
                <span data-id="agent-window-lead-badge" className="rounded-full border border-blue-400/20 bg-blue-500/10 px-2 py-0.5 text-[10px] uppercase tracking-[0.18em] text-blue-300">
                  lead
                </span>
              )}
            </div>
            <div data-id="agent-window-meta-row" className="-mt-[2px] flex items-center gap-2 pl-3 text-[11px] text-zinc-500">
              <div data-id="agent-window-pane-id-wrap" className="flex items-center gap-1.5">
                <span
                  data-id="agent-window-pane-id"
                  className="font-mono"
                >
                  {item.paneId}
                </span>
                <button
                  type="button"
                  data-id="agent-window-pane-id-copy"
                  data-agent-window-no-drag="true"
                  onClick={(event) => {
                    event.stopPropagation();
                    void copyPaneId();
                  }}
                  className="cursor-pointer rounded p-0.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"
                  title={copiedPaneId ? '已复制' : '复制成员 ID'}
                >
                  {copiedPaneId ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
                </button>
              </div>
              {item.machineLabel ? <span data-id="agent-window-machine-label" className="truncate">{item.machineLabel}</span> : null}
              {item.contextUsage != null ? <span data-id="agent-window-context-usage">{item.contextUsage}%</span> : null}
            </div>
          </div>
        </div>
        <div
          data-id="agent-window-drag-zone"
          className="mx-3 h-7 flex-1 cursor-grab active:cursor-grabbing"
          onMouseDown={startDrag}
          title="拖拽窗口"
        />
        <div data-id="agent-window-header-right" className="flex items-center gap-1">
          {onOpenOpenClaw ? (
            <button
              type="button"
              data-id="agent-window-openclaw"
              onClick={(event) => {
                event.stopPropagation();
                onOpenOpenClaw();
              }}
              className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
              title="OpenClaw"
            >
              <span className="text-[13px] leading-none" aria-label="OpenClaw">🦞</span>
            </button>
          ) : null}
          {!item.isApiOnly && onOpenCodeService ? (
            <button
              type="button"
              data-id="agent-window-code-service"
              onClick={(event) => {
                event.stopPropagation();
                onOpenCodeService();
              }}
              className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
              title="代码服务"
            >
              <Folder className="h-3.5 w-3.5" />
            </button>
          ) : null}
          {onOpenSettings ? (
            <button
              type="button"
              data-id="agent-window-settings"
              onClick={(event) => {
                event.stopPropagation();
                onOpenSettings();
              }}
              className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
              title="设置"
            >
              <Settings className="h-3.5 w-3.5" />
            </button>
          ) : null}
        </div>
      </div>

      <div data-id="agent-window-body" className="relative h-[calc(100%-3rem)] bg-black">
        {showOverview ? (
          <div className="absolute inset-0 flex flex-col justify-between bg-[radial-gradient(circle_at_top,rgba(59,130,246,0.12),transparent_35%),linear-gradient(180deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01))] p-4">
            <div>
              <div className="text-xs uppercase tracking-[0.24em] text-zinc-600">workspace</div>
              <div className="mt-2 truncate text-sm text-zinc-300">{item.workspace || `~/workers/${item.paneId}`}</div>
            </div>
            <div className="rounded-xl border border-white/[0.06] bg-white/[0.03] p-3">
              <div className="text-xs text-zinc-500">协作概览</div>
              <div className="mt-2 text-sm text-zinc-200">
                {item.isApiOnly
                  ? '该成员当前只支持 API 能力，不提供 ttyd 终端。'
                  : layout.minimized
                    ? '缩放到更近或展开窗口以进入实时工作现场。'
                    : '点击窗口后将按需连接实时 ttyd 工作现场。'}
              </div>
            </div>
          </div>
        ) : shouldRenderLiveView ? (
          <div className="absolute inset-0">
            <WebFrame src={item.ttydSrc} className="h-full w-full border-0 bg-black" title={`canvas-${item.paneId}`} />
          </div>
        ) : (
          <div className="absolute inset-0 flex items-center justify-center text-sm text-zinc-500">
            暂无可用工作现场
          </div>
        )}
        <button
          type="button"
          data-id="agent-window-resize-handle"
          data-agent-window-no-drag="true"
          onMouseDown={startResize}
          className="absolute bottom-2 right-2 z-30 flex h-4 w-4 cursor-se-resize items-end justify-end text-zinc-600/70 transition-colors hover:text-zinc-300"
          title="调整窗口大小"
        >
          <svg
            viewBox="0 0 16 16"
            className="pointer-events-none h-3 w-3 opacity-90"
            aria-hidden="true"
          >
            <path
              d="M6.5 12L12 6.5M9 12L12 9M11 12L12 11"
              fill="none"
              stroke="currentColor"
              strokeWidth="1"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>

    </div>
  );
}
