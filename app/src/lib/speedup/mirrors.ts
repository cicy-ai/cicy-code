// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// CN mirror catalog. For each category we list candidates and how to (a) probe
// them with a 1 MB Range-GET and (b) apply the choice via shell. The default
// id is what we'd pick if the probe is skipped — it's the one we've measured
// fastest on residential CN networks during recent dev sessions.

export type Category =
  | 'gh'        // GitHub raw/release proxy (used for cicy-code download)
  | 'apt'       // Ubuntu apt sources (WSL Ubuntu only)
  | 'rootfs'    // Ubuntu noble-base rootfs tarball (Windows WSL install only)
  | 'pypi'      // pip index
  | 'npm'       // npm registry
  | 'go'        // go module proxy
  | 'brew';     // Homebrew bottle/git remote (Mac only)

export interface MirrorCandidate {
  id: string;
  label: string;
  // 1 MB-ish URL we can Range-GET in ~2-5s to measure bandwidth.
  probeUrl: string;
  // Free-form payload consumed by apply.ts.
  config: Record<string, string>;
}

// ---- GitHub proxies (cicy-code release download, raw.githubusercontent rescue) ----
export const GH_PROXIES: MirrorCandidate[] = [
  { id: 'ghfast',   label: 'ghfast.top',         probeUrl: 'https://ghfast.top/https://github.com/git/git/raw/master/README.md', config: { prefix: 'https://ghfast.top/' } },
  { id: 'ghproxy',  label: 'ghproxy.net',        probeUrl: 'https://ghproxy.net/https://github.com/git/git/raw/master/README.md', config: { prefix: 'https://ghproxy.net/' } },
  { id: 'gh-proxy', label: 'gh-proxy.com',       probeUrl: 'https://gh-proxy.com/https://github.com/git/git/raw/master/README.md', config: { prefix: 'https://gh-proxy.com/' } },
  { id: 'direct',   label: 'github.com (direct)', probeUrl: 'https://raw.githubusercontent.com/git/git/master/README.md',          config: { prefix: '' } },
];

// ---- Ubuntu apt mirrors (sources.list rewrite target) ----
export const APT_MIRRORS: MirrorCandidate[] = [
  { id: 'tuna',   label: 'TUNA (Tsinghua)',  probeUrl: 'https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ls-lR.gz',     config: { host: 'mirrors.tuna.tsinghua.edu.cn' } },
  { id: 'ustc',   label: 'USTC',             probeUrl: 'https://mirrors.ustc.edu.cn/ubuntu/ls-lR.gz',              config: { host: 'mirrors.ustc.edu.cn' } },
  { id: 'aliyun', label: 'Aliyun',           probeUrl: 'https://mirrors.aliyun.com/ubuntu/ls-lR.gz',               config: { host: 'mirrors.aliyun.com' } },
  { id: '163',    label: '163',              probeUrl: 'https://mirrors.163.com/ubuntu/ls-lR.gz',                  config: { host: 'mirrors.163.com' } },
  { id: 'direct', label: 'archive.ubuntu.com', probeUrl: 'http://archive.ubuntu.com/ubuntu/ls-lR.gz',              config: { host: 'archive.ubuntu.com' } },
];

// ---- Ubuntu rootfs tarball (`wsl --import` source) ----
// noble-base-amd64.tar.gz is ~300 MB upstream; both mirrors host it.
export const ROOTFS_MIRRORS: MirrorCandidate[] = [
  { id: 'tuna', label: 'TUNA', probeUrl: 'https://mirrors.tuna.tsinghua.edu.cn/ubuntu-cloud-images/wsl/noble/current/ubuntu-noble-wsl-amd64-ubuntu24.04lts.rootfs.tar.gz', config: { url: 'https://mirrors.tuna.tsinghua.edu.cn/ubuntu-cloud-images/wsl/noble/current/ubuntu-noble-wsl-amd64-ubuntu24.04lts.rootfs.tar.gz' } },
  { id: 'ustc', label: 'USTC', probeUrl: 'https://mirrors.ustc.edu.cn/ubuntu-cloud-images/wsl/noble/current/ubuntu-noble-wsl-amd64-ubuntu24.04lts.rootfs.tar.gz', config: { url: 'https://mirrors.ustc.edu.cn/ubuntu-cloud-images/wsl/noble/current/ubuntu-noble-wsl-amd64-ubuntu24.04lts.rootfs.tar.gz' } },
];

// ---- pip index ----
export const PYPI_MIRRORS: MirrorCandidate[] = [
  { id: 'tuna',    label: 'TUNA',    probeUrl: 'https://pypi.tuna.tsinghua.edu.cn/simple/pip/',  config: { index: 'https://pypi.tuna.tsinghua.edu.cn/simple', host: 'pypi.tuna.tsinghua.edu.cn' } },
  { id: 'aliyun',  label: 'Aliyun',  probeUrl: 'https://mirrors.aliyun.com/pypi/simple/pip/',    config: { index: 'https://mirrors.aliyun.com/pypi/simple/',  host: 'mirrors.aliyun.com'   } },
  { id: 'tencent', label: 'Tencent', probeUrl: 'https://mirrors.cloud.tencent.com/pypi/simple/pip/', config: { index: 'https://mirrors.cloud.tencent.com/pypi/simple/', host: 'mirrors.cloud.tencent.com' } },
  { id: 'direct',  label: 'pypi.org', probeUrl: 'https://pypi.org/simple/pip/',                  config: { index: 'https://pypi.org/simple/', host: 'pypi.org' } },
];

// ---- npm registry ----
export const NPM_MIRRORS: MirrorCandidate[] = [
  { id: 'npmmirror', label: 'npmmirror.com',   probeUrl: 'https://registry.npmmirror.com/lodash',  config: { registry: 'https://registry.npmmirror.com' } },
  { id: 'cnpmjs',    label: 'cnpmjs.org',      probeUrl: 'https://r.cnpmjs.org/lodash',            config: { registry: 'https://r.cnpmjs.org' } },
  { id: 'direct',    label: 'registry.npmjs',  probeUrl: 'https://registry.npmjs.org/lodash',      config: { registry: 'https://registry.npmjs.org' } },
];

// ---- Go module proxy ----
export const GO_PROXIES: MirrorCandidate[] = [
  { id: 'goproxy-cn', label: 'goproxy.cn',  probeUrl: 'https://goproxy.cn/github.com/gin-gonic/gin/@v/list', config: { goproxy: 'https://goproxy.cn,direct' } },
  { id: 'goproxy-io', label: 'goproxy.io',  probeUrl: 'https://goproxy.io/github.com/gin-gonic/gin/@v/list', config: { goproxy: 'https://goproxy.io,direct' } },
  { id: 'aliyun',     label: 'Aliyun',       probeUrl: 'https://mirrors.aliyun.com/goproxy/github.com/gin-gonic/gin/@v/list', config: { goproxy: 'https://mirrors.aliyun.com/goproxy/,direct' } },
  { id: 'direct',     label: 'proxy.golang.org', probeUrl: 'https://proxy.golang.org/github.com/gin-gonic/gin/@v/list', config: { goproxy: 'https://proxy.golang.org,direct' } },
];

// ---- Homebrew (Mac only) ----
export const BREW_MIRRORS: MirrorCandidate[] = [
  { id: 'tuna', label: 'TUNA', probeUrl: 'https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/bottles/ca-certificates-2025-08-14.bottle.tar.gz', config: { brew: 'https://mirrors.tuna.tsinghua.edu.cn/git/homebrew/brew.git', core: 'https://mirrors.tuna.tsinghua.edu.cn/git/homebrew/homebrew-core.git', bottle: 'https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles' } },
  { id: 'ustc', label: 'USTC', probeUrl: 'https://mirrors.ustc.edu.cn/homebrew-bottles/bottles/ca-certificates-2025-08-14.bottle.tar.gz', config: { brew: 'https://mirrors.ustc.edu.cn/brew.git',                core: 'https://mirrors.ustc.edu.cn/homebrew-core.git',                bottle: 'https://mirrors.ustc.edu.cn/homebrew-bottles' } },
];

export const CN_MIRRORS: Record<Category, MirrorCandidate[]> = {
  gh: GH_PROXIES,
  apt: APT_MIRRORS,
  rootfs: ROOTFS_MIRRORS,
  pypi: PYPI_MIRRORS,
  npm: NPM_MIRRORS,
  go: GO_PROXIES,
  brew: BREW_MIRRORS,
};

// Which categories apply for a given environment.
export function categoriesFor(os: 'mac' | 'linux' | 'windows' | 'unknown', wslPresent: boolean): Category[] {
  if (os === 'mac')   return ['gh', 'pypi', 'npm', 'go', 'brew'];
  if (os === 'linux') return ['gh', 'pypi', 'npm', 'go'];
  if (os === 'windows') {
    return wslPresent
      ? ['gh', 'apt', 'pypi', 'npm', 'go']     // configure inside WSL
      : ['gh', 'rootfs'];                       // first need WSL itself
  }
  return ['gh'];
}
