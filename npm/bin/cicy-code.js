#!/usr/bin/env node
// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Launcher for the npm distribution. Resolves the prebuilt binary that ships
// in the platform-specific optionalDependency (cicy-code-<os>-<cpu>) and execs
// it — no network, no postinstall download. ALL binary args/subcommands are
// passed straight through (skill <...>, --dev, --hot, --helper=1, --agents=…,
// --public, --cn, etc.), so `npx cicy-code <anything>` == the binary.
//
// Only when actually starting the server, it mirrors dev.py's startup: if PORT
// (default 8008) is held by a *cicy-code* process it kills it and waits, so a
// stale instance never blocks the new one. A non-cicy occupant is left alone
// and we abort. Utility invocations (--help/--version, `skill …`) skip the
// port dance entirely — `npx cicy-code --version` must never touch :8008.
const { spawn, execSync } = require('child_process');
const fs = require('fs');

const args = process.argv.slice(2);
const PORT = process.env.PORT || '8008';

// Package name uses "windows", not the process.platform value "win32":
// npm's spam filter rejects (403) new package names containing win32.
const platformName = process.platform === 'win32' ? 'windows' : process.platform;
const platformPkg = `cicy-code-${platformName}-${process.arch}`;
// Windows ships the binary with the .exe extension (CreateProcess requires it).
const binName = process.platform === 'win32' ? 'cicy-code.exe' : 'cicy-code';
let binPath;
try {
  binPath = require.resolve(`${platformPkg}/${binName}`);
} catch {
  console.error(`cicy-code: no prebuilt binary for ${process.platform}-${process.arch}.`);
  console.error(`The optional dependency "${platformPkg}" is not installed.`);
  console.error(`Supported platforms: darwin-arm64, darwin-x64, linux-x64, linux-arm64, windows-x64.`);
  console.error(`Reinstall: npm install -g cicy-code` +
    ` (in China add --registry=https://registry.npmmirror.com)`);
  process.exit(1);
}
try { fs.chmodSync(binPath, 0o755); } catch {}

// Utility invocations don't start the server, so they must NOT kill :8008.
const isUtility =
  args.some((a) => a === '-h' || a === '--help' || a === '-v' || a === '--version') ||
  args[0] === 'skill';

if (!isUtility) ensurePortFree(PORT);

const child = spawn(binPath, args, {
  stdio: 'inherit',
  env: { ...process.env, PORT },
});
child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code == null ? 0 : code);
});

// --- dev.py-style port hygiene --------------------------------------------

function ensurePortFree(port) {
  const existing = pidOnPort(port);
  if (!existing) return;
  const cmd = processCommand(existing);
  if (/cicy-code/.test(cmd)) {
    console.log(`cicy-code: stopping existing instance on :${port} (pid=${existing})`);
    if (!killPid(existing) || pidOnPort(port)) {
      console.error(`cicy-code: port ${port} still in use after kill — aborting`);
      process.exit(1);
    }
  } else {
    console.error(`cicy-code: port ${port} is held by a non-cicy process (pid=${existing}): ${cmd}`);
    console.error(`cicy-code: free it or set PORT=<other> — aborting`);
    process.exit(1);
  }
}

function pidOnPort(port) {
  // win32: netstat -ano (no lsof/ss).
  if (process.platform === 'win32') {
    try {
      const out = execSync(`netstat -ano -p TCP`, {
        encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'],
      });
      for (const line of out.split('\n')) {
        const m = line.match(/TCP\s+\S+:(\d+)\s+\S+\s+LISTENING\s+(\d+)/);
        if (m && m[1] === String(port)) return m[2];
      }
    } catch {}
    return null;
  }
  // lsof first (macOS + Linux), then ss (Linux without lsof).
  try {
    const out = execSync(`lsof -ti TCP:${port} -sTCP:LISTEN`, {
      encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    if (out) return out.split('\n')[0].trim();
  } catch {}
  try {
    const out = execSync(`ss -tlnp 'sport = :${port}'`, {
      encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'],
    });
    const m = out.match(/pid=(\d+)/);
    if (m) return m[1];
  } catch {}
  return null;
}

function processCommand(pid) {
  if (process.platform === 'win32') {
    try {
      const out = execSync(`tasklist /fi "PID eq ${pid}" /fo csv /nh`, {
        encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'],
      });
      const m = out.match(/^"([^"]+)"/);
      return m ? m[1] : '';
    } catch { return ''; }
  }
  try {
    return execSync(`ps -p ${pid} -o command=`, {
      encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
  } catch { return ''; }
}

function isAlive(pid) {
  try { process.kill(Number(pid), 0); return true; }
  catch (e) { return e.code === 'EPERM'; }
}

function waitExit(pid, timeoutMs) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (!isAlive(pid)) return true;
    // Portable 200ms block (win32 has no `sleep`).
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
  }
  return !isAlive(pid);
}

function killPid(pid) {
  try { process.kill(Number(pid), 'SIGTERM'); }
  catch (e) { if (e.code === 'ESRCH') return true; }
  if (waitExit(pid, 6000)) return true;
  try { process.kill(Number(pid), 'SIGKILL'); }
  catch (e) { if (e.code === 'ESRCH') return true; }
  return waitExit(pid, 2000);
}
