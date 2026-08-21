import { describe, expect, it } from 'vitest';
import { dispatcherBusySignalFromStatus, nextDispatcherBusy } from './dispatcherBusy';

describe('nextDispatcherBusy', () => {
  it('enters busy from the shared history poll', () => {
    expect(nextDispatcherBusy(false, { busy: true })).toBe(true);
  });

  it('only clears busy for an authoritative terminal snapshot', () => {
    expect(nextDispatcherBusy(true, { busy: false })).toBe(true);
    expect(nextDispatcherBusy(true, { busy: false, terminal: true })).toBe(false);
  });
});

describe('dispatcherBusySignalFromStatus', () => {
  it('releases the queue on authoritative idle states', () => {
    expect(dispatcherBusySignalFromStatus('completed')).toEqual({ busy: false, terminal: true });
    expect(dispatcherBusySignalFromStatus('idle')).toEqual({ busy: false, terminal: true });
  });

  it('locks the queue on authoritative working states', () => {
    expect(dispatcherBusySignalFromStatus('tool_use')).toEqual({ busy: true, terminal: false });
  });

  it('ignores unknown states', () => {
    expect(dispatcherBusySignalFromStatus('')).toBeNull();
  });
});
