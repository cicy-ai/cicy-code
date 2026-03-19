const https = require('https');
const fs = require('fs');
const path = require('path');

const VERSION = '0.1.1';
const GH_URL = `https://github.com/cicy-ai/cicy-code/releases/download/v${VERSION}`;
const CN_URL = `https://gh-proxy.com/https://github.com/cicy-ai/cicy-code/releases/download/v${VERSION}`;

const cn = process.env.CN_MIRROR === '1' || process.argv.includes('--cn');
const BASE_URL = cn ? CN_URL : GH_URL;

const PLATFORMS = {
  'darwin-arm64': 'cicy-code-darwin-arm64',
  'darwin-x64': 'cicy-code-darwin-amd64',
  'linux-x64': 'cicy-code-linux-amd64',
  'linux-arm64': 'cicy-code-linux-arm64',
};

const key = `${process.platform}-${process.arch}`;
const binName = PLATFORMS[key];

if (!binName) {
  console.error(`Unsupported platform: ${key}`);
  process.exit(1);
}

const binDir = path.join(__dirname, '..', 'bin');
const binPath = path.join(binDir, 'cicy-code');
const url = `${BASE_URL}/${binName}`;

console.log(`Downloading ${binName}${cn ? ' (CN mirror)' : ''}...`);

function download(url, dest, redirects = 5) {
  return new Promise((resolve, reject) => {
    if (redirects === 0) return reject(new Error('Too many redirects'));
    const proto = url.startsWith('https') ? https : require('http');
    proto.get(url, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        return download(res.headers.location, dest, redirects - 1).then(resolve).catch(reject);
      }
      if (res.statusCode !== 200) return reject(new Error(`HTTP ${res.statusCode}`));
      const file = fs.createWriteStream(dest);
      res.pipe(file);
      file.on('finish', () => { file.close(); fs.chmodSync(dest, 0o755); resolve(); });
    }).on('error', reject);
  });
}

download(url, binPath)
  .then(() => console.log('Done!'))
  .catch((err) => {
    console.error('Download failed:', err.message);
    if (!cn) console.error('Try: CN_MIRROR=1 npx cicy-code');
    process.exit(1);
  });
