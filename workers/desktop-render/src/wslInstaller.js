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
//   installing-deps     — apt-install unzip/jq/CJK fonts so cicy-code's
//                          first-launch baseTools check passes instantly
//   picking-agents      — pause for user to choose claude / codex / opencode
//                          (no pre-install — cicy-code installs lazily on first
//                           agent create with the picked set passed via --agents)
//   starting            — boot cicy-code --agents=<csv> + wait for /api/health 200
//   mounting-files      — Desktop shortcut + Quick Access pin to WSL home
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
  wslBoot:    180_000,
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
  // silent: skip the log preview entirely — used by tight polling loops
  // (e.g. download progress) where every call would otherwise spam the
  // shell log with a 5-line script preview per tick.
  if (!opts.silent) {
    _emit({ kind: "log", text: `$ powershell:\n${summarize(scriptText, { maxLines: 5, maxChars: 400 })}` });
  }
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

// ── fast cmd.exe / curl.exe helpers ─────────────────────────────────────
// On some Windows machines AV real-time scanning throttles powershell.exe
// startup to 30–60 s, which blows past every install-step timeout. cmd.exe
// and curl.exe (native since Win10 1803) start instantly, so all network
// fetches and simple file ops go through them instead of PowerShell.

// Run a cmd.exe builtin. exec_shell already runs inside a shell, so the
// nested `cmd /d /c "…"` is unwrapped by cmd's /s parsing (verified).
async function cmdExec(cmdLine, opts = {}) {
  if (!opts.silent) _emit({ kind: "log", text: `$ cmd: ${previewCmd(cmdLine)}` });
  return sh(`cmd /d /c "${cmdLine}"`, { ...opts, silent: true });
}

// File size in bytes, or -1 if the file is missing.
async function fileSize(path) {
  const p = path.replace(/"/g, "");
  const r = await cmdExec(
    `if exist "${p}" (for %I in ("${p}") do @echo SZ=%~zI) else (echo NONE)`,
    { timeoutMs: 12_000, silent: true },
  );
  const m = (r.stdout || "").match(/SZ=(\d+)/);
  return m ? parseInt(m[1], 10) : -1;
}

async function makeDir(path) {
  const p = path.replace(/"/g, "");
  return cmdExec(`if not exist "${p}" mkdir "${p}"`, { timeoutMs: 12_000, silent: true });
}

async function delFile(path) {
  const p = path.replace(/"/g, "");
  return cmdExec(`del /f /q "${p}" 2>nul & exit /b 0`, { timeoutMs: 12_000, silent: true });
}

// Fetch text from the first reachable URL via curl.exe. { ok, body, url }.
async function curlText(urls, { connectTimeout = 10, maxTime = 25, timeoutMs = 35_000 } = {}) {
  for (const u of (Array.isArray(urls) ? urls : [urls])) {
    _emit({ kind: "log", text: `$ curl ${previewCmd(u)}` });
    const r = await sh(
      `curl.exe -fsSL --connect-timeout ${connectTimeout} --max-time ${maxTime} "${u}"`,
      { timeoutMs, silent: true },
    );
    if (r.ok && r.stdout && r.stdout.trim()) return { ok: true, body: r.stdout.trim(), url: u };
    _emit({ kind: "log", text: `! curl miss (${u.split("/")[2] || u})` });
  }
  return { ok: false };
}

// Order URLs fastest-first via sequential curl HEAD probes (each starts
// instantly, so even 4 probes finish in a few seconds). Unreachable URLs
// drop to the back rather than being removed — they stay as last-resort.
async function curlOrder(urls, { connectTimeout = 8, maxTime = 14, timeoutMs = 22_000 } = {}) {
  const timed = [];
  for (const u of urls) {
    const r = await sh(
      `curl.exe -sI -o NUL --connect-timeout ${connectTimeout} --max-time ${maxTime} -w "%{http_code} %{time_total}" "${u}"`,
      { timeoutMs, silent: true },
    );
    const m = (r.stdout || "").trim().match(/(\d{3})\s+([\d.]+)/);
    const code = m ? parseInt(m[1], 10) : 0;
    if (r.ok && code >= 200 && code < 400) timed.push({ u, t: parseFloat(m[2]) || 999 });
  }
  timed.sort((a, b) => a.t - b.t);
  const ok = timed.map((x) => x.u);
  return ok.length ? [...ok, ...urls.filter((u) => !ok.includes(u))] : urls;
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
// Cached for the page session so a retry resumes instantly instead of
// re-spending ~30s on probes. Only a confident cn/global result is cached;
// "unknown" (likely a transient probe failure) is always re-tried.
let _cachedNetwork = null;
// Agent selection cached for the page session (resume path — see picking-agents).
let _cachedAgents = null;
async function detectNetwork() {
  if (_cachedNetwork) return _cachedNetwork;
  // curl.exe HEAD probes — start instantly (powershell.exe can take 30–60 s
  // to spawn under AV scanning). baidu first → cn fast path; google fallback
  // distinguishes global vs offline.
  // Generous --max-time: on AV-heavy machines curl's own TLS+spawn can take
  // 8–12 s even for a fast endpoint. Too tight a limit aborts mid-handshake
  // and reports http_code 000 (looks like "no network").
  const cn = await sh(
    `curl.exe -sI -o NUL --connect-timeout 8 --max-time 14 -w "%{http_code}" "https://www.baidu.com/"`,
    { timeoutMs: 22_000, silent: true },
  );
  if (cn.ok && /^2\d\d/.test((cn.stdout || "").trim())) return (_cachedNetwork = "cn");
  const gl = await sh(
    `curl.exe -sI -o NUL --connect-timeout 8 --max-time 14 -w "%{http_code}" "https://www.google.com/generate_204"`,
    { timeoutMs: 22_000, silent: true },
  );
  if (gl.ok && /^20[04]/.test((gl.stdout || "").trim())) return (_cachedNetwork = "global");
  return "unknown";
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
  const r = await curlText(urls, { connectTimeout: 10, maxTime: 20, timeoutMs: 30_000 });
  if (!r.ok) return { ok: false, error: "no reachable manifest" };
  try {
    const m = JSON.parse(r.body);
    if (!m.version || !m.assets) return { ok: false, error: "manifest malformed" };
    return { ok: true, version: m.version, assets: m.assets, sizes: m.sizes || {} };
  } catch (e) {
    return { ok: false, error: "json parse: " + e.message };
  }
}

// ── download with mirror race ────────────────────────────────────────
async function downloadStaged({ assetUrl, network, dstPath, expectSize = 0, expectMin = 1_000_000 }) {
  // 0. Reuse exact cached file if present and size matches expected.
  if (expectSize && expectSize > 0) {
    const localSize = await fileSize(dstPath);
    if (localSize === expectSize) {
      _emit({ kind: "log", text: `(reusing cached download: ${(localSize / 1024 / 1024).toFixed(1)} MB)` });
      return { ok: true, size: localSize, url: dstPath, path: dstPath, reused: true };
    }
    if (localSize > 0) {
      _emit({ kind: "log", text: `(cached file size ${localSize} != expected ${expectSize}, redownloading)` });
      await delFile(dstPath);
    }
  }

  // 1. Build URL list with mirrors first if cn-ish.
  // network=="unknown" likely means we're behind a firewall / GFW that
  // blocks both google and baidu probes — better to lead with mirrors
  // than with the (probably-slow) GitHub direct URL.
  const urls = (network === "cn" || network === "unknown")
    ? [...GH_MIRRORS.map(m => m + assetUrl), assetUrl]
    : [assetUrl, ...GH_MIRRORS.map(m => m + assetUrl)];

  // 2. Order mirrors fastest-first (curl HEAD probes).
  const ordered = await curlOrder(urls);

  // 3. Sequential download from fastest, fall through on failure.
  await makeDir(dstPath.replace(/[\\\/][^\\\/]*$/, ""));
  for (const url of ordered) {
    // Download into a per-attempt .part file so a stale earlier download
    // process that still has a write lock on `dst` can't block us.
    const partPath = dstPath + `.part-${Date.now()}`;
    _emit({ kind: "log", text: `$ curl ↓ ${previewCmd(url)}` });
    const dl = await sh(
      `curl.exe -fL --connect-timeout 15 --max-time 600 -o "${partPath}" "${url}"`,
      { timeoutMs: DEFAULT_TIMEOUTS.download, silent: true },
    );
    if (!dl.ok) { await delFile(partPath); continue; }
    const size = await fileSize(partPath);
    if (size < expectMin) { await delFile(partPath); continue; }
    // If manifest gave us an exact size, reject mismatched bytes — a
    // truncated or HTML error page from a flaky mirror wouldn't trip
    // expectMin but would fail size equality.
    if (expectSize && expectSize > 0 && size !== expectSize) {
      _emit({ kind: "log", text: `(downloaded ${size} != expected ${expectSize}, trying next mirror)` });
      await delFile(partPath);
      continue;
    }
    // Atomically place the .part at dst; if dst is locked, keep the .part
    // path — the rest of the installer only cares about the bytes.
    const mv = await cmdExec(
      `move /y "${partPath}" "${dstPath}"`,
      { timeoutMs: 12_000, silent: true },
    );
    const finalPath = mv.ok ? dstPath : partPath;
    return { ok: true, size, url, path: finalPath };
  }
  return { ok: false, error: "all mirrors failed" };
}

// ── WSL detection ─────────────────────────────────────────────────────
async function checkWslStatus() {
  const status = await sh("wsl --status", { timeoutMs: 20_000 });
  if (!status.ok) return { installed: false, supported: true };

  // Detect default version
  const m = status.stdout.match(/Default Version:\s*(\d)/i);
  const defaultVer = m ? parseInt(m[1], 10) : 2;

  const list = await sh("wsl -l -v", { timeoutMs: 20_000 });
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

// ── pick install drive ─────────────────────────────────────────────────
// Default WSL install path (Microsoft Store + `wsl --install -d Ubuntu`)
// always lands the ext4.vhdx under %LOCALAPPDATA%, which is on C:. On
// machines where C: is a small SSD (typical OEM laptops with separate D:
// data drives), this fills the system drive within months as Docker
// images, agent CLIs, and code accumulate inside WSL.
//
// Smart-pick algorithm — we score each fixed-NTFS drive and pick the
// winner, rather than blindly taking max-free-space. The score combines:
//
//   1. Non-system bonus (+50 GB equivalent). Prefer D:/E:/etc. over C:
//      when they have meaningful headroom — keeps the system drive
//      lean and avoids the "Windows can't update because C: is full"
//      class of bugs.
//   2. Absolute free GB. Bigger is better; this is the dominant signal
//      once the bonus is applied.
//   3. SSD bonus (+30 GB equivalent). Win32_PhysicalMedia distinguishes
//      SSD (MediaType=4) from HDD (MediaType=3). WSL on SSD is several
//      multiples faster for compile / docker-build workloads.
//
// A drive must have ≥10 GB free to be considered. Selection must be
// bulletproof — it gates the whole install and the user expects zero manual
// drive picking. Two rules learned the hard way:
//   1. The Storage module (Get-PhysicalDisk / Get-Partition) is NEVER on the
//      critical path. It can be slow or hang (RAID, VMs, a wedged Storage
//      service); a hang there used to time out the whole picker → bogus
//      "C: 0 GB" fallback → install aborted with a 31 GB D: sitting right
//      there. SSD detection is now a separate, optional scoring bonus.
//   2. Drives are enumerated via two independent mechanisms (CIM, then
//      wmic) so one flaky/slow shell can't sink selection. wmic stays
//      responsive even on a PowerShell wedged after a botched WSL import.
// Install path is always `<drive>:\CiCy\WSL\Ubuntu\` — a visible top-level
// location, not buried in AppData.
async function pickInstallDrive() {
  const MIN_GB = 10;
  const drives = await enumerateFixedNtfsDrives();

  if (drives === null) {
    // Both enumeration mechanisms failed (transiently wedged shell). Don't
    // hard-fail a likely-installable machine on a flaky probe — proceed
    // optimistically on C:; the rootfs import surfaces a real ENOSPC if
    // space is genuinely lacking.
    return { letter: "C", freeGb: 0, totalGb: 0, isSSD: false, all: [],
             isSystemDrive: true, probeFailed: true, installDir: `C:\\CiCy\\WSL\\Ubuntu` };
  }

  const ssd = await detectSsdLetters(); // best-effort tiebreaker, never blocks
  const scored = drives
    .map((d) => {
      const isNonSystem = d.Letter !== "C";
      const isSSD = ssd.has(d.Letter);
      // Non-system bonus (50) > SSD bonus (30): prefer a roomy data drive
      // over the system SSD when both are comparable.
      return { ...d, IsSSD: isSSD, IsNonSystem: isNonSystem,
               Score: Math.round((d.FreeGB + (isNonSystem ? 50 : 0) + (isSSD ? 30 : 0)) * 10) / 10 };
    })
    .filter((d) => d.FreeGB >= MIN_GB)
    .sort((a, b) => b.Score - a.Score || b.FreeGB - a.FreeGB);

  if (scored.length === 0) {
    // Enumeration worked but no fixed NTFS drive has ≥ MIN_GB free. Genuine
    // low disk — report C:'s actual free space in the error.
    const cFree = (drives.find((d) => d.Letter === "C") || {}).FreeGB || 0;
    return { letter: "C", freeGb: cFree, totalGb: 0, isSSD: false, all: [],
             isSystemDrive: true, lowDisk: true, installDir: `C:\\CiCy\\WSL\\Ubuntu` };
  }

  const best = scored[0];
  return {
    letter: best.Letter,
    freeGb: best.FreeGB,
    totalGb: best.TotalGB,
    isSSD: best.IsSSD,
    score: best.Score,
    all: scored,            // objects keep .Letter/.FreeGB for downstream checks
    isSystemDrive: best.Letter === "C",
    installDir: `${best.Letter}:\\CiCy\\WSL\\Ubuntu`,
  };
}

// Enumerate fixed (DriveType=3) NTFS drives as [{Letter, FreeGB, TotalGB}].
// Returns null only when BOTH mechanisms fail to produce any answer.
async function enumerateFixedNtfsDrives() {
  const toObj = (letter, freeBytes, sizeBytes) => ({
    Letter: String(letter).replace(":", "").trim(),
    FreeGB: Math.round((Number(freeBytes) / 1073741824) * 10) / 10,
    TotalGB: Math.round((Number(sizeBytes) / 1073741824) * 10) / 10,
  });

  // Mechanism 1 — wmic via cmd.exe. Starts instantly (powershell.exe can
  // take 30–60 s to spawn under AV scanning). /format:list gives robust
  // key=value blocks. Deprecated on 24H2+ but present on most machines.
  try {
    const r = await sh(
      `wmic logicaldisk where "DriveType=3" get DeviceID,FileSystem,FreeSpace,Size /format:list`,
      { timeoutMs: 15_000, silent: true }
    );
    if (r.ok && r.stdout) {
      const out = [];
      for (const block of r.stdout.split(/\n\s*\n/)) {
        const kv = {};
        for (const line of block.split("\n")) {
          const i = line.indexOf("=");
          if (i > 0) kv[line.slice(0, i).trim()] = line.slice(i + 1).trim();
        }
        if (kv.DeviceID && kv.FileSystem === "NTFS" && kv.FreeSpace) {
          out.push(toObj(kv.DeviceID, kv.FreeSpace, kv.Size));
        }
      }
      if (out.length) return out;
    }
  } catch {}

  // Mechanism 2 — CIM via PowerShell. Fallback for 24H2+ where wmic is gone.
  // Pipe-delimited rows so we never depend on ConvertTo-Json quirks.
  try {
    const r = await ps(`
$rows = @()
Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" -ErrorAction SilentlyContinue |
  Where-Object { $_.FileSystem -eq "NTFS" } |
  ForEach-Object { $rows += ($_.DeviceID.TrimEnd(":") + "|" + [int64]$_.FreeSpace + "|" + [int64]$_.Size) }
Write-Output ("ROWS:" + ($rows -join ";"))
`, { timeoutMs: 45_000 });
    if (r.ok && r.stdout && r.stdout.indexOf("ROWS:") >= 0) {
      const body = r.stdout.slice(r.stdout.indexOf("ROWS:") + 5).trim();
      const out = [];
      for (const part of body.split(";")) {
        const [letter, free, size] = part.split("|");
        if (letter && free) out.push(toObj(letter, free, size));
      }
      if (out.length) return out;
    }
  } catch {}

  return null;
}

// Best-effort SSD detection — a scoring tiebreaker only. Uses wmic (cmd,
// instant) since the only reliable SSD signal (Get-PhysicalDisk MediaType)
// needs PowerShell, which can be minutes-slow under AV scanning. wmic can't
// distinguish SSD/HDD reliably, so we simply skip the bonus on machines
// where it'd cost a 40 s+ PowerShell spawn. Returns a Set (may be empty);
// drive selection still works on free-space + non-system-drive scoring.
async function detectSsdLetters() {
  return new Set();
}

// ── WSL install ───────────────────────────────────────────────────────
async function installWsl(network, installDrive, emit) {
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

  // Prefer rootfs-import whenever we have a non-system drive available.
  // Rationale: `wsl --install -d Ubuntu` (Microsoft Store) always lands
  // the vhdx under %LOCALAPPDATA% on C: and there's no flag to redirect.
  // Rootfs-import is the only path that lets us pick the install
  // directory, so if the best drive is D:/E:/etc., we always use it.
  // Also covers CN / restricted networks where the MS Store path blocks
  // for 10+ min hitting raw.githubusercontent.com.
  const preferRootfs = !installDrive.isSystemDrive || network === "cn" || network === "unknown";
  if (preferRootfs) {
    if (await verifyDistroPresent()) return { ok: true, method: "already-installed" };
    const where = installDrive.isSystemDrive
      ? "from mirrors (faster than Microsoft Store)"
      : `to ${installDrive.letter}: drive (${installDrive.freeGb} GB free)`;
    emit({ phase: "installing-wsl", message: `Importing Ubuntu rootfs ${where}…` });
    const ir = await importUbuntuFromRootfs(network, installDrive, emit);
    if (ir.ok) return { ok: true, method: "rootfs-import", installDir: installDrive.installDir };
    if (await verifyDistroPresent()) return { ok: true, method: "already-installed" };
    // Mirrors all unreachable — only fall back to MS Store if we're
    // landing on C: anyway (Store always picks C:). For non-system
    // drives, fail loudly so the user knows we can't honor their
    // disk choice rather than silently dumping to C:.
    if (!installDrive.isSystemDrive) {
      return { ok: false, error: ir.error || "All rootfs mirrors unreachable; cannot install to chosen drive" };
    }
    emit({ phase: "installing-wsl", message: "Rootfs import failed, falling back to Microsoft Store…" });
  }

  emit({ phase: "installing-wsl", message: "Installing WSL2 + Ubuntu (5–10 min, requires admin)…" });
  // global network with C: as chosen drive: try Microsoft Store install
  // first (uses GitHub direct), web-download if it fails.
  const r = await sh(`wsl --install --no-launch -d Ubuntu`, { timeoutMs: 8 * 60_000 });
  if (r.ok || (await verifyDistroPresent())) return { ok: true };

  emit({ phase: "installing-wsl", message: "Microsoft Store install failed, retrying with --web-download…" });
  const r2 = await sh(`wsl --install --web-download --no-launch -d Ubuntu`, { timeoutMs: 8 * 60_000 });
  if (r2.ok || (await verifyDistroPresent())) return { ok: true };

  // Last resort: rootfs import (only reached for global+system-drive
  // path — non-system already tried it above).
  emit({ phase: "installing-wsl", message: "Falling back to direct rootfs import…" });
  const ir = await importUbuntuFromRootfs(network, installDrive, emit);
  if (ir.ok) return { ok: true, method: "rootfs-import", installDir: installDrive.installDir };
  return { ok: false, error: r2.stderr || ir.error || `wsl --install exit ${r2.code}` };
}

// Direct rootfs install: download Ubuntu rootfs from cloud-images
// mirrors (NJU > USTC > tuna > cloud-images) and `wsl --import` to the
// caller-chosen install directory. Bypasses Microsoft Store + the
// DistributionInfo manifest fetch (raw.githubusercontent.com), so this
// path also works behind firewalls that block raw.githubusercontent.
// `installDrive.installDir` controls where the ext4.vhdx ends up — we
// surface this as a top-level `<drive>:\CiCy\WSL\Ubuntu\` path so the
// user can find their WSL files from File Explorer without spelunking
// through AppData.
async function importUbuntuFromRootfs(network, installDrive, emit) {
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

  // curl HEAD probes → fastest mirror first.
  const ordered = await curlOrder(urls);

  emit({ phase: "installing-wsl", message: "Downloading Ubuntu rootfs (~350MB, may take a few minutes)…", status: "running", progress: 0 });

  // Resolve %TEMP% once so the JS poller can stat the exact file curl writes.
  const tarName = "ubuntu-jammy-wsl-amd64.tar.gz";
  const tmpProbe = await cmdExec(`echo %TEMP%`, { timeoutMs: 12_000, silent: true });
  const tmpDir = (tmpProbe.stdout || "").trim() || "C:\\Windows\\Temp";
  const tarPath = `${tmpDir}\\${tarName}`;

  // Ubuntu 22.04 rootfs is ~350 MB across mirrors. Progress-bar denominator
  // only — we don't reject on size here, just sanity-check >50 MB after.
  const ROOTFS_BYTES = 350 * 1024 * 1024;

  await makeDir(installDrive.installDir);
  await delFile(tarPath); // drop any stale/locked leftover before fetching

  // Download via curl (sequential mirror fallthrough), then wsl --import.
  // curl runs as its own RPC so the JS poller can read the growing file.
  // `downloading` gates the size poller: once the tarball is fully fetched
  // we flip it off so the poller stops spamming "下载中 …%" over the
  // (unmeterable, multi-minute) import — otherwise the bar looks frozen at
  // 93% and the user thinks it hung.
  let downloading = true;
  const downloadPromise = (async () => {
    let fetched = false;
    for (const u of ordered) {
      _emit({ kind: "log", text: `$ curl ↓ ${previewCmd(u)}` });
      const dl = await sh(
        `curl.exe -fL --connect-timeout 15 --max-time 1800 -o "${tarPath}" "${u}"`,
        { timeoutMs: 32 * 60_000, silent: true },
      );
      if (dl.ok && (await fileSize(tarPath)) > 50_000_000) { fetched = true; break; }
    }
    if (!fetched) { downloading = false; await delFile(tarPath); return { ok: false, error: "no reachable rootfs mirror" }; }
    downloading = false;
    emit({ phase: "installing-wsl", message: "正在导入运行环境…", status: "running", progress: 0.98 });
    const imp = await sh(
      `wsl --import Ubuntu "${installDrive.installDir}" "${tarPath}" --version 2`,
      { timeoutMs: 20 * 60_000 },
    );
    await delFile(tarPath);
    if (!imp.ok) return { ok: false, error: imp.stderr || imp.stdout || `wsl --import exit ${imp.code}` };
    return { ok: true };
  })();

  // Poll the temp file size while the download runs → live progress bar.
  // Capped at 0.98 so the bar stays active until `wsl --import` finishes.
  let downloadDone = false;
  const poller = (async () => {
    while (!downloadDone) {
      await new Promise((res) => setTimeout(res, 3000));
      if (downloadDone) break;
      if (!downloading) continue; // import in progress — don't overwrite its message
      try {
        const bytes = await fileSize(tarPath);
        if (bytes > 0) {
          const pct = Math.min(0.98, bytes / ROOTFS_BYTES);
          const mb = (bytes / 1024 / 1024).toFixed(1);
          emit({
            phase: "installing-wsl",
            message: `Downloading Ubuntu rootfs… ${mb} MB`,
            status: "running",
            progress: pct,
          });
        }
      } catch {/* swallow — poller failures shouldn't break install */}
    }
  })();

  const r = await downloadPromise;
  downloadDone = true;
  await poller; // let the loop exit cleanly on next sleep tick
  if (!r.ok) return { ok: false, error: r.error || "rootfs-import failed" };
  return { ok: true, method: "rootfs-import" };
}

async function waitForDistroReady(distro, emit, deadlineMs = DEFAULT_TIMEOUTS.wslBoot) {
  const start = Date.now();
  let shutdownTries = 0;
  while (Date.now() - start < deadlineMs) {
    const r = await wslExec(distro, ["true"], { timeoutMs: 20_000 });
    if (r.ok) return { ok: true };
    // A freshly imported distro very often throws `Wsl/Service/E_UNEXPECTED`
    // (or just hangs) until the WSL2 lightweight VM is cycled once. A single
    // `wsl --shutdown` reliably clears it. We do this proactively on the
    // first failure rather than burning the whole deadline retrying a VM
    // that will never come up on its own. Bounded to 2 cycles.
    const out = (r.stderr || "") + (r.stdout || "");
    const stuck = /E_UNEXPECTED|Wsl\/Service|HRESULT|0x8/i.test(out);
    if (shutdownTries < 2 && (stuck || Date.now() - start > 20_000)) {
      shutdownTries++;
      emit({ phase: "waiting-distro", message: "正在重启运行环境…", status: "running" });
      await sh("wsl --shutdown", { timeoutMs: 30_000 });
      await new Promise(res => setTimeout(res, 6_000));
      continue;
    }
    emit({ phase: "waiting-distro", message: "正在启动运行环境…", status: "running" });
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

# ── 3) Mirror the binary into cicy's $HOME/.local/bin
# cicy-desktop's main process probes $HOME/.local/bin/cicy-code under
# the default user to decide if the install completed. Symlink there
# (rather than copying) keeps everything pointing at /usr/local/bin so
# upgrades stay atomic. We only mirror for cicy — root doesn't need it
# (cicy-code binary is already in /usr/local/bin which is on root's
# PATH, and the /etc/wsl.conf [boot] command su's to cicy anyway).
H=/home/cicy
sudo_or_root mkdir -p "$H/.local/bin"
sudo_or_root ln -sfn "$DST" "$H/.local/bin/cicy-code"
sudo_or_root sh -c "printf '%s' '$ACT' > '$H/.local/bin/cicy-code.version'"
sudo_or_root chown -R cicy:cicy "$H/.local" 2>/dev/null || true

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

// ── Windows shell integration ──────────────────────────────────────────
// Make WSL's /home/cicy obvious from Windows by:
//   1. Creating a Desktop shortcut "CiCy" → \\wsl$\<distro>\home\cicy
//   2. Pinning the same UNC path to File Explorer's Quick Access
// Both are best-effort — failures degrade silently. The renderer's
// banner already has an "Open WSL Files" button as the primary access
// point; these shortcuts are for users who close cicy-desktop and want
// to find their files later without re-launching the homepage.
async function createWindowsWslShortcuts(wslHomePath, emit) {
  emit({ phase: "mounting-files", message: "Creating Desktop shortcut + Quick Access pin…", status: "running" });
  // PowerShell escapes: the UNC path uses backslashes, which inside a
  // PS single-quoted string survive verbatim. We pass it interpolated
  // into the script body (JS → PS literal) without further escaping.
  // .lnk creation via WScript.Shell COM is the standard Windows-shell
  // idiom; it requires no admin and works on every Windows since 7.
  // Quick Access pin uses the verb "pintohome" on the Shell32 namespace
  // — same path File Explorer's right-click "Pin to Quick access" uses.
  const script = `
$ErrorActionPreference = 'Continue'
$target = '${wslHomePath.replace(/'/g, "''")}'

# 1. Desktop shortcut
try {
  $desktop = [Environment]::GetFolderPath('Desktop')
  $lnk = Join-Path $desktop 'CiCy.lnk'
  $shell = New-Object -ComObject WScript.Shell
  $sc = $shell.CreateShortcut($lnk)
  $sc.TargetPath = $target
  $sc.Description = 'CiCy WSL files (AI agent workspace)'
  $sc.IconLocation = 'shell32.dll,3'  # generic folder icon
  $sc.Save()
  Write-Output "SHORTCUT_OK $lnk"
} catch { Write-Output "SHORTCUT_FAIL $_" }

# 2. Quick Access pin
try {
  $shell = New-Object -ComObject Shell.Application
  $folder = $shell.Namespace($target)
  if ($folder) {
    $folder.Self.InvokeVerb('pintohome')
    Write-Output "QUICKACCESS_OK"
  } else {
    Write-Output "QUICKACCESS_SKIP target unreachable"
  }
} catch { Write-Output "QUICKACCESS_FAIL $_" }
`;
  // Only step that genuinely needs PowerShell (WScript.Shell / Shell.Application
  // COM for .lnk + Quick Access pin — no cmd/curl equivalent). Generous
  // timeout so a slow (AV-throttled) powershell.exe spawn still completes;
  // failure is non-fatal and handled below.
  const r = await ps(script, { timeoutMs: 75_000 });
  // Non-fatal regardless — the "Open WSL Files" button in the banner
  // covers the primary access path. We only surface success/skip in
  // the timeline detail so the user knows the shortcut was created.
  if (r.ok && r.stdout.includes("SHORTCUT_OK")) {
    const pinned = r.stdout.includes("QUICKACCESS_OK");
    emit({
      phase: "mounting-files",
      message: pinned ? "Desktop shortcut + Quick Access pin created" : "Desktop shortcut created",
      status: "done",
    });
  } else {
    emit({ phase: "mounting-files", message: `Warning: shortcut creation failed — open via the banner button instead`, status: "warning" });
  }
}

// ── apt runtime dependency install ─────────────────────────────────────
// cicy-code's setup.go runs `checkEnvironment()` at first launch and
// blocks the API server until missing baseTools (unzip, jq, etc.) are
// apt-installed. On a fresh Ubuntu rootfs this is ~30 seconds of
// "Installed v2.1.0" → click "open" → nothing-responds limbo.
//
// We front-load the apt install here so the API is up the moment the
// "done" event fires. Skipping ffmpeg (240 MB, async on the cicy-code
// side anyway — non-blocking even if installed lazily).
async function installRuntimeDeps(distro, emit) {
  emit({ phase: "installing-deps", message: "Installing apt packages (unzip, jq, CJK fonts)…", status: "running" });
  // sudoPrefix mirrors setup.go's logic — apt-get needs root, but the
  // current user might already be root (fresh rootfs imports run as
  // root by default before /etc/wsl.conf default=cicy takes effect).
  //
  // `set -o pipefail` so `apt | tail` doesn't mask apt's exit code, plus
  // a per-package dpkg check after install — apt-get can exit 0 while
  // some packages silently fail (e.g. missing from configured mirror).
  // Reporting "done" with packages missing leaves cicy-code to apt-fight
  // with our background apt-get update and the UI lies about readiness.
  const script = `set -eo pipefail
SUDO=""
if [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1; then SUDO="sudo "; fi
PKGS="unzip jq fonts-wqy-microhei fontconfig"
# Don't run update if we already have one from the apt-mirror config step
# (timestamp <5 min old). Saves ~10 seconds on warm runs.
if [ ! -f /var/cache/apt/pkgcache.bin ] || [ $(($(date +%s) - $(stat -c %Y /var/cache/apt/pkgcache.bin))) -gt 300 ]; then
  DEBIAN_FRONTEND=noninteractive $SUDO apt-get update -qq >/dev/null 2>&1 || true
fi
APT_OUT=$(DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y --no-install-recommends $PKGS 2>&1) || {
  echo "$APT_OUT" | tail -5 >&2
  exit 1
}
MISSING=""
for p in $PKGS; do
  dpkg -s "$p" >/dev/null 2>&1 || MISSING="$MISSING $p"
done
if [ -n "$MISSING" ]; then
  echo "$APT_OUT" | tail -5 >&2
  echo "PACKAGES_MISSING:$MISSING" >&2
  exit 2
fi
echo "$APT_OUT" | tail -3
`;
  // Run the apt packages AND the Node.js pre-install concurrently — Node is
  // the long pole of cicy-code's first-launch checkEnvironment() (it blocks
  // the :8008 bind until node is on PATH). Front-loading it here, overlapped
  // with apt + the user's agent-pick think-time, turns the later "启动 AI
  // 引擎" step from a 3–5 min wait into a near-instant health check.
  const [r] = await Promise.all([
    wslBash(distro, script, { timeoutMs: 5 * 60_000 }),
    preinstallNode(distro, emit),
  ]);
  if (!r.ok) {
    // Non-fatal: cicy-code can apt-install on its own at first launch.
    // We just lose the "API ready instantly" guarantee.
    emit({ phase: "installing-deps", message: `Warning: ${summarize(r.stderr, { maxLines: 1 })} — cicy-code will retry`, status: "warning" });
    return { ok: false, error: r.stderr };
  }
  emit({ phase: "installing-deps", message: "运行依赖已就绪", status: "done" });
  return { ok: true };
}

// Best-effort Node.js pre-install — mirrors api/mgr/setup.go nodeInstallCmd
// (Node 24 prebuilt tarball into $HOME/.local, npmmirror→nodejs.org). Lands
// `node` on $HOME/.local/bin which cicy-code's extendPATH() exports, so its
// checkEnvironment() finds node already present and skips the slow install,
// binding :8008 immediately. Purely an optimization: if anything here fails,
// cicy-code installs node itself at first launch (just slower). Never throws.
async function preinstallNode(distro, emit) {
  emit({ phase: "installing-deps", message: "正在预装运行依赖（Node.js）…", status: "running" });
  const script = `set -e
if command -v node >/dev/null 2>&1 && [ -x "$HOME/.local/bin/node" ]; then echo NODE_OK; exit 0; fi
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) NARCH=x64 ;;
  aarch64|arm64) NARCH=arm64 ;;
  *) exit 1 ;;
esac
INDEX_URL=""
for url in "https://npmmirror.com/mirrors/node/index.json" "https://nodejs.org/dist/index.json"; do
  if curl -fsI --max-time 6 "$url" >/dev/null 2>&1; then INDEX_URL="$url"; break; fi
done
[ -n "$INDEX_URL" ] || exit 1
NODE_VER=$(curl -fsSL --max-time 15 "$INDEX_URL" | grep -m1 -oE '"v24\\.[0-9]+\\.[0-9]+"' | head -1 | tr -d '"')
[ -n "$NODE_VER" ] || NODE_VER="v24.0.0"
BASE=$(dirname "$INDEX_URL")
TARBALL="$BASE/\${NODE_VER}/node-\${NODE_VER}-linux-\${NARCH}.tar.xz"
mkdir -p "$HOME/.local" "$HOME/.local/bin"
TMP=$(mktemp -t node.XXXXXX.tar.xz)
curl -fsSL --max-time 300 "$TARBALL" -o "$TMP"
tar -xJf "$TMP" -C "$HOME/.local"
rm -f "$TMP"
ln -sfn "$HOME/.local/node-\${NODE_VER}-linux-\${NARCH}" "$HOME/.local/node"
ln -sf "$HOME/.local/node/bin/node" "$HOME/.local/bin/node"
ln -sf "$HOME/.local/node/bin/npm" "$HOME/.local/bin/npm"
ln -sf "$HOME/.local/node/bin/npx" "$HOME/.local/bin/npx"
"$HOME/.local/bin/node" --version >/dev/null 2>&1 || exit 1
echo NODE_OK`;
  try {
    const r = await wslBash(distro, script, { timeoutMs: 6 * 60_000 });
    if (!(r.ok && /NODE_OK/.test(r.stdout))) {
      _emit({ kind: "log", text: "(node pre-install skipped/failed — cicy-code will install it at first launch)" });
    }
  } catch {/* non-fatal optimization */}
}

// Rewrite /etc/wsl.conf's [boot] command so cicy-code is launched with
// --agents=<csv> at every distro start (and on the post-install
// `wsl --terminate` we do below). Without this the user's agent
// selection only sticks for the current launch — next reboot they'd lose
// it. Idempotent: replaces the whole [boot] section.
async function applyAgentsToBoot(distro, agents, emit) {
  const csv = (Array.isArray(agents) ? agents : []).join(",");
  const bootCmd = `pgrep -f cicy-code >/dev/null 2>&1 || setsid -f $HOME/.local/bin/cicy-code ${csv ? `--agents=${csv} ` : ""}</dev/null >>$HOME/.cicy-code.log 2>&1`;
  // Use sed to replace just the [boot] block — keeps [user] etc. intact.
  // python3 is the cleanest tool, but not always present at this stage;
  // fall back to awk which IS guaranteed by base-files in every Ubuntu.
  const script = `set -e
SUDO=""
if [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1; then SUDO="sudo "; fi
F=/etc/wsl.conf
$SUDO touch "$F"
$SUDO cp "$F" "$F.bak.$(date +%s)" 2>/dev/null || true
$SUDO awk -v cmd=${JSON.stringify(bootCmd)} '
BEGIN { in_boot=0; printed=0 }
/^\\[boot\\]/ { in_boot=1; print; print "systemd=false"; print "command=su - cicy -c \\047" cmd "\\047"; printed=1; next }
/^\\[/ && in_boot { in_boot=0 }
in_boot { next }
{ print }
END {
  if (!printed) {
    print ""
    print "[boot]"
    print "systemd=false"
    print "command=su - cicy -c \\047" cmd "\\047"
  }
}
' "$F" > /tmp/wsl.conf.new
$SUDO mv /tmp/wsl.conf.new "$F"
$SUDO chmod 644 "$F"
echo OK
`;
  const r = await wslBash(distro, script, { timeoutMs: 15_000 });
  if (!r.ok || !/OK/.test(r.stdout || "")) {
    // Non-fatal: cicy-code can still be started ad-hoc this session, the
    // user just won't get persistent --agents across reboots.
    emit?.({ phase: "starting", message: `Warning: wsl.conf agent update failed (${r.stderr || "unknown"})`, status: "warning" });
    return { ok: false };
  }
  return { ok: true };
}

// ── agent CLI install (claude/codex/opencode) ─────────────────────────
// Kept for reference / future "manual install" UI. NOT called from
// windowsInstall anymore — installer used to pre-warm all three CLIs
// before the install was reported done, which added ~3–5 minutes and
// failed often on flaky networks. cicy-code's own setup.go now installs
// the requested subset (from --agents) lazily on first agent create, so
// the UI flips to "ready" the moment :8008 binds, and the user picks
// claude / codex / opencode in the drawer before start.
//
// Original npm-install logic referenced below — preserved because the
// nested-native-binary verify dance is non-obvious. cicy-code's setup.go
// uses the same approach internally.
// platform-native sibling when the network drops mid-install. The
// wrapper still prints a version string but actually launching the CLI
// errors with "native binary not installed". Pre-installing here with
// strict verification means:
//   - the bug is hot-fixable via CF Worker without re-releasing cicy-code
//   - first agent start after install is instant (no first-time npm wait)
//
// Each upstream wrapper ships platform binaries via optionalDependencies
// using its own naming convention. npm puts these *nested* inside the
// wrapper's own node_modules — we verify there, NOT at the top level.
// We do not pass the native subpackage as a positional install arg:
// codex's optional deps are npm-aliases to version-tagged builds
// (`npm:@openai/codex@<v>-linux-x64`), so `@openai/codex-linux-x64@latest`
// 404s and would kill the whole install command.
const AGENT_SPECS = {
  claude:   { pkg: "@anthropic-ai/claude-code", nativePrefix: "@anthropic-ai/claude-code-linux-" },
  codex:    { pkg: "@openai/codex",             nativePrefix: "@openai/codex-linux-" },
  opencode: { pkg: "opencode-ai",               nativePrefix: "opencode-linux-" },
};

async function installAgentClis(distro, network, agents, emit) {
  // CN networks must use npmmirror; with registry.npmjs.org the wrapper
  // download alone can stall for 20+ min before npm gives up on optional
  // deps, which is the exact path that produces the "wrapper installed,
  // native missing" state we're trying to prevent.
  const registry = (network === "cn" || network === "unknown")
    ? "https://registry.npmmirror.com"
    : "https://registry.npmjs.org";
  const fallbackRegistry = registry === "https://registry.npmmirror.com"
    ? "https://registry.npmjs.org"
    : "https://registry.npmmirror.com";

  // WSL is always Linux; map uname -m → npm platform suffix.
  // x86_64 → x64, aarch64/arm64 → arm64. Reject anything else with a
  // clear error rather than silently picking the wrong package.
  const archProbe = await wslBash(distro, "uname -m", { timeoutMs: 5_000 });
  const m = (archProbe.stdout || "").trim();
  const arch = m === "x86_64" ? "x64" : (m === "aarch64" || m === "arm64") ? "arm64" : "";
  if (!arch) return { ok: false, error: `unsupported arch: ${m}` };

  // One install attempt for one agent against a given registry. Returns
  // { ok, error }. The caller handles retry/fallback policy.
  async function tryInstall(name, spec, nativePkg, reg) {
    const script = `
set -e
export PATH="$HOME/.npm-global/bin:$HOME/.local/node/bin:$PATH"
npm install -g --include=optional --fetch-timeout=60000 --fetch-retries=2 \\
  --registry=${reg} --prefix "$HOME/.npm-global" \\
  ${spec.pkg}@latest
`;
    const r = await wslBash(distro, script, { timeoutMs: 8 * 60_000 });
    if (!r.ok) {
      return { ok: false, error: summarize(r.stderr || r.stdout, { maxLines: 2, maxChars: 200 }) };
    }
    // The native subpackage lives *inside* the wrapper's own node_modules,
    // not as a top-level sibling. This is npm's standard placement for
    // optionalDependencies resolved during install.
    const verify = `[ -d "$HOME/.npm-global/lib/node_modules/${spec.pkg}/node_modules/${nativePkg}" ] && echo OK || { echo MISSING; exit 1; }`;
    const v = await wslBash(distro, verify, { timeoutMs: 5_000 });
    if (!v.ok || !v.stdout.includes("OK")) {
      return { ok: false, error: `native subpackage missing (${nativePkg})` };
    }
    return { ok: true };
  }

  const results = {};
  for (const name of agents) {
    const spec = AGENT_SPECS[name];
    if (!spec) {
      results[name] = { ok: false, error: "unknown agent" };
      continue;
    }
    const nativePkg = `${spec.nativePrefix}${arch}`;
    emit({ phase: "installing-agents", message: `Installing ${name}…`, status: "running", agent: name });

    // First attempt against the preferred registry.
    let r = await tryInstall(name, spec, nativePkg, registry);

    // If verification failed (or the install errored), drop the cache
    // and retry against the fallback registry. The most common cause
    // we've seen is npmmirror returning a zero-byte tarball whose
    // sha512 doesn't match — once cached, every retry against the same
    // mirror reproduces the same failure until the cache is purged.
    if (!r.ok) {
      emit({ phase: "installing-agents", message: `${name}: ${r.error}; retrying via ${fallbackRegistry}`, status: "running", agent: name });
      await wslBash(distro, "npm cache clean --force >/dev/null 2>&1 || true", { timeoutMs: 30_000 });
      r = await tryInstall(name, spec, nativePkg, fallbackRegistry);
    }

    if (!r.ok) {
      results[name] = r;
      emit({ phase: "installing-agents", message: `${name}: ${r.error}`, status: "error", agent: name });
      continue;
    }
    results[name] = { ok: true, native: nativePkg };
    emit({ phase: "installing-agents", message: `${name} ✓`, status: "done", agent: name });
  }

  const failed = Object.entries(results).filter(([, v]) => !v.ok).map(([k]) => k);
  if (failed.length) return { ok: false, error: `failed: ${failed.join(", ")}`, results };
  return { ok: true, results };
}

// ── stage path ────────────────────────────────────────────────────────
// Each version gets its own filename so a stale download from an older
// release doesn't get reused as if it were the new binary.
async function resolveStagePath(version) {
  const safe = String(version || "unknown").replace(/[^A-Za-z0-9._-]/g, "_");
  // Resolve %APPDATA% via cmd (instant) then build + mkdir the stage dir.
  const r = await cmdExec(`echo %APPDATA%`, { timeoutMs: 12_000, silent: true });
  if (!r.ok || !r.stdout.trim()) throw new Error("stage path resolution failed");
  const dir = `${r.stdout.trim()}\\CiCy Desktop\\cicy-code\\wsl-stage`;
  await makeDir(dir);
  return `${dir}\\cicy-code-v${safe}-staged`;
}

async function cleanupStage(path) {
  await delFile(path);
}

// ── main flow ─────────────────────────────────────────────────────────
// onPickAgents: async ({ defaults, available }) → string[]
// Called once between deps-install and start-cicy-code. UI shows a checkbox
// picker; we await user confirmation, then pass the selected list to cicy-code
// via --agents=<csv>. cicy-code installs only those CLIs lazily on first
// agent create, so the installer doesn't block on npm. Default: all three.
export async function windowsInstall({ onProgress = () => {}, onPickAgents } = {}) {
  const emit = (e) => { try { onProgress(e); } catch {} };
  setEmit(emit);
  assertRPC();

  // 1. Network detect
  emit({ phase: "detecting", message: "Detecting network…" });
  const network = await detectNetwork();
  emit({ phase: "detecting", message: `Network: ${network}`, network });

  // 1b. Pick install drive — auto-select the fixed NTFS drive with the
  // most free space. On OEM laptops where C: is a small SSD and D: is
  // a large data drive, the user wants WSL on D:. The picker handles
  // this without any UI; we surface the choice in the timeline so the
  // user understands where their files will live.
  const installDrive = await pickInstallDrive();
  if (installDrive.lowDisk) {
    // Enumeration succeeded but no fixed NTFS drive has ≥10 GB free — a
    // genuine low-disk condition. Bail with the real free figure rather than
    // failing mid-install with a confusing ENOSPC / vhdx I/O error.
    throw new Error(`LOW_DISK_SPACE:${installDrive.freeGb}:10`);
  }
  if (installDrive.probeFailed) {
    // Drive probe was inconclusive (a flaky/slow shell). Proceed on C:
    // rather than blocking a machine that may well be installable — the
    // import will surface a real disk error only if space truly runs out.
    emit({ phase: "detecting", message: "Drive probe inconclusive — defaulting to C:", status: "warning", installDrive: "C" });
  }
  emit({
    phase: "detecting",
    message: `Install drive: ${installDrive.letter}: (${installDrive.freeGb} GB free of ${installDrive.totalGb} GB, ${installDrive.isSSD ? "SSD" : "HDD"}${installDrive.isSystemDrive ? ", system" : ""})`,
    network,
    installDrive: installDrive.letter,
    installDir: installDrive.installDir,
    isSSD: installDrive.isSSD,
  });
  // Also assert C: has enough headroom for staged downloads + Windows
  // itself, even when the WSL vhdx lands elsewhere. cicy-code's staged
  // binary download (~20 MB) plus npm cache + temp files during the
  // rootfs import can still push C: over the edge on near-full systems.
  if (installDrive.letter !== "C") {
    const cFree = (installDrive.all.find(d => d.Letter === "C") || {}).FreeGB;
    const C_MIN_GB = 2;
    if (Number.isFinite(cFree) && cFree < C_MIN_GB) {
      throw new Error(`LOW_DISK_SPACE:${cFree}:${C_MIN_GB}`);
    }
  }

  // 2. Latest manifest
  emit({ phase: "checking", message: "Checking latest version…" });
  const mf = await fetchLatestManifest(network);
  if (!mf.ok) throw new Error("manifest fetch failed: " + mf.error);
  const version = mf.version;
  const assetUrl = mf.assets["linux-amd64"];
  if (!assetUrl) throw new Error("manifest has no linux-amd64 asset");  // dev-only, never seen by users
  const expectSize = (mf.sizes && mf.sizes["linux-amd64"]) || 0;

  // Resolve the staging path early so we can check the local cache before
  // making any network calls — if we already have a same-size copy of the
  // target binary on disk, no need to ping GitHub at all.
  const stagePath = await resolveStagePath(version);
  let cachedHit = false;
  if (expectSize > 0) {
    cachedHit = (await fileSize(stagePath)) === expectSize;
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
    let reachable = false;
    for (const u of checkUrls) {
      const r = await sh(
        `curl.exe -sI -o NUL --connect-timeout 8 --max-time 14 -w "%{http_code}" "${u}"`,
        { timeoutMs: 22_000, silent: true },
      );
      const code = parseInt((r.stdout || "").trim(), 10) || 0;
      if (r.ok && code >= 200 && code < 400) { reachable = true; break; }
    }
    if (!reachable) {
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
    if (!dl.ok) throw new Error("download failed: " + (dl.error || "all mirrors failed"));
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
      const ins = await installWsl(network, installDrive, emit);
      if (!ins.ok) throw new Error("wsl install: " + (ins.error || "failed"));
      // Explicit "done" so the installing-wsl step settles to ✓ — the
      // download poller's last emit is a "running" tick, so without this
      // the step would linger at "•" (looking stuck) even though import
      // succeeded and later steps advance.
      emit({ phase: "installing-wsl", message: "运行环境已就绪", status: "done", progress: 1 });
      wsl = await checkWslStatus();
      if (!wsl.usableDistro) throw new Error("WSL installed but no usable distro detected — Windows may need a reboot");
    }
    // Always verify distro is responsive — covers both fresh installs and
    // pre-existing distros that may be stopped or in a degraded state.
    const w = await waitForDistroReady(wsl.usableDistro, emit);
    if (!w.ok) throw new Error(w.error);
    // Settle waiting-distro to ✓ — its last emit was a "running" boot tick,
    // so without this it lingers at "•" even though the distro is up.
    emit({ phase: "waiting-distro", message: "运行环境已启动", status: "done", progress: 1 });
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

  // 7b. Front-load cicy-code's baseTools apt-install so its first launch
  //     binds :8008 immediately (no 30s "checking environment" stall).
  await installRuntimeDeps(distro, emit);

  // 8. Ask the user which AI agents to enable BEFORE starting cicy-code.
  //    We pre-install nothing — cicy-code's setup.go installs only the
  //    requested CLIs lazily on first agent create. This shaves 3–5 min
  //    off install time and avoids the "60% of users get blocked because
  //    one of three CLIs failed via flaky npm" problem.
  //
  //    Default: claude (single most-used CLI). User multi-selects via
  //    the drawer; we await their choice before continuing.
  const AVAILABLE_AGENTS = ["claude", "codex", "opencode"];
  let pickedAgents = ["claude"];
  if (_cachedAgents && _cachedAgents.length > 0) {
    // Resume path: the user already chose on an earlier attempt — don't
    // make them pick again, just reuse it so retry continues seamlessly.
    pickedAgents = _cachedAgents;
    emit({ phase: "picking-agents", message: `Agents: ${pickedAgents.join(", ")}`, status: "done", agents: pickedAgents });
  } else if (typeof onPickAgents === "function") {
    emit({ phase: "picking-agents", message: "Waiting for agent selection…", status: "running" });
    try {
      const picked = await onPickAgents({ defaults: pickedAgents, available: AVAILABLE_AGENTS });
      if (Array.isArray(picked) && picked.length > 0) {
        // Filter to known agents only — don't trust UI to send valid
        // names; an unknown agent passed to --agents would break startup.
        pickedAgents = picked.filter((a) => AVAILABLE_AGENTS.includes(a));
        if (pickedAgents.length === 0) pickedAgents = ["claude"];
      }
    } catch (e) {
      emit({ phase: "picking-agents", message: `pick error: ${e.message} — using default (claude)`, status: "warning" });
    }
    _cachedAgents = pickedAgents;
    emit({ phase: "picking-agents", message: `Agents: ${pickedAgents.join(", ")}`, status: "done", agents: pickedAgents });
  }

  // 9. Persist the agent selection in /etc/wsl.conf [boot] so cicy-code
  //    starts with the same flag on every reboot. Idempotent — replaces
  //    the [boot] section in-place.
  await applyAgentsToBoot(distro, pickedAgents, emit);

  // 10. Reload the distro so the new /etc/wsl.conf [user] default=cicy
  //     AND [boot] command take effect, then start cicy-code under the
  //     new default user so :8008 is up by the time the UI flips to
  //     "uptodate".
  emit({ phase: "starting", message: "正在启动 AI 引擎…", status: "running" });
  // Make Ubuntu the *default* distro so `wsl.exe -e bash` (no -d) — used
  // by cicy-desktop's main process to read $HOME/cicy-ai/global.json for
  // the auto-login token — lands in Ubuntu, not docker-desktop (which
  // ships without bash). This is mandatory for the "open in browser"
  // flow to inject the api_token query param.
  await sh(`wsl --set-default ${distro}`, { timeoutMs: 10_000 });
  await sh(`wsl --terminate ${distro}`, { timeoutMs: 15_000 });
  // First post-terminate command takes a few seconds while wsl service
  // re-initialises — that's also when the new wsl.conf is parsed.
  const agentsArg = pickedAgents.join(",");
  const startCmd = `wsl -d ${distro} -- bash -lc "pgrep -f cicy-code >/dev/null 2>&1 || setsid -f /usr/local/bin/cicy-code --agents=${agentsArg} </dev/null >>~/.cicy-code.log 2>&1 ; sleep 1 ; pgrep -fa cicy-code | head -1"`;
  await sh(startCmd, { timeoutMs: 30_000 });
  // First launch runs cicy-code's checkEnvironment() before binding :8008.
  // We front-loaded Node.js + apt deps in installRuntimeDeps, so this is now
  // usually seconds — but we still MUST wait for a real 200 from /api/health
  // (never a fake "done") in case the pre-install fell back to lazy install.
  emit({ phase: "starting", message: "正在启动 AI 引擎，请稍候…", status: "running" });
  let _apiUp = false;
  const _hDeadline = Date.now() + 8 * 60_000;
  while (Date.now() < _hDeadline) {
    const _hc = await sh(
      `wsl -d ${distro} -- bash -lc "curl -s -o /dev/null -w '%{http_code}' --max-time 4 http://localhost:8008/api/health 2>/dev/null || echo 000"`,
      { timeoutMs: 15_000, silent: true },
    );
    if (_hc.ok && /200/.test(_hc.stdout)) { _apiUp = true; break; }
    const _left = Math.max(0, Math.round((_hDeadline - Date.now()) / 1000));
    emit({ phase: "starting", message: `正在配置运行环境，请稍候…（最多约 ${Math.ceil(_left / 60)} 分钟）`, status: "running" });
    await new Promise(r => setTimeout(r, 5_000));
  }
  emit({
    phase: "starting",
    message: _apiUp ? "AI 引擎已就绪" : "AI 引擎仍在后台配置，稍后会自动就绪",
    status: _apiUp ? "done" : "warning",
    agents: pickedAgents,
  });

  // 11. Done. We deliberately do NOT create a Desktop shortcut / Quick
  //     Access pin to the WSL home: a first-time user is here to use the
  //     AI product, not to browse Linux files — a "\\wsl$\Ubuntu" folder
  //     shortcut just adds confusion. (It was also the slowest remaining
  //     PowerShell step, ~75s on AV-throttled machines.) Files stay
  //     reachable via Explorer's network path for anyone who needs them.
  emit({
    phase: "done",
    message: `Installed v${r.version}`,
    version: r.version,
    status: "done",
    installDrive: installDrive.letter,
    installDir: installDrive.installDir,
  });
  return { ok: true, version: r.version, installDir: installDrive.installDir };
  // Note: stagePath is intentionally not deleted — downloadStaged checks
  // size before reuse, so cached files speed up retries / repeat installs.
}

export function canRunRendererInstall() {
  return typeof window !== "undefined" && typeof window.electronRPC === "function";
}
