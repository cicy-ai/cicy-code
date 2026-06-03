#!/usr/bin/env node
// Launcher for the npm distribution. Resolves the prebuilt binary that ships
// in the platform-specific optionalDependency (cicy-code-<os>-<cpu>) and execs
// it — no network, no postinstall download.
//
// Before exec it mirrors dev.py's startup: if PORT (default 8008) is already
// held by a *cicy-code* process it kills it and waits, so a stale instance
// never blocks the new one. A non-cicy occupant is left alone and we abort
// with a clear message (don't clobber someone else's server).
const { spawn, execSync } = require('child_process');
const fs = require('fs');

const PORT = process.env.PORT || '8008';

const platformPkg = `cicy-code-${process.platform}-${process.arch}`;
let binPath;
try {
  binPath = require.resolve(`${platformPkg}/cicy-code`);
} catch {
  console.error(`cicy-code: no prebuilt binary for ${process.platform}-${process.arch}.`);
  console.error(`The optional dependency "${platformPkg}" is not installed.`);
  console.error(`Supported platforms: darwin-arm64, darwin-x64, linux-x64, linux-arm64.`);
  console.error(`Reinstall: npm install -g cicy-code` +
    ` (in China add --registry=https://registry.npmmirror.com)`);
  process.exit(1);
}
try { fs.chmodSync(binPath, 0o755); } catch {}

// --- dev.py-style port hygiene on PORT (default 8008) ----------------------

function pidOnPort(port) {
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
    try { execSync('sleep 0.2'); } catch {}
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

const existing = pidOnPort(PORT);
if (existing) {
  const cmd = processCommand(existing);
  if (/cicy-code/.test(cmd)) {
    console.log(`cicy-code: stopping existing instance on :${PORT} (pid=${existing})`);
    if (!killPid(existing) || pidOnPort(PORT)) {
      console.error(`cicy-code: port ${PORT} still in use after kill — aborting`);
      process.exit(1);
    }
  } else {
    console.error(`cicy-code: port ${PORT} is held by a non-cicy process (pid=${existing}): ${cmd}`);
    console.error(`cicy-code: free it or set PORT=<other> — aborting`);
    process.exit(1);
  }
}

// --- launch ----------------------------------------------------------------

const child = spawn(binPath, process.argv.slice(2), {
  stdio: 'inherit',
  env: { ...process.env, PORT },
});
child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code == null ? 0 : code);
});
