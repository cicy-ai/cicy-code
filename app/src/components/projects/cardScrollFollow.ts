// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

/**
 * Only a user-driven scroll may replace the remembered sentinel visibility.
 * Layout, reconciliation and our own scrollTop writes preserve the value that
 * was sampled before content growth.
 */
export function resolveCardScrollFollow(
  currentFollow: boolean,
  loadingVisible: boolean,
  userDriven: boolean,
): boolean {
  return userDriven ? loadingVisible : currentFollow;
}
