// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Global drag/resize lock — prevents iframes from stealing pointer events
let count = 0;
const listeners = new Set<(v: boolean) => void>();

function notifyPointerLock(locked: boolean) {
  listeners.forEach(fn => fn(locked));
}

export function lockPointer() {
  if (++count === 1) notifyPointerLock(true);
}

export function unlockPointer() {
  if (--count <= 0) { count = 0; notifyPointerLock(false); }
}

export function clearPointerLock() {
  if (count === 0) return;
  count = 0;
  notifyPointerLock(false);
}

function onPointerLockChange(fn: (locked: boolean) => void) {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}

import { useState, useEffect } from 'react';
import { devStore } from './devStore';

// Register to devStore
onPointerLockChange(locked => {
  devStore.register('pointerLock', { locked, refCount: count });
});
devStore.register('pointerLock', { locked: false, refCount: 0 });

export function usePointerLock() {
  const [locked, setLocked] = useState(false);
  useEffect(() => onPointerLockChange(setLocked), []);
  return locked;
}

// Auto-detect: lock when dragging react-resizable-panels separators
if (typeof window !== 'undefined') {
  let separatorLocked = false;
  const forceRelease = () => {
    separatorLocked = false;
    clearPointerLock();
  };
  window.addEventListener('pointerdown', (e) => {
    if ((e.target as HTMLElement)?.closest?.('[role="separator"]')) {
      lockPointer();
      separatorLocked = true;
    }
  }, true);
  window.addEventListener('pointerup', () => {
    if (separatorLocked) { unlockPointer(); separatorLocked = false; }
  }, true);
  window.addEventListener('blur', forceRelease);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') forceRelease();
  });
}
