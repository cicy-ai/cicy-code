import { execShell } from './rpc';
import type { MirrorCandidate } from './mirrors';

export interface ProbeResult {
  id: string;
  label: string;
  ok: boolean;
  bytesPerSec: number;   // peak download speed during probe; 0 on failure
  timeSec: number;
  httpCode: number;
  error?: string;
}

// Returns the curl command used to probe one URL. We Range-GET up to 1 MB and
// cap the whole probe at `timeoutSec`. -w prints a stable, easy-to-parse line.
// `curl.exe` ships with Windows 10/11 so this works cross-platform from the
// host shell (no WSL required for the probe itself).
function probeCmd(url: string, timeoutSec: number): string {
  const fmt = 'PROBE %{http_code} %{speed_download} %{time_total}';
  // -L follow redirects, -s silent, -o discard body, --range 0-1048575,
  // --max-time hard wall clock cap.
  return `curl -L -s -o /dev/null -w "${fmt}" --max-time ${timeoutSec} -r 0-1048575 ${JSON.stringify(url)} 2>/dev/null || echo "PROBE 000 0 0"`;
}

function parseProbe(out: string): { httpCode: number; bytesPerSec: number; timeSec: number } {
  const m = out.match(/PROBE\s+(\d+)\s+([\d.]+)\s+([\d.]+)/);
  if (!m) return { httpCode: 0, bytesPerSec: 0, timeSec: 0 };
  return { httpCode: parseInt(m[1], 10), bytesPerSec: parseFloat(m[2]), timeSec: parseFloat(m[3]) };
}

export async function probeOne(c: MirrorCandidate, timeoutSec = 5): Promise<ProbeResult> {
  try {
    const out = await execShell(probeCmd(c.probeUrl, timeoutSec));
    const { httpCode, bytesPerSec, timeSec } = parseProbe(out);
    const ok = httpCode >= 200 && httpCode < 400 && bytesPerSec > 0;
    return { id: c.id, label: c.label, ok, bytesPerSec, timeSec, httpCode };
  } catch (err: any) {
    return { id: c.id, label: c.label, ok: false, bytesPerSec: 0, timeSec: 0, httpCode: 0, error: err.message || String(err) };
  }
}

// Probes all candidates in parallel via a single shell invocation. We pack
// every probe into one bash -lc so we pay the electronRPC round-trip cost
// once instead of N times. Ranked best-to-worst.
export async function probeAll(candidates: MirrorCandidate[], timeoutSec = 5): Promise<ProbeResult[]> {
  const parts = candidates.map((c, i) => `(echo IDX:${i}; ${probeCmd(c.probeUrl, timeoutSec)}; echo) &`);
  const script = `${parts.join('\n')}\nwait`;
  const out = await execShell(`bash -lc ${JSON.stringify(script)}`);
  const blocks = out.split(/IDX:(\d+)/).slice(1);
  const results: ProbeResult[] = candidates.map(c => ({
    id: c.id, label: c.label, ok: false, bytesPerSec: 0, timeSec: 0, httpCode: 0,
  }));
  for (let i = 0; i < blocks.length; i += 2) {
    const idx = parseInt(blocks[i], 10);
    const body = blocks[i + 1] || '';
    if (Number.isNaN(idx) || idx < 0 || idx >= candidates.length) continue;
    const { httpCode, bytesPerSec, timeSec } = parseProbe(body);
    const ok = httpCode >= 200 && httpCode < 400 && bytesPerSec > 0;
    results[idx] = { id: candidates[idx].id, label: candidates[idx].label, ok, bytesPerSec, timeSec, httpCode };
  }
  return results.slice().sort((a, b) => Number(b.ok) - Number(a.ok) || b.bytesPerSec - a.bytesPerSec);
}

// Convenience: pick the winner (or null if all candidates failed).
export function winner(results: ProbeResult[]): ProbeResult | null {
  const ok = results.find(r => r.ok);
  return ok || null;
}

export function formatSpeed(bytesPerSec: number): string {
  if (!bytesPerSec) return '—';
  if (bytesPerSec >= 1024 * 1024) return `${(bytesPerSec / 1024 / 1024).toFixed(2)} MB/s`;
  if (bytesPerSec >= 1024) return `${(bytesPerSec / 1024).toFixed(0)} KB/s`;
  return `${bytesPerSec.toFixed(0)} B/s`;
}
