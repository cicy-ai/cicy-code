#!/usr/bin/env node
// `npx cicy-code-docker` — load the bundled cicy-code runtime image into the
// local Docker daemon. The gzipped `docker save` tarball ships inside this npm
// package, so it is fetched from npm (npmmirror in CN — fast, no Docker Hub
// pull) and then `docker load`-ed. After loading, the image appears in
// `docker images` under its original tag (cicybot/cicy-code:<version>).
const { execFileSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const tar = path.join(__dirname, 'cicy-code.tar.gz');
if (!fs.existsSync(tar)) {
  console.error('cicy-code-docker: image tarball missing from package.');
  process.exit(1);
}
try {
  execFileSync('docker', ['version'], { stdio: 'ignore' });
} catch {
  console.error('cicy-code-docker: docker not found or the daemon is not running.');
  process.exit(1);
}

const { version } = require('./package.json');
console.log(`cicy-code-docker: loading cicy-code ${version} image (docker load)…`);
execFileSync('docker', ['load', '-i', tar], { stdio: 'inherit' });
console.log('cicy-code-docker: done — see `docker images | grep cicy-code`.');
