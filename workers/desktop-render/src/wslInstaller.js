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
// Module-level emit so every sh/ps/wslBash call can push a `{kind:"log"}`
// event without threading the callback through every helper signature.
// Set by windowsInstall() before any shell call runs.
let _emit = () => {};
function setEmit(fn) { _emit = fn || (() => {}); }

// Trim a multi-line shell payload (cmd, stdout, stderr) into a few short
// log lines. Keeps output readable inside a small in-app log panel.
function summarize(text, { maxLines = 8, maxChars = 600 } = {}) {
  if (!text) return "";
  let s = String(text).replace(/\u0000/g, "").replace(/\r/g, "").trim();
  if (!s) return "";
  if (s.length > maxChars) s = s.slice(0, maxChars) + "…";
  const lines = s.split("\n");
  if (lines.length <= maxLines) return lines.join("\n");
  const head = lines.slice(0, maxLines - 1).join("\n");
  return `${head}\n… (+${lines.length - maxLines + 1} more lines)`;
}

// One-line preview of a long command (PowerShell scripts can be 80+ lines).
function previewCmd(cmd) {
  if (!cmd) return "";
  const oneLine = String(cmd).replace(/\s+/g, " ").trim();
  return oneLine.length > 200 ? oneLine.slice(0, 200) + "…" : oneLine;
}

function assertRPC() {
  if (typeof window === "undefined" || typeof window.electronRPC !== "function") {
    throw new Error("electronRPC unavailable — open this page inside cicy-desktop's homepage window (v2.1.12+)");
  }
}

async function sh(cmd, { timeoutMs = DEFAULT_TIMEOUTS.shellMed, silent = false } = {}) {
  assertRPC();
  if (!silent) _emit({ kind: "log", text: `$ ${previewCmd(cmd)}` });
  // cicy-desktop's main exec_shell handler doesn't honour the timeout_ms
  // hint — child_process.exec runs without a kill timer, so a hung
  // command (e.g. `wsl --install` waiting on Microsoft Store) just blocks
  // forever. Race the RPC against a renderer-side timer so callers can
  // actually fall back. The OS-side child stays around (we have no clean
  // way to signal it from the renderer), but the JS promise resolves so
  // the installer can proceed to the next mirror / strategy.
  const rpcCall = window.electronRPC("exec_shell", { command: cmd, timeout_ms: timeoutMs });
  const r = await Promise.race([
    rpcCall,
    new Promise((resolve) => setTimeout(
      () => resolve({ content: [{ type: "text", text: JSON.stringify({ stdout: "", stderr: `client-side timeout after ${timeoutMs}ms`, exitCode: 124 }) }], isError: true }),
      timeoutMs + 5_000, // grace period over the requested timeout
    )),
  ]);
  // electronRPC returns the raw MCP shape: { content: [{type:"text",text:"<json>"}] }
  // homepage-preload's `tx()` flattens for window.cicy.system.*; raw RPC needs us to parse.
  let parsed = r;
  if (r && r.content) {
    const txt = (r.content || []).map(c => c.text).filter(Boolean).join("");
    try { parsed = JSON.parse(txt); }
    catch { parsed = { ok: true, stdout: txt, stderr: "", exitCode: 0 }; }
  }
  const out = {
    ok: (parsed.exitCode || 0) === 0,
    stdout: clean(parsed.stdout),
    stderr: clean(parsed.stderr),
    code: parsed.exitCode || 0,
  };
  const stdoutSummary = summarize(out.stdout);
  const stderrSummary = summarize(out.stderr);
  if (stdoutSummary) _emit({ kind: "log", text: stdoutSummary });
  if (stderrSummary) _emit({ kind: "log", text: `! ${stderrSummary}` });
  if (!out.ok && !stderrSummary && !stdoutSummary) {
    _emit({ kind: "log", text: `! exit ${out.code}` });
  }
  return out;
}

function clean(s) {
  // PowerShell + WSL bridge output occasionally has stray NULs and CRs.
  return String(s || "").replace(/\u0000/g, "").replace(/\r/g, "");
}

async function ps(scriptText, opts = {}) {
  // Avoid quoting hell by passing the script as base64 through -EncodedCommand.
  // PowerShell expects UTF-16LE base64.
  _emit({ kind: "log", text: `$ powershell:\n${summarize(scriptText, { maxLines: 5, maxChars: 400 })}` });
  // Force PowerShell to emit UTF-8 to stdout/stderr so cicy-desktop's
  // exec_shell (which decodes as UTF-8) doesn't garble Chinese / box-drawing
  // characters from `wsl --list`, `chcp` etc. The chcp call mirrors the same
  // setting at the cmd level for any nested invocations.
  const wrapped =
    `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8;\n` +
    `$OutputEncoding = [System.Text.Encoding]::UTF8;\n` +
    `chcp 65001 > $null;\n` +
    scriptText;
  const utf16 = new Uint16Array(wrapped.length);
  for (let i = 0; i < wrapped.length; i++) utf16[i] = wrapped.charCodeAt(i);
  const b64 = btoa(String.fromCharCode(...new Uint8Array(utf16.buffer)));
  return sh(`powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ${b64}`, { ...opts, silent: true });
}

async function wslBash(distro, script, opts = {}) {
  // Ship the bash script through base64 to avoid Windows quoting hell.
  // Use TextEncoder for proper UTF-8.
  _emit({ kind: "log", text: `$ wsl ${distro}:\n${summarize(script, { maxLines: 5, maxChars: 400 })}` });
  const bytes = new TextEncoder().encode(script);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  const b64 = btoa(bin);
  return sh(`wsl -d ${distro} -- bash -c "echo ${b64} | base64 -d | bash -l"`, { ...opts, silent: true });
}

async function wslExec(distro, args, opts = {}) {
  const escaped = args.map(a => `"${String(a).replace(/"/g, '\\"')}"`).join(" ");
  return sh(`wsl -d ${distro} -e ${escaped}`, opts);
}

// ── network detection ─────────────────────────────────────────────────
async function detectNetwork() {
  // Sequential probe — Start-Job's cold start on Windows is 1–2 s before
  // the script body even runs, which made the previous parallel race
  // routinely time out before either response arrived. Trying baidu
  // first gets cn-network users the fast path; falling back to google
  // distinguishes global vs offline.
  const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
try {
  $r = Invoke-WebRequest 'https://www.baidu.com/' -Method Head -UseBasicParsing -TimeoutSec 4
  if ($r.StatusCode -eq 200) { Write-Output 'cn'; exit 0 }
} catch {}
try {
  $r = Invoke-WebRequest 'https://www.google.com/generate_204' -UseBasicParsing -TimeoutSec 4
  if ($r.StatusCode -eq 204) { Write-Output 'global'; exit 0 }
} catch {}
Write-Output 'unknown'
`, { timeoutMs: 12_000 });
  return r.ok ? r.stdout.trim() : "unknown";
}

// ── manifest fetch ────────────────────────────────────────────────────
function manifestUrls(network) {
  const base = "https://github.com/cicy-ai/cicy-code/releases/latest/download/manifest.json";
  return (network === "cn" || network === "unknown")
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
    return { ok: true, version: m.version, assets: m.assets, sizes: m.sizes || {} };
  } catch (e) {
    return { ok: false, error: "json parse: " + e.message };
  }
}

// ── download with mirror race ────────────────────────────────────────
async function downloadStaged({ assetUrl, network, dstPath, expectSize = 0, expectMin = 1_000_000 }) {
  // 0a. Reuse exact cached file if present and size matches expected.
  if (expectSize && expectSize > 0) {
    const probe = await ps(`
$dst = '${dstPath.replace(/'/g, "''")}'
if (Test-Path $dst) {
  Write-Output ((Get-Item $dst).Length)
} else {
  Write-Output 'NONE'
}
`, { timeoutMs: 5_000 });
    if (probe.ok) {
      const out = probe.stdout.trim();
      if (out !== "NONE") {
        const localSize = parseInt(out, 10) || 0;
        if (localSize === expectSize) {
          _emit({ kind: "log", text: `(reusing cached download: ${(localSize / 1024 / 1024).toFixed(1)} MB)` });
          return { ok: true, size: localSize, url: dstPath, path: dstPath, reused: true };
        }
        _emit({ kind: "log", text: `(cached file size ${localSize} != expected ${expectSize}, redownloading)` });
        await ps(`Remove-Item -Force '${dstPath.replace(/'/g, "''")}' -ErrorAction SilentlyContinue`,
          { timeoutMs: 5_000 });
      }
    }

    // 0b. Look for any sibling staged file in the same directory whose
    //     size matches expected (e.g. an unversioned `cicy-code-staged`
    //     left over from earlier installer versions). If we find one,
    //     rename it to the new versioned path — saves re-downloading
    //     ~19 MB when the binary already exists from a prior install.
    const stage = await ps(`
$dst = '${dstPath.replace(/'/g, "''")}'
$dir = Split-Path $dst
$want = ${expectSize}
if (Test-Path $dir) {
  Get-ChildItem -File $dir -ErrorAction SilentlyContinue | Where-Object { $_.Length -eq $want } | Select-Object -First 1 -ExpandProperty FullName
}
`, { timeoutMs: 5_000 });
    if (stage.ok && stage.stdout.trim()) {
      const found = stage.stdout.trim();
      _emit({ kind: "log", text: `(found existing ${expectSize}-byte staged file: ${found.split(/[\\\/]/).pop()}, reusing)` });
      const mv = await ps(`
$src = '${found.replace(/'/g, "''")}'
$dst = '${dstPath.replace(/'/g, "''")}'
# dst might be locked by an old download process — drop it then move.
try {
  Move-Item -Force $src $dst
} catch {
  Remove-Item -Force $dst -ErrorAction SilentlyContinue
  Move-Item -Force $src $dst
}
`, { timeoutMs: 5_000 });
      if (mv.ok) {
        return { ok: true, size: expectSize, url: dstPath, path: dstPath, reused: true };
      }
    }
  }

  // 1. Build URL list with mirrors first if cn-ish.
  // network=="unknown" likely means we're behind a firewall / GFW that
  // blocks both google and baidu probes — better to lead with mirrors
  // than with the (probably-slow) GitHub direct URL.
  const urls = (network === "cn" || network === "unknown")
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
    // Download into a per-attempt .part file so a stale earlier download
    // process that still has a write lock on `dst` can't block us.
    // After download completes, try to atomically place it at dst; if
    // dst is locked we just use the .part path directly — the rest of
    // the installer doesn't care about the filename, only the bytes.
    const partPath = dstPath + `.part-${Date.now()}-${i}`;
    const r = await ps(`
$ProgressPreference = 'SilentlyContinue'
$dst  = '${dstPath.replace(/'/g, "''")}'
$part = '${partPath.replace(/'/g, "''")}'
$url  = '${url.replace(/'/g, "''")}'
New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
try {
  Invoke-WebRequest -Uri $url -OutFile $part -UseBasicParsing -TimeoutSec 120
  $size = (Get-Item $part).Length
  $finalPath = $part
  try {
    Move-Item -Force $part $dst
    $finalPath = $dst
  } catch {
    try {
      Remove-Item -Force $dst -ErrorAction Stop
      Move-Item -Force $part $dst
      $finalPath = $dst
    } catch {
      # dst still locked; keep the .part path. The bytes are correct.
    }
  }
  Write-Output "OK $size $finalPath"
} catch {
  Remove-Item -Force $part -ErrorAction SilentlyContinue
  Write-Output "FAIL $($_.Exception.Message)"
  exit 1
}
`, { timeoutMs: DEFAULT_TIMEOUTS.download });
    if (r.ok && r.stdout.startsWith("OK ")) {
      const m = r.stdout.match(/OK (\d+) (.+)/);
      const size = parseInt(m?.[1] || "0", 10);
      const actualPath = m?.[2]?.trim() || dstPath;
      if (size < expectMin) continue;
      // If manifest gave us an exact size, reject mismatched bytes — a
      // truncated or HTML error page from a flaky mirror wouldn't trip
      // expectMin but would fail size equality.
      if (expectSize && expectSize > 0 && size !== expectSize) {
        _emit({ kind: "log", text: `(downloaded ${size} != expected ${expectSize}, trying next mirror)` });
        continue;
      }
      return { ok: true, size, url, path: actualPath };
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
  // Helper: a "failed" install often actually succeeded — `wsl --install`
  // can register the distro then hang on Microsoft Store metadata, or
  // report ERROR_ALREADY_EXISTS because a previous attempt left it
  // installed. Always re-check the distro list before declaring failure.
  const verifyDistroPresent = async () => {
    const wsl = await checkWslStatus();
    if (wsl.usableDistro) {
      emit({ phase: "installing-wsl", message: `Ubuntu already installed (${wsl.usableDistro})`, status: "done" });
      return true;
    }
    return false;
  };

  // China / restricted networks: jump straight to direct rootfs import.
  // `wsl --install -d Ubuntu` (with or without --web-download) blocks for
  // 10+ minutes hitting the Microsoft Store / DistributionInfo.json from
  // raw.githubusercontent.com — both effectively unreachable from CN.
  // The rootfs path uses NJU/USTC/aliyun mirrors which are fast.
  if (network === "cn" || network === "unknown") {
    if (await verifyDistroPresent()) return { ok: true, method: "already-installed" };
    emit({ phase: "installing-wsl", message: "Importing Ubuntu rootfs from China mirrors (faster than Microsoft Store)…" });
    const ir = await importUbuntuFromRootfs(network, emit);
    if (ir.ok) return { ok: true, method: "rootfs-import" };
    if (await verifyDistroPresent()) return { ok: true, method: "already-installed" };
    // Fall through to the Microsoft Store path if mirrors all failed
    // (rare, but covers the case where NJU/USTC are also blocked).
    emit({ phase: "installing-wsl", message: "Rootfs import failed, falling back to Microsoft Store…" });
  }

  emit({ phase: "installing-wsl", message: "Installing WSL2 + Ubuntu (5–10 min, requires admin)…" });
  // global network: try Microsoft Store install first (uses GitHub
  // direct), web-download if it fails.
  const r = await sh(`wsl --install --no-launch -d Ubuntu`, { timeoutMs: 8 * 60_000 });
  if (r.ok || (await verifyDistroPresent())) return { ok: true };

  emit({ phase: "installing-wsl", message: "Microsoft Store install failed, retrying with --web-download…" });
  const r2 = await sh(`wsl --install --web-download --no-launch -d Ubuntu`, { timeoutMs: 8 * 60_000 });
  if (r2.ok || (await verifyDistroPresent())) return { ok: true };

  // Fallback: download Ubuntu rootfs directly from cloud-images mirror
  // and `wsl --import`. Works in restricted networks (Myanmar, CN) where
  // raw.githubusercontent.com (which Microsoft fetches DistributionInfo
  // from) is unreachable so `wsl --install -d <name>` cannot proceed.
  // (Only reached for global network — cn/unknown already tried this
  // path first above.)
  if (network !== "cn" && network !== "unknown") {
    emit({ phase: "installing-wsl", message: "Falling back to direct rootfs import…" });
    const ir = await importUbuntuFromRootfs(network, emit);
    if (ir.ok) return { ok: true, method: "rootfs-import" };
    return { ok: false, error: r2.stderr || ir.error || `wsl --install exit ${r2.code}` };
  }
  return { ok: false, error: r2.stderr || `wsl --install exit ${r2.code}` };
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
# Per-attempt unique temp filename so a stale earlier download process
# (which still holds a write lock on the previous tar.gz) can't make us
# fail with "正由另一进程使用". The file is removed at the end.
$tar = Join-Path $env:TEMP ("ubuntu-jammy-wsl-" + [Guid]::NewGuid().ToString("N").Substring(0,8) + ".tar.gz")
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
if (-not $ok) { Remove-Item -Force $tar -ErrorAction SilentlyContinue; Write-Output "ERR no mirror"; exit 1 }
& wsl --import Ubuntu $dst $tar --version 2
$importExit = $LASTEXITCODE
Remove-Item -Force $tar -ErrorAction SilentlyContinue
if ($importExit -ne 0) { Write-Output ("IMPORT_FAIL " + $importExit); exit 1 }
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

# ── 1) Create a non-root default user ────────────────────────────────
# cicy-code refuses to start as root after bootstrap, so a fresh
# Ubuntu image (which has only root) can't host it. Create a "cicy"
# user with passwordless sudo and the same UID 1000 convention every
# Ubuntu setup uses. Idempotent — skips if already there.
if ! id cicy >/dev/null 2>&1; then
  useradd -m -s /bin/bash cicy
  usermod -aG sudo cicy
  printf 'cicy ALL=(ALL) NOPASSWD:ALL\\n' > /etc/sudoers.d/90-cicy
  chmod 440 /etc/sudoers.d/90-cicy
fi

# ── 2) Install the binary into a system path ─────────────────────────
DST=/usr/local/bin/cicy-code
sudo_or_root() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }
TMP=$(mktemp)
cp '${wslPath.replace(/'/g, `'\\''`)}' "$TMP"
chmod +x "$TMP"
sudo_or_root mv -f "$TMP" "$DST"
ACT=$("$DST" --version 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+' | head -1 || echo unknown)
sudo_or_root sh -c "printf '%s' \\"$ACT\\" > /usr/local/bin/cicy-code.version" || true

# ── 3) Mirror the binary into both root's and cicy's $HOME/.local/bin
# cicy-desktop's main process probes $HOME/.local/bin/cicy-code under
# the default user to decide if the install completed. Symlink there
# (rather than copying) keeps everything pointing at /usr/local/bin so
# upgrades stay atomic.
for U in root cicy; do
  H=$(eval echo "~$U")
  [ -d "$H" ] || continue
  mkdir -p "$H/.local/bin"
  ln -sfn "$DST" "$H/.local/bin/cicy-code"
  printf '%s' "$ACT" > "$H/.local/bin/cicy-code.version"
  chown -R "$U":"$U" "$H/.local" 2>/dev/null || true
done

# ── 4) Set up cicy user's PATH for npm-global and node binaries.
# When cicy-code spawns agent CLIs (claude/codex/opencode), it runs
# them through bash and expects to find them on PATH. Without this:
#   - npm install -g installs to ~/.npm-global/bin but PATH doesn't
#     include it → cicy-code keeps reinstalling on every agent start
#   - node downloaded to ~/.local/node-vX.Y.Z is not on PATH → npm
#     itself isn't found, breaking the whole install chain
# We append (not prepend) so /usr/local/bin still wins for cicy-code.
H=/home/cicy
[ -f "$H/.bashrc" ] || touch "$H/.bashrc"
if ! grep -q 'CICY_PATH_SETUP' "$H/.bashrc" 2>/dev/null; then
  cat >> "$H/.bashrc" <<'BASHRC'
# CICY_PATH_SETUP — added by cicy-desktop installer
export PATH="$HOME/.npm-global/bin:$PATH"
# Add the most recent node-vX.Y.Z bin dir under ~/.local (cicy-code
# downloads node here when bootstrapping a fresh Ubuntu).
for d in "$HOME"/.local/node-v*-linux-x64/bin; do
  [ -d "$d" ] && export PATH="$d:$PATH"
done
BASHRC
fi
# Profile too — login shells (the [boot] command runs as su - cicy).
[ -f "$H/.profile" ] || touch "$H/.profile"
if ! grep -q 'CICY_PATH_SETUP' "$H/.profile" 2>/dev/null; then
  cat >> "$H/.profile" <<'PROFILE'
# CICY_PATH_SETUP — added by cicy-desktop installer
export PATH="$HOME/.npm-global/bin:$PATH"
for d in "$HOME"/.local/node-v*-linux-x64/bin; do
  [ -d "$d" ] && export PATH="$d:$PATH"
done
PROFILE
fi
chown cicy:cicy "$H/.bashrc" "$H/.profile" 2>/dev/null || true

# Pre-create npm-global so \`npm install -g\` works without --prefix.
sudo -u cicy bash -lc 'mkdir -p $HOME/.npm-global/{bin,lib} && npm config set prefix $HOME/.npm-global 2>/dev/null || true' >/dev/null 2>&1 || true

# ── 5) Make cicy the default WSL user ────────────────────────────────
# Without this, every \`wsl -d Ubuntu\` lands as root and cicy-code
# refuses to run. /etc/wsl.conf is read at distro boot, so the caller
# must \`wsl --terminate <distro>\` for this to take effect.
# systemd=false is intentional: enabling systemd in WSL2 has caused
# distro-boot failures (E_FAIL with corrupted ext4.vhdx state) in
# field reports. We use [boot] command to autostart cicy-code instead.
if ! grep -q '^default=cicy' /etc/wsl.conf 2>/dev/null; then
  cat > /etc/wsl.conf <<'EOF'
[user]
default=cicy

[boot]
systemd=false
command=su - cicy -c 'pgrep -f cicy-code >/dev/null 2>&1 || setsid -f $HOME/.local/bin/cicy-code </dev/null >>$HOME/.cicy-code.log 2>&1'
EOF
fi

echo "INSTALLED:$ACT"`;
  const r = await wslBash(distro, script, { timeoutMs: 60_000 });
  if (!r.ok) return { ok: false, error: r.stderr || "install failed" };
  const m = r.stdout.match(/INSTALLED:([0-9.]+)/);
  return { ok: true, version: m ? m[1] : expectVersion };
}

// ── stage path ────────────────────────────────────────────────────────
// Each version gets its own filename so a stale download from an older
// release doesn't get reused as if it were the new binary.
async function resolveStagePath(version) {
  const safe = String(version || "unknown").replace(/[^A-Za-z0-9._-]/g, "_");
  const r = await ps(`
$dir = Join-Path $env:APPDATA 'CiCy Desktop\\cicy-code\\wsl-stage'
New-Item -ItemType Directory -Force $dir | Out-Null
Write-Output (Join-Path $dir 'cicy-code-v${safe}-staged')
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
  setEmit(emit);
  assertRPC();

  // 1. Network detect
  emit({ phase: "detecting", message: "Detecting network…" });
  const network = await detectNetwork();
  emit({ phase: "detecting", message: `Network: ${network}`, network });

  // 1b. Disk-space precheck on C: (or whatever drive holds %APPDATA% +
  // the WSL distro vhdx). The whole install pipeline writes ~500MB of
  // staged files plus an ext4.vhdx that grows as agent CLIs install
  // their own deps. If the user is already near 0 free, bail out
  // early with a clear error instead of failing mid-install with
  // confusing ENOSPC / vhdx I/O errors.
  {
    const REQUIRED_GB = 5;
    const dr = await ps(
      `$d = Get-PSDrive C; Write-Output ([math]::Round($d.Free/1GB,2))`,
      { timeoutMs: 5_000 }
    );
    const freeGb = dr.ok ? parseFloat(dr.stdout.trim()) : NaN;
    if (Number.isFinite(freeGb) && freeGb < REQUIRED_GB) {
      throw new Error(`LOW_DISK_SPACE:${freeGb}:${REQUIRED_GB}`);
    }
    emit({ phase: "detecting", message: `Free space: ${Number.isFinite(freeGb) ? freeGb + ' GB' : 'unknown'}`, network });
  }

  // 2. Latest manifest
  emit({ phase: "checking", message: "Checking latest version…" });
  const mf = await fetchLatestManifest(network);
  if (!mf.ok) throw new Error("manifest fetch failed: " + mf.error);
  const version = mf.version;
  const assetUrl = mf.assets["linux-amd64"];
  if (!assetUrl) throw new Error("manifest has no linux-amd64 asset");
  const expectSize = (mf.sizes && mf.sizes["linux-amd64"]) || 0;

  // Resolve the staging path early so we can check the local cache before
  // making any network calls — if we already have a same-size copy of the
  // target binary on disk, no need to ping GitHub at all.
  const stagePath = await resolveStagePath(version);
  let cachedHit = false;
  if (expectSize > 0) {
    const probe = await ps(`
$dst = '${stagePath.replace(/'/g, "''")}'
if (Test-Path $dst) { Write-Output ((Get-Item $dst).Length) } else { Write-Output 'NONE' }
`, { timeoutMs: 5_000 });
    if (probe.ok && probe.stdout.trim() !== "NONE") {
      cachedHit = parseInt(probe.stdout.trim(), 10) === expectSize;
    }
  }

  // Guard: verify the asset actually exists on the server before declaring
  // a new version is available. Prevents the case where manifest.json was
  // uploaded first but the binary hasn't finished uploading yet (Release
  // workflow still in progress). Skip this when we have a verified cached
  // copy — there's nothing to fetch.
  if (!cachedHit) {
    emit({ phase: "checking", message: `Verifying release v${version}…`, status: "running", version });
    const checkUrls = (network === "cn" || network === "unknown")
      ? [...GH_MIRRORS.map(m => m + assetUrl), assetUrl]
      : [assetUrl, ...GH_MIRRORS.map(m => m + assetUrl)];
    const checkScript = `
$ProgressPreference='SilentlyContinue'
$urls = '${JSON.stringify(checkUrls).replace(/'/g,"''")}' | ConvertFrom-Json
foreach ($u in $urls) {
  try {
    $r = Invoke-WebRequest -Uri $u -Method Head -UseBasicParsing -TimeoutSec 6
    if ($r.StatusCode -lt 400) { Write-Output "OK"; exit 0 }
  } catch { continue }
}
Write-Output "ERR"; exit 1`;
    const cr = await ps(checkScript, { timeoutMs: 30_000 });
    if (!cr.ok || !cr.stdout.includes("OK")) {
      // Stable, identifiable error tag — banner can localize it via t() if
      // it sees this prefix.
      const e = new Error(`RELEASE_NOT_READY:${version}`);
      e.code = "RELEASE_NOT_READY";
      e.version = version;
      throw e;
    }
  }

  emit({ phase: "checking", message: `Latest: v${version}`, version, network });

  // ── 3 + 4–5: download cicy-code AND prepare WSL in parallel ──────
  // The two are independent: download is pure network I/O, WSL setup is
  // a local Windows operation. Running them concurrently can cut total
  // install time by the duration of whichever finishes first.
  // stagePath was resolved earlier (during the cache pre-check).
  emit({ phase: "downloading", message: `Downloading cicy-code v${version}…`, status: "running", version, network });
  emit({ phase: "checking-wsl", message: "Checking WSL state…", status: "running" });

  let actualBinaryPath = stagePath;
  const downloadTask = (async () => {
    const dl = await downloadStaged({ assetUrl, network, dstPath: stagePath, expectSize });
    if (!dl.ok) throw new Error("download failed: " + dl.error);
    // dl.path is the actual on-disk filename — usually equals stagePath
    // but may be a `.part-*` fallback if stagePath was locked by a stale
    // download process from an earlier interrupted install.
    if (dl.path) actualBinaryPath = dl.path;
    emit({
      phase: "downloading",
      message: dl.reused
        ? `Using cached ${(dl.size / 1024 / 1024).toFixed(1)} MB`
        : `Downloaded ${(dl.size / 1024 / 1024).toFixed(1)} MB`,
      status: "done",
      progress: 1,
      version,
    });
    return dl;
  })();

  const wslTask = (async () => {
    let wsl = await checkWslStatus();
    if (!wsl.installed || !wsl.usableDistro) {
      const ins = await installWsl(network, emit);
      if (!ins.ok) throw new Error("wsl install: " + ins.error);
      wsl = await checkWslStatus();
      if (!wsl.usableDistro) throw new Error("WSL installed but no usable distro detected — Windows may need a reboot");
      const w = await waitForDistroReady(wsl.usableDistro, emit);
      if (!w.ok) throw new Error(w.error);
    }
    emit({ phase: "checking-wsl", message: `Using distro: ${wsl.usableDistro}`, status: "done" });
    return wsl;
  })();

  // Promise.all rejects on the first error, but we still want both
  // failures surfaced — `allSettled` lets us emit error status for each
  // failing branch before re-throwing.
  const [dlRes, wslRes] = await Promise.allSettled([downloadTask, wslTask]);
  if (dlRes.status === "rejected") {
    emit({ phase: "downloading", message: dlRes.reason?.message || String(dlRes.reason), status: "error" });
  }
  if (wslRes.status === "rejected") {
    emit({ phase: "checking-wsl", message: wslRes.reason?.message || String(wslRes.reason), status: "error" });
  }
  if (dlRes.status === "rejected") throw dlRes.reason;
  if (wslRes.status === "rejected") throw wslRes.reason;
  const wsl = wslRes.value;
  const distro = wsl.usableDistro;

  // 6. Apt mirror — depends on WSL being ready (not on cicy-code download).
  const apt = await ensureAptSourcesReachable(distro, network, emit);
  if (!apt.ok) emit({ phase: "configuring-apt", message: `Warning: apt mirror config failed (${apt.error}), continuing` });

  // 7. Copy + verify — depends on BOTH download and WSL being ready.
  const r = await installCicyCodeIntoWsl(distro, actualBinaryPath, version, emit);
  if (!r.ok) throw new Error("install: " + r.error);

  // 8. Reload the distro so the new /etc/wsl.conf [user] default=cicy
  //    takes effect. Without this, subsequent `wsl -d <distro>` calls
  //    still land as root and cicy-code refuses to start. Then start
  //    cicy-code under the new default user so :8008 is up by the time
  //    the UI flips to "uptodate".
  emit({ phase: "starting", message: `Restarting ${distro} so default user takes effect…`, status: "running" });
  // Make Ubuntu the *default* distro so `wsl.exe -e bash` (no -d) — used
  // by cicy-desktop's main process to read $HOME/cicy-ai/global.json for
  // the auto-login token — lands in Ubuntu, not docker-desktop (which
  // ships without bash). This is mandatory for the "open in browser"
  // flow to inject the api_token query param.
  await sh(`wsl --set-default ${distro}`, { timeoutMs: 10_000 });
  await sh(`wsl --terminate ${distro}`, { timeoutMs: 15_000 });
  // First post-terminate command takes a few seconds while wsl service
  // re-initialises — that's also when the new wsl.conf is parsed.
  const startR = await sh(
    `wsl -d ${distro} -- bash -lc "pgrep -f cicy-code >/dev/null 2>&1 || setsid -f /usr/local/bin/cicy-code </dev/null >>~/.cicy-code.log 2>&1 ; sleep 1 ; pgrep -fa cicy-code | head -1"`,
    { timeoutMs: 30_000 }
  );
  emit({ phase: "starting", message: startR.ok ? "cicy-code started" : "started (verify health)", status: "done" });

  emit({ phase: "done", message: `Installed v${r.version}`, version: r.version, status: "done" });
  return { ok: true, version: r.version };
  // Note: stagePath is intentionally not deleted — downloadStaged checks
  // size before reuse, so cached files speed up retries / repeat installs.
}

export function canRunRendererInstall() {
  return typeof window !== "undefined" && typeof window.electronRPC === "function";
}
