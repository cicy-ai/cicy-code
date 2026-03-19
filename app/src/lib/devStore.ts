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

function notify() {
  snapshot = { ...stores };
  listeners.forEach(fn => fn());
}

export const devStore = {
  register(name: string, state: Record<string, any>, setter?: Setter) {
    stores[name] = { state, setter };
    notify();
  },
  unregister(name: string) {
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
