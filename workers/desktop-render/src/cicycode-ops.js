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

// ── WSL token reader (Windows only) ──────────────────────────────────
// Reads api_token from the cicy-code daemon's ~/cicy-ai/global.json inside
// WSL. Called by api.js to build a token-bearing local URL without relying
// on the main-process window-manager (which may not have the fix deployed yet).
async function readWslToken() {
  if (!isWin()) return "";
  try {
    // Use chr() to avoid quote nesting issues across WSL/shell boundaries.
    const home = String.raw`os.path.expanduser(chr(126)+chr(47)+chr(99)+chr(105)+chr(99)+chr(121)+chr(45)+chr(97)+chr(105)+chr(47)+chr(103)+chr(108)+chr(111)+chr(98)+chr(97)+chr(108)+chr(46)+chr(106)+chr(115)+chr(111)+chr(110))`;
    const key  = String.raw`chr(97)+chr(112)+chr(105)+chr(95)+chr(116)+chr(111)+chr(107)+chr(101)+chr(110)`;
    const r = await exec(`wsl -d Ubuntu -- python3 -c "import json,os; d=json.load(open(${home})); print(d.get(${key},chr(110)))"`);
    const t = r.stdout.trim();
    return t === "n" ? "" : t;
  } catch { return ""; }
}
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
  // First: verify wsl.exe exists at all (missing on old Win10 builds).
  const which = await exec("where wsl.exe");
  if (!which.ok || !which.stdout.trim()) {
    return {
      ok: false,
      kind: "wsl",
      required: "WSL2 + Ubuntu",
      version: null,
      noExe: true,   // wsl.exe itself is missing
      installUrl: "https://aka.ms/wsl-install",
      installCommand: null,
    };
  }

  const r = await exec("wsl --status");
  const installed = r.ok && r.stdout && r.stdout.trim().length > 0;
  const distroMatch = r.stdout && r.stdout.match(/Default Distribution:\s*(\S+)/i);
  // `wsl --list --quiet` tells us if any distro is registered.
  const listR = await exec("wsl --list --quiet");
  const hasDistro = listR.ok && listR.stdout.trim().length > 0;
  return {
    ok: installed && hasDistro,
    installed,
    hasDistro,
    kind: "wsl",
    required: "WSL2 + Ubuntu",
    version: distroMatch ? distroMatch[1] : (installed ? "WSL2" : null),
    installUrl: "https://learn.microsoft.com/en-us/windows/wsl/install",
  };
}

async function checkPrereq() {
  const plat = platform();
  if (isWin()) {
    // Windows: WSL only — Docker path was removed by request. The wsl
    // installer drives the entire install flow from the renderer (see
    // wslInstaller.js), so we surface the WSL prereq state here.
    const wsl = await checkWsl();
    return { platform: "windows", ...wsl };
  }
  const base = await checkNode();
  return { ...base, platform: plat };
}

// ── cicy-code installation state ─────────────────────────────────────
async function checkCicyCodeInstalled() {
  if (isWin()) {
    // WSL only: docker inspect path was removed by request.
    const wr = await exec("wsl bash -lc 'command -v cicy-code >/dev/null && cicy-code --version 2>&1 || true'");
    const m = wr.stdout && wr.stdout.match(/(\d+\.\d+\.\d+)/);
    return { installed: !!m, version: m ? m[1] : null, runtime: "wsl" };
  }
  const cmd = "command -v cicy-code >/dev/null && cicy-code --version 2>&1 || npx --no-install cicy-code --version 2>&1 || true";
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
  async install() {
    // Windows install runs through wslInstaller.js which is invoked from
    // App.jsx handleInstall directly — this path is only hit on macOS/Linux.
    return exec(wrap("npm i -g cicy-code@latest"));
  },
  async upgrade() {
    if (isWin()) {
      // WSL path: re-run the npm install inside the user's WSL distro.
      return exec(wrap("npm i -g cicy-code@latest"));
    }
    return exec(wrap("npm i -g cicy-code@latest"));
  },
  async start(port = 8008) {
    return exec(wrap(`nohup cicy-code > ${LOG} 2>&1 & disown`));
  },
  async stop() {
    return exec(wrap("pkill -f 'cicy-code' || true"));
  },
  async uninstall() {
    return exec(wrap("npm uninstall -g cicy-code"));
  },
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

export const systemOps = { checkNode, checkWsl, checkPrereq, checkCicyCodeInstalled, localIPs, platform, isWin, readWslToken };
