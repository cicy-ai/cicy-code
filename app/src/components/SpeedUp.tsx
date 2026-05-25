import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { detectHost, type HostInfo } from '../lib/speedup/detect';
import { CN_MIRRORS, categoriesFor, type Category } from '../lib/speedup/mirrors';
import { probeAll, formatSpeed, type ProbeResult } from '../lib/speedup/probe';
import { buildPlan, readPersisted, writePersisted, type ApplyStep, type Env } from '../lib/speedup/apply';
import { Spinner } from './ui/Spinner';

type Phase = 'idle' | 'detecting' | 'probing' | 'ready' | 'applying' | 'done' | 'error';

interface CategoryReport {
  category: Category;
  results: ProbeResult[];
  pickId: string;
}

interface StepLog {
  step: ApplyStep;
  state: 'pending' | 'running' | 'ok' | 'fail';
  message?: string;
}

export default function SpeedUp() {
  const { t } = useTranslation('speedUp');
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<string | null>(null);
  const [host, setHost] = useState<HostInfo | null>(null);
  const [reports, setReports] = useState<CategoryReport[]>([]);
  const [steps, setSteps] = useState<StepLog[]>([]);
  const [skipReason, setSkipReason] = useState<string | null>(null);

  // On first mount, check persisted config; if recent + same OS, show a
  // "previously configured" panel and skip probing unless user clicks
  // re-detect. Otherwise auto-start detection.
  useEffect(() => { (async () => {
    const persisted = await readPersisted();
    if (persisted && Date.now() - new Date(persisted.ts).getTime() < 7 * 24 * 60 * 60 * 1000) {
      setSkipReason(t('alreadyConfigured', {
        date: new Date(persisted.ts).toLocaleString(),
        os: persisted.os,
        region: persisted.region,
      }));
      return;
    }
    runDetect();
  })(); }, []);

  const env: Env | null = useMemo(() => {
    if (!host) return null;
    const wslDistro = host.wsl.installed ? host.wsl.distros[0] : undefined;
    return { os: host.os, wslDistro };
  }, [host]);

  async function runDetect() {
    setPhase('detecting');
    setError(null);
    try {
      const h = await detectHost();
      setHost(h);
      await runProbe(h);
    } catch (e: any) {
      setError(e.message || String(e));
      setPhase('error');
    }
  }

  async function runProbe(h: HostInfo) {
    setPhase('probing');
    try {
      const cats = categoriesFor(h.os, h.wsl.installed);
      const out: CategoryReport[] = [];
      // Probe categories sequentially so the global parallelism stays sane
      // on slow connections; within a category we parallelize candidates.
      for (const cat of cats) {
        const candidates = CN_MIRRORS[cat];
        const results = await probeAll(candidates, 5);
        const w = results.find(r => r.ok);
        out.push({ category: cat, results, pickId: w?.id || candidates[0].id });
        // incremental render so user sees progress
        setReports([...out]);
      }
      setPhase('ready');
    } catch (e: any) {
      setError(e.message || String(e));
      setPhase('error');
    }
  }

  function setPick(category: Category, pickId: string) {
    setReports(prev => prev.map(r => r.category === category ? { ...r, pickId } : r));
  }

  async function runApply() {
    if (!env || !host) return;
    setPhase('applying');
    const picks: Partial<Record<Category, string>> = {};
    reports.forEach(r => { picks[r.category] = r.pickId; });
    const plan = buildPlan(env, picks);
    const logs: StepLog[] = plan.map(s => ({ step: s, state: 'pending' }));
    setSteps(logs);
    for (let i = 0; i < plan.length; i++) {
      logs[i] = { ...logs[i], state: 'running' };
      setSteps([...logs]);
      try {
        const { ok, message } = await plan[i].run();
        logs[i] = { ...logs[i], state: ok ? 'ok' : 'fail', message };
      } catch (e: any) {
        logs[i] = { ...logs[i], state: 'fail', message: e.message || String(e) };
      }
      setSteps([...logs]);
    }
    await writePersisted({ os: host.os, region: host.region, picks, ts: new Date().toISOString(), v: 1 });
    setPhase('done');
  }

  // ---------- render ----------

  return (
    <div data-id="speedup" className="max-w-3xl mx-auto p-6 text-zinc-200">
      <h2 className="text-lg font-semibold mb-1">{t('title')}</h2>
      <p className="text-sm text-zinc-500 mb-4">
        {t('description')}
      </p>

      {skipReason && phase === 'idle' && (
        <div data-id="speedup-skip" className="rounded border border-zinc-700 bg-zinc-900 p-3 mb-4 text-sm">
          <div className="text-zinc-300">{skipReason}</div>
          <button className="mt-2 text-xs text-blue-400 hover:underline" onClick={runDetect}>{t('reDetect')}</button>
        </div>
      )}

      {phase === 'detecting' && (
        <div className="flex items-center gap-2 text-sm"><Spinner size="sm" /> {t('detectingHost')}</div>
      )}

      {host && (
        <div data-id="speedup-host" className="rounded border border-zinc-800 bg-zinc-900/50 p-3 mb-4 text-xs grid grid-cols-3 gap-2">
          <div><span className="text-zinc-500">{t('fieldOs')}</span><div>{host.os}</div></div>
          <div><span className="text-zinc-500">{t('fieldRegion')}</span><div>{host.region}</div></div>
          <div><span className="text-zinc-500">{t('fieldWsl')}</span><div>{host.wsl.installed ? host.wsl.distros.join(', ') : t('wslNotInstalled')}</div></div>
        </div>
      )}

      {phase === 'probing' && (
        <div className="flex items-center gap-2 text-sm mb-3"><Spinner size="sm" /> {t('probingMirrors')}</div>
      )}

      {reports.length > 0 && (
        <div data-id="speedup-reports" className="space-y-3 mb-4">
          {reports.map(r => (
            <div key={r.category} className="rounded border border-zinc-800 bg-zinc-900/50 p-3">
              <div className="text-sm font-medium mb-2">{t(`category.${r.category}`)}</div>
              <div className="space-y-1">
                {r.results.map(res => (
                  <label key={res.id} className="flex items-center gap-2 text-xs cursor-pointer">
                    <input
                      type="radio"
                      name={`pick-${r.category}`}
                      checked={r.pickId === res.id}
                      onChange={() => setPick(r.category, res.id)}
                    />
                    <span className={res.ok ? 'text-zinc-200' : 'text-zinc-500 line-through'}>{res.label}</span>
                    <span className="ml-auto tabular-nums text-zinc-400">{formatSpeed(res.bytesPerSec)}</span>
                    {!res.ok && <span className="text-rose-500">HTTP {res.httpCode || '—'}</span>}
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {phase === 'ready' && (
        <button
          data-id="speedup-apply"
          className="px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 text-white text-sm disabled:opacity-50"
          onClick={runApply}
        >{t('applyButton')}</button>
      )}

      {(phase === 'applying' || phase === 'done') && (
        <div data-id="speedup-steps" className="mt-4 space-y-2">
          {steps.map(s => (
            <div key={s.step.id} className="rounded border border-zinc-800 bg-zinc-900/50 p-2 text-xs flex items-start gap-2">
              <div className="w-4 flex-shrink-0 mt-0.5">
                {s.state === 'running' && <Spinner size="xs" />}
                {s.state === 'ok' && <span className="text-emerald-400">✓</span>}
                {s.state === 'fail' && <span className="text-rose-500">✗</span>}
                {s.state === 'pending' && <span className="text-zinc-600">·</span>}
              </div>
              <div className="flex-1">
                <div>{s.step.label}</div>
                {s.message && <pre className="mt-1 text-[10px] text-zinc-500 whitespace-pre-wrap">{s.message}</pre>}
              </div>
            </div>
          ))}
        </div>
      )}

      {phase === 'done' && (
        <div className="mt-4 text-sm text-emerald-400">{t('doneMessage')}</div>
      )}

      {phase === 'error' && error && (
        <div data-id="speedup-error" className="rounded border border-rose-700 bg-rose-900/30 p-3 text-sm">
          <div className="text-rose-300">{t('failed', { error })}</div>
          <button className="mt-2 text-xs text-blue-400 hover:underline" onClick={runDetect}>{t('retry')}</button>
        </div>
      )}
    </div>
  );
}
