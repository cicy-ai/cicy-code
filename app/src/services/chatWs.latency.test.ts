import { describe, it, expect } from 'vitest';
import { nextEventLoopLag, debiasLatency } from './chatWs';

describe('network-signal latency de-biasing', () => {
  describe('debiasLatency', () => {
    it('passes a clean RTT through untouched when the loop is idle', () => {
      // Real network ~2ms, no main-thread jank → no subtraction.
      expect(debiasLatency(2, 0)).toBe(2);
      expect(debiasLatency(2, 5)).toBe(2); // sub-threshold jitter ignored
    });

    it('does NOT hide a genuinely slow network (no jank)', () => {
      // 800ms network, idle loop → stays 800 (real problem still surfaces).
      expect(debiasLatency(800, 0)).toBe(800);
    });

    it('strips main-thread jank: a 3000ms reading under heavy lag → ~network', () => {
      // The user's case: pong callback ran ~2900ms late while the network was
      // ~1ms; tracker measured ~2900ms of event-loop delay.
      const raw = 2901;     // performance.now() - pingSentAt, inflated by jank
      const elLag = 2900;   // tracked event-loop delay over the in-flight window
      expect(debiasLatency(raw, elLag)).toBe(1);
    });

    it('never returns negative if lag slightly over-estimates', () => {
      expect(debiasLatency(50, 200)).toBe(0);
    });
  });

  describe('nextEventLoopLag (EWMA tracker)', () => {
    it('rises toward sustained drift and ignores negative drift', () => {
      let lag = 0;
      // Sustained ~1000ms-per-tick stalls drive the estimate up.
      for (let i = 0; i < 10; i++) lag = nextEventLoopLag(lag, 1000);
      expect(lag).toBeGreaterThan(900);

      // Quiet ticks (early/negative drift) decay it back toward 0.
      for (let i = 0; i < 20; i++) lag = nextEventLoopLag(lag, -3);
      expect(lag).toBeLessThan(5);
    });

    it('a single brief spike does not fully swing the estimate (EWMA smooths)', () => {
      const lag = nextEventLoopLag(0, 1000);
      expect(lag).toBeLessThan(1000); // one spike only partially counts
      expect(lag).toBeGreaterThan(0);
    });
  });
});
