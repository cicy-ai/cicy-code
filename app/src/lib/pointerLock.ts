// Global drag/resize lock — prevents iframes from stealing pointer events
let count = 0;
const listeners = new Set<(v: boolean) => void>();

export function lockPointer() {
  if (++count === 1) listeners.forEach(fn => fn(true));
}

export function unlockPointer() {
  if (--count <= 0) { count = 0; listeners.forEach(fn => fn(false)); }
}

export function onPointerLockChange(fn: (locked: boolean) => void) {
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
  window.addEventListener('pointerdown', (e) => {
    if ((e.target as HTMLElement)?.closest?.('[role="separator"]')) {
      lockPointer();
      separatorLocked = true;
    }
  }, true);
  window.addEventListener('pointerup', () => {
    if (separatorLocked) { unlockPointer(); separatorLocked = false; }
  }, true);
}
