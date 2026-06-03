#!/usr/bin/env node
// Thin launcher: resolves the prebuilt binary that ships in the
// platform-specific optionalDependency (cicy-code-<os>-<cpu>) and execs it.
// No network, no postinstall download — npm installs only the sub-package
// matching the current os/cpu (the others are skipped via their os/cpu
// fields), so a CN user pulls just their ~30MB slice from npmmirror.
const { spawn } = require('child_process');
const fs = require('fs');

const platformPkg = `cicy-code-${process.platform}-${process.arch}`;

let binPath;
try {
  // require.resolve finds the binary inside the installed sub-package,
  // wherever the package manager hoisted it.
  binPath = require.resolve(`${platformPkg}/cicy-code`);
} catch {
  console.error(`cicy-code: no prebuilt binary for ${process.platform}-${process.arch}.`);
  console.error(`The optional dependency "${platformPkg}" is not installed.`);
  console.error(`Supported platforms: darwin-arm64, darwin-x64, linux-x64, linux-arm64.`);
  console.error(`Reinstall: npm install -g cicy-code` +
    ` (in China add --registry=https://registry.npmmirror.com)`);
  process.exit(1);
}

// npm restores the 0755 mode from the tarball, but chmod defensively in case
// a mirror or extraction stripped the exec bit.
try { fs.chmodSync(binPath, 0o755); } catch {}

const child = spawn(binPath, process.argv.slice(2), { stdio: 'inherit', env: process.env });
child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code == null ? 0 : code);
});
