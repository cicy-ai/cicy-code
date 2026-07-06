// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Global dev store — all context state registered here for inspection/mutation
type Listener = () => void;
type Setter = (path: string, value: any) => void;

interface StoreEntry {
  state: Record<string, any>;
  setter?: Setter;
}

let stores: Record<string, StoreEntry> = {};
let listeners: Set<Listener> = new Set();
let snapshot = { ...stores };

function isPlainObject(value: any): value is Record<string, any> {
  if (!value || typeof value !== 'object') return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function isEqual(a: any, b: any, seen = new WeakMap<object, object>()): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a == null || b == null) return false;
  if (typeof a !== 'object') return false;

  if (seen.get(a) === b) return true;
  seen.set(a, b);

  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i += 1) {
      if (!isEqual(a[i], b[i], seen)) return false;
    }
    return true;
  }

  if (!isPlainObject(a) || !isPlainObject(b)) return false;

  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;

  for (const key of aKeys) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!isEqual(a[key], b[key], seen)) return false;
  }

  return true;
}

function notify() {
  snapshot = { ...stores };
  listeners.forEach(fn => fn());
}

export const devStore = {
  register(name: string, state: Record<string, any>, setter?: Setter) {
    const prev = stores[name];
    const sameState = !!prev && isEqual(prev.state, state);
    const sameSetter = prev?.setter === setter;

    if (prev && sameState && sameSetter) return;

    stores[name] = { state, setter };
    if (!prev || !sameState) notify();
  },
  unregister(name: string) {
    if (!stores[name]) return;
    delete stores[name];
    notify();
  },
  getSnapshot: () => snapshot,
  subscribe(fn: Listener) {
    listeners.add(fn);
    return () => { listeners.delete(fn); };
  },
  set(storeName: string, path: string, value: any) {
    stores[storeName]?.setter?.(path, value);
  },
};

if (typeof globalThis !== 'undefined') {
  (globalThis as any).devStore = devStore;
}

// Hook: register context state into devStore
import { useEffect, useSyncExternalStore } from 'react';

export function useDevRegister(name: string, state: Record<string, any>, setters?: Record<string, (v: any) => void>) {
  useEffect(() => {
    const setter: Setter | undefined = setters ? (path, value) => {
      setters[path]?.(value);
    } : undefined;
    devStore.register(name, state, setter);
  });
  useEffect(() => () => devStore.unregister(name), [name]);
}

export function useDevStore() {
  return useSyncExternalStore(devStore.subscribe, devStore.getSnapshot);
}
