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
  | { kind: 'poll' };

export function interpretCicyUpdateResponse(
  data: CicyUpdateResponse | null | undefined,
  fallbackError: string,
): CicyUpdateOutcome {
  if (!data?.started) {
    const message = String(data?.error || '').trim() || fallbackError;
    return { kind: 'failed', message };
  }
  if (data.completed && data.restart_required) {
    return { kind: 'restart-required', target: String(data.target || '').trim() };
  }
  return { kind: 'poll' };
}
