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
const crypto = require('crypto');
const fs = require('fs');
const https = require('https');
const os = require('os');
const path = require('path');

const rawArgs = process.argv.slice(2);
const { email: cloudEmail, team: cloudTeam, args } = takeCloudArgs(rawArgs);
const PORT = process.env.PORT || '8008';
const CLOUD_ORIGIN = (process.env.CICY_CLOUD_ORIGIN || 'https://cicy-ai.com').replace(/\/$/, '');

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
  console.error(`Supported platforms: darwin-arm64, darwin-x64, linux-x64, linux-arm64.`);
  console.error(`Reinstall: npm install -g cicy-code` +
    ` (in China add --registry=https://registry.npmmirror.com)`);
  process.exit(1);
}
try { fs.chmodSync(binPath, 0o755); } catch {}

// Utility invocations don't start the server, so they must NOT kill :8008.
const isUtility =
  args.some((a) => a === '-h' || a === '--help' || a === '-v' || a === '--version') ||
  args[0] === 'skill';

main().catch((err) => {
  console.error(`cicy-code: ${err && err.message ? err.message : err}`);
  process.exit(1);
});

async function main() {
  let cloudEnv = {};
  if (cloudEmail) cloudEnv = await cloudLogin(cloudEmail, cloudTeam);
  if (!isUtility) ensurePortFree(PORT);
  const child = spawn(binPath, args, {
    stdio: 'inherit',
    env: { ...process.env, ...cloudEnv, PORT },
  });
  child.on('exit', (code, signal) => {
    if (signal) process.kill(process.pid, signal);
    else process.exit(code == null ? 0 : code);
  });
}

function takeCloudArgs(input) {
  const output = [];
  let email = '';
  let team = '';
  for (let i = 0; i < input.length; i += 1) {
    const value = input[i];
    if (value === '--email') {
      email = String(input[i + 1] || '').trim().toLowerCase();
      i += 1;
    } else if (value.startsWith('--email=')) {
      email = value.slice('--email='.length).trim().toLowerCase();
    } else if (value === '--team') {
      team = String(input[i + 1] || '').trim();
      i += 1;
    } else if (value.startsWith('--team=')) {
      team = value.slice('--team='.length).trim();
    } else {
      output.push(value);
    }
  }
  if (email && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
    throw new Error('invalid --email address');
  }
  if (team && !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(team)) {
    throw new Error('invalid --team identifier');
  }
  return { email, team, args: output };
}

async function cloudLogin(email, requestedTeam) {
  const credentialPath = cloudCredentialPath();
  const saved = readJSON(credentialPath);
  const instanceId = String(saved.instance_id || '').trim() ||
    `code-${crypto.randomBytes(18).toString('hex')}`;
  const teamId = requestedTeam || String(saved.team_id || '').trim() || `team-${Date.now()}`;
  let token = saved.email === email ? String(saved.token || '') : '';

  if (token) {
    try {
      await registerCloudInstance(token, instanceId, teamId);
      writeCredential(credentialPath, {
        ...saved,
        email,
        instance_id: instanceId,
        team_id: teamId,
        token,
        cloud_origin: CLOUD_ORIGIN,
        updated_at: new Date().toISOString(),
      });
      console.log(`cicy-code: CiCy Cloud connected as ${email}`);
      return cloudEnvironment(email, instanceId, teamId, token);
    } catch {
      token = '';
    }
  }

  const state = crypto.randomBytes(32).toString('hex');
  await requestJSON('/api/auth/email/request', {
    method: 'POST',
    body: {
      email,
      state,
      flow: 'desktop_poll',
      lang: preferredLanguage(),
      ...loginRequestInfo(instanceId),
      teamId,
    },
  });
  console.log(`cicy-code: login email sent to ${email}`);
  console.log('cicy-code: click the link in the email; waiting for confirmation…');

  const deadline = Date.now() + 15 * 60 * 1000;
  while (Date.now() < deadline) {
    await delay(2000);
    const result = await requestJSON(`/api/auth/desktop/poll?state=${encodeURIComponent(state)}`);
    if (result.status === 'pending') continue;
    if (result.status !== 'ready' || !result.token) {
      throw new Error('email login expired; start cicy-code again');
    }
    token = String(result.token);
    await registerCloudInstance(token, instanceId, teamId);
    writeCredential(credentialPath, {
      email,
      instance_id: instanceId,
      team_id: teamId,
      token,
      cloud_origin: CLOUD_ORIGIN,
      updated_at: new Date().toISOString(),
    });
    console.log(`cicy-code: login successful; this device is now bound to ${email}`);
    return cloudEnvironment(email, instanceId, teamId, token);
  }
  throw new Error('email login timed out; start cicy-code again');
}

function loginRequestInfo(instanceId) {
  const cpus = os.cpus();
  const cpu = cpus.length > 0 ? String(cpus[0].model || '').trim() : '';
  let gpu = '';
  try {
    gpu = execSync('nvidia-smi --query-gpu=name,memory.total --format=csv,noheader', {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
      timeout: 3000,
    }).trim();
  } catch {}
  let clientVersion = '';
  try {
    clientVersion = String(require('../package.json').version || '');
  } catch {}
  return {
    platform: isColab() ? 'colab' : process.platform,
    system: `${os.type()} ${os.release()}`.trim(),
    arch: process.arch,
    runtime: isColab() ? 'colab' : (process.env.GITHUB_ACTIONS === 'true' ? 'github-actions' : 'native'),
    hostname: os.hostname(),
    instanceId,
    clientVersion,
    cpu,
    memory: `${Math.round(os.totalmem() / 1024 / 1024)} MB`,
    gpu,
  };
}

async function registerCloudInstance(token, instanceId, teamId) {
  await requestJSON('/api/code/instances/register', {
    method: 'POST',
    token,
    body: {
      instanceId,
      teamId,
      platform: isColab() ? 'colab' : process.platform,
      arch: process.arch,
      runtime: isColab() ? 'colab' : 'native',
      systemLanguage: preferredLanguage(),
    },
  });
}

function cloudEnvironment(email, instanceId, teamId, token) {
  return {
    CICY_CLOUD_ORIGIN: CLOUD_ORIGIN,
    CICY_CLOUD_EMAIL: email,
    CICY_CLOUD_INSTANCE_ID: instanceId,
    CICY_CLOUD_TEAM_ID: teamId,
    CICY_CLOUD_TOKEN: token,
  };
}

function cloudCredentialPath() {
  return path.join(cloudHome(), 'db', 'cloud-device.json');
}

function cloudHome() {
  return process.env.CICY_HOME || path.join(os.homedir(), 'cicy-ai');
}

function isColab() {
  return process.platform === 'linux' && (
    Boolean(process.env.COLAB_RELEASE_TAG) ||
    Boolean(process.env.COLAB_GPU) ||
    fs.existsSync('/content')
  );
}

function readJSON(file) {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')); } catch { return {}; }
}

function writeCredential(file, value) {
  writeJSONAtomic(file, value);
}

function writeJSONAtomic(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const temporary = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(temporary, file);
  try { fs.chmodSync(file, 0o600); } catch {}
}

function preferredLanguage() {
  const lang = String(process.env.LANG || '').toLowerCase();
  if (lang.startsWith('ja')) return 'ja';
  if (lang.startsWith('fr')) return 'fr';
  if (lang.startsWith('en')) return 'en';
  return 'zh';
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function requestJSON(route, options = {}) {
  return new Promise((resolve, reject) => {
    const url = new URL(route, CLOUD_ORIGIN);
    const body = options.body ? Buffer.from(JSON.stringify(options.body)) : null;
    const req = https.request(url, {
      method: options.method || 'GET',
      headers: {
        accept: 'application/json',
        ...(body ? { 'content-type': 'application/json', 'content-length': body.length } : {}),
        ...(options.token ? { authorization: `Bearer ${options.token}` } : {}),
      },
      timeout: 15000,
    }, (res) => {
      const chunks = [];
      res.on('data', (chunk) => chunks.push(chunk));
      res.on('end', () => {
        let data;
        try { data = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
        catch { return reject(new Error(`CiCy Cloud returned HTTP ${res.statusCode}`)); }
        if (res.statusCode < 200 || res.statusCode >= 300) {
          return reject(new Error(data.error || `CiCy Cloud returned HTTP ${res.statusCode}`));
        }
        resolve(data);
      });
    });
    req.on('timeout', () => req.destroy(new Error('CiCy Cloud request timed out')));
    req.on('error', reject);
    if (body) req.write(body);
    req.end();
  });
}

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
