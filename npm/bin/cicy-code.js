#!/usr/bin/env node
const { spawn, execSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const https = require('https');

const pkg = require('../package.json');
const binPath = path.join(__dirname, 'cicy-code');
const os = require('os');

const cn = process.argv.includes('--cn') || process.env.CN_MIRROR === '1';
const desktopMode = process.argv.includes('--desktop');

if (process.argv.includes('--cn')) {
  process.env.CN_MIRROR = '1';
}

if (cn) {
  console.log('  [mirror] Using Chinese mirrors (npm + GitHub proxy)');
}

// Check for updates
function checkUpdate() {
  const registry = cn
    ? 'https://registry.npmmirror.com/cicy-code/latest'
    : 'https://registry.npmjs.org/cicy-code/latest';
  if (cn) console.log(`  [mirror] Registry: registry.npmmirror.com`);
  return new Promise((resolve) => {
    https.get(registry, (res) => {
      let data = '';
      res.on('data', (c) => data += c);
      res.on('end', () => {
        try {
          const latest = JSON.parse(data).version;
          if (latest && latest !== pkg.version) {
            resolve(latest);
          } else {
            resolve(null);
          }
        } catch { resolve(null); }
      });
    }).on('error', () => resolve(null));
  });
}

async function main() {
  // Check update (non-blocking, timeout 3s)
  const latest = await Promise.race([
    checkUpdate(),
    new Promise(r => setTimeout(() => r(null), 3000))
  ]);

  if (latest) {
    console.log(`\n  Update available: ${pkg.version} → ${latest}`);
    console.log(`  Updating...\n`);
    try {
      const npmCmd = cn
        ? `npm install -g cicy-code@${latest} --registry=https://registry.npmmirror.com`
        : `npm install -g cicy-code@${latest}`;
      execSync(npmCmd, { stdio: 'inherit' });
      console.log(`\n  Updated to ${latest}! Restarting...\n`);
      // Re-exec with new version
      const child = spawn('cicy-code', process.argv.slice(2), { stdio: 'inherit', env: process.env });
      child.on('exit', (code) => process.exit(code || 0));
      return;
    } catch (e) {
      console.log(`  Update failed, running current version.\n`);
    }
  }

  // Install globally if not already
  try {
    const globalBin = execSync('npm prefix -g', { encoding: 'utf8' }).trim() + '/bin/cicy-code';
    if (!fs.existsSync(globalBin)) throw new Error('not installed');
  } catch {
    console.log('  Installing cicy-code globally...');
    try {
      const npmCmd = cn
        ? 'npm install -g cicy-code --registry=https://registry.npmmirror.com'
        : 'npm install -g cicy-code';
      execSync(npmCmd, { stdio: 'inherit' });
      console.log('  Installed! You can now run: cicy-code\n');
    } catch {}
  }

  if (!fs.existsSync(binPath)) {
    console.error('Binary not found. Reinstall: npm install -g cicy-code');
    process.exit(1);
  }

  // Desktop mode: start API server in background, then launch Electron
  if (desktopMode) {
    return launchDesktop();
  }

  const child = spawn(binPath, process.argv.slice(2), {
    stdio: 'inherit',
    env: process.env
  });
  child.on('exit', (code) => process.exit(code || 0));
}

function getToken() {
  try {
    const globalJson = path.join(os.homedir(), 'global.json');
    const data = JSON.parse(fs.readFileSync(globalJson, 'utf8'));
    return data.api_token || '';
  } catch { return ''; }
}

function waitForServer(port, timeout) {
  const http = require('http');
  const start = Date.now();
  return new Promise((resolve, reject) => {
    const check = () => {
      if (Date.now() - start > timeout) return reject(new Error('Server start timeout'));
      const req = http.get(`http://127.0.0.1:${port}/api/health`, (res) => {
        resolve();
      });
      req.on('error', () => setTimeout(check, 500));
      req.setTimeout(1000, () => { req.destroy(); setTimeout(check, 500); });
    };
    check();
  });
}

async function launchDesktop() {
  const port = process.env.PORT || 18008;

  // 1. Start API server in background
  const serverArgs = process.argv.slice(2).filter(a => a !== '--desktop');
  const server = spawn(binPath, serverArgs, {
    stdio: 'ignore',
    detached: true,
    env: process.env
  });
  server.unref();
  console.log(`  🚀 Starting cicy-code server (PID: ${server.pid})...`);

  // 2. Wait for server ready
  try {
    await waitForServer(port, 30000);
  } catch {
    console.error('  ❌ Server failed to start within 30s');
    process.exit(1);
  }
  console.log(`  ✅ Server ready on port ${port}`);

  // 3. Get token
  const token = getToken();
  const url = `http://127.0.0.1:${port}/?token=${token}`;

  // 4. Find and launch Electron (electron-mcp)
  let electronBin = null;

  // Check if 'cicy' (electron-mcp) is globally installed
  try {
    electronBin = execSync('which cicy 2>/dev/null || where cicy 2>nul', { encoding: 'utf8' }).trim();
  } catch {}

  // Check bundled desktop/ submodule
  if (!electronBin) {
    const desktopDir = path.join(__dirname, '..', '..', 'desktop');
    const desktopBin = path.join(desktopDir, 'node_modules', '.bin', 'electron');
    if (fs.existsSync(desktopBin)) {
      electronBin = desktopBin;
      // Launch via electron directly with desktop/src/main.js
      const desktop = spawn(electronBin, [path.join(desktopDir, 'src', 'main.js'), `--url=${url}`], {
        stdio: 'inherit',
        env: { ...process.env, ELECTRON_RUN_AS_NODE: '' }
      });
      desktop.on('exit', (code) => {
        try { process.kill(server.pid); } catch {}
        process.exit(code || 0);
      });
      return;
    }
  }

  if (electronBin) {
    // Launch via globally installed 'cicy' CLI
    console.log(`  🖥️  Opening desktop: ${url}`);
    const desktop = spawn(electronBin, [`--url=${url}`], { stdio: 'inherit' });
    desktop.on('exit', (code) => {
      try { process.kill(server.pid); } catch {}
      process.exit(code || 0);
    });
    return;
  }

  // Fallback: try npx cicy
  console.log(`  🖥️  Launching desktop via npx cicy...`);
  const npxCmd = process.platform === 'win32' ? 'npx.cmd' : 'npx';
  const desktop = spawn(npxCmd, ['cicy', `--url=${url}`], { stdio: 'inherit' });
  desktop.on('exit', (code) => {
    try { process.kill(server.pid); } catch {}
    process.exit(code || 0);
  });
}

main();
