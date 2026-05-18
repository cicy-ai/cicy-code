import "./style.css";
import { api, apiMode, devLocalBase } from "./api.js";
import { TOS_TEXT, TOS_VERSION } from "./tos.js";

const $local  = document.getElementById("local-cards");
const $cloud  = document.getElementById("cloud-cards");
const $recent = document.getElementById("recent-list");
const $addForm = document.getElementById("add-form");
const $fStatus = document.getElementById("f-status");
const $toast  = document.getElementById("toast");
const $envPill = document.getElementById("env-pill");
const $topVer = document.getElementById("topbar-version");

$envPill.textContent = apiMode;
if (apiMode === "browser") $envPill.title = `Browser dev mode\nLocal base: ${devLocalBase}`;

const esc = s => String(s == null ? "" : s).replace(/[&<>"']/g, ch => ({
  "&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"
}[ch]));
const bytes = n => { if (!n) return "—"; const u=["B","KB","MB","GB"]; let i=0; while(n>1024&&i<u.length-1){n/=1024;i++;} return n.toFixed(i?1:0)+" "+u[i]; };
const dur = sec => { if (!sec || sec<0) return "—"; const d=Math.floor(sec/86400), h=Math.floor(sec%86400/3600), m=Math.floor(sec%3600/60); if(d) return `${d}d ${h}h`; if(h) return `${h}h ${m}m`; if(m) return `${m}m`; return `${sec}s`; };
const ago = ms => { const s=Math.floor((Date.now()-ms)/1000); if(s<60) return s+"s ago"; if(s<3600) return Math.floor(s/60)+"m ago"; if(s<86400) return Math.floor(s/3600)+"h ago"; return Math.floor(s/86400)+"d ago"; };

function dotClass(health) {
  if (!health) return "s-unknown";
  if (health.ok && health.status === "ok") return "s-up";
  if (health.ok) return "s-warn";
  return "s-down";
}

let healthByBackend = {};
let updateInfo = null;
let lastBackends = [];
let prereq = null;          // { platform, kind, ok, required, version, installUrl }
let installState = null;    // { installed, version }

function showToast(msg, isErr = false) {
  $toast.textContent = msg;
  $toast.className = "toast show" + (isErr ? " err" : "");
  setTimeout(() => { $toast.className = "toast"; }, 2200);
}

function localState() {
  if (!prereq || !prereq.ok)   return "prereq-missing";
  if (!installState || !installState.installed) return "not-installed";
  const h = healthByBackend["local"];
  return (h && h.ok) ? "running" : "stopped";
}

// Local card has four states. Each shares the header + footer skeleton but
// swaps the metric strip and action set. Window-vs-non-Windows shows up as a
// difference in prereq.kind ("docker" vs "node") — the action labels follow.

function renderLocal(backend) {
  const state = localState();
  const isWin = prereq && prereq.platform === "windows";
  const kindLabel = isWin ? "Docker" : "Node.js";
  return ({
    "prereq-missing":  renderLocalPrereqMissing(backend, isWin),
    "not-installed":   renderLocalNotInstalled(backend, isWin, kindLabel),
    "stopped":         renderLocalStopped(backend, isWin),
    "running":         renderLocalRunning(backend, isWin),
  })[state];
}

function localHeader(backend, dotClsName, title, sub, right = "") {
  return `
    <div class="card-row">
      <div class="status-dot ${dotClsName}"></div>
      <div class="head-text">
        <div class="name">${esc(backend.name)} <span class="badge">LOCAL</span></div>
        <div class="meta">${title} · ${sub}</div>
      </div>
      <div class="head-right">${right}</div>
    </div>
  `;
}

function renderLocalPrereqMissing(backend, isWin) {
  const req = prereq && prereq.required || (isWin ? "Docker Desktop" : "Node.js 22+");
  const link = prereq && prereq.installUrl || "#";
  const cur = prereq && prereq.version ? `installed: ${esc(prereq.version)}` : "not installed";
  return `
    <div class="card" data-backend="${esc(backend.id)}">
      ${localHeader(backend, "s-down", `Local engine — ${esc(req)} required`, cur, "")}
      <div class="metric-row">
        <div class="m"><span class="k">Reason</span><span class="v">${isWin ? "cicy-code runs in a Docker container on Windows" : "cicy-code's npm package needs Node 22+"}</span></div>
      </div>
      <div class="action-row">
        <button class="primary" data-act="install-prereq" data-url="${esc(link)}">⤓ Install ${esc(req)}</button>
        <button data-act="recheck">↻ I installed it — re-check</button>
        <span class="grow"></span>
        <span class="upd-tip" style="color:var(--fg-mute)">Cloud engines still work without local.</span>
      </div>
    </div>
  `;
}

function renderLocalNotInstalled(backend, isWin, kindLabel) {
  const installCmd = isWin
    ? "docker pull ghcr.io/cicy-ai/cicy-code:latest"
    : "npm i -g cicy-code@latest";
  return `
    <div class="card" data-backend="${esc(backend.id)}">
      ${localHeader(backend, "s-warn", `Local engine — not installed`, `prereq ${kindLabel} ready`, "")}
      <div class="metric-row">
        <div class="m"><span class="k">Install via</span><span class="v" style="font-family:ui-monospace,monospace;font-size:11px">${esc(installCmd)}</span></div>
      </div>
      <div class="action-row">
        <button class="primary" data-act="install-cicy">⤓ Install cicy-code</button>
        <button data-act="copy-install-cmd" data-cmd="${esc(installCmd)}">⎘ Copy command</button>
        <span class="grow"></span>
      </div>
    </div>
  `;
}

function renderLocalStopped(backend, isWin) {
  const installed = installState && installState.version || "?";
  return `
    <div class="card" data-backend="${esc(backend.id)}">
      ${localHeader(backend, "s-warn", `Local engine — stopped`, `installed v${esc(installed)}`, `v${esc(installed)}`)}
      <div class="metric-row">
        <div class="m"><span class="k">Status</span><span class="v">Stopped</span></div>
        <div class="m"><span class="k">Mode</span><span class="v">${isWin ? "docker" : "npx"}</span></div>
      </div>
      <div class="action-row">
        <button class="primary" data-act="start">▷ Start</button>
        <button data-act="check-update">⇧ Check for updates</button>
        <button data-act="uninstall" class="danger">✕ Uninstall</button>
        <span class="grow"></span>
      </div>
    </div>
  `;
}

function renderLocalRunning(backend, isWin) {
  const h = healthByBackend[backend.id] || {};
  const memTxt = h.mem_bytes ? bytes(h.mem_bytes) : "—";
  const upTxt = h.uptime_sec != null ? dur(h.uptime_sec) : "—";
  const agentsTxt = h.agents_count != null && h.agents_count >= 0 ? h.agents_count : "—";
  const verTxt = h.version ? `v${esc(h.version)}` : "—";
  const pidTxt = h.pid ? `pid ${h.pid}` : "—";
  const updTip = updateInfo && updateInfo.hasUpdate
    ? `<span class="upd-tip">Update available: v${esc(updateInfo.latestVersion)}</span>`
    : "";
  if (h.version) $topVer.textContent = `v${esc(h.version)}`;
  return `
    <div class="card" data-backend="${esc(backend.id)}">
      ${localHeader(backend, "s-up", `${isWin ? "docker container" : "npx cicy-code"} on :8008`, esc(pidTxt), esc(verTxt))}
      <div class="metric-row">
        <div class="m"><span class="k">Status</span><span class="v">Running</span></div>
        <div class="m"><span class="k">Uptime</span><span class="v">${esc(upTxt)}</span></div>
        <div class="m"><span class="k">Mem</span><span class="v">${esc(memTxt)}</span></div>
        <div class="m"><span class="k">Agents</span><span class="v">${esc(String(agentsTxt))}</span></div>
      </div>
      <div class="action-row">
        <button class="primary" data-act="open">▷ Open</button>
        <button data-act="restart">↻ Restart</button>
        <button data-act="stop">◼ Stop</button>
        <button data-act="check-update">⇧ Check for updates</button>
        <button data-act="copy">⎘ Copy URL</button>
        <span class="grow"></span>
        ${updTip}
      </div>
    </div>
  `;
}

function renderCloud(backend) {
  const h = healthByBackend[backend.id] || null;
  const dot = dotClass(h);
  const latencyTxt = h && h.latencyMs != null ? `${h.latencyMs} ms` : "—";
  const verTxt = h && h.version ? `v${esc(h.version)}` : (h && h.ok ? "ok" : "—");
  const agentsTxt = h && h.agents_count != null && h.agents_count >= 0 ? h.agents_count : "—";
  return `
    <div class="card" data-backend="${esc(backend.id)}">
      <div class="card-row">
        <div class="status-dot ${dot}"></div>
        <div class="head-text">
          <div class="name">${esc(backend.name)}</div>
          <div class="meta">${esc(backend.url || "")}</div>
        </div>
        <div class="head-right">${esc(verTxt)} · ${esc(latencyTxt)}</div>
      </div>
      <div class="metric-row">
        <div class="m"><span class="k">Status</span><span class="v">${h && h.ok ? "Healthy" : h ? "Unreachable" : "Unknown"}</span></div>
        <div class="m"><span class="k">Agents</span><span class="v">${esc(String(agentsTxt))}</span></div>
        ${h && h.uptime_sec != null ? `<div class="m"><span class="k">Uptime</span><span class="v">${esc(dur(h.uptime_sec))}</span></div>` : ""}
      </div>
      <div class="action-row">
        <button class="primary" data-act="open">▷ Open</button>
        <button data-act="copy">⎘ Copy URL</button>
        <span class="grow"></span>
        <button class="danger" data-act="remove">✕ Remove</button>
      </div>
    </div>
  `;
}

function wireCards(list) {
  document.querySelectorAll(".card").forEach(card => {
    const id = card.getAttribute("data-backend");
    const backend = list.find(b => b.id === id);
    if (!backend) return;
    card.querySelectorAll("[data-act]").forEach(btn => {
      const act = btn.getAttribute("data-act");
      btn.onclick = (event) => handleAction(act, backend, event);
    });
  });
}

async function refreshBackends() {
  lastBackends = await api.backends.list();
  // Pull prereq + install state in parallel so the Local card draws the
  // correct state machine immediately.
  if (api.system) {
    const [p, i] = await Promise.all([
      api.system.checkPrereq().catch(() => null),
      api.system.checkCicyCodeInstalled().catch(() => null),
    ]);
    prereq = p;
    installState = i;
  }
  rerender();
  await refreshHealth();
}
async function refreshHealth() {
  try {
    const results = await api.backends.healthAll();
    results.forEach(r => { healthByBackend[r.id] = r.health; });
  } catch {}
  rerender();
}
function rerender() {
  const local = lastBackends.filter(b => b.kind === "local");
  const cloud = lastBackends.filter(b => b.kind !== "local");
  $local.innerHTML = local.map(renderLocal).join("") || `<div class="empty">No local engine</div>`;
  $cloud.innerHTML = cloud.length ? cloud.map(renderCloud).join("") : `<div class="empty">No cloud backends yet — add one above.</div>`;
  wireCards(lastBackends);
}

async function handleAction(act, backend, event) {
  switch (act) {
    case "open": {
      const r = await api.backends.open(backend.id);
      if (r && r.ok) { showToast(`Opened ${backend.name}`); setTimeout(refreshWindows, 600); }
      else showToast(`Open failed: ${r && r.error || "unknown"}`, true);
      break;
    }
    case "copy": {
      const url = backend.resolvedUrl || backend.url || "";
      await api.clipboard.write(url);
      showToast("URL copied");
      break;
    }
    case "remove": {
      if (!confirm(`Remove backend "${backend.name}"?`)) return;
      await api.backends.remove(backend.id);
      await refreshBackends();
      showToast(`Removed ${backend.name}`);
      break;
    }
    case "restart": {
      showToast("Restarting cicy-code sidecar…");
      const r = await api.backends.restartSidecar();
      if (r && r.ok) { showToast(`Sidecar restarted (pid ${r.pid})`); setTimeout(refreshHealth, 1500); }
      else showToast(`Restart failed: ${r && r.error || "unknown"}`, true);
      break;
    }
    case "check-update": {
      showToast("Checking GitHub for updates…");
      try {
        updateInfo = await api.updates.check();
        if (updateInfo.hasUpdate) {
          const accept = confirm(`Update available: v${updateInfo.latestVersion} (you have v${updateInfo.currentVersion || "?"})\n\nOpen the release page in your browser?`);
          if (accept) await api.updates.openReleasePage(updateInfo.releaseUrl);
        } else {
          showToast(`You're on the latest version (v${updateInfo.currentVersion || "?"})`);
        }
        rerender();
      } catch (e) {
        showToast(`Update check failed: ${e.message}`, true);
      }
      break;
    }
    // --- new state-machine actions for the Local card. Real implementations
    // land once cicy-desktop exposes the corresponding IPC; for browser-dev
    // they just toast / open URLs / copy strings.
    case "install-prereq": {
      const url = (event?.target || document.activeElement)?.dataset?.url
        || (prereq && prereq.installUrl);
      if (url) {
        await api.updates.openReleasePage(url);
        showToast("Opened install page in browser");
      }
      break;
    }
    case "recheck": {
      showToast("Re-checking prerequisites…");
      await refreshBackends();
      showToast(prereq && prereq.ok ? "Prereq OK" : "Still missing", !(prereq && prereq.ok));
      break;
    }
    case "install-cicy":
    case "start":
    case "stop":
    case "uninstall": {
      const verb = { "install-cicy": "install", "start": "start", "stop": "stop", "uninstall": "uninstall" }[act];
      if (verb === "uninstall" && !confirm("Uninstall cicy-code?")) return;
      showToast(`${verb}ing cicy-code…`);
      const fn = api.cicycode[verb === "install-cicy" ? "install" : verb] || api.cicycode[verb];
      const r = await fn();
      if (r && r.ok) {
        showToast(`cicy-code ${verb} ok`);
        // Re-probe so the card flips state immediately.
        setTimeout(refreshBackends, 800);
      } else {
        showToast(`${verb} failed: ${r && r.error || "unknown"}`, true);
      }
      break;
    }
    case "copy-install-cmd": {
      const cmd = (event?.target || document.activeElement)?.dataset?.cmd || "";
      if (cmd) { await api.clipboard.write(cmd); showToast("Install command copied"); }
      break;
    }
  }
}

async function refreshWindows() {
  const wins = await api.windows.list();
  if (!wins.length) {
    $recent.innerHTML = `<div class="empty">No backend windows open yet.</div>`;
    return;
  }
  const backends = await api.backends.list();
  const byId = Object.fromEntries(backends.map(b => [b.id, b]));
  $recent.innerHTML = wins.map(w => {
    const backend = byId[w.backendId] || { name: "?" };
    return `
      <div class="recent-row" data-win="${w.windowId}">
        <div class="label">
          <div class="name">${esc(backend.name)}${w.title ? " — " + esc(w.title) : ""}</div>
          <div class="sub">window ${w.windowId} · ${esc(ago(w.openedAt))}</div>
        </div>
        <span class="ago"></span>
        <button data-act="focus">Focus</button>
      </div>
    `;
  }).join("");
  $recent.querySelectorAll(".recent-row").forEach(row => {
    const id = Number(row.getAttribute("data-win"));
    row.querySelector("[data-act=focus]").onclick = async () => { await api.windows.focus(id); };
  });
}

document.getElementById("btn-toggle-add").onclick = () => {
  $addForm.classList.toggle("open");
  if ($addForm.classList.contains("open")) document.getElementById("f-name").focus();
};
document.getElementById("f-cancel").onclick = () => {
  $addForm.classList.remove("open");
  $fStatus.className = "status"; $fStatus.textContent = "";
};
document.getElementById("f-submit").onclick = async () => {
  const name = document.getElementById("f-name").value.trim();
  const url = document.getElementById("f-url").value.trim();
  const token = document.getElementById("f-token").value.trim() || undefined;
  if (!url) { $fStatus.className = "status err"; $fStatus.textContent = "URL required"; return; }
  $fStatus.className = "status"; $fStatus.textContent = "Probing…";
  try {
    const probe = await api.backends.probe({ url, token });
    if (probe && probe.ok) { $fStatus.className = "status ok"; $fStatus.textContent = `Probe OK (${probe.statusCode})`; }
    else { $fStatus.className = "status err"; $fStatus.textContent = `Probe ${probe && probe.error || probe && probe.statusCode || "failed"} (saving anyway)`; }
    await api.backends.add({ name, url, token });
    document.getElementById("f-name").value = "";
    document.getElementById("f-url").value = "";
    document.getElementById("f-token").value = "";
    $addForm.classList.remove("open");
    await refreshBackends();
    showToast(`Added ${name || url}`);
  } catch (e) { $fStatus.className = "status err"; $fStatus.textContent = `Error: ${e.message}`; }
};

document.getElementById("btn-refresh").onclick = () => {
  refreshBackends();
  refreshWindows();
};

// --- ToS gate ---
// Block all backend rendering until the user has accepted the current
// version. Acceptance is stored in localStorage (browser-dev) or via the
// future tos:accept IPC (Electron). Versioned key so a future legal text
// bump re-prompts every user.
const TOS_KEY = "cicy-tos-accepted";
function tosAccepted() {
  try {
    const v = localStorage.getItem(TOS_KEY);
    return v === TOS_VERSION;
  } catch { return false; }
}
function markTosAccepted() {
  try { localStorage.setItem(TOS_KEY, TOS_VERSION); } catch {}
}
function renderTos() {
  const overlay = document.createElement("div");
  overlay.className = "tos-overlay";
  overlay.innerHTML = `
    <div class="tos-modal">
      <div class="tos-header">
        <span class="icon">📜</span>
        <h2>使用条款 / Terms of Service</h2>
        <span class="ver">v${TOS_VERSION}</span>
      </div>
      <div class="tos-body" id="tos-body"></div>
      <div class="tos-footer">
        <label>
          <input type="checkbox" id="tos-read" />
          我已阅读并理解以上条款 / I have read and understand the terms
        </label>
        <button class="decline" id="tos-decline" type="button">Decline</button>
        <button class="accept" id="tos-accept" type="button" disabled>Accept &amp; Continue</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  // Render TOS text. Keep it minimal — no full markdown engine, just newlines.
  document.getElementById("tos-body").textContent = TOS_TEXT;
  const $read = document.getElementById("tos-read");
  const $accept = document.getElementById("tos-accept");
  $read.addEventListener("change", () => { $accept.disabled = !$read.checked; });
  $accept.addEventListener("click", () => {
    markTosAccepted();
    overlay.remove();
    bootHomepage();
  });
  document.getElementById("tos-decline").addEventListener("click", () => {
    if (apiMode === "electron" && window.close) window.close();
    else window.location.href = "about:blank";
  });
}

function bootHomepage() {
  refreshBackends();
  refreshWindows();
  refreshLocalIPs();
  setInterval(refreshHealth, 30000);
  setInterval(refreshWindows, 5000);
  setInterval(refreshLocalIPs, 60000);
}

// Stash this machine's non-loopback IPv4 addresses in localStorage so any
// part of the UI (frp-server install drawer, multi-backend selector, etc.)
// can read them without re-probing the host shell. Stored as JSON so we
// don't need to re-parse comma vs newline. Schema is intentionally simple:
//   { ips: ["192.168.1.5", "10.0.0.7"], primary: "192.168.1.5", at: 17... }
async function refreshLocalIPs() {
  try {
    const ips = await api.system.localIPs();
    const payload = { ips, primary: ips[0] || null, at: Date.now() };
    localStorage.setItem("cicy.localIPs", JSON.stringify(payload));
    if (window.dispatchEvent && window.CustomEvent) {
      window.dispatchEvent(new CustomEvent("cicy:local-ips", { detail: payload }));
    }
  } catch (e) {
    console.warn("[localIPs] probe failed:", e?.message || e);
  }
}

if (tosAccepted()) {
  bootHomepage();
} else {
  renderTos();
}
