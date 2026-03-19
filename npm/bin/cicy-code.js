#!/usr/bin/env node
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

const binDir = path.join(__dirname);
const platform = process.platform;

const binName = platform === 'win32' ? 'cicy-code.exe' : 'cicy-code';
const binPath = path.join(binDir, binName);

if (!fs.existsSync(binPath)) {
  console.error(`Binary not found: ${binPath}`);
  console.error('Run: npm install cicy-code');
  process.exit(1);
}

const child = spawn(binPath, process.argv.slice(2), {
  stdio: 'inherit',
  env: process.env
});

child.on('exit', (code) => process.exit(code || 0));
