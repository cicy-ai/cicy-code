import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The panel pulls in the whole settings tree otherwise; only the API surface
// openInstanceHost actually touches needs to be real.
vi.mock('../../services/api', () => ({
  default: {
    openCiCyCloudInstance: vi.fn(async () => ({ data: { url: 'https://signed.example/?token=abc' } })),
  },
}));

import apiService from '../../services/api';
import { openInstanceHost } from './CloudAccountPanel';

const inst = (over: Record<string, any> = {}) => ({
  instanceId: 'i-1',
  proxyHost: 'xs-1001.hub.cicy-ai.com',
  hub: true,
  ...over,
}) as any;

describe('openInstanceHost', () => {
  let rpc: ReturnType<typeof vi.fn>;
  let openSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.clearAllMocks();
    openSpy = vi.fn(() => ({ closed: false, opener: {}, location: { href: '' } }));
    vi.stubGlobal('open', openSpy);
  });
  afterEach(() => {
    delete (window as any).electronRPC;
    vi.unstubAllGlobals();
  });

  const asDesktop = () => {
    rpc = vi.fn(async (tool: string) => {
      if (tool === 'electron_tabs') return JSON.stringify({ accountIdx: 0, tabs: [] });
      return JSON.stringify({ success: true });
    });
    (window as any).electronRPC = rpc;
  };

  it('opens in electron profile 0, in the foreground', async () => {
    asDesktop();
    await openInstanceHost(inst());

    const openCall = rpc.mock.calls.find(([tool]) => tool === 'electron_tab_open');
    expect(openCall, 'electron_tab_open should have been called').toBeTruthy();
    // accountIdx 0 is the home profile. A `||` fallback anywhere on this path
    // would coerce 0 to another profile — the exact bug this guards.
    expect(openCall![1]).toMatchObject({
      accountIdx: 0,
      url: 'https://signed.example/?token=abc',
      activate: true,
    });
    expect(rpc.mock.calls.find(([tool]) => tool === 'electron_tabs')![1]).toMatchObject({ accountIdx: 0 });
  });

  it('never opens a stray blank tab inside cicy-desktop', async () => {
    asDesktop();
    await openInstanceHost(inst());
    // The old code called window.open('about:blank') synchronously to dodge the
    // popup blocker and steered it after the async resolve, leaving an empty
    // tab in the strip. The desktop has no popup blocker, so nothing is opened.
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('re-uses an existing tab for the same url instead of opening another', async () => {
    rpc = vi.fn(async (tool: string) => {
      if (tool === 'electron_tabs') {
        return JSON.stringify({
          accountIdx: 0,
          tabs: [{ webContentsId: 42, url: 'https://signed.example/?token=abc' }],
        });
      }
      return JSON.stringify({ success: true });
    });
    (window as any).electronRPC = rpc;

    await openInstanceHost(inst());
    expect(rpc.mock.calls.find(([tool]) => tool === 'electron_tab_activate')![1]).toMatchObject({ webContentsId: 42 });
    expect(rpc.mock.calls.some(([tool]) => tool === 'electron_tab_open')).toBe(false);
  });

  it('falls back to the plain-browser path when not in cicy-desktop', async () => {
    await openInstanceHost(inst());
    // Synchronous about:blank first (user gesture for the popup blocker), then
    // steered to the resolved url — no second window.open.
    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(openSpy).toHaveBeenCalledWith('about:blank', '_blank');
    const tab = openSpy.mock.results[0].value;
    expect(tab.location.href).toBe('https://signed.example/?token=abc');
  });

  it('uses the port sub-domain and skips the hub round-trip for non-hub nodes', async () => {
    asDesktop();
    await openInstanceHost(inst({ hub: false }), 8080);
    expect(apiService.openCiCyCloudInstance).not.toHaveBeenCalled();
    expect(rpc.mock.calls.find(([tool]) => tool === 'electron_tab_open')![1]).toMatchObject({
      accountIdx: 0,
      url: 'https://xs-1001-8080.hub.cicy-ai.com',
    });
  });
});
