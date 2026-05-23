// Renderer-side Windows WSL installer.
//
// All cicy-desktop versions ≥ v2.1.x expose `window.electronRPC(tool, args)`
// which dispatches to the main-process tool registry. `exec_shell` is one
// of those tools, so the renderer can drive arbitrary shell commands on
// the host. We re-implement the entire wsl install flow in the renderer
// so we can iterate the install logic by just deploying the CF Worker —
// no .exe rebuild required.
//
// Public API:
//   await windowsInstall({ version, network, onProgress, signal })
//     → { ok, version }
//
// onProgress receives:
//   { phase, message, progress?, version?, network?, received?, total? }

const DOCKER_DISTROS = new Set([
  "docker-desktop",
  "docker-desktop-data",
  "docker-desktop-bootstrap",
]);

const PREFERRED_DISTROS = [
  "Ubuntu",
  "Ubuntu-24.04",
  "Ubuntu-22.04",
  "Ubuntu-20.04",
  "Debian",
];

const MIRRORS = [
  "https://ghproxy.net/",
  "https://gh-proxy.com/",
];

// ── shell helper ──────────────────────────────────────────────────────
async function sh(cmd, { timeoutMs } = {}) {
  if (!window.electronRPC) throw new Error("electronRPC not available — open this page inside cicy-desktop's homepage window");
  const r = await window.electronRPC("exec_shell", { command: cmd, timeout_ms: timeoutMs });
  // exec_shell returns content array; homepage-preload's tx() already flattens
  // for window.cicy.system.*, but raw electronRPC returns the MCP shape.
  // Try to parse as JSON.
  let parsed = r;
  if (r && r.content) {
    const txt = (r.content || []).map(c => c.text).filter(Boolean).join("\n");
    try { parsed = JSON.parse(txt); } catch { parsed = { ok: true, stdout: txt, stderr: "", exitCode: 0 }; }
  }
  return {
    ok: parsed.exitCode === 0,
    stdout: (parsed.stdout || "").replace(/\u0000/g, ""),
    stderr: (parsed.stderr || "").replace(/\u0000/g, ""),
    code: parsed.exitCode || 0,
  };
}

// PowerShell with proper UTF-8 + base64 to avoid quoting hell.
async function ps(scriptText, { timeoutMs } = {}) {
  // Encode to UTF-16LE base64 for PowerShell -EncodedCommand
  const utf16 = new Uint16Array(scriptText.length);
  for (let i = 0; i < scriptText.length; i++) utf16[i] = scriptText.charCodeAt(i);
  const bytes = new Uint8Array(utf16.buffer);
  const b64 = btoa(String.fromCharCode(...bytes));
  return sh(`powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ${b64}`, { timeoutMs });
}

async function wslExec(distro, args, { timeoutMs } = {}) {
  const escaped = args.map(a => `"${a.replace(/"/g, '\\"')}"`).join(" ");
  return sh(`wsl -d ${distro} -e ${escaped}`, { timeoutMs });
}

async function wslBash(distro, script, { timeoutMs } = {}) {
  // base64 the script to avoid quoting; decode inside bash
  const b64 = btoa(unescape(encodeURIComponent(script)));
  return sh(`wsl -d ${distro} -- bash -c "echo ${b64} | base64 -d | bash -l"`, { timeoutMs });
}

// ── WSL detection ─────────────────────────────────────────────────────
async function checkWslStatus() {
  const status = await sh("wsl --status", { timeoutMs: 5000 });
  if (!status.ok) return { installed: false };

  const list = await sh("wsl -l -v", { timeoutMs: 5000 });
  if (!list.ok || !list.stdout) return { installed: true, hasDistro: false };

  const lines = list.stdout.split(/\r?\n/).filter(Boolean);
  const distros = [];
  let defaultDistro = null;
  for (const raw of lines) {
    const line = raw.trim();
    if (line.toUpperCase().startsWith("NAME")) continue;
    const isDefault = raw.trimStart().startsWith("*");
    const stripped = raw.replace(/^\s*\*\s*/, "").trim();
    const parts = stripped.split(/\s+/);
    if (parts.length < 3) continue;
    const [name, state, version] = parts;
    distros.push({ name, state, version });
    if (isDefault) defaultDistro = name;
  }

  let usable = null;
  for (const want of PREFERRED_DISTROS) {
    const found = distros.find(d => d.name.toLowerCase() === want.toLowerCase());
    if (found) { usable = found.name; break; }
  }
  if (!usable) {
    const general = distros.find(d => !DOCKER_DISTROS.has(d.name.toLowerCase()));
    if (general) usable = general.name;
  }

  return { installed: true, hasDistro: usable !== null, distros, defaultDistro, usableDistro: usable };
}

// ── apt sources fix ──────────────────────────────────────────────────
async function ensureAptSourcesReachable(distro, network, emit) {
  emit({ phase: "configuring-apt", message: "检查 apt 镜像可达性…" });

  // Probe current mirror
  const cur = await wslBash(distro, `MIRROR=$(grep -m1 -oE 'https?://[^ /]+' /etc/apt/sources.list 2>/dev/null | head -1) || true
if [ -n "$MIRROR" ]; then
  if curl -fsI --max-time 5 "$MIRROR" >/dev/null 2>&1; then echo "ok:$MIRROR"; else echo "fail:$MIRROR"; fi
else
  echo "none"
fi`, { timeoutMs: 15000 });
  if (cur.ok && cur.stdout.startsWith("ok:")) {
    return { ok: true, mirror: cur.stdout.slice(3), changed: false };
  }

  emit({ phase: "configuring-apt", message: "当前 apt 镜像不可达，切换到可达镜像…" });
  const candidates = network === "cn"
    ? ["https://mirrors.aliyun.com/ubuntu", "https://mirrors.tuna.tsinghua.edu.cn/ubuntu", "http://archive.ubuntu.com/ubuntu"]
    : ["http://archive.ubuntu.com/ubuntu", "https://mirrors.aliyun.com/ubuntu", "https://mirrors.tuna.tsinghua.edu.cn/ubuntu"];

  const probeScript = candidates
    .map(c => `if curl -fsI --max-time 5 "${c}/dists/jammy/Release" >/dev/null 2>&1; then echo "${c}"; exit 0; fi`)
    .join("\n");
  const pick = await wslBash(distro, probeScript + "\nexit 1", { timeoutMs: 35000 });
  if (!pick.ok || !pick.stdout) return { ok: false, error: "no reachable mirror" };
  const mirror = pick.stdout.trim().split(/\r?\n/).pop();

  const codename = (await wslBash(distro, ". /etc/os-release; echo $VERSION_CODENAME", { timeoutMs: 5000 })).stdout.trim() || "jammy";
  const newSources = [
    `deb ${mirror} ${codename} main restricted universe multiverse`,
    `deb ${mirror} ${codename}-updates main restricted universe multiverse`,
    `deb ${mirror} ${codename}-backports main restricted universe multiverse`,
    `deb ${mirror} ${codename}-security main restricted universe multiverse`,
  ].join("\n");

  const w = await wslBash(distro, `set -e
CONTENT='${newSources.replace(/'/g, `'\\''`)}'
if [ -w /etc/apt/sources.list ]; then
  echo "$CONTENT" > /etc/apt/sources.list
elif command -v sudo >/dev/null 2>&1; then
  echo "$CONTENT" | sudo tee /etc/apt/sources.list >/dev/null
else
  echo "no-write-access" >&2; exit 1
fi
echo updated`, { timeoutMs: 8000 });

  if (!w.ok) return { ok: false, error: w.stderr || "write failed" };
  emit({ phase: "configuring-apt", message: `apt 镜像已切换到 ${mirror}` });
  return { ok: true, mirror, changed: true };
}

// ── download with mirror race ────────────────────────────────────────
async function downloadWithRace(githubUrl, dstPath, network, emit) {
  const urls = network === "cn"
    ? [...MIRRORS.map(m => m + githubUrl), githubUrl]
    : [githubUrl, ...MIRRORS.map(m => m + githubUrl)];

  // Use PowerShell to do parallel HEAD probes, then download from the fastest.
  // Simpler approach: try in order with short timeouts (each TimeoutSec 60).
  for (let i = 0; i < urls.length; i++) {
    const url = urls[i];
    const isMirror = i < urls.length - (network === "cn" ? 1 : 0);
    emit({ phase: "downloading", message: `下载中 (${isMirror && network === "cn" ? "镜像" : "直连"})`, progress: null });
    const r = await ps(`
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$dst = '${dstPath.replace(/'/g, "''")}'
$url = '${url.replace(/'/g, "''")}'
New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
try {
  Invoke-WebRequest -Uri $url -OutFile $dst -UseBasicParsing -TimeoutSec 60
  $size = (Get-Item $dst).Length
  Write-Output "OK $size"
} catch {
  Write-Output "FAIL $($_.Exception.Message)"
  exit 1
}
`, { timeoutMs: 90000 });
    if (r.ok && r.stdout.startsWith("OK ")) {
      const size = parseInt(r.stdout.match(/OK (\d+)/)?.[1] || "0", 10);
      emit({ phase: "downloading", message: `下载完成 (${(size / 1024 / 1024).toFixed(1)} MB)`, progress: 1 });
      return { ok: true, size, url };
    }
    emit({ phase: "downloading", message: `镜像 ${i + 1}/${urls.length} 失败，尝试下一个…` });
  }
  return { ok: false, error: "all mirrors failed" };
}

// ── checkLatest ──────────────────────────────────────────────────────
async function fetchLatestVersion(network) {
  const manifestUrl = "https://github.com/cicy-ai/cicy-code/releases/latest/download/manifest.json";
  const urls = network === "cn"
    ? [...MIRRORS.map(m => m + manifestUrl), manifestUrl]
    : [manifestUrl, ...MIRRORS.map(m => m + manifestUrl)];

  for (const url of urls) {
    const r = await ps(`
try {
  $r = Invoke-RestMethod -Uri '${url.replace(/'/g, "''")}' -UseBasicParsing -TimeoutSec 8
  Write-Output ($r | ConvertTo-Json -Depth 5 -Compress)
} catch {
  Write-Output "ERR $($_.Exception.Message)"
  exit 1
}
`, { timeoutMs: 12000 });
    if (r.ok && r.stdout && !r.stdout.startsWith("ERR")) {
      try {
        const m = JSON.parse(r.stdout);
        if (m.version && m.assets) return { ok: true, version: m.version, assets: m.assets };
      } catch {}
    }
  }
  return { ok: false, error: "manifest fetch failed" };
}

// ── network detect ───────────────────────────────────────────────────
async function detectNetwork() {
  const r = await ps(`
try { $g = Invoke-WebRequest -Uri 'https://www.google.com/generate_204' -UseBasicParsing -TimeoutSec 4; if ($g.StatusCode -eq 204) { 'global'; exit } } catch {}
try { $b = Invoke-WebRequest -Uri 'https://www.baidu.com/' -Method Head -UseBasicParsing -TimeoutSec 4; if ($b.StatusCode -eq 200) { 'cn'; exit } } catch {}
'unknown'
`, { timeoutMs: 12000 });
  return r.ok ? r.stdout.trim() : "unknown";
}

// ── main install flow ────────────────────────────────────────────────
export async function windowsInstall({ onProgress = () => {} } = {}) {
  const emit = (e) => { try { onProgress(e); } catch {} };

  emit({ phase: "detecting", message: "检测网络…" });
  const network = await detectNetwork();
  emit({ phase: "detecting", message: `网络: ${network}`, network });

  emit({ phase: "checking", message: "检查最新版本…" });
  const check = await fetchLatestVersion(network);
  if (!check.ok) throw new Error("无法获取最新版本: " + check.error);
  const version = check.version;
  const assetUrl = check.assets["linux-amd64"];
  if (!assetUrl) throw new Error("manifest 缺少 linux-amd64 asset");
  emit({ phase: "checking", message: `最新版 v${version}`, version, network });

  // Stage path on Windows
  const stageDir = `\$env:APPDATA\\CiCy Desktop\\cicy-code\\wsl-stage`;
  const stagePath = `${stageDir}\\cicy-code-staged`;
  // Resolve absolute path for wsl
  const resolveR = await ps(`Write-Output "$env:APPDATA\\CiCy Desktop\\cicy-code\\wsl-stage\\cicy-code-staged"`, { timeoutMs: 5000 });
  const absStage = resolveR.stdout.trim();

  emit({ phase: "downloading", message: `下载 cicy-code v${version}…`, version, network });
  const dl = await downloadWithRace(assetUrl, absStage, network, emit);
  if (!dl.ok) throw new Error("下载失败: " + dl.error);

  emit({ phase: "checking-wsl", message: "检查 WSL 状态…" });
  let wsl = await checkWslStatus();
  if (!wsl.installed || !wsl.usableDistro) {
    emit({ phase: "installing-wsl", message: "需要安装 WSL2 + Ubuntu (5-10 分钟，需管理员权限)…" });
    const flag = (network === "cn" || network === "unknown") ? "--web-download" : "";
    const r = await sh(`wsl --install ${flag} --no-launch -d Ubuntu`, { timeoutMs: 15 * 60 * 1000 });
    if (!r.ok) throw new Error("WSL 安装失败: " + (r.stderr || "exit " + r.code));
    wsl = await checkWslStatus();
    if (!wsl.usableDistro) throw new Error("WSL 安装后仍未检测到可用 Linux 发行版（可能需要重启 Windows）");
  }
  emit({ phase: "checking-wsl", message: `使用 WSL distro: ${wsl.usableDistro}` });

  // Fix apt sources
  const apt = await ensureAptSourcesReachable(wsl.usableDistro, network, emit);
  if (!apt.ok) emit({ phase: "configuring-apt", message: `警告: apt 镜像配置失败 (${apt.error})，继续安装` });

  // wslpath translate + install
  emit({ phase: "installing-cicy-code", message: `安装 cicy-code v${version} 到 ${wsl.usableDistro}…`, version });
  const trans = await sh(`wsl -d ${wsl.usableDistro} -e wslpath -a "${absStage.replace(/\\/g, "\\\\")}"`, { timeoutMs: 5000 });
  if (!trans.ok) throw new Error("wslpath 失败: " + trans.stderr);
  const wslPath = trans.stdout.trim();

  const installScript = `set -eu
mkdir -p $HOME/.local/bin
cp '${wslPath.replace(/'/g, `'\\''`)}' $HOME/.local/bin/cicy-code.new
chmod +x $HOME/.local/bin/cicy-code.new
mv -f $HOME/.local/bin/cicy-code.new $HOME/.local/bin/cicy-code
ACT=$($HOME/.local/bin/cicy-code --version 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+' | head -1 || echo unknown)
echo "INSTALLED:$ACT"
printf '%s' "$ACT" > $HOME/.local/bin/cicy-code.version`;
  const ins = await wslBash(wsl.usableDistro, installScript, { timeoutMs: 60000 });
  if (!ins.ok) throw new Error("WSL 安装失败: " + ins.stderr);
  const m = ins.stdout.match(/INSTALLED:([0-9.]+)/);
  const installedVer = m ? m[1] : version;

  emit({ phase: "done", message: `已安装 v${installedVer}`, version: installedVer });
  return { ok: true, version: installedVer };
}

// Probe whether the renderer can call exec_shell. Used by the UI to decide
// whether to expose the renderer-side install path.
export function canRunRendererInstall() {
  return typeof window !== "undefined" && typeof window.electronRPC === "function";
}
