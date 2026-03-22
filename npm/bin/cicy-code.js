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

  if (latest && !process.env.CICY_SKIP_UPDATE) {
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
  const port = process.env.PORT || 8008;
  const desktopPort = 18101;

  // 0. Kill existing electron/cicy-code and free ports
  try { execSync(`pkill -f 'electron' 2>/dev/null || true`, { shell: true }); } catch {}
  try { execSync(`pkill -f 'cicy-code' 2>/dev/null || true`, { shell: true }); } catch {}
  try { execSync(`lsof -ti:${desktopPort} | xargs kill -9 2>/dev/null || true`, { shell: true }); } catch {}
  try { execSync(`lsof -ti:${port} | xargs kill -9 2>/dev/null || true`, { shell: true }); } catch {}
  await new Promise(resolve => setTimeout(resolve, 500));

  // 1. Start API server in background
  const serverArgs = process.argv.slice(2).filter(a => a !== '--desktop');
  const server = spawn(binPath, serverArgs, {
    stdio: 'ignore',
    detached: true,
    env: { ...process.env, CICY_NO_BROWSER: '1' }
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

  // 4. Launch Electron via global 'electron' binary (no signing needed)
  //    cicy-desktop uses official Electron binary + our JS code
  //    RPC/MCP server starts on desktopPort (18101)
  let electronBinary = null;
  try {
    electronBinary = execSync('which electron 2>/dev/null', { encoding: 'utf8' }).trim();
  } catch {}

  if (!electronBinary) {
    console.log('  ⚠️  Electron not found. Installing...');
    try {
      execSync('npm install -g electron', { stdio: 'inherit' });
      electronBinary = execSync('which electron', { encoding: 'utf8' }).trim();
    } catch {
      console.error('  ❌ Failed to install Electron. Install manually: npm install -g electron');
      console.log(`  📱 Fallback: open browser → ${url}`);
      return;
    }
  }

  // Find cicy-desktop package (global cicy or bundled desktop/)
  let desktopDir = null;

  // Check global 'cicy-desktop' package
  try {
    const cicyBin = execSync('which cicy 2>/dev/null', { encoding: 'utf8' }).trim();
    desktopDir = path.resolve(path.dirname(cicyBin), '..', 'lib', 'node_modules', 'cicy-desktop');
    if (!fs.existsSync(path.join(desktopDir, 'src', 'main.js'))) desktopDir = null;
  } catch {}

  // Fallback: bundled desktop/ submodule
  if (!desktopDir) {
    const bundled = path.join(__dirname, '..', '..', 'desktop');
    if (fs.existsSync(path.join(bundled, 'src', 'main.js'))) {
      desktopDir = bundled;
    }
  }

  if (!desktopDir) {
    console.log('  ⚠️  cicy-desktop not found. Installing...');
    try {
      execSync('npm install -g cicy-desktop', { stdio: 'inherit' });
      const cicyBin = execSync('which cicy', { encoding: 'utf8' }).trim();
      desktopDir = path.resolve(path.dirname(cicyBin), '..', 'lib', 'node_modules', 'cicy-desktop');
    } catch {
      console.error('  ❌ Failed to install cicy. Install manually: npm install -g cicy-desktop');
      console.log(`  📱 Fallback: open browser → ${url}`);
      return;
    }
  }

  console.log(`  🖥️  Opening desktop: ${url}`);
  console.log(`  🔧 RPC/MCP server: http://127.0.0.1:${desktopPort}`);

  const desktop = spawn(electronBinary, [desktopDir, `--url=${url}`, `--port=${desktopPort}`], {
    stdio: 'inherit',
    env: { ...process.env, PORT: String(desktopPort) }
  });

  desktop.on('exit', (code) => {
    try { process.kill(server.pid); } catch {}
    process.exit(code || 0);
  });
}

main();
