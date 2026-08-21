// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export function mergeAgentStatusPush(
  statuses: Record<string, any>,
  payload: Record<string, any>,
  fallbackAgentId = '',
): Record<string, any> {
  const status = String(payload?.status || '').trim();
  const shortId = String(payload?.agent_id || fallbackAgentId || '').trim().split(':')[0];
  if (!status || !shortId) return statuses;

  const fullId = `${shortId}:main.0`;
  const previous = statuses[fullId] || statuses[shortId] || {};
  if (previous.status === status && previous.updated_at === payload.updated_at) return statuses;

  return {
    ...statuses,
    [fullId]: {
      ...previous,
      ...payload,
      status,
    },
  };
}
