// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { execShellBackground, tailLog, isLogDone } from '../lib/speedup/rpc';
import { ROOTFS_MIRRORS, GH_PROXIES } from '../lib/speedup/mirrors';
import { probeAll, formatSpeed, type ProbeResult } from '../lib/speedup/probe';
import { Spinner } from './ui/Spinner';

type Phase = 'idle' | 'probing' | 'ready' | 'downloading' | 'importing' | 'done' | 'error';

// First-time WSL install for CN Windows users where `wsl --install -d Ubuntu`
// fails (raw.githubusercontent.com is unreachable). Strategy: download an
// Ubuntu noble rootfs tarball from a CN mirror and `wsl --import` it. This is
// the exact recipe we proved out in the session that preceded this commit.
export default function WSLInstall() {
  const { t } = useTranslation('wslInstall');
  const [phase, setPhase] = useState<Phase>('idle');
  const [rootfs, setRootfs] = useState<ProbeResult[]>([]);
  const [ghproxy, setGhproxy] = useState<ProbeResult[]>([]);
  const [rootfsPick, setRootfsPick] = useState<string>('');
  const [ghProxyPick, setGhProxyPick] = useState<string>('');
  const [log, setLog] = useState<string>('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => { runProbe(); }, []);

  async function runProbe() {
    setPhase('probing');
    try {
      const [r, g] = await Promise.all([
        probeAll(ROOTFS_MIRRORS, 6),
        probeAll(GH_PROXIES, 5),
      ]);
      setRootfs(r); setGhproxy(g);
      setRootfsPick((r.find(x => x.ok) || r[0]).id);
      setGhProxyPick((g.find(x => x.ok) || g[0]).id);
      setPhase('ready');
    } catch (e: any) {
      setError(e.message || String(e));
      setPhase('error');
    }
  }

  async function runInstall() {
    setError(null);
    const rootfsURL = ROOTFS_MIRRORS.find(m => m.id === rootfsPick)?.config.url!;
    const ghPrefix  = GH_PROXIES.find(m => m.id === ghProxyPick)?.config.prefix!;
    setPhase('downloading');
    try {
      const target = 'C:\\WSL\\Ubuntu';
      const tar = `${target}\\rootfs.tar.gz`;
      // Step 1: create install dir + download rootfs via curl. Long, so go
      // background and poll the log; status pane updates from tail.
      const prep = [
        `if not exist ${target} mkdir ${target}`,
        `curl -L -o ${tar} ${JSON.stringify(rootfsURL)}`,
      ].join(' && ');
      const logPath = await execShellBackground(prep, 'wsl-rootfs');
      await pollUntilDone(logPath, 'downloading');

      // Step 2: wsl --import. Fast but we still background it because of the
      // 30s RPC ceiling.
      setPhase('importing');
      const importLog = await execShellBackground(`wsl --import Ubuntu ${target} ${tar} --version 2`, 'wsl-import');
      await pollUntilDone(importLog, 'importing');

      // Step 3: download cicy-code via gh proxy, inside the new distro.
      // The shell helper writes ghPrefix to ~/.cicy/gh_proxy too.
      const ccScript = `set -e
mkdir -p "$HOME/.cicy"
printf '%s' '${ghPrefix.replace(/'/g, "'\\''")}' > "$HOME/.cicy/gh_proxy"
URL="${ghPrefix}https://github.com/cicy-ai/cicy-code/releases/latest/download/cicy-code-linux-amd64"
curl -L -o "$HOME/.cicy/cicy-code" "$URL"
chmod +x "$HOME/.cicy/cicy-code"
"$HOME/.cicy/cicy-code" --version || true`;
      const b64 = btoa(unescape(encodeURIComponent(ccScript)));
      const cicyLog = await execShellBackground(
        `wsl -d Ubuntu -- bash -lc "echo ${b64} | base64 -d | bash"`,
        'cicy-code-download',
      );
      await pollUntilDone(cicyLog, 'importing');

      setPhase('done');
    } catch (e: any) {
      setError(e.message || String(e));
      setPhase('error');
    }
  }

  async function pollUntilDone(logPath: string, _label: Phase) {
    for (let i = 0; i < 3600; i++) {       // up to ~30 min
      await new Promise(r => setTimeout(r, 2000));
      const content = await tailLog(logPath, 80);
      setLog(content);
      const { done, exitCode } = isLogDone(content);
      if (done) {
        if (exitCode !== 0) throw new Error(t('stepFailed', { code: exitCode, tail: content }));
        return;
      }
    }
    throw new Error(t('stepTimeout'));
  }

  return (
    <div data-id="wsl-install" className="max-w-3xl mx-auto p-6 text-zinc-200">
      <h2 className="text-lg font-semibold mb-1">{t('title')}</h2>
      <p className="text-sm text-zinc-500 mb-4">
        <Trans
          t={t}
          i18nKey="description"
          components={[<code key="cmd1" />, <code key="cmd2" />]}
        />
      </p>

      {phase === 'probing' && (
        <div className="flex items-center gap-2 text-sm"><Spinner size="sm" /> {t('probingMirrors')}</div>
      )}

      {rootfs.length > 0 && (
        <Section title={t('rootfsMirrorTitle')} results={rootfs} pick={rootfsPick} setPick={setRootfsPick} />
      )}
      {ghproxy.length > 0 && (
        <Section title={t('ghProxyTitle')} results={ghproxy} pick={ghProxyPick} setPick={setGhProxyPick} />
      )}

      {phase === 'ready' && (
        <button
          className="mt-2 px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 text-white text-sm"
          onClick={runInstall}
        >{t('installButton')}</button>
      )}

      {(phase === 'downloading' || phase === 'importing') && (
        <div className="mt-4 text-xs">
          <div className="flex items-center gap-2 mb-2">
            <Spinner size="sm" />
            <span>{phase === 'downloading' ? t('downloadingRootfs') : t('importingWsl')}</span>
          </div>
          <pre className="rounded border border-zinc-800 bg-black/50 p-2 max-h-64 overflow-auto whitespace-pre-wrap text-zinc-400">{log || '…'}</pre>
        </div>
      )}

      {phase === 'done' && (
        <div className="mt-4 text-sm text-emerald-400">
          <Trans
            t={t}
            i18nKey="doneMessage"
            components={[<code key="cmd" />]}
          />
        </div>
      )}

      {phase === 'error' && error && (
        <div className="mt-4 rounded border border-rose-700 bg-rose-900/30 p-3 text-sm">
          <div className="text-rose-300">{error}</div>
          <button className="mt-2 text-xs text-blue-400 hover:underline" onClick={runProbe}>{t('reProbe')}</button>
        </div>
      )}
    </div>
  );
}

function Section({ title, results, pick, setPick }: { title: string; results: ProbeResult[]; pick: string; setPick: (id: string) => void }) {
  return (
    <div className="rounded border border-zinc-800 bg-zinc-900/50 p-3 mb-3">
      <div className="text-sm font-medium mb-2">{title}</div>
      <div className="space-y-1">
        {results.map(res => (
          <label key={res.id} className="flex items-center gap-2 text-xs cursor-pointer">
            <input type="radio" name={title} checked={pick === res.id} onChange={() => setPick(res.id)} />
            <span className={res.ok ? 'text-zinc-200' : 'text-zinc-500 line-through'}>{res.label}</span>
            <span className="ml-auto tabular-nums text-zinc-400">{formatSpeed(res.bytesPerSec)}</span>
            {!res.ok && <span className="text-rose-500">HTTP {res.httpCode || '—'}</span>}
          </label>
        ))}
      </div>
    </div>
  );
}
