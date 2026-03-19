#!/usr/bin/env node
const { spawn, execSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const https = require('https');

const pkg = require('../package.json');
const binPath = path.join(__dirname, 'cicy-code');

const cn = process.argv.includes('--cn') || process.env.CN_MIRROR === '1';

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
    execSync('which cicy-code', { stdio: 'ignore' });
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

  const child = spawn(binPath, process.argv.slice(2), {
    stdio: 'inherit',
    env: process.env
  });
  child.on('exit', (code) => process.exit(code || 0));
}

main();
