// Platform-specific cicy-code lifecycle commands. Built entirely on top of
// window.electronRPC("exec_shell", ...) so changing any command string is
// a Vite HMR refresh — no .app rebuild.
//
// Windows path: WSL only. The Go binary doesn't compile for Windows
// natively (POSIX pty deps), and Docker Desktop is too heavy / has
// licensing friction. WSL is built into Win10+, is the path cicy-code's
// own npm postinstall recommends, and reuses 90% of the Unix code path.
//
// Mac / Linux path: npx cicy-code, npm i -g cicy-code, etc.

function rpc(tool, args) {
  if (typeof window === "undefined" || !window.electronRPC) {
    return Promise.resolve({ isError: true, content: [{ type: "text", text: "no electronRPC (browser dev mode)" }] });
  }
  return window.electronRPC(tool, args || {});
}

function flatten(res) {
  if (!res) return { ok: false, error: "no response" };
  if (res.isError) {
    const msg = (res.content || []).map(c => c.text).filter(Boolean).join("\n");
    return { ok: false, error: msg || "tool error" };
  }
  // Tools return `{content:[{type:"text", text:"<json string with stdout/stderr/exitCode>"}]}`
  // Parse the inner JSON; fall back to raw text.
  const raw = (res.content || []).map(c => c.text).filter(Boolean).join("\n");
  try {
    const inner = JSON.parse(raw);
    return { ok: inner.exitCode === 0, stdout: inner.stdout || "", stderr: inner.stderr || "", exitCode: inner.exitCode };
  } catch {
    return { ok: true, stdout: raw, stderr: "", exitCode: 0 };
  }
}

async function exec(cmd) { return flatten(await rpc("exec_shell", { command: cmd })); }

// ── platform detection ───────────────────────────────────────────────
// URL ?platform=win|mac|linux overrides the auto-detected platform so the
// UI can be previewed in a browser without actually being on that OS.
// (Real exec_shell calls still go to the host OS; this only changes how
// the Local card renders and which command strings are picked.)
function platform() {
  if (typeof window !== "undefined") {
    try {
      const force = new URLSearchParams(window.location.search).get("platform");
      if (force === "win") return "win32";
      if (force === "mac") return "darwin";
      if (force === "linux") return "linux";
    } catch {}
    if (window.cicy && window.cicy.platform) return window.cicy.platform;
  }
  const ua = (typeof navigator !== "undefined" ? navigator.userAgent : "").toLowerCase();
  if (ua.includes("windows")) return "win32";
  if (ua.includes("mac")) return "darwin";
  return "linux";
}
const isWin = () => platform() === "win32";

// ── prereq checks ────────────────────────────────────────────────────
async function checkNode() {
  const r = await exec("node --version");
  if (!r.ok) return { ok: false, kind: "node", required: "Node.js 22+", version: null, installUrl: "https://nodejs.org/en/download" };
  const m = r.stdout.trim().match(/v?(\d+)\.(\d+)\.(\d+)/);
  const major = m ? Number(m[1]) : 0;
  return { ok: major >= 22, kind: "node", required: "Node.js 22+", version: m ? `v${m[1]}.${m[2]}.${m[3]}` : null, installUrl: "https://nodejs.org/en/download" };
}

// Windows: WSL status check. `wsl --status` lists default distro + kernel version.
// Exit 0 + non-empty stdout = installed. Exit non-0 = WSL not enabled.
async function checkWsl() {
  const r = await exec("wsl --status");
  const installed = r.ok && r.stdout && r.stdout.trim().length > 0;
  // Try to extract a default distro name; non-critical
  const distroMatch = r.stdout && r.stdout.match(/Default Distribution:\s*(\S+)/i);
  return {
    ok: installed,
    kind: "wsl",
    required: "WSL2 + Ubuntu",
    version: distroMatch ? distroMatch[1] : (installed ? "WSL2" : null),
    installUrl: "https://learn.microsoft.com/en-us/windows/wsl/install",
    installCommand: "wsl --install -d Ubuntu",
  };
}

async function checkPrereq() {
  const plat = platform();
  const base = isWin() ? await checkWsl() : await checkNode();
  return { ...base, platform: plat === "win32" ? "windows" : plat };
}

// ── cicy-code installation state ─────────────────────────────────────
async function checkCicyCodeInstalled() {
  // Both WSL and native Unix expose `cicy-code` on PATH after `npm i -g`.
  // On WSL we shell into wsl. On Mac/Linux we just call directly.
  const cmd = isWin()
    ? "wsl bash -lc 'command -v cicy-code >/dev/null && cicy-code --version 2>&1 || true'"
    : "command -v cicy-code >/dev/null && cicy-code --version 2>&1 || npx --no-install cicy-code --version 2>&1 || true";
  const r = await exec(cmd);
  const m = r.stdout && r.stdout.match(/(\d+\.\d+\.\d+)/);
  return { installed: !!m, version: m ? m[1] : null };
}

// ── lifecycle commands ───────────────────────────────────────────────
const LOG = "~/.cicy-code.log";

function wrap(cmd) {
  // Wrap a Unix command for WSL execution on Windows.
  return isWin() ? `wsl bash -lc ${JSON.stringify(cmd)}` : cmd;
}

export const cicycodeOps = {
  // Real impls — call from main.js handleAction
  async install() { return exec(wrap("npm i -g cicy-code@latest")); },
  async upgrade() { return exec(wrap("npm i -g cicy-code@latest")); },
  async start(port = 8008)   { return exec(wrap(`nohup cicy-code > ${LOG} 2>&1 & disown`)); },
  async stop()    { return exec(wrap("pkill -f 'cicy-code' || true")); },
  async uninstall() { return exec(wrap("npm uninstall -g cicy-code")); },
  async installPrereq() {
    // Windows: trigger WSL install (requires elevated shell — open the
    // Microsoft Store / docs page so the user runs it themselves).
    if (isWin()) {
      // Best-effort: try `wsl --install` in the same exec_shell. If it
      // requires elevation, fail and we open the docs URL instead.
      const r = await exec("wsl --install -d Ubuntu");
      if (r.ok) return r;
      return { ok: false, error: "Need to run `wsl --install` as Administrator. Opening docs.", installUrl: "https://learn.microsoft.com/en-us/windows/wsl/install" };
    }
    return { ok: false, error: "Open https://nodejs.org/en/download to install Node.js 22+" };
  },
};

// Enumerate this machine's non-loopback IPv4 addresses via the host shell.
// Cross-platform fallback chain — no Node dependency, since this runs
// before the cicy-code prereq check has been satisfied.
async function localIPs() {
  let cmd;
  if (isWin()) {
    // Get-NetIPAddress is on Windows 10+ by default. Strips loopback +
    // APIPA (169.254.*) and joins on newline so we can split below.
    cmd = `powershell -NoProfile -Command "Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.IPAddress -notmatch '^(127|169\\.254)'} | Select-Object -ExpandProperty IPAddress"`;
  } else if (platform() === "darwin") {
    cmd = `ifconfig | awk '/inet / && $2 !~ /^127/ {print $2}'`;
  } else {
    // hostname -I is portable across most Linux distros.
    cmd = `hostname -I 2>/dev/null | tr ' ' '\\n' | grep -v '^$' | grep -v '^127'`;
  }
  const r = await exec(cmd);
  if (!r.ok || !r.stdout) return [];
  return r.stdout
    .split(/\r?\n/)
    .map(s => s.trim())
    .filter(s => /^\d+\.\d+\.\d+\.\d+$/.test(s) && !s.startsWith("127.") && !s.startsWith("169.254."));
}

export const systemOps = { checkNode, checkWsl, checkPrereq, checkCicyCodeInstalled, localIPs, platform, isWin };
