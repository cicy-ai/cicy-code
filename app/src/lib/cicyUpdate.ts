// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export type CicyUpdateResponse = {
  started?: boolean;
  completed?: boolean;
  restart_required?: boolean;
  target?: string;
  error?: string;
};

export type CicyUpdateOutcome =
  | { kind: 'failed'; message: string }
  | { kind: 'restart-required'; target: string }
  | { kind: 'poll'; target: string };

export function interpretCicyUpdateResponse(
  data: CicyUpdateResponse | null | undefined,
  fallbackError: string,
): CicyUpdateOutcome {
  if (!data?.started) {
    const message = String(data?.error || '').trim() || fallbackError;
    return { kind: 'failed', message };
  }
  const target = String(data.target || '').trim();
  if (!target) {
    return { kind: 'failed', message: fallbackError };
  }
  if (data.completed && data.restart_required) {
    return { kind: 'restart-required', target };
  }
  return { kind: 'poll', target };
}

/**
 * Whether an auto-update should fire now.
 *
 * The badge stays lit while an install is pending, so a failed auto-update
 * would otherwise re-arm the instant `updating` clears and reinstall in a tight
 * loop. Gating on the target version means each published release is attempted
 * at most once and the manual update button remains the retry path.
 */
export function shouldAutoUpdate(state: {
  enabled: boolean;
  hasUpdate: boolean;
  updating: boolean;
  target: string;
  attempted: string;
}): boolean {
  if (!state.enabled || !state.hasUpdate || state.updating) return false;
  const target = String(state.target || '').trim();
  return target !== '' && target !== state.attempted;
}
