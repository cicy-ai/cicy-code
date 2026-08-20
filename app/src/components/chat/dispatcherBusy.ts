export type DispatcherBusySignal = { busy?: boolean; terminal?: boolean };

export function nextDispatcherBusy(current: boolean, signal: DispatcherBusySignal): boolean {
  if (signal.busy) return true;
  if (signal.terminal) return false;
  return current;
}
