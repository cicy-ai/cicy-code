// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export type AgentSendTargetSource = 'none' | 'team' | 'project';

export interface AgentSendTarget {
  source: AgentSendTargetSource;
  paneId: string;
}

const EMPTY_TARGET: AgentSendTarget = { source: 'none', paneId: '' };
let currentTarget: AgentSendTarget = EMPTY_TARGET;
const listeners = new Set<() => void>();

export function getAgentSendTarget(): AgentSendTarget {
  return currentTarget;
}

export function setAgentSendTarget(target: AgentSendTarget): void {
  const paneId = String(target?.paneId || '').trim();
  const source = target?.source;
  const next = paneId && (source === 'team' || source === 'project')
    ? { source, paneId }
    : EMPTY_TARGET;
  if (currentTarget.source === next.source && currentTarget.paneId === next.paneId) return;
  currentTarget = next;
  listeners.forEach((listener) => listener());
}

export function clearAgentSendTarget(source?: Exclude<AgentSendTargetSource, 'none'>): void {
  if (source && currentTarget.source !== source) return;
  setAgentSendTarget(EMPTY_TARGET);
}

export function subscribeAgentSendTarget(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
