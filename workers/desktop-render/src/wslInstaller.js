// Renderer-side Windows WSL installer.
//
// Drives the entire wsl install flow from the React renderer using
// `window.electronRPC("exec_shell", { command })`. This means future
// fixes (apt mirror probing, distro selection, cicy-code download,
// version verify) ship via a CF Worker deploy with NO .exe rebuild.
//
//   await windowsInstall({ onProgress, signal }) → { ok, version }
//
// onProgress event shape (matches main-process sidecar:progress):
//   { phase, message, progress?, version?, network? }
//
// Phases (in order):
//   detecting           — network probe
//   checking            — fetch latest manifest
//   downloading         — staging the cicy-code binary on Windows
//   checking-wsl        — wsl --status / -l -v
//   installing-wsl      — wsl --install -d Ubuntu (long, may need reboot)
//   waiting-distro      — first-boot of Ubuntu after fresh install
//   configuring-apt     — fix /etc/apt/sources.list to a reachable mirror
//   installing-cicy-code— cp + chmod + --version
//   done                — final ok

// ── tunables ──────────────────────────────────────────────────────────
const DOCKER_DISTROS = new Set([
  "docker-desktop",
  "docker-desktop-data",
  "docker-desktop-bootstrap",
]);

const PREFERRED_DISTROS = [
  "Ubuntu", "Ubuntu-24.04", "Ubuntu-22.04", "Ubuntu-20.04", "Debian",
];

// gh proxy mirrors. Pinned to ones that work from APAC (Myanmar, etc).
const GH_MIRRORS = [
  "https://ghproxy.net/",
  "https://gh-proxy.com/",
];

// Apt mirror candidates by network.
const APT_MIRRORS = {
  cn:     ["https://mirrors.aliyun.com/ubuntu", "https://mirrors.tuna.tsinghua.edu.cn/ubuntu", "http://archive.ubuntu.com/ubuntu"],
  global: ["http://archive.ubuntu.com/ubuntu", "https://mirrors.aliyun.com/ubuntu"],
};

const DEFAULT_TIMEOUTS = {
  shellShort: 10_000,
  shellMed:   30_000,
  shellLong:  120_000,
  download:   120_000,
  wslInstall: 15 * 60_000,
  wslBoot:    90_000,
};

// ── shell helpers ─────────────────────────────────────────────────────
function assertRPC() {
  if (typeof window === "undefined" || typeof window.electronRPC !== "function") {
    throw new Error("electronRPC unavailable — open this page inside cicy-desktop's homepage window (v2.1.12+)");
  }
}

async function sh(cmd, { timeoutMs = DEFAULT_TIMEOUTS.shellMed } = {}) {
  assertRPC();
  const r = await window.electronRPC("exec_shell", { command: cmd, timeout_ms: timeoutMs });
  // electronRPC returns the raw MCP shape: { content: [{type:"text",text:"<json>"}] }
  // homepage-preload's `tx()` flattens for window.cicy.system.*; raw RPC needs us to parse.
  let parsed = r;
  if (r && r.content) {
    const txt = (r.content || []).map(c => c.text).filter(Boolean).join("");
    try { parsed = JSON.parse(txt); }
    catch { parsed = { ok: true, stdout: txt, stderr: "", exitCode: 0 }; }
  }
  return {
    ok: (parsed.exitCode || 0) === 0,
    stdout: clean(parsed.stdout),
    stderr: clean(parsed.stderr),
    code: parsed.exitCode || 0,
  };
}

function clean(s) {
  // PowerShell + WSL bridge output occasionally has stray NULs and CRs.
  return String(s || "").replace(/\u0000/g, "").replace(/\r/g, "");
}

async function ps(scriptText, opts = {}) {
  // Avoid quoting hell by passing the script as base64 through -EncodedCommand.
  // PowerShell expects UTF-16LE base64.
  const utf16 = new Uint16Array(scriptText.length);
  for (let i = 0; i < scriptText.length; i++) utf16[i] = scriptText.charCodeAt(i);
  const b64 = btoa(String.fromCharCode(...new Uint8Array(utf16.buffer)));
  return sh(`powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ${b64}`, opts);
}

async function wslBash(distro, script, opts = {}) {
  // Ship the bash script through base64 to avoid Windows quoting hell.
  // Use TextEncoder for proper UTF-8.
  const bytes = new TextEncoder().encode(script);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  const b64 = btoa(bin);
  return sh(`wsl -d ${distro} -- bash -c "echo ${b64} | base64 -d | bash -l"`, opts);
}

async function wslExec(distro, args, opts = {}) {
  const escaped = args.map(a => `"${String(a).replace(/"/g, '\\"')}"`).join(" ");
  return sh(`wsl -d ${distro} -e ${escaped}`, opts);
}

// ── network detection ─────────────────────────────────────────────────
async function detectNetwork() {
  const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
$result = 'unknown'
# Race: probe google first, baidu second; first reachable wins.
$jobs = @(
  Start-Job -ScriptBlock { try { $r = Invoke-WebRequest 'https://www.google.com/generate_204' -UseBasicParsing -TimeoutSec 3; if ($r.StatusCode -eq 204) { 'global' } } catch {} },
  Start-Job -ScriptBlock { try { $r = Invoke-WebRequest 'https://www.baidu.com/' -Method Head -UseBasicParsing -TimeoutSec 3; if ($r.StatusCode -eq 200) { 'cn' } } catch {} }
)
foreach ($j in $jobs) {
  Wait-Job $j -Timeout 4 | Out-Null
  $out = Receive-Job $j
  if ($out -and $result -eq 'unknown') { $result = $out }
  Remove-Job $j -Force | Out-Null
}
Write-Output $result
`, { timeoutMs: DEFAULT_TIMEOUTS.shellShort });
  return r.ok ? r.stdout.trim() : "unknown";
}

// ── manifest fetch ────────────────────────────────────────────────────
function manifestUrls(network) {
  const base = "https://github.com/cicy-ai/cicy-code/releases/latest/download/manifest.json";
  return network === "cn"
    ? [...GH_MIRRORS.map(m => m + base), base]
    : [base, ...GH_MIRRORS.map(m => m + base)];
}

async function fetchLatestManifest(network) {
  const urls = manifestUrls(network);
  const json = JSON.stringify(urls);
  const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
$urls = '${json.replace(/'/g, "''")}' | ConvertFrom-Json
foreach ($u in $urls) {
  try {
    $m = Invoke-RestMethod -Uri $u -UseBasicParsing -TimeoutSec 8
    Write-Output ('OK ' + ($m | ConvertTo-Json -Depth 6 -Compress))
    exit 0
  } catch { continue }
}
Write-Output 'ERR no reachable manifest'
exit 1
`, { timeoutMs: 60_000 });
  if (!r.ok || !r.stdout.startsWith("OK ")) return { ok: false, error: r.stdout || r.stderr || "unreachable" };
  try {
    const m = JSON.parse(r.stdout.slice(3));
    if (!m.version || !m.assets) return { ok: false, error: "manifest malformed" };
    return { ok: true, version: m.version, assets: m.assets };
  } catch (e) {
    return { ok: false, error: "json parse: " + e.message };
  }
}

// ── download with mirror race ────────────────────────────────────────
async function downloadStaged({ assetUrl, network, dstPath, expectMin = 1_000_000 }) {
  // 1. Build URL list with mirrors first if cn-ish.
  const urls = network === "cn"
    ? [...GH_MIRRORS.map(m => m + assetUrl), assetUrl]
    : [assetUrl, ...GH_MIRRORS.map(m => m + assetUrl)];

  // 2. Parallel HEAD probe to pick the fastest reachable mirror.
  const json = JSON.stringify(urls);
  const probe = await ps(`
$ProgressPreference = 'SilentlyContinue'
$urls = '${json.replace(/'/g, "''")}' | ConvertFrom-Json
$jobs = @()
foreach ($u in $urls) {
  $jobs += Start-Job -ScriptBlock {
    param($url)
    try {
      $sw = [Diagnostics.Stopwatch]::StartNew()
      $r = Invoke-WebRequest -Uri $url -Method Head -UseBasicParsing -TimeoutSec 5 -MaximumRedirection 5
      $sw.Stop()
      if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 400) {
        Write-Output ("OK $($sw.ElapsedMilliseconds) $url")
      }
    } catch {}
  } -ArgumentList $u
}
Wait-Job $jobs -Timeout 7 | Out-Null
$results = @()
foreach ($j in $jobs) { $r = Receive-Job $j; if ($r) { $results += $r }; Remove-Job $j -Force | Out-Null }
$results | Sort-Object { [int]($_.Split(' ')[1]) }
`, { timeoutMs: 12_000 });

  let ordered = urls;
  if (probe.ok && probe.stdout) {
    const lines = probe.stdout.split("\n").map(s => s.trim()).filter(s => s.startsWith("OK "));
    const sorted = lines.map(l => l.split(" ").slice(2).join(" ")).filter(Boolean);
    if (sorted.length) ordered = [...sorted, ...urls.filter(u => !sorted.includes(u))];
  }

  // 3. Sequential download from fastest, fall through on failure.
  for (let i = 0; i < ordered.length; i++) {
    const url = ordered[i];
    const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
$dst = '${dstPath.replace(/'/g, "''")}'
$url = '${url.replace(/'/g, "''")}'
New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
try {
  Invoke-WebRequest -Uri $url -OutFile $dst -UseBasicParsing -TimeoutSec 120
  $size = (Get-Item $dst).Length
  Write-Output "OK $size"
} catch {
  Write-Output "FAIL $($_.Exception.Message)"
  exit 1
}
`, { timeoutMs: DEFAULT_TIMEOUTS.download });
    if (r.ok && r.stdout.startsWith("OK ")) {
      const size = parseInt(r.stdout.match(/OK (\d+)/)?.[1] || "0", 10);
      if (size < expectMin) continue;
      return { ok: true, size, url };
    }
  }
  return { ok: false, error: "all mirrors failed" };
}

// ── WSL detection ─────────────────────────────────────────────────────
async function checkWslStatus() {
  const status = await sh("wsl --status", { timeoutMs: 8_000 });
  if (!status.ok) return { installed: false, supported: true };

  // Detect default version
  const m = status.stdout.match(/Default Version:\s*(\d)/i);
  const defaultVer = m ? parseInt(m[1], 10) : 2;

  const list = await sh("wsl -l -v", { timeoutMs: 8_000 });
  if (!list.ok || !list.stdout.trim()) {
    return { installed: true, supported: true, hasDistro: false, defaultVer };
  }

  const distros = [];
  let defaultDistro = null;
  for (const raw of list.stdout.split(/\r?\n/)) {
    const isDefault = raw.trimStart().startsWith("*");
    const stripped = raw.replace(/^\s*\*?\s*/, "").trim();
    if (!stripped || /^NAME\b/i.test(stripped)) continue;
    const parts = stripped.split(/\s+/);
    if (parts.length < 3) continue;
    const [name, state, version] = parts;
    distros.push({ name, state, version: parseInt(version, 10) || 1 });
    if (isDefault) defaultDistro = name;
  }

  const usable = pickUsableDistro(distros);
  return {
    installed: true,
    supported: true,
    hasDistro: usable !== null,
    distros,
    defaultDistro,
    usableDistro: usable,
    defaultVer,
  };
}

function pickUsableDistro(distros) {
  for (const want of PREFERRED_DISTROS) {
    const found = distros.find(d => d.name.toLowerCase() === want.toLowerCase());
    if (found) return found.name;
  }
  for (const d of distros) {
    if (!DOCKER_DISTROS.has(d.name.toLowerCase())) return d.name;
  }
  return null;
}

// ── WSL install ───────────────────────────────────────────────────────
async function installWsl(network, emit) {
  emit({ phase: "installing-wsl", message: "Installing WSL2 + Ubuntu (5–10 min, requires admin)…" });
  const useWebDownload = network === "cn" || network === "unknown";
  const flag = useWebDownload ? "--web-download" : "";
  // --no-launch avoids a UAC prompt for Ubuntu's first-run setup.
  const r = await sh(`wsl --install ${flag} --no-launch -d Ubuntu`, { timeoutMs: DEFAULT_TIMEOUTS.wslInstall });
  if (r.ok) return { ok: true };

  // Retry path varies by initial flag.
  if (useWebDownload) {
    emit({ phase: "installing-wsl", message: "Web download failed, retrying via Microsoft Store…" });
    const r2 = await sh(`wsl --install --no-launch -d Ubuntu`, { timeoutMs: DEFAULT_TIMEOUTS.wslInstall });
    if (r2.ok) return { ok: true };
  }

  // Fallback: download Ubuntu rootfs directly from cloud-images mirror
  // and `wsl --import`. Works in restricted networks (Myanmar, CN) where
  // raw.githubusercontent.com (which Microsoft fetches DistributionInfo
  // from) is unreachable so `wsl --install -d <name>` cannot proceed.
  emit({ phase: "installing-wsl", message: "Falling back to direct rootfs import…" });
  const ir = await importUbuntuFromRootfs(network, emit);
  if (ir.ok) return { ok: true, method: "rootfs-import" };

  return { ok: false, error: r.stderr || ir.error || `wsl --install exit ${r.code}` };
}

// Last-resort path: download Ubuntu rootfs directly from cloud-images
// mirrors (NJU > USTC > tuna > cloud-images) and `wsl --import`. Bypasses
// Microsoft's DistributionInfo manifest fetch + Microsoft Store entirely.
async function importUbuntuFromRootfs(network, emit) {
  const baseMirrors = network === "cn"
    ? [
        "https://mirror.nju.edu.cn/ubuntu-cloud-images/wsl/jammy/current",
        "https://mirrors.ustc.edu.cn/ubuntu-cloud-images/wsl/jammy/current",
        "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-cloud-images/wsl/jammy/current",
        "https://cloud-images.ubuntu.com/wsl/jammy/current",
      ]
    : [
        "https://cloud-images.ubuntu.com/wsl/jammy/current",
        "https://mirror.nju.edu.cn/ubuntu-cloud-images/wsl/jammy/current",
        "https://mirrors.ustc.edu.cn/ubuntu-cloud-images/wsl/jammy/current",
      ];
  const fileName = "ubuntu-jammy-wsl-amd64-ubuntu22.04lts.rootfs.tar.gz";
  const urls = baseMirrors.map(b => `${b}/${fileName}`);

  emit({ phase: "installing-wsl", message: "Picking fastest Ubuntu rootfs mirror…" });

  // Parallel HEAD probe → sort by latency.
  const json = JSON.stringify(urls);
  const probe = await ps(`
$ProgressPreference = 'SilentlyContinue'
$urls = '${json.replace(/'/g, "''")}' | ConvertFrom-Json
$jobs = @()
foreach ($u in $urls) {
  $jobs += Start-Job -ScriptBlock {
    param($url)
    try {
      $sw = [Diagnostics.Stopwatch]::StartNew()
      $r = Invoke-WebRequest -Uri $url -Method Head -UseBasicParsing -TimeoutSec 5
      $sw.Stop()
      if ($r.StatusCode -ge 200) { Write-Output ("OK $($sw.ElapsedMilliseconds) $url") }
    } catch {}
  } -ArgumentList $u
}
Wait-Job $jobs -Timeout 7 | Out-Null
$res = @()
foreach ($j in $jobs) { $r = Receive-Job $j; if ($r) { $res += $r }; Remove-Job $j -Force | Out-Null }
$res | Sort-Object { [int]($_.Split(' ')[1]) }
`, { timeoutMs: 12_000 });
  let ordered = urls;
  if (probe.ok && probe.stdout) {
    const sorted = probe.stdout.split("\n")
      .map(s => s.trim())
      .filter(s => s.startsWith("OK "))
      .map(l => l.split(" ").slice(2).join(" "))
      .filter(Boolean);
    if (sorted.length) ordered = [...sorted, ...urls.filter(u => !sorted.includes(u))];
  }

  emit({ phase: "installing-wsl", message: "Downloading Ubuntu rootfs (~350MB, may take a few minutes)…" });

  const orderedJson = JSON.stringify(ordered);
  const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
$tar = Join-Path $env:TEMP 'ubuntu-jammy-wsl.tar.gz'
$dst = Join-Path $env:LOCALAPPDATA 'WSL\\Ubuntu'
New-Item -ItemType Directory -Force $dst | Out-Null
$urls = '${orderedJson.replace(/'/g, "''")}' | ConvertFrom-Json
$ok = $false
foreach ($u in $urls) {
  try {
    Invoke-WebRequest -Uri $u -OutFile $tar -UseBasicParsing -TimeoutSec 1800
    if ((Get-Item $tar).Length -gt 50000000) { $ok = $true; Write-Output ("DOWNLOADED " + $u); break }
  } catch { Write-Output ("FAIL " + $u + " " + $_.Exception.Message) }
}
if (-not $ok) { Write-Output "ERR no mirror"; exit 1 }
& wsl --import Ubuntu $dst $tar --version 2
if ($LASTEXITCODE -ne 0) { Write-Output ("IMPORT_FAIL " + $LASTEXITCODE); exit 1 }
Remove-Item -Force $tar -ErrorAction SilentlyContinue
Write-Output "IMPORTED"
`, { timeoutMs: 35 * 60_000 });
  if (!r.ok || !/IMPORTED/.test(r.stdout)) {
    return { ok: false, error: r.stderr || r.stdout || "rootfs-import failed" };
  }
  return { ok: true, method: "rootfs-import" };
}

async function waitForDistroReady(distro, emit, deadlineMs = DEFAULT_TIMEOUTS.wslBoot) {
  const start = Date.now();
  while (Date.now() - start < deadlineMs) {
    const r = await wslExec(distro, ["true"], { timeoutMs: 10_000 });
    if (r.ok) return { ok: true };
    emit({ phase: "waiting-distro", message: `Waiting for ${distro} to boot…` });
    await new Promise(res => setTimeout(res, 3_000));
  }
  return { ok: false, error: `${distro} did not boot in ${Math.round(deadlineMs / 1000)}s` };
}

// ── apt sources fix ──────────────────────────────────────────────────
async function ensureAptSourcesReachable(distro, network, emit) {
  emit({ phase: "configuring-apt", message: "Probing apt mirror reachability…" });

  // Detect codename + sources file format (jammy uses sources.list, noble uses /etc/apt/sources.list.d/ubuntu.sources)
  const probe = await wslBash(distro, `set -e
. /etc/os-release
echo "CODENAME=$VERSION_CODENAME"
if [ -s /etc/apt/sources.list ]; then
  echo "FILE=/etc/apt/sources.list"
elif [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  echo "FILE=/etc/apt/sources.list.d/ubuntu.sources"
else
  echo "FILE=/etc/apt/sources.list"
fi
CUR=$(grep -m1 -oE 'https?://[^ /]+' /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null | head -1 | sed 's/.*: *//') || true
echo "CUR=$CUR"
if [ -n "$CUR" ] && curl -fsI --max-time 5 "$CUR" >/dev/null 2>&1; then
  echo "REACHABLE=1"
else
  echo "REACHABLE=0"
fi`, { timeoutMs: 25_000 });

  if (!probe.ok) return { ok: false, error: "probe failed: " + probe.stderr };

  const env = Object.fromEntries(probe.stdout.split("\n").map(l => {
    const i = l.indexOf("="); return i < 0 ? [l, ""] : [l.slice(0, i), l.slice(i + 1)];
  }));

  if (env.REACHABLE === "1") {
    emit({ phase: "configuring-apt", message: `apt mirror reachable: ${env.CUR}` });
    return { ok: true, mirror: env.CUR, changed: false };
  }

  // Pick a reachable mirror from candidates.
  const candidates = APT_MIRRORS[network] || APT_MIRRORS.global;
  const probeScript = candidates.map(c =>
    `if curl -fsI --max-time 5 "${c}/dists/${env.CODENAME}/Release" >/dev/null 2>&1; then echo "${c}"; exit 0; fi`
  ).join("\n");
  const pick = await wslBash(distro, probeScript + "\nexit 1", { timeoutMs: 35_000 });
  if (!pick.ok || !pick.stdout) return { ok: false, error: "no reachable apt mirror" };
  const mirror = pick.stdout.trim().split(/\r?\n/).pop();

  // Rewrite sources.list (works for jammy). For noble and later, also detect/replace ubuntu.sources.
  const codename = env.CODENAME || "jammy";
  const newList = [
    `deb ${mirror} ${codename} main restricted universe multiverse`,
    `deb ${mirror} ${codename}-updates main restricted universe multiverse`,
    `deb ${mirror} ${codename}-backports main restricted universe multiverse`,
    `deb ${mirror} ${codename}-security main restricted universe multiverse`,
  ].join("\n");

  const w = await wslBash(distro, `set -e
CONTENT='${newList.replace(/'/g, `'\\''`)}'
write_file() {
  local f=$1
  if [ -w "$f" ] || [ ! -e "$f" ]; then echo "$CONTENT" > "$f";
  elif command -v sudo >/dev/null 2>&1; then echo "$CONTENT" | sudo tee "$f" >/dev/null;
  else return 1; fi
}
# Disable any existing deb822 sources (Noble+) so our deb-style takes effect.
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  if [ -w /etc/apt/sources.list.d/ubuntu.sources ]; then
    mv /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list.d/ubuntu.sources.bak 2>/dev/null
  elif command -v sudo >/dev/null 2>&1; then
    sudo mv /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list.d/ubuntu.sources.bak 2>/dev/null
  fi
fi
write_file /etc/apt/sources.list || { echo "no-write-access" >&2; exit 1; }
echo "MIRROR=${mirror}"`, { timeoutMs: 10_000 });

  if (!w.ok) return { ok: false, error: w.stderr || "write failed" };
  emit({ phase: "configuring-apt", message: `apt mirror switched to ${mirror}` });
  return { ok: true, mirror, changed: true };
}

// ── cicy-code install into wsl ────────────────────────────────────────
async function installCicyCodeIntoWsl(distro, hostStagePath, expectVersion, emit) {
  emit({ phase: "installing-cicy-code", message: `Installing cicy-code v${expectVersion} into ${distro}…`, version: expectVersion });

  // Translate Windows path to wsl path.
  const tr = await sh(`wsl -d ${distro} -e wslpath -a "${hostStagePath.replace(/\\/g, "\\\\")}"`, { timeoutMs: 10_000 });
  if (!tr.ok) return { ok: false, error: "wslpath failed: " + tr.stderr };
  const wslPath = tr.stdout.trim();

  const script = `set -eu
mkdir -p $HOME/.local/bin
cp '${wslPath.replace(/'/g, `'\\''`)}' $HOME/.local/bin/cicy-code.new
chmod +x $HOME/.local/bin/cicy-code.new
mv -f $HOME/.local/bin/cicy-code.new $HOME/.local/bin/cicy-code
ACT=$($HOME/.local/bin/cicy-code --version 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+' | head -1 || echo unknown)
printf '%s' "$ACT" > $HOME/.local/bin/cicy-code.version
echo "INSTALLED:$ACT"`;
  const r = await wslBash(distro, script, { timeoutMs: 60_000 });
  if (!r.ok) return { ok: false, error: r.stderr || "install failed" };
  const m = r.stdout.match(/INSTALLED:([0-9.]+)/);
  return { ok: true, version: m ? m[1] : expectVersion };
}

// ── stage path ────────────────────────────────────────────────────────
async function resolveStagePath() {
  const r = await ps(`
$dir = Join-Path $env:APPDATA 'CiCy Desktop\\cicy-code\\wsl-stage'
New-Item -ItemType Directory -Force $dir | Out-Null
Write-Output (Join-Path $dir 'cicy-code-staged')
`, { timeoutMs: 5_000 });
  if (!r.ok) throw new Error("stage path resolution failed");
  return r.stdout.trim();
}

async function cleanupStage(path) {
  await ps(`Remove-Item -Force '${path.replace(/'/g, "''")}' -ErrorAction SilentlyContinue`, { timeoutMs: 5_000 });
}

// ── main flow ─────────────────────────────────────────────────────────
export async function windowsInstall({ onProgress = () => {} } = {}) {
  const emit = (e) => { try { onProgress(e); } catch {} };
  assertRPC();

  // 1. Network detect
  emit({ phase: "detecting", message: "Detecting network…" });
  const network = await detectNetwork();
  emit({ phase: "detecting", message: `Network: ${network}`, network });

  // 2. Latest manifest
  emit({ phase: "checking", message: "Checking latest version…" });
  const mf = await fetchLatestManifest(network);
  if (!mf.ok) throw new Error("manifest fetch failed: " + mf.error);
  const version = mf.version;
  const assetUrl = mf.assets["linux-amd64"];
  if (!assetUrl) throw new Error("manifest has no linux-amd64 asset");
  emit({ phase: "checking", message: `Latest: v${version}`, version, network });

  // 3. Stage download
  const stagePath = await resolveStagePath();
  emit({ phase: "downloading", message: `Downloading cicy-code v${version}…`, version, network });
  const dl = await downloadStaged({ assetUrl, network, dstPath: stagePath });
  if (!dl.ok) throw new Error("download failed: " + dl.error);
  emit({ phase: "downloading", message: `Downloaded ${(dl.size / 1024 / 1024).toFixed(1)} MB`, progress: 1, version });

  try {
    // 4. WSL state
    emit({ phase: "checking-wsl", message: "Checking WSL state…" });
    let wsl = await checkWslStatus();

    // 5. Install WSL if needed
    if (!wsl.installed || !wsl.usableDistro) {
      const ins = await installWsl(network, emit);
      if (!ins.ok) throw new Error("wsl install: " + ins.error);
      // Re-check
      wsl = await checkWslStatus();
      if (!wsl.usableDistro) throw new Error("WSL installed but no usable distro detected — Windows may need a reboot");
      const w = await waitForDistroReady(wsl.usableDistro, emit);
      if (!w.ok) throw new Error(w.error);
    }
    const distro = wsl.usableDistro;
    emit({ phase: "checking-wsl", message: `Using distro: ${distro}` });

    // 6. Apt mirror
    const apt = await ensureAptSourcesReachable(distro, network, emit);
    if (!apt.ok) emit({ phase: "configuring-apt", message: `Warning: apt mirror config failed (${apt.error}), continuing` });

    // 7. Copy + verify
    const r = await installCicyCodeIntoWsl(distro, stagePath, version, emit);
    if (!r.ok) throw new Error("install: " + r.error);

    emit({ phase: "done", message: `Installed v${r.version}`, version: r.version });
    return { ok: true, version: r.version };
  } finally {
    cleanupStage(stagePath).catch(() => {});
  }
}

export function canRunRendererInstall() {
  return typeof window !== "undefined" && typeof window.electronRPC === "function";
}
