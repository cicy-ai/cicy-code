export type DispatcherBusySignal = { busy?: boolean; terminal?: boolean };

export function nextDispatcherBusy(current: boolean, signal: DispatcherBusySignal): boolean {
  if (signal.busy) return true;
  if (signal.terminal) return false;
  return current;
}

export function dispatcherBusySignalFromStatus(status: unknown): DispatcherBusySignal | null {
  const value = String(status || '').trim().toLowerCase();
  if (/^(completed|complete|done|idle|aborted|error|canceled|cancelled|failed|stopped)$/.test(value)) {
    return { busy: false, terminal: true };
  }
  if (/^(thinking|working|running|streaming|pending|tool_use|tool_call|in_progress)$/.test(value)) {
    return { busy: true, terminal: false };
  }
  return null;
}
