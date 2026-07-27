/* NodeOS web UI. Vanilla JS, no dependencies: consumes the nodeosd REST API
   and the /api/events SSE stream. */

"use strict";

// ---------- state ----------

let snapshot = null;   // last /api/status payload
let alertsLog = [];    // newest first
let poolLoaded = false;
let scanPoller = null;
let workFormLoaded = false;

// ---------- helpers ----------

const $ = (id) => document.getElementById(id);

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

function fmtHash(gh) {
  if (!gh || gh <= 0) return "0 GH/s";
  if (gh >= 1e6) return (gh / 1e6).toFixed(2) + " PH/s";
  if (gh >= 1e3) return (gh / 1e3).toFixed(2) + " TH/s";
  return gh.toFixed(1) + " GH/s";
}

function fmtHashParts(gh) {
  const s = fmtHash(gh).split(" ");
  return `${s[0]} <small>${s[1]}</small>`;
}

function fmtDiff(d) {
  if (!d || d <= 0) return "–";
  const units = [["T", 1e12], ["G", 1e9], ["M", 1e6], ["k", 1e3]];
  for (const [u, m] of units) if (d >= m) return (d / m).toFixed(2) + u;
  return d.toFixed(0);
}

function fmtBytes(b) {
  if (!b) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return b.toFixed(i === 0 ? 0 : 1) + " " + u[i];
}

function fmtDur(sec) {
  if (sec == null || !isFinite(sec) || sec <= 0) return "–";
  const y = sec / 31557600;
  if (y >= 1000) return Math.round(y).toLocaleString() + " years";
  if (y >= 2) return y.toFixed(1) + " years";
  const d = sec / 86400;
  if (d >= 2) return d.toFixed(1) + " days";
  const h = sec / 3600;
  if (h >= 2) return h.toFixed(1) + " hours";
  const m = sec / 60;
  if (m >= 2) return m.toFixed(0) + " min";
  return Math.round(sec) + " s";
}

function fmtUptime(sec) {
  if (!sec) return "–";
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600),
        m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m ${sec % 60}s`;
}

function timeAgo(iso) {
  const t = new Date(iso).getTime();
  if (!t) return "–";
  const s = (Date.now() - t) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}

// ---------- theme ----------

(function initTheme() {
  const saved = localStorage.getItem("nodeos-theme");
  if (saved) document.documentElement.dataset.theme = saved;
})();

function toggleTheme() {
  const root = document.documentElement;
  const next = root.dataset.theme === "light" ? "dark" : "light";
  root.dataset.theme = next;
  localStorage.setItem("nodeos-theme", next);
  if (snapshot) { renderFleetChart(); }
}

// ---------- toasts ----------

function toast(msg, kind = "") {
  const el = document.createElement("div");
  el.className = "toast" + (kind ? " " + kind : "");
  el.textContent = msg;
  $("toasts").appendChild(el);
  setTimeout(() => {
    el.style.transition = "opacity 200ms, transform 200ms";
    el.style.opacity = "0";
    el.style.transform = "translateX(16px)";
    setTimeout(() => el.remove(), 220);
  }, kind === "err" ? 7000 : 4000);
}

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  const data = await resp.json().catch(() => ({}));
  if (resp.status === 401 && !path.startsWith("/api/auth")) showAuth("login");
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

// ---------- auth ----------

let authMode = "login";

function showAuth(mode) {
  authMode = mode;
  const setup = mode === "setup";
  $("auth-title").textContent = setup ? "Welcome to NodeOS" : "Log in";
  $("auth-note").textContent = setup
    ? "Choose an admin password to protect this NodeOS (min 8 characters)."
    : "";
  $("auth-pw2").hidden = !setup;
  $("auth-btn").textContent = setup ? "Set password & continue" : "Log in";
  $("auth-result").innerHTML = "";
  $("auth-overlay").hidden = false;
  $("auth-pw").focus();
}

function hideAuth() {
  $("auth-overlay").hidden = true;
  $("auth-pw").value = "";
  $("auth-pw2").value = "";
}

async function submitAuth() {
  const pw = $("auth-pw").value;
  try {
    if (authMode === "setup") {
      if (pw !== $("auth-pw2").value) throw new Error("passwords do not match");
      await api("POST", "/api/auth/setup", { password: pw });
    } else {
      await api("POST", "/api/auth/login", { password: pw });
    }
    hideAuth();
    loadAll();
  } catch (err) {
    $("auth-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
}

$("auth-btn").addEventListener("click", submitAuth);
$("auth-pw").addEventListener("keydown", (e) => { if (e.key === "Enter") submitAuth(); });
$("auth-pw2").addEventListener("keydown", (e) => { if (e.key === "Enter") submitAuth(); });

// ---------- tabs ----------

$("nav").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  document.querySelectorAll("nav button").forEach((b) => b.classList.toggle("active", b === btn));
  document.querySelectorAll(".tab").forEach((t) =>
    t.classList.toggle("active", t.id === "tab-" + btn.dataset.tab));
  $("page-title").textContent = btn.querySelector(".lbl").textContent;
  $("sidebar").classList.remove("open"); // close the drawer on mobile
  if (btn.dataset.tab === "lightning") loadLightning();
  if (btn.dataset.tab === "system") loadSysHistory();
  if (snapshot) render();
});

// the map is the dashboard's centrepiece: keep it alive while visible
setInterval(() => {
  if (document.querySelector("#tab-dashboard.active") && $("auth-overlay").hidden &&
      !document.hidden && peersLoadedOnce) {
    loadPeers();
  }
}, 30000);

$("menu-toggle").addEventListener("click", () => $("sidebar").classList.toggle("open"));
$("theme-btn").addEventListener("click", toggleTheme);

$("logout-btn").addEventListener("click", async () => {
  try { await api("POST", "/api/auth/logout"); } catch {}
  location.reload();
});

// ---------- live updates ----------

let es = null;

function connectSSE() {
  if (es) es.close();
  es = new EventSource("/api/events");
  es.addEventListener("snapshot", (e) => {
    snapshot = JSON.parse(e.data);
    render();
  });
  es.addEventListener("alert", (e) => {
    alertsLog.unshift(JSON.parse(e.data));
    if (alertsLog.length > 200) alertsLog.pop();
    renderAlerts();
  });
  es.onerror = () => {
    es.close();
    es = null;
    setTimeout(() => { if ($("auth-overlay").hidden) connectSSE(); }, 3000);
  };
}

async function loadAll() {
  try { alertsLog = await api("GET", "/api/alerts"); } catch {}
  try {
    const pool = await api("GET", "/api/pool");
    fillPoolForm(pool);
    poolLoaded = true;
  } catch {}
  try {
    snapshot = await api("GET", "/api/status");
    render();
  } catch {}
  refreshNodeSetup();
  loadNodeConfig();
  loadServices();
  try { tuningPresets = await api("GET", "/api/tuning"); } catch {}
  loadPeers(); // the dashboard map greets the user alive, not empty
  connectSSE();
}

async function bootstrap() {
  try {
    const st = await api("GET", "/api/auth/state");
    if (!st.authenticated && !st.disabled) {
      showAuth(st.setup_required ? "setup" : "login");
      return;
    }
  } catch {}
  loadAll();
}

// ---------- render ----------

function render() {
  if (!snapshot) return;
  renderHeader();
  renderHero();
  renderTiles();
  renderDashNode();
  renderFleetChart();
  renderSolo();
  renderMiners();
  renderNode();
  renderSystem();
  renderWork();
  renderAlerts();
  renderAbout();
}

function renderHeader() {
  const { fleet, node, version, demo } = snapshot;
  $("version").textContent = "v" + version;
  $("demo-badge").hidden = !demo;
  $("hs-hash").textContent = fmtHash(fleet.total_hash_gh);
  $("hs-miners").textContent = `${fleet.online} / ${fleet.count}`;
  $("nav-miner-count").textContent = fleet.count ? `(${fleet.count})` : "";
  const n = $("hs-node");
  if (node.available) {
    n.textContent = node.progress >= 0.9999
      ? `synced @ ${node.blocks.toLocaleString()}`
      : `syncing ${(node.progress * 100).toFixed(1)}%`;
  } else {
    n.textContent = "offline";
  }
}

// The hero answers the two questions a miner opens the page for: how much am
// I hashing, and where is that work going.
function renderHero() {
  const { fleet, work, node } = snapshot;
  const hero = $("hero");
  const live = fleet.online > 0 && fleet.total_hash_gh > 0;

  $("hero-hash").innerHTML = fmtHashParts(fleet.total_hash_gh);

  let sub;
  if (fleet.count === 0) sub = "No miners yet — run a scan on the Miners page.";
  else if (!live) sub = `${fleet.count} miner${fleet.count > 1 ? "s" : ""} registered, none hashing right now.`;
  else sub = `${fleet.online} of ${fleet.count} miners hashing · ${fleet.total_power_w.toFixed(0)} W` +
    (fleet.efficiency_j_th ? ` · ${fleet.efficiency_j_th.toFixed(1)} J/TH` : "");
  $("hero-sub").textContent = sub;

  // routing: own node beats pool; that distinction is the whole product
  const onOwnNode = !!(work && work.state === "running" && work.switched);
  hero.classList.toggle("on-node", onOwnNode);
  const route = $("hero-route");
  route.className = "route" + (onOwnNode ? " own" : "");
  const pool = poolLoaded ? `${$("p-url").value}:${$("p-port").value}` : "";
  let dest, hint;
  if (onOwnNode) {
    dest = "your own node";
    hint = work.settings && work.settings.mode === "ocean" ? "OCEAN · your templates" : "pure solo";
  } else if (work && work.state === "waiting_node") {
    dest = pool || "pool";
    hint = node.available ? `own node at ${(node.progress * 100).toFixed(1)} % sync` : "waiting for node";
  } else {
    dest = pool || "not configured";
    hint = work && work.state === "running" ? "engine ready — not switched" : "external pool";
  }
  route.innerHTML =
    `<span class="pulse ${live ? "live" : "idle"}"></span>` +
    `<span class="dest">${esc(dest)}</span>` +
    `<span style="color:var(--muted);font-weight:500">${esc(hint)}</span>`;
}

const ICO = {
  hash: "◈", miners: "⛏", power: "⚡", eff: "◇", best: "★", odds: "◔", engine: "⚙",
  height: "▤", peers: "⇄", mempool: "≋", disk: "▣",
};

function tileHTML(t) {
  return `
    <div class="tile">
      <div class="tile-head"><span class="ico">${t.i || "•"}</span>${t.l}</div>
      <div class="value">${t.v}</div>
      ${t.s ? `<div class="sub ${t.c || ""}">${t.s}</div>` : ""}
    </div>`;
}

function renderTiles() {
  const { fleet, solo, system } = snapshot;
  const ln = snapshot.lightning || {};
  const svcs = snapshot.services || [];
  const svcRunning = svcs.filter((x) => x.running).length;
  const svcInstalled = svcs.filter((x) => x.installed).length;
  const oddsDay = solo.odds_per_day;
  const memPct = system && system.mem_total_b
    ? 100 * (system.mem_total_b - system.mem_avail_b) / system.mem_total_b : 0;

  const tiles = [
    { i: ICO.miners, l: "Miners online", v: `${fleet.online} <small>/ ${fleet.count}</small>`,
      s: fleet.count && fleet.online < fleet.count ? `${fleet.count - fleet.online} offline` : "all reachable",
      c: fleet.count && fleet.online < fleet.count ? "warn" : "good" },
    { i: ICO.power, l: "Power draw", v: `${fleet.total_power_w.toFixed(0)} <small>W</small>`,
      s: fleet.total_power_w ? `${(fleet.total_power_w * 24 / 1000).toFixed(1)} kWh per day` : "" },
    { i: ICO.best, l: "Best share", v: fleet.best_diff ? esc(fleet.best_diff_str) : "–",
      s: solo.network_difficulty && fleet.best_diff
        ? `${(fleet.best_diff / solo.network_difficulty * 100).toFixed(2)} % of a block` : "all-time best" },
    { i: ICO.odds, l: "Chance of a block", v: oddsDay ? `${(oddsDay * 100).toPrecision(2)} <small>% / day</small>` : "–",
      s: solo.expected_seconds ? `≈ ${fmtDur(solo.expected_seconds)} on average`
        : solo.syncing ? "waiting for full node sync" : "needs node + hashrate",
      c: solo.syncing ? "warn" : "" },
    workTile(snapshot.work),
    { i: "⚡", l: "Lightning", v: ln.available ? esc(ln.alias || "ready") : ln.connected ? "starting" : "–",
      s: ln.available ? (ln.syncing ? "waiting for chain sync" : "operational")
        : "install via Services", c: ln.available && !ln.syncing ? "good" : ln.available ? "warn" : "" },
    { i: "◆", l: "Services", v: `${svcRunning} <small>/ ${svcInstalled}</small>`,
      s: svcInstalled ? "running / installed" : "none installed yet" },
    { i: "◱", l: "System", v: system && system.cpu_pct != null ? `${system.cpu_pct.toFixed(0)} <small>% CPU</small>` : "–",
      s: memPct ? `RAM ${memPct.toFixed(0)} % · load ${system.load1.toFixed(2)}` : "",
      c: system && (system.cpu_pct > 92 || memPct > 92) ? "warn" : "" },
  ];
  $("tiles").innerHTML = tiles.map(tileHTML).join("");
}

// nodeTiles builds the four node stat tiles shared by the dashboard and the
// node page.
function nodeTiles(n) {
  return [
    { i: ICO.height, l: "Block height", v: n.blocks.toLocaleString(),
      s: n.tip_time ? `tip ${ago(n.tip_time)}` : "" },
    { i: ICO.peers, l: "Peers", v: String(n.connections),
      s: `${n.peers_out || 0} out · ${n.connections_in} in`,
      c: n.connections < 3 ? "warn" : "" },
    { i: ICO.mempool, l: "Mempool", v: `${n.mempool_txs.toLocaleString()} <small>tx</small>`,
      s: `${fmtBytes(n.mempool_bytes)}${n.mempool_max ? " of " + fmtBytes(n.mempool_max) : ""}` },
    { i: ICO.disk, l: "Chain on disk", v: fmtBytes(n.size_on_disk),
      s: n.pruned ? `pruned${n.prune_target_b ? " to " + fmtBytes(n.prune_target_b) : ""}` : "full node" },
  ];
}

// the dashboard's compact Bitcoin-node overview (sync strip + tiles)
function renderDashNode() {
  const n = snapshot.node;
  if (!n.available) {
    $("dash-node-state").innerHTML = `<span class="status-chip offline"><span class="dot"></span>offline</span>`;
    $("dash-node-sync").innerHTML = `<div class="empty" style="padding:12px">No Bitcoin node reachable — set one up on the node page.</div>`;
    $("dash-node-tiles").innerHTML = "";
    return;
  }
  const pct = n.progress * 100;
  const syncing = n.ibd || n.progress < 0.9999;
  const behind = Math.max(0, n.headers - n.blocks);
  $("dash-node-state").innerHTML = syncing
    ? `<span class="status-chip warn"><span class="dot"></span>synchronising ${pct.toFixed(2)} %</span>`
    : `<span class="status-chip online"><span class="dot"></span>in sync</span>`;
  $("dash-node-sync").innerHTML = syncing ? `
    <div class="progress"><div style="width:${pct.toFixed(2)}%"></div></div>
    <div class="note">block ${n.blocks.toLocaleString()} of ${n.headers.toLocaleString()} · ${behind.toLocaleString()} to go</div>`
    : "";
  $("dash-node-tiles").innerHTML = nodeTiles(n).map(tileHTML).join("");
}

function workTile(w) {
  const base = { i: ICO.engine, l: "Work engine" };
  if (!w || !w.state || w.state === "disabled") {
    return { ...base, v: "off", s: "solo mining via your own node" };
  }
  if (w.state === "running" && w.switched) {
    return { ...base, v: "SOLO", s: "fleet mines on YOUR node", c: "good" };
  }
  if (w.state === "running") return { ...base, v: "ready", s: w.endpoint || "" };
  if (w.state === "waiting_node") return { ...base, v: "waiting", s: "node still syncing", c: "warn" };
  return { ...base, v: esc(w.state.replace("_", " ")), s: w.detail ? esc(w.detail.slice(0, 40)) : "" };
}

// ---------- fleet chart ----------

let chartPoints = []; // [{t, gh, x, y}] in pixel space, for the tooltip

// SVG cannot use CSS custom properties for stroke/fill attributes, so the
// chart reads the current theme's tokens instead of hard-coding colours.
const C = new Proxy({}, {
  get(_, name) {
    const map = {
      series: "--series-1", grid: "--grid", muted: "--muted",
      baseline: "--baseline", surface: "--surface", accent: "--accent",
      good: "--good", warning: "--warning", critical: "--critical",
    };
    return getComputedStyle(document.documentElement).getPropertyValue(map[name] || name).trim();
  },
});

function fleetSeries() {
  // Sum per-miner history into poll-interval buckets.
  const buckets = new Map();
  for (const m of snapshot.miners || []) {
    for (const s of m.history || []) {
      const b = Math.round(s.t / 10) * 10;
      buckets.set(b, (buckets.get(b) || 0) + s.h);
    }
  }
  return [...buckets.entries()].sort((a, b) => a[0] - b[0]).slice(-360);
}

function renderFleetChart() {
  const svg = $("fleet-chart");
  const wrap = $("fleet-chart-wrap");
  const W = Math.max(wrap.clientWidth, 320), H = 220;
  const padL = 56, padR = 12, padT = 12, padB = 24;
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);

  const series = fleetSeries();
  if (series.length < 2) {
    svg.innerHTML = `<text x="${W / 2}" y="${H / 2}" text-anchor="middle"
      fill="#898781" font-size="13">Collecting samples…</text>`;
    chartPoints = [];
    return;
  }

  const ts = series.map((d) => d[0]), vs = series.map((d) => d[1]);
  const tMin = ts[0], tMax = ts[ts.length - 1];
  const vMax = Math.max(...vs) * 1.15 || 1;
  const x = (t) => padL + ((t - tMin) / Math.max(tMax - tMin, 1)) * (W - padL - padR);
  const y = (v) => padT + (1 - v / vMax) * (H - padT - padB);

  chartPoints = series.map(([t, v]) => ({ t, gh: v, x: x(t), y: y(v) }));

  // gridlines + y labels (4 ticks)
  let grid = "";
  for (let i = 0; i <= 3; i++) {
    const v = (vMax / 3) * i;
    const yy = y(v);
    grid += `<line x1="${padL}" y1="${yy}" x2="${W - padR}" y2="${yy}"
      stroke="${C.grid}" stroke-width="1"/>
      <text x="${padL - 8}" y="${yy + 4}" text-anchor="end" fill="${C.muted}"
      font-size="11">${fmtHash(v)}</text>`;
  }
  // x labels (3 ticks)
  let xlab = "";
  for (let i = 0; i <= 2; i++) {
    const t = tMin + ((tMax - tMin) / 2) * i;
    const d = new Date(t * 1000);
    const hh = String(d.getHours()).padStart(2, "0"),
          mm = String(d.getMinutes()).padStart(2, "0");
    const anchor = i === 0 ? "start" : i === 2 ? "end" : "middle";
    xlab += `<text x="${x(t)}" y="${H - 6}" text-anchor="${anchor}"
      fill="${C.muted}" font-size="11">${hh}:${mm}</text>`;
  }

  const line = chartPoints.map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join("");
  const area = line + `L${chartPoints[chartPoints.length - 1].x.toFixed(1)},${y(0)}L${chartPoints[0].x.toFixed(1)},${y(0)}Z`;

  svg.innerHTML = `
    <defs>
      <linearGradient id="areaFill" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color="${C.series}" stop-opacity="0.28"/>
        <stop offset="100%" stop-color="${C.series}" stop-opacity="0.02"/>
      </linearGradient>
    </defs>
    ${grid}
    <line x1="${padL}" y1="${y(0)}" x2="${W - padR}" y2="${y(0)}" stroke="${C.baseline}" stroke-width="1"/>
    ${xlab}
    <path d="${area}" fill="url(#areaFill)"/>
    <path d="${line}" fill="none" stroke="${C.series}" stroke-width="2"
      stroke-linejoin="round" stroke-linecap="round"/>
    <line id="crosshair" x1="0" y1="${padT}" x2="0" y2="${H - padB}"
      stroke="${C.muted}" stroke-width="1" stroke-dasharray="3,3" visibility="hidden"/>
    <circle id="hoverdot" r="4.5" fill="${C.series}" stroke="${C.surface}" stroke-width="2"
      visibility="hidden"/>`;
}

$("fleet-chart-wrap").addEventListener("mousemove", (e) => {
  if (!chartPoints.length) return;
  const svg = $("fleet-chart");
  const rect = svg.getBoundingClientRect();
  const vb = svg.viewBox.baseVal;
  const mx = ((e.clientX - rect.left) / rect.width) * vb.width;
  let best = chartPoints[0];
  for (const p of chartPoints) if (Math.abs(p.x - mx) < Math.abs(best.x - mx)) best = p;
  const cross = $("crosshair"), dot = $("hoverdot"), tip = $("fleet-tooltip");
  if (!cross) return;
  cross.setAttribute("x1", best.x); cross.setAttribute("x2", best.x);
  cross.setAttribute("visibility", "visible");
  dot.setAttribute("cx", best.x); dot.setAttribute("cy", best.y);
  dot.setAttribute("visibility", "visible");
  const d = new Date(best.t * 1000);
  tip.innerHTML = `<b>${fmtHash(best.gh)}</b><br><span class="t">${d.toLocaleTimeString()}</span>`;
  tip.style.display = "block";
  const px = (best.x / vb.width) * rect.width;
  tip.style.left = Math.min(px + 12, rect.width - 130) + "px";
  tip.style.top = ((best.y / vb.height) * rect.height - 14) + "px";
});
$("fleet-chart-wrap").addEventListener("mouseleave", () => {
  $("fleet-tooltip").style.display = "none";
  const c = $("crosshair"), d = $("hoverdot");
  if (c) c.setAttribute("visibility", "hidden");
  if (d) d.setAttribute("visibility", "hidden");
});
window.addEventListener("resize", () => { if (snapshot) renderFleetChart(); });

// ---------- solo odds ----------

function renderSolo() {
  const { solo, fleet, node } = snapshot;
  const el = $("solo-body");
  if (!node.available) {
    el.innerHTML = `<div class="empty">Connect a Bitcoin node to see real solo odds.</div>`;
    return;
  }
  if (solo.syncing) {
    el.innerHTML = `<div class="empty">Node syncing — ${(node.progress * 100).toFixed(1)} %</div>`;
    return;
  }
  if (!solo.expected_seconds) {
    el.innerHTML = `<div class="empty">Waiting for hashrate and difficulty…</div>`;
    return;
  }
  const oddsDay = solo.odds_per_day;
  const oneIn = oddsDay > 0 ? Math.round(1 / oddsDay) : 0;
  // 30 pips = one month of daily draws; lit pips scale with the daily chance
  const lit = Math.max(0, Math.min(30, Math.round(oddsDay * 30 * 30)));
  el.innerHTML = `
    <div class="odds">
      <span class="big">${fmtDur(solo.expected_seconds)}</span>
      <span class="cap">expected time to a block, on average</span>
    </div>
    <div class="lottery" aria-hidden="true">
      ${Array.from({ length: 30 }, (_, i) => `<i class="${i < lit ? "on" : ""}"></i>`).join("")}
    </div>
    <dl class="kv">
      <dt>Chance in next 24 h</dt><dd>${(oddsDay * 100).toPrecision(2)} % — about 1 in ${oneIn.toLocaleString()}</dd>
      <dt>Your fleet</dt><dd>${fmtHash(fleet.total_hash_gh)}</dd>
      <dt>Network difficulty</dt><dd>${fmtDiff(solo.network_difficulty)}</dd>
      <dt>Network hashrate</dt><dd>${node.network_hashps ? fmtHash(node.network_hashps / 1e9) : "–"}</dd>
      <dt>Block reward</dt><dd>3.125 BTC + fees</dd>
    </dl>`;
}

// ---------- miners ----------

function sparkline(history) {
  const hs = (history || []).slice(-60);
  if (hs.length < 2) return `<div class="spark" style="height:30px"></div>`;
  const vals = hs.map((s) => s.h);
  const max = Math.max(...vals) * 1.08 || 1;
  const min = Math.min(...vals) * 0.92;
  const W = 120, H = 30;
  const pt = (s, i) =>
    `${((i / (hs.length - 1)) * W).toFixed(2)},${(H - ((s.h - min) / Math.max(max - min, 0.001)) * (H - 5) - 2.5).toFixed(2)}`;
  const line = hs.map(pt).join(" ");
  return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none"
    style="width:100%;height:30px" aria-hidden="true">
    <polygon points="0,${H} ${line} ${W},${H}" fill="${C.series}" fill-opacity="0.10"/>
    <polyline points="${line}" fill="none" stroke="${C.series}" stroke-width="1.6"
      stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke"/></svg>`;
}

// Where a device is actually sending its work — the one thing a fleet owner
// must be able to see at a glance.
function destination(i) {
  if (!i.stratumURL) return { cls: "", text: "no pool configured" };
  const target = `${i.stratumURL}:${i.stratumPort}`;
  const engine = snapshot.work && snapshot.work.endpoint;
  if (i.isUsingFallbackStratum) return { cls: "fallback", text: `fallback: ${target}` };
  if (engine && target === engine) return { cls: "own", text: "your own node" };
  return { cls: "", text: target };
}

let minerView = localStorage.getItem("nodeos-miner-view") || "cards";
let tuningPresets = {};

function tempClass(t) { return t >= 68 ? "hot" : t >= 60 ? "warn" : ""; }

function renderMiners() {
  const miners = snapshot.miners || [];
  $("miners-empty").hidden = miners.length > 0;
  document.querySelectorAll("#miner-view button").forEach((b) =>
    b.classList.toggle("active", b.dataset.view === minerView));

  const grid = $("miner-grid");
  if (minerView === "table") {
    grid.className = "";
    grid.innerHTML = minerTable(miners);
    return;
  }
  grid.className = "miner-grid";
  grid.innerHTML = miners.map((m) => {
    const i = m.info || {};
    const dest = destination(i);
    const tc = tempClass(i.temp || 0);
    const tempPct = Math.min(100, ((i.temp || 0) / 80) * 100);
    return `
    <div class="miner-card ${m.online ? "" : "offline"}" data-host="${esc(m.host)}">
      <div class="top">
        <a class="name miner-link" href="http://${esc(m.host)}/" target="_blank" rel="noopener"
           title="Open this miner's own web interface (AxeOS)">${esc(m.label)}<span class="ext">↗</span></a>
        <span class="model">${esc(i.ASICModel || m.source)}</span>
        <span class="status-chip ${m.online ? "online" : "offline"}"><span class="dot"></span>${m.online ? "online" : "offline"}</span>
      </div>
      <div class="hash">${m.online ? fmtHashParts(i.hashRate || 0) : "–"}</div>
      ${sparkline(m.history)}
      <div class="metrics">
        <div class="metric"><span class="k">Temp</span><span class="v ${tc}">${i.temp ? i.temp.toFixed(0) + " °C" : "–"}</span></div>
        <div class="metric"><span class="k">Power</span><span class="v">${i.power ? i.power.toFixed(1) + " W" : "–"}</span></div>
        <div class="metric"><span class="k">Shares</span><span class="v">${(i.sharesAccepted ?? 0).toLocaleString()}${i.sharesRejected ? ` <span style="color:var(--muted)">/${i.sharesRejected}</span>` : ""}</span></div>
        <div class="metric"><span class="k">Best</span><span class="v">${esc(i.bestDiff || "–")}</span></div>
        <div class="metric"><span class="k">Freq</span><span class="v">${i.frequency ? i.frequency + " MHz" : "–"}</span></div>
        <div class="metric"><span class="k">Uptime</span><span class="v">${fmtUptime(i.uptimeSeconds)}</span></div>
      </div>
      ${i.temp ? `<div class="bar"><i class="${tc}" style="width:${tempPct.toFixed(0)}%"></i></div>` : ""}
      <div class="dest-line ${dest.cls}" title="${esc(i.stratumUser || "")}">
        <span>→</span><span class="val">${esc(dest.text)}</span>
      </div>
      ${m.last_error && !m.online ? `<div class="dest-line" style="color:var(--critical)"><span class="val">${esc(m.last_error)}</span></div>` : ""}
      ${tuningRow(i)}
      <div class="actions">
        <button class="btn small" data-act="restart">Restart</button>
        <button class="btn small danger" data-act="remove">Remove</button>
      </div>
    </div>`;
  }).join("");
}

// tuning presets are per ASIC family; unknown models simply get no row
function tuningRow(info) {
  const list = tuningPresets[(info.ASICModel || "").toUpperCase()];
  if (!list || !info.frequency) return "";
  const active = list.find((p) => Math.abs(p.frequency - info.frequency) < 1);
  return `<div class="tune-row">
    <span class="k">Tuning</span>
    ${list.map((p) => `<button class="tune-btn${active && active.id === p.id ? " active" : ""}"
      data-act="tune" data-preset="${p.id}" title="${esc(p.name)} — ${p.frequency} MHz, ${p.core_voltage} mV${p.note ? " · " + esc(p.note) : ""}">${esc(p.name)}</button>`).join("")}
  </div>`;
}

function minerTable(miners) {
  if (!miners.length) return "";
  return `<div class="panel" style="margin:0"><div class="table-wrap"><table class="plain">
    <thead><tr>
      <th>Miner</th><th>Model</th><th class="num">Hashrate</th><th class="num">Temp</th>
      <th class="num">Power</th><th class="num">Shares</th><th class="num">Best</th>
      <th>Destination</th><th></th>
    </tr></thead>
    <tbody>${miners.map((m) => {
      const i = m.info || {};
      const d = destination(i);
      return `<tr data-host="${esc(m.host)}">
        <td><span class="status-chip ${m.online ? "online" : "offline"}" style="margin:0"><span class="dot"></span></span>
          <a class="miner-link" href="http://${esc(m.host)}/" target="_blank" rel="noopener"
             title="Open this miner's own web interface">${esc(m.label)}<span class="ext">↗</span></a></td>
        <td>${esc(i.ASICModel || m.source)}</td>
        <td class="num">${m.online ? fmtHash(i.hashRate || 0) : "–"}</td>
        <td class="num ${tempClass(i.temp || 0)}">${i.temp ? i.temp.toFixed(0) + " °C" : "–"}</td>
        <td class="num">${i.power ? i.power.toFixed(1) + " W" : "–"}</td>
        <td class="num">${(i.sharesAccepted ?? 0).toLocaleString()}</td>
        <td class="num">${esc(i.bestDiff || "–")}</td>
        <td style="color:var(--${d.cls === "own" ? "accent" : d.cls === "fallback" ? "warning" : "muted"})">${esc(d.text)}</td>
        <td class="num"><button class="btn small" data-act="restart">Restart</button></td>
      </tr>`;
    }).join("")}</tbody>
  </table></div></div>`;
}

$("miner-view").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-view]");
  if (!btn) return;
  minerView = btn.dataset.view;
  localStorage.setItem("nodeos-miner-view", minerView);
  if (snapshot) renderMiners();
});

$("miner-grid").addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-act]");
  if (!btn) return;
  const row = btn.closest("[data-host]");
  const host = row && row.dataset.host;
  if (!host) return;
  try {
    if (btn.dataset.act === "tune") {
      const preset = btn.dataset.preset;
      if (!confirm(`Apply the "${preset}" preset? The miner restarts and hashrate changes.`)) return;
      btn.disabled = true;
      await api("POST", `/api/miners/${encodeURIComponent(host)}/tune`, { preset });
      toast(`${preset} applied — miner restarting`, "ok");
      return;
    }
    if (btn.dataset.act === "restart") {
      btn.disabled = true;
      await api("POST", `/api/miners/${encodeURIComponent(host)}/restart`);
      btn.textContent = "Restarting…";
      setTimeout(() => { btn.disabled = false; btn.textContent = "Restart"; }, 4000);
    } else if (btn.dataset.act === "remove") {
      if (!confirm(`Remove ${host} from NodeOS? The device itself keeps mining.`)) return;
      await api("DELETE", `/api/miners/${encodeURIComponent(host)}`);
      snapshot.miners = snapshot.miners.filter((m) => m.host !== host);
      renderMiners();
    }
  } catch (err) {
    toast(err.message, "err");
    btn.disabled = false;
  }
});

// ---------- firmware ----------

let fwData = null;

async function loadFirmware() {
  const btn = $("fw-load");
  btn.disabled = true;
  btn.textContent = "Checking…";
  try {
    fwData = await api("GET", "/api/firmware");
    renderFirmware();
  } catch (err) {
    $("fw-body").innerHTML = `<div class="note fail">${esc(err.message)}</div>`;
  } finally {
    btn.disabled = false;
    btn.textContent = "Check for firmware";
  }
}

function renderFirmware() {
  if (!fwData) return;
  const miners = (snapshot && snapshot.miners) || [];
  const versions = {};
  for (const m of miners) {
    if (m.online && m.info && m.info.version) versions[m.info.version] = (versions[m.info.version] || 0) + 1;
  }
  $("fw-versions").textContent = Object.entries(versions)
    .map(([v, n]) => `${v} ×${n}`).join(" · ") || "no devices";

  if (fwData.error) {
    $("fw-body").innerHTML = `<div class="note fail">${esc(fwData.error)}</div>`;
  } else {
    const rels = (fwData.releases || []).slice(0, 5);
    $("fw-body").innerHTML = rels.length ? `
      <div class="table-wrap"><table class="plain">
        <thead><tr><th>Release</th><th>File</th><th class="num">Size</th><th></th></tr></thead>
        <tbody>${rels.flatMap((r) => r.assets
          .filter((a) => a.kind !== "factory")
          .map((a) => `<tr>
            <td>${esc(r.tag)}</td>
            <td>${esc(a.name)}${a.kind === "www" ? ` <span style="color:var(--muted)">web UI</span>` : ""}</td>
            <td class="num">${fmtBytes(a.size)}</td>
            <td class="num"><button class="btn small" data-fw="${esc(a.url)}" data-www="${a.kind === "www"}">Roll out</button></td>
          </tr>`)).join("")}</tbody>
      </table></div>
      <div class="note">Factory images are hidden — they cannot be flashed over the air.
        A rollout always starts with one canary device and only continues once it is
        back and hashing.</div>` :
      `<div class="note">No releases found.</div>`;
  }
  renderRollout(fwData.rollout);
}

function renderRollout(ro) {
  const box = $("fw-rollout");
  if (!ro || (!ro.running && !ro.devices?.length)) { box.hidden = true; return; }
  box.hidden = false;
  $("fw-roll-title").textContent = ro.running
    ? `Rolling out ${ro.firmware}`
    : `Rollout ${ro.ok ? "complete" : "stopped"} — ${ro.firmware}`;
  $("fw-cancel").hidden = !ro.running;
  $("fw-roll-msg").innerHTML = `${esc(ro.message || "")}${
    ro.sha256 ? ` <span style="color:var(--muted)">· sha256 ${esc(ro.sha256.slice(0, 16))}…</span>` : ""}`;
  const chip = (st) => ({
    ok: `<span class="status-chip online"><span class="dot"></span>done</span>`,
    failed: `<span class="status-chip offline"><span class="dot"></span>failed</span>`,
    flashing: `<span class="status-chip warn"><span class="dot"></span>flashing</span>`,
    verifying: `<span class="status-chip warn"><span class="dot"></span>verifying</span>`,
    skipped: `<span class="badge">skipped</span>`,
  }[st] || `<span class="badge">pending</span>`);
  $("fw-roll-list").innerHTML = `<div class="table-wrap"><table class="plain">
    <tbody>${ro.devices.map((d, i) => `<tr>
      <td>${esc(d.label)}${d.host === ro.canary ? ` <span class="badge accent">canary</span>` : ""}</td>
      <td>${chip(d.state)}</td>
      <td style="color:var(--muted)">${esc(d.detail || "")}</td>
    </tr>`).join("")}</tbody></table></div>`;
}

$("fw-load").addEventListener("click", loadFirmware);

$("fw-body").addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-fw]");
  if (!btn) return;
  const online = ((snapshot && snapshot.miners) || []).filter((m) => m.online);
  if (!online.length) { toast("No online miners", "err"); return; }
  if (!confirm(`Flash ${online.length} miner(s)?\n\n` +
      `NodeOS flashes ONE device first and only continues once it is back and hashing. ` +
      `Do not power off any miner during the rollout.`)) return;
  try {
    await api("POST", "/api/firmware/rollout", {
      url: btn.dataset.fw, www: btn.dataset.www === "true",
      hosts: online.map((m) => m.host),
    });
    toast("Rollout started — canary first", "ok");
    loadFirmware();
  } catch (err) { toast(err.message, "err"); }
});

$("fw-cancel").addEventListener("click", async () => {
  try {
    await api("POST", "/api/firmware/cancel");
    toast("Rollout will stop after the current device", "ok");
  } catch (err) { toast(err.message, "err"); }
});

// keep a running rollout live
setInterval(() => {
  if (document.querySelector("#tab-miners.active") && $("auth-overlay").hidden &&
      !document.hidden && fwData && fwData.rollout && fwData.rollout.running) {
    loadFirmware();
  }
}, 5000);

// ---------- node ----------

function ago(unix) {
  if (!unix) return "–";
  const s = Math.max(0, Math.floor(Date.now() / 1000) - unix);
  if (s < 90) return `${s} s ago`;
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 172800) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

function renderNode() {
  const n = snapshot.node;

  if (!n.available) {
    $("node-hero").innerHTML = `
      <div class="panel">
        <div class="empty">
          <strong>No Bitcoin node reachable</strong>
          ${n.error ? `<code>${esc(n.error)}</code><br><br>` : ""}
          Install one below, or point <code>/etc/nodeos/config.json</code> at an
          existing node's RPC.
        </div>
      </div>`;
    $("node-tiles").innerHTML = "";
    $("node-chain").innerHTML = `<div class="empty">–</div>`;
    $("node-net").innerHTML = `<div class="empty">–</div>`;
    return;
  }

  const pct = n.progress * 100;
  const syncing = n.ibd || n.progress < 0.9999;
  const behind = Math.max(0, n.headers - n.blocks);

  $("node-hero").innerHTML = `
    <div class="hero">
      <div class="hero-top">
        <div style="min-width:0">
          <div class="hero-label">${syncing ? "Synchronising" : "Node in sync"}</div>
          <div class="hero-value">${syncing ? pct.toFixed(2) + "<small>%</small>"
            : n.blocks.toLocaleString()}</div>
          <div class="hero-sub">
            ${syncing
              ? `block ${n.blocks.toLocaleString()} of ${n.headers.toLocaleString()} · ${behind.toLocaleString()} to go`
              : `tip ${ago(n.tip_time)} · ${esc(n.subversion || "")}`}
          </div>
        </div>
        <div class="spacer" style="margin-left:auto"></div>
        <div style="text-align:right">
          <div class="hero-label" style="margin-bottom:8px">Status</div>
          <div class="route ${syncing ? "" : "own"}">
            <span class="pulse ${syncing ? "idle" : "live"}"></span>
            <span class="dest">${syncing ? "catching up" : "ready for solo mining"}</span>
          </div>
        </div>
      </div>
      ${syncing ? `<div class="progress" style="margin-top:16px"><div style="width:${pct.toFixed(2)}%"></div></div>` : ""}
      ${n.warnings ? `<div class="hero-sub" style="color:var(--warning)">⚠ ${esc(n.warnings)}</div>` : ""}
    </div>`;

  $("node-tiles").innerHTML = nodeTiles(n).map(tileHTML).join("");

  $("node-chain").innerHTML = `
    <dl class="kv">
      <dt>Implementation</dt><dd>${esc(n.subversion || "Bitcoin Core")}</dd>
      <dt>Network</dt><dd>${esc(n.chain)}</dd>
      <dt>Blocks / headers</dt><dd>${n.blocks.toLocaleString()} / ${n.headers.toLocaleString()}</dd>
      <dt>Verification</dt><dd>${pct.toFixed(3)} %</dd>
      <dt>Difficulty</dt><dd>${fmtDiff(n.difficulty)}</dd>
      <dt>Network hashrate</dt><dd>${n.network_hashps ? fmtHash(n.network_hashps / 1e9) : "–"}</dd>
      <dt>Node uptime</dt><dd>${fmtUptime(n.uptime_s)}</dd>
      ${n.tip_hash ? `<dt>Tip hash</dt><dd style="word-break:break-all;font-size:11.5px">${esc(n.tip_hash)}</dd>` : ""}
    </dl>`;

  const memPct = n.mempool_max ? Math.min(100, (n.mempool_usage / n.mempool_max) * 100) : 0;
  $("node-net").innerHTML = `
    <dl class="kv">
      <dt>Connections</dt><dd>${n.connections} total · ${n.peers_out || 0} outbound · ${n.connections_in} inbound</dd>
      <dt>Traffic received</dt><dd>${fmtBytes(n.bytes_recv)}</dd>
      <dt>Traffic sent</dt><dd>${fmtBytes(n.bytes_sent)}</dd>
      <dt>Mempool</dt><dd>${n.mempool_txs.toLocaleString()} tx · ${fmtBytes(n.mempool_usage || n.mempool_bytes)}</dd>
      <dt>Minimum fee</dt><dd>${n.mempool_min_fee ? (n.mempool_min_fee * 1e5).toFixed(2) + " sat/vB" : "–"}</dd>
    </dl>
    ${n.mempool_max ? `<div class="bar" style="margin-top:12px"><i style="width:${memPct.toFixed(1)}%"></i></div>
      <div class="note">Mempool ${memPct.toFixed(0)} % full</div>` : ""}`;
}

// ---------- peer map ----------
//
// Equirectangular projection into the 1000×500 viewBox that world.js is
// generated for. Peers are placed at their country's centroid — the geo table
// is country-level on purpose, so the map must not pretend to be more precise
// than that.

const MAP_W = 1000, MAP_H = 500;
const proj = (lat, lon) => [(lon + 180) / 360 * MAP_W, (90 - lat) / 180 * MAP_H];

let peerData = { peers: [], self: null };
let peersLoadedOnce = false;

// ---- zoom & pan (viewBox based; strokes are non-scaling) ----

let mapView = { x: 0, y: 0, w: MAP_W, h: MAP_H };

function applyMapView() {
  const svg = $("peer-map");
  svg.setAttribute("viewBox", `${mapView.x.toFixed(1)} ${mapView.y.toFixed(1)} ${mapView.w.toFixed(1)} ${mapView.h.toFixed(1)}`);
  // counter-scale point markers (sqrt: shrink a little, stay visible)
  const k = 1 / Math.sqrt(MAP_W / mapView.w);
  svg.querySelectorAll(".peer-dot").forEach((el) =>
    el.setAttribute("r", (parseFloat(el.dataset.r) * k).toFixed(2)));
  const sm = svg.querySelector(".self-mark");
  if (sm) sm.setAttribute("transform",
    `translate(${sm.dataset.x} ${sm.dataset.y}) scale(${k.toFixed(3)})`);
}

function mapZoom(factor, cx, cy) {
  // cx/cy in viewBox coordinates; defaults to the centre of the current view
  if (cx === undefined) { cx = mapView.x + mapView.w / 2; cy = mapView.y + mapView.h / 2; }
  let w = Math.min(MAP_W, Math.max(MAP_W / 10, mapView.w * factor));
  const h = w / 2;
  let x = cx - (cx - mapView.x) * (w / mapView.w);
  let y = cy - (cy - mapView.y) * (h / mapView.h);
  mapView = {
    x: Math.max(0, Math.min(MAP_W - w, x)),
    y: Math.max(0, Math.min(MAP_H - h, y)),
    w, h,
  };
  applyMapView();
}

function mapPoint(e) {
  const rect = $("peer-map").getBoundingClientRect();
  return [
    mapView.x + ((e.clientX - rect.left) / rect.width) * mapView.w,
    mapView.y + ((e.clientY - rect.top) / rect.height) * mapView.h,
  ];
}

$("map-zin").addEventListener("click", () => mapZoom(1 / 1.5));
$("map-zout").addEventListener("click", () => mapZoom(1.5));
$("map-zreset").addEventListener("click", () => { mapView = { x: 0, y: 0, w: MAP_W, h: MAP_H }; applyMapView(); });

$("peer-map").addEventListener("wheel", (e) => {
  e.preventDefault();
  const [mx, my] = mapPoint(e);
  mapZoom(e.deltaY < 0 ? 1 / 1.25 : 1.25, mx, my);
}, { passive: false });

$("peer-map").addEventListener("dblclick", (e) => {
  e.preventDefault();
  const [mx, my] = mapPoint(e);
  mapZoom(1 / 1.8, mx, my);
});

let panState = null;
$("peer-map").addEventListener("pointerdown", (e) => {
  panState = { px: e.clientX, py: e.clientY, x: mapView.x, y: mapView.y, moved: false };
  $("peer-map").setPointerCapture(e.pointerId);
});
$("peer-map").addEventListener("pointermove", (e) => {
  if (!panState) return;
  if (Math.abs(e.clientX - panState.px) + Math.abs(e.clientY - panState.py) > 5) panState.moved = true;
  if (!panState.moved) return;
  const rect = $("peer-map").getBoundingClientRect();
  const dx = (e.clientX - panState.px) * (mapView.w / rect.width);
  const dy = (e.clientY - panState.py) * (mapView.h / rect.height);
  mapView.x = Math.max(0, Math.min(MAP_W - mapView.w, panState.x - dx));
  mapView.y = Math.max(0, Math.min(MAP_H - mapView.h, panState.y - dy));
  applyMapView();
});
$("peer-map").addEventListener("pointerup", () => { panState = null; });
$("peer-map").addEventListener("pointercancel", () => { panState = null; });

// ---- rendering ----

function renderPeerMap() {
  const svg = $("peer-map");
  if (typeof WORLD_PATHS === "undefined") return;

  const land = WORLD_PATHS.map((d) => `<path d="${d}"/>`).join("");

  // group peers by country so a dot's size means "how many peers there"
  const byCC = new Map();
  let unlocated = 0;
  for (const p of peerData.peers) {
    const cc = p.country && COUNTRY_LATLON[p.country] ? p.country : null;
    if (!cc) { unlocated++; continue; }
    if (!byCC.has(cc)) byCC.set(cc, []);
    byCC.get(cc).push(p);
  }

  // self position: explicit coordinates, or the country of the node's own
  // public address resolved to its centroid
  let self = null;
  const sf = peerData.self;
  if (sf) {
    if (sf.lat !== undefined && sf.lon !== undefined) self = proj(sf.lat, sf.lon);
    else if (sf.country && COUNTRY_LATLON[sf.country]) {
      const [la, lo] = COUNTRY_LATLON[sf.country];
      self = proj(la, lo);
    }
  }

  let arcs = "", dots = "", packets = "";
  let i = 0;
  for (const [cc, list] of byCC) {
    const [lat, lon] = COUNTRY_LATLON[cc];
    const [x, y] = proj(lat, lon);
    const r = Math.min(9, 3.2 + Math.log2(list.length + 1) * 1.6);
    const inbound = list.filter((p) => p.inbound).length;
    const outbound = list.length - inbound;

    if (self) {
      // quadratic arc bowed away from the straight line, so overlapping
      // connections stay distinguishable; path runs node → peer
      const mx = (self[0] + x) / 2, my = (self[1] + y) / 2;
      const dx = x - self[0], dy = y - self[1];
      const len = Math.hypot(dx, dy) || 1;
      const bow = Math.min(70, len * 0.22);
      const cx = mx - (dy / len) * bow, cy = my + (dx / len) * bow;
      const d = `M${self[0].toFixed(1)} ${self[1].toFixed(1)}Q${cx.toFixed(1)} ${cy.toFixed(1)} ${x.toFixed(1)} ${y.toFixed(1)}`;
      arcs += `<path class="arc" d="${d}"/>`;

      // data packets travelling the arc: gold outward, green inward
      const dur = (2.2 + len / 130).toFixed(2);
      const begin = ((i * 0.53) % 3).toFixed(2);
      if (outbound || !inbound) {
        packets += `<circle class="packet out" r="2.1">
          <animateMotion dur="${dur}s" begin="${begin}s" repeatCount="indefinite" path="${d}"/>
        </circle>`;
      }
      if (inbound) {
        packets += `<circle class="packet in" r="2.1">
          <animateMotion dur="${dur}s" begin="${(+begin + 1.1).toFixed(2)}s" repeatCount="indefinite"
            keyPoints="1;0" keyTimes="0;1" calcMode="linear" path="${d}"/>
        </circle>`;
      }
    }
    dots += `<circle class="peer-dot${inbound === list.length && list.length ? " inbound" : ""}"
      cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="${r.toFixed(1)}" data-r="${r.toFixed(1)}"
      data-cc="${cc}" data-n="${list.length}" data-in="${inbound}"/>`;
    i++;
  }

  const selfMark = self ? `
    <g class="self-mark" data-x="${self[0].toFixed(1)}" data-y="${self[1].toFixed(1)}"
       transform="translate(${self[0].toFixed(1)} ${self[1].toFixed(1)})">
      <circle class="halo" r="14"/>
      <circle class="core" r="4.5"/>
    </g>` : "";

  svg.innerHTML = `<g class="land">${land}</g><g class="arcs">${arcs}</g>` +
    `<g class="packets">${packets}</g>${selfMark}<g class="dots">${dots}</g>`;
  applyMapView(); // keep the current zoom across refreshes

  const located = peerData.peers.length - unlocated;
  $("map-legend").innerHTML = self ? `
    <span><i class="key self"></i>your node · ${esc(sf.zone)}</span>
    <span><i class="key out"></i>outbound</span>
    <span><i class="key in"></i>inbound</span>
    <span class="muted">${located}/${peerData.peers.length} peers located${
      unlocated ? ` · ${unlocated} Tor/unknown` : ""}</span>` : `
    <span><i class="key out"></i>outbound</span>
    <span><i class="key in"></i>inbound</span>
    <span class="muted">${located}/${peerData.peers.length} peers located · node position pending (learned from peers)</span>`;

  svg.querySelectorAll(".peer-dot").forEach((el) => {
    el.addEventListener("mouseenter", (e) => {
      const cc = el.dataset.cc, n = +el.dataset.n, inb = +el.dataset.in;
      const tip = $("map-tooltip");
      tip.innerHTML = `<b>${esc(cc)}</b> — ${n} peer${n > 1 ? "s" : ""}<br>
        <span class="t">${inb} inbound · ${n - inb} outbound</span>`;
      tip.style.display = "block";
      const wrap = svg.parentElement.getBoundingClientRect();
      const dot = el.getBoundingClientRect();
      tip.style.left = Math.min(dot.left - wrap.left + 14, wrap.width - 150) + "px";
      tip.style.top = (dot.top - wrap.top - 8) + "px";
    });
    el.addEventListener("mouseleave", () => { $("map-tooltip").style.display = "none"; });
  });
}

$("map-refresh").addEventListener("click", loadPeers);

// Placing the node by clicking the map keeps the whole feature offline: no
// geocoder, no public-IP probe, and precise enough for a country-level map.
// ---------- peers ----------

$("peers-refresh").addEventListener("click", loadPeers);

async function loadPeers() {
  const btn = $("peers-refresh");
  btn.disabled = true;
  peersLoadedOnce = true;
  try {
    const resp = await api("GET", "/api/node/peers");
    const peers = resp.peers || [];
    peerData = { peers, self: resp.self || null };
    renderPeerMap();
    if (!peers.length) {
      $("peers-body").innerHTML = `<div class="empty">No peers connected.</div>`;
      return;
    }
    peers.sort((a, b) => b.connected_s - a.connected_s);
    $("peers-body").innerHTML = `
      <div class="table-wrap"><table class="plain">
        <thead><tr>
          <th>Address</th><th>Country</th><th>Type</th><th>Client</th>
          <th class="num">Ping</th><th class="num">Height</th>
          <th class="num">In</th><th class="num">Out</th><th class="num">Connected</th>
        </tr></thead>
        <tbody>${peers.map((p) => `
          <tr>
            <td style="max-width:230px;overflow:hidden;text-overflow:ellipsis">${esc(p.addr)}</td>
            <td>${p.country ? esc(p.country) : `<span style="color:var(--muted)">–</span>`}</td>
            <td>${p.inbound ? "inbound" : "outbound"}${p.network && p.network !== "not_publicly_routable"
              ? ` <span style="color:var(--muted)">${esc(p.network)}</span>` : ""}</td>
            <td style="max-width:190px;overflow:hidden;text-overflow:ellipsis">${esc(p.subver || "–")}</td>
            <td class="num">${p.ping_ms ? p.ping_ms.toFixed(0) + " ms" : "–"}</td>
            <td class="num">${p.height ? p.height.toLocaleString() : "–"}</td>
            <td class="num">${fmtBytes(p.bytes_recv)}</td>
            <td class="num">${fmtBytes(p.bytes_sent)}</td>
            <td class="num">${fmtUptime(p.connected_s)}</td>
          </tr>`).join("")}</tbody>
      </table></div>
      <div class="note">${peers.length} peers · updated ${new Date().toLocaleTimeString()}</div>`;
  } catch (err) {
    $("peers-body").innerHTML = `<div class="empty fail">${esc(err.message)}</div>`;
  } finally {
    btn.disabled = false;
    btn.textContent = "Refresh peers";
  }
}

// ---------- lightning ----------

function fmtSats(msat) {
  return Math.floor((msat || 0) / 1000).toLocaleString();
}

async function loadLightning() {
  let ln;
  try {
    ln = await api("GET", "/api/lightning");
  } catch (err) {
    $("ln-body").innerHTML = `<div class="empty fail">${esc(err.message)}</div>`;
    return;
  }
  const body = $("ln-body");
  const svcInstalled = svcData && (svcData.status || []).some((s) => s.id === "lightning" && s.installed);
  const svcRunning = svcData && (svcData.status || []).some((s) => s.id === "lightning" && s.running);

  if (!svcInstalled) {
    body.innerHTML = `<div class="empty"><strong>Core Lightning is not installed</strong>
      Install it on the Services page — this panel comes alive automatically.</div>`;
    $("ln-actions").hidden = true;
    $("ln-channels-panel").hidden = true;
    return;
  }
  if (!ln.connected) {
    body.innerHTML = `<div class="panel" style="text-align:center; padding:40px">
      <div style="font-size:28px; margin-bottom:8px">⚡</div>
      <p style="margin-bottom:16px">Core Lightning is ${svcRunning ? "running" : "installed"} —
        connect the panel once to manage it from here.</p>
      <button class="btn primary" id="ln-connect">Connect panel</button>
      <div class="note" style="margin-top:10px">Mints an access rune via the privileged helper;
        the RPC socket itself stays root-only.</div>
    </div>`;
    $("ln-actions").hidden = true;
    $("ln-channels-panel").hidden = true;
    $("ln-connect").addEventListener("click", async () => {
      try {
        await api("POST", "/api/lightning/connect");
        toast("Connecting… rune is being minted", "ok");
        setTimeout(loadLightning, 4000);
      } catch (err) { toast(err.message, "err"); }
    });
    return;
  }
  if (!ln.available) {
    const transient = (ln.error || "").includes("unreachable");
    body.innerHTML = transient
      ? `<div class="empty"><strong>Core Lightning is starting up</strong>
          Waiting for the node — this page retries automatically every 15 s.</div>`
      : `<div class="empty fail">${esc(ln.error || "Core Lightning is not answering")}</div>`;
    $("ln-actions").hidden = true;
    $("ln-channels-panel").hidden = true;
    return;
  }

  body.innerHTML = `
    <div class="hero">
      <div class="hero-top">
        <div style="min-width:0">
          <div class="hero-label">Lightning node</div>
          <div class="hero-value" style="font-size:30px">${esc(ln.alias || "–")}</div>
          <div class="hero-sub" style="word-break:break-all">${esc(ln.id || "")}</div>
        </div>
        <div class="spacer" style="margin-left:auto"></div>
        <div style="text-align:right">
          <div class="hero-label" style="margin-bottom:8px">Status</div>
          <div class="route ${ln.sync_warning ? "" : "own"}">
            <span class="pulse ${ln.sync_warning ? "idle" : "live"}"></span>
            <span class="dest">${ln.sync_warning ? "syncing" : "ready"}</span>
          </div>
        </div>
      </div>
      ${ln.sync_warning ? `<div class="hero-sub" style="color:var(--warning)">${esc(ln.sync_warning)}</div>` : ""}
    </div>
    <div class="tiles">
      ${[
        { i: "◈", l: "On-chain", v: `${fmtSats(ln.onchain_msat)} <small>sats</small>`,
          s: ln.onchain_unconf_msat ? `+ ${fmtSats(ln.onchain_unconf_msat)} unconfirmed` : "" },
        { i: "→", l: "Can send", v: `${fmtSats(ln.chan_spendable_msat)} <small>sats</small>` },
        { i: "←", l: "Can receive", v: `${fmtSats(ln.chan_receivable_msat)} <small>sats</small>` },
        { i: "⇄", l: "Channels", v: String((ln.channels || []).length),
          s: `${ln.num_peers} peer${ln.num_peers === 1 ? "" : "s"} connected` },
      ].map(tileHTML).join("")}
    </div>`;

  $("ln-actions").hidden = false;
  $("ln-settings-panel").hidden = false;
  loadLnSettings();
  const chPanel = $("ln-channels-panel");
  const chs = ln.channels || [];
  chPanel.hidden = false;
  $("ln-channels").innerHTML = chs.length ? `
    <div class="table-wrap"><table class="plain">
      <thead><tr><th>Peer</th><th>State</th><th class="num">Capacity</th>
        <th class="num">Ours</th><th class="num">Can send</th><th class="num">Can receive</th></tr></thead>
      <tbody>${chs.map((c) => `
        <tr>
          <td style="max-width:220px;overflow:hidden;text-overflow:ellipsis">
            <span class="status-chip ${c.connected ? "online" : "offline"}" style="margin:0"><span class="dot"></span></span>
            ${esc(c.short_id || c.peer_id.slice(0, 20) + "…")}</td>
          <td>${esc(c.state.replace("CHANNELD_", "").toLowerCase())}</td>
          <td class="num">${fmtSats(c.total_msat)}</td>
          <td class="num">${fmtSats(c.ours_msat)}</td>
          <td class="num">${fmtSats(c.spendable_msat)}</td>
          <td class="num">${fmtSats(c.receivable_msat)}</td>
        </tr>`).join("")}</tbody>
    </table></div>` :
    `<div class="empty">No channels yet. Fund the on-chain wallet, then open a channel —
      channel management lands in the next iteration; until then use the CLI:
      <code>podman exec nodeos-svc-lightning lightning-cli fundchannel …</code></div>`;
}

let lnSettingsLoaded = false;

async function loadLnSettings() {
  if (lnSettingsLoaded) return;
  lnSettingsLoaded = true;
  try {
    const s = await api("GET", "/api/lightning/settings");
    if (s.alias) $("ln-set-alias").value = s.alias;
    if (s.rgb) $("ln-set-rgb").value = "#" + s.rgb;
  } catch {}
}

$("ln-set-apply").addEventListener("click", async () => {
  const alias = $("ln-set-alias").value.trim();
  if (alias && !/^[A-Za-z0-9._-]{1,32}$/.test(alias)) {
    $("ln-set-result").innerHTML = `<span class="fail">Alias: 1–32 characters, letters/digits/._- only (no spaces)</span>`;
    return;
  }
  if (!confirm("Apply Lightning options? Core Lightning restarts (a few seconds).")) return;
  try {
    await api("PUT", "/api/lightning/settings", {
      alias, rgb: $("ln-set-rgb").value.replace("#", ""),
    });
    $("ln-set-result").innerHTML = `<span class="ok">Applying — Lightning is restarting…</span>`;
    toast("Lightning options applied", "ok");
    setTimeout(loadLightning, 8000);
  } catch (err) {
    $("ln-set-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

async function copyText(text, btn) {
  try {
    await navigator.clipboard.writeText(text);
    const old = btn.textContent;
    btn.textContent = "Copied ✓";
    setTimeout(() => { btn.textContent = old; }, 1500);
  } catch {
    toast("Clipboard blocked — select and copy manually", "err");
  }
}

$("ln-addr-btn").addEventListener("click", async () => {
  try {
    const d = await api("POST", "/api/lightning/address");
    $("ln-addr").textContent = d.address;
    $("ln-addr-out").hidden = false;
  } catch (err) { toast(err.message, "err"); }
});
$("ln-addr-copy").addEventListener("click", (e) => copyText($("ln-addr").textContent, e.target));

$("ln-inv-btn").addEventListener("click", async () => {
  try {
    const d = await api("POST", "/api/lightning/invoice", {
      amount_sat: parseInt($("ln-inv-amount").value, 10) || 0,
      description: $("ln-inv-desc").value || "NodeOS",
    });
    $("ln-inv").textContent = d.bolt11;
    $("ln-inv-out").hidden = false;
    $("ln-result").innerHTML = "";
  } catch (err) {
    $("ln-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});
$("ln-inv-copy").addEventListener("click", (e) => copyText($("ln-inv").textContent, e.target));

setInterval(() => {
  if (document.querySelector("#tab-lightning.active") && $("auth-overlay").hidden && !document.hidden) {
    loadLightning();
  }
}, 15000);

// ---------- services ----------

let svcData = null; // /api/services payload
let svcLogsFor = null;

async function loadServices() {
  try {
    svcData = await api("GET", "/api/services");
    renderServices();
  } catch (err) {
    $("svc-grid").innerHTML = `<div class="empty fail">${esc(err.message)}</div>`;
  }
}

const SVC_ICON = { lightning: "⚡", electrs: "🔌", mempool: "◱", btcpay: "₿" };

function renderServices() {
  if (!svcData) return;
  const stById = {};
  for (const st of svcData.status || []) stById[st.id] = st;
  const installedCount = (svcData.status || []).filter((s) => s.installed).length;
  $("nav-svc-count").textContent = installedCount ? `(${installedCount})` : "";

  // the Bitcoin node presented like any other app — it IS the first app
  const n = snapshot ? snapshot.node : null;
  let nodeCard = "";
  if (n) {
    const impl = n.subversion || "Bitcoin Core";
    const pct = (n.progress * 100).toFixed(1);
    const chip = !n.available
      ? `<span class="status-chip offline"><span class="dot"></span>offline</span>`
      : n.ibd || n.progress < 0.9999
        ? `<span class="status-chip warn"><span class="dot"></span>syncing ${pct} %</span>`
        : `<span class="status-chip online"><span class="dot"></span>running</span>`;
    nodeCard = `
    <div class="miner-card svc-card">
      <div class="top">
        <span class="svc-ico">⛓</span>
        <span class="name">Bitcoin node</span>
        ${chip}
      </div>
      <div class="note svc-desc">${esc(impl)} — the foundation every other service builds on.
        ${n.available ? `${n.blocks.toLocaleString()} blocks · ${n.connections} peers${n.pruned ? " · pruned" : ""}` : ""}</div>
      <div class="actions">
        <button class="btn small primary" data-goto="node">Manage</button>
        <button class="btn small" data-goto="lightning">Lightning</button>
      </div>
    </div>`;
  }

  $("svc-grid").innerHTML = nodeCard + (svcData.catalog || []).map((c) => {
    const st = stById[c.id] || {};
    let chip;
    if (c.planned) chip = `<span class="badge">planned</span>`;
    else if (!st.installed) chip = `<span class="badge">not installed</span>`;
    else if (st.running) chip = `<span class="status-chip online"><span class="dot"></span>running</span>`;
    else if (st.degraded) chip = `<span class="status-chip warn"><span class="dot"></span>starting / degraded</span>`;
    else chip = `<span class="status-chip offline"><span class="dot"></span>stopped</span>`;

    const warns = [];
    if (c.requires.full_node && svcData.pruned) {
      warns.push(`needs a full node — yours is pruned`);
    }
    if (c.requires.synced && snapshot && snapshot.solo && snapshot.solo.syncing) {
      warns.push(`starts once the node is synced (${(snapshot.node.progress * 100).toFixed(1)} %)`);
    }
    if (c.requires.disk_gb) warns.push(`~${c.requires.disk_gb} GB disk`);

    let actions = "";
    if (!c.planned) {
      if (!st.installed) {
        actions = `<button class="btn primary small" data-svc="${c.id}" data-act="install"
          ${svcData.helper_available ? "" : "disabled"}>Install</button>`;
      } else {
        actions = `
          ${st.running || st.degraded
            ? `<button class="btn small" data-svc="${c.id}" data-act="stop">Stop</button>
               <button class="btn small" data-svc="${c.id}" data-act="restart">Restart</button>`
            : `<button class="btn small primary" data-svc="${c.id}" data-act="start">Start</button>`}
          <button class="btn small" data-svc="${c.id}" data-act="logs">Logs</button>
          <button class="btn small danger" data-svc="${c.id}" data-act="remove">Remove</button>`;
      }
    }
    const openLink = !c.planned && st.running && c.web_path
      ? `<a class="btn small" href="http://${location.hostname}:${c.port}${c.web_path}" target="_blank" rel="noopener">Open ↗</a>` : "";

    return `
    <div class="miner-card svc-card">
      <div class="top">
        <span class="svc-ico">${SVC_ICON[c.id] || "◆"}</span>
        <span class="name">${esc(c.name)}</span>
        ${chip}
      </div>
      <div class="note svc-desc">${esc(c.description)}</div>
      ${warns.length ? `<div class="dest-line fallback"><span class="val">${esc(warns.join(" · "))}</span></div>` : ""}
      <div class="actions">${actions}${openLink}</div>
    </div>`;
  }).join("");

  renderSvcJob(svcData.job);
}

function renderSvcJob(job) {
  // an explicitly opened log view owns the panel; job output never steals it
  if (svcLogsFor) return;
  const panel = $("svc-job-panel");
  if (!job || !job.log || !job.log.length) { panel.hidden = true; return; }
  panel.hidden = false;
  $("svc-job-title").textContent = "Service job";
  const chip = $("svc-job-state");
  chip.className = "status-chip " + (job.running ? "warn" : job.ok ? "online" : "offline");
  chip.innerHTML = `<span class="dot"></span>${job.running ? esc(job.name) + " running" : job.ok ? "done" : "failed"}`;
  $("svc-job-log").textContent = job.log.join("\n");
  $("svc-job-log").scrollTop = $("svc-job-log").scrollHeight;
}

async function refreshSvcLogs() {
  if (!svcLogsFor) return;
  try {
    const d = await api("GET", `/api/services/${svcLogsFor}/logs`);
    if (!svcLogsFor) return; // closed while fetching
    const panel = $("svc-job-panel");
    const atBottom = $("svc-job-log").scrollHeight - $("svc-job-log").scrollTop
      - $("svc-job-log").clientHeight < 40;
    panel.hidden = false;
    $("svc-job-title").textContent = `Logs — ${svcLogsFor}`;
    $("svc-job-state").className = "status-chip online";
    $("svc-job-state").innerHTML = `<span class="dot"></span>live · refreshes every 5 s`;
    $("svc-job-log").textContent = d.logs || "(empty)";
    if (atBottom) $("svc-job-log").scrollTop = $("svc-job-log").scrollHeight;
  } catch (err) {
    $("svc-job-log").textContent = err.message;
  }
}

$("svc-job-close").addEventListener("click", () => {
  svcLogsFor = null;
  $("svc-job-panel").hidden = true;
});

function gotoTab(tab) {
  const btn = document.querySelector(`nav button[data-tab="${tab}"]`);
  if (btn) btn.click();
}

$("svc-grid").addEventListener("click", async (e) => {
  const nav = e.target.closest("button[data-goto]");
  if (nav) { gotoTab(nav.dataset.goto); return; }
  const btn = e.target.closest("button[data-svc]");
  if (!btn) return;
  const id = btn.dataset.svc, act = btn.dataset.act;
  try {
    if (act === "logs") {
      svcLogsFor = id;
      await refreshSvcLogs();
      $("svc-job-panel").scrollIntoView({ behavior: "smooth", block: "nearest" });
      return;
    }
    if (act === "remove" && !confirm(`Remove ${id}? Its data under /var/lib/nodeos-services stays on disk.`)) return;
    btn.disabled = true;
    await api("POST", `/api/services/${id}/${act}`);
    toast(`${id}: ${act} started`, "ok");
    setTimeout(loadServices, 1500);
  } catch (err) {
    toast(err.message, "err");
    btn.disabled = false;
  }
});

// keep the services page fresh while a job runs; opened logs refresh live
setInterval(() => {
  if (document.querySelector("#tab-services.active") && $("auth-overlay").hidden && !document.hidden) {
    loadServices();
    refreshSvcLogs();
  }
}, 5000);

// ---------- node settings (bitcoin.conf) ----------

let cfgState = null; // {schema, values, impl, helper_available}

async function loadNodeConfig() {
  try {
    cfgState = await api("GET", "/api/node/config");
    renderNodeConfig();
  } catch (err) {
    $("cfg-body").innerHTML = `<div class="empty fail">${esc(err.message)}</div>`;
  }
}

function renderNodeConfig() {
  if (!cfgState) return;
  const { schema, values, impl, helper_available, conf_file } = cfgState;
  $("cfg-impl").textContent = impl === "knots" ? "Bitcoin Knots" : "Bitcoin Core";
  $("cfg-path").textContent = conf_file || "bitcoin.conf";

  if (cfgState.error) {
    $("cfg-body").innerHTML = `<div class="empty">
      <strong>Settings unavailable</strong>
      ${esc(cfgState.error)}<br>
      NodeOS can only manage a node it installed itself.</div>`;
    $("cfg-apply").disabled = true;
    return;
  }

  const groups = [];
  for (const s of schema) {
    if (s.knots_only && impl !== "knots") continue;
    let g = groups.find((x) => x.name === s.group);
    if (!g) groups.push((g = { name: s.group, items: [] }));
    g.items.push(s);
  }

  $("cfg-body").innerHTML = groups.map((g) => `
    <div style="margin-bottom:22px">
      <div class="hero-label" style="margin-bottom:10px">${esc(g.name)}</div>
      <div class="grid-2">
        ${g.items.map((s) => {
          const v = values[s.key] ?? s.default;
          let field;
          if (s.type === "bool") {
            field = `<label class="check" style="margin-top:6px">
              <input type="checkbox" data-cfg="${s.key}" ${v === "1" ? "checked" : ""}>
              <span>${v === "1" ? "enabled" : "disabled"}</span></label>`;
          } else if (s.type === "enum") {
            field = `<select data-cfg="${s.key}">${s.options.map((o) =>
              `<option value="${esc(o)}" ${o === v ? "selected" : ""}>${o === "" ? "all networks" : esc(o)}</option>`
            ).join("")}</select>`;
          } else {
            field = `<input data-cfg="${s.key}" value="${esc(v)}"
              ${s.type === "int" ? `type="number" min="${s.min}" max="${s.max}"` : ""}>`;
          }
          return `
            <div>
              <label>${esc(s.label)}${s.unit ? ` <span style="color:var(--muted)">(${esc(s.unit)})</span>` : ""}
                ${s.reindex ? `<span class="badge" style="color:var(--warning);margin-left:4px">reindex</span>` : ""}
              </label>
              ${field}
              <div class="note" style="margin-top:6px">${esc(s.help)}</div>
            </div>`;
        }).join("")}
      </div>
    </div>`).join("") +
    (helper_available ? "" :
      `<div class="note fail">The privileged helper is not installed, so settings cannot be applied from here.</div>`);

  $("cfg-apply").disabled = true;
  $("cfg-body").querySelectorAll("[data-cfg]").forEach((el) => {
    el.addEventListener("input", () => {
      if (el.type === "checkbox") el.nextElementSibling.textContent = el.checked ? "enabled" : "disabled";
      $("cfg-apply").disabled = !helper_available || collectCfgChanges().count === 0;
    });
  });
}

function collectCfgChanges() {
  const changed = {};
  let count = 0;
  if (!cfgState) return { changed, count };
  $("cfg-body").querySelectorAll("[data-cfg]").forEach((el) => {
    const key = el.dataset.cfg;
    const val = el.type === "checkbox" ? (el.checked ? "1" : "0") : String(el.value).trim();
    if (val !== String(cfgState.values[key] ?? "")) { changed[key] = val; count++; }
  });
  return { changed, count };
}

$("cfg-reset").addEventListener("click", renderNodeConfig);

$("cfg-apply").addEventListener("click", async () => {
  const { changed, count } = collectCfgChanges();
  if (!count) return;
  const reindex = (cfgState.schema || []).filter((s) => s.reindex && changed[s.key] !== undefined);
  const warn = reindex.length
    ? `\n\nThis change forces a reindex: ${reindex.map((s) => s.label).join(", ")}. ` +
      `The node will be busy for hours and cannot build templates meanwhile.`
    : "";
  if (!confirm(`Apply ${count} change(s) and restart the Bitcoin node?${warn}`)) return;
  $("cfg-apply").disabled = true;
  try {
    await api("PUT", "/api/node/config", changed);
    toast("Settings applied — the node is restarting", "ok");
    $("cfg-result").innerHTML = `<span class="ok">Applied. Watching the node…</span>`;
    setTimeout(loadNodeConfig, 8000);
  } catch (err) {
    toast(err.message, "err");
    $("cfg-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
    $("cfg-apply").disabled = false;
  }
});

// ---------- system health ----------

function renderSystem() {
  const sys = snapshot.system;
  const el = $("sys-body");
  if (!sys || !sys.checked_at || sys.cpu_count === 0) {
    el.innerHTML = `<div class="empty">System metrics unavailable (Linux only).</div>`;
    $("sys-tiles").innerHTML = "";
    return;
  }
  const memPct = sys.mem_total_b ? 100 * (sys.mem_total_b - sys.mem_avail_b) / sys.mem_total_b : 0;
  const dataDisk = (sys.disks || []).reduce((a, d) => (!a || d.free_b < a.free_b ? d : a), null);
  $("sys-tiles").innerHTML = [
    { i: "◱", l: "CPU", v: sys.cpu_pct != null ? `${sys.cpu_pct.toFixed(0)} <small>%</small>` : "–",
      s: `${sys.cpu_count} cores · load ${sys.load1.toFixed(2)}`,
      c: sys.cpu_pct > 92 ? "warn" : "" },
    { i: "▤", l: "Memory", v: `${memPct.toFixed(0)} <small>%</small>`,
      s: `${fmtBytes(sys.mem_total_b - sys.mem_avail_b)} of ${fmtBytes(sys.mem_total_b)}`,
      c: memPct > 92 ? "warn" : "" },
    { i: "🌡", l: "CPU temp", v: sys.cpu_temp_c ? `${sys.cpu_temp_c.toFixed(0)} <small>°C</small>` : "–",
      s: sys.cpu_temp_c >= 80 ? "check airflow" : "", c: sys.cpu_temp_c >= 80 ? "warn" : "" },
    { i: "▣", l: "Disk free", v: dataDisk ? fmtBytes(dataDisk.free_b) : "–",
      s: dataDisk ? `${dataDisk.mount} · ${dataDisk.used_pct.toFixed(0)} % used` : "",
      c: dataDisk && dataDisk.used_pct >= 90 ? "warn" : "" },
    { i: "◔", l: "Uptime", v: fmtUptime(sys.uptime_s), s: "" },
  ].map(tileHTML).join("");
  const mem = sys.mem_total_b
    ? `${fmtBytes(sys.mem_total_b - sys.mem_avail_b)} / ${fmtBytes(sys.mem_total_b)}` : "–";
  const disks = (sys.disks || []).map((d) =>
    `<dt>Disk ${esc(d.mount)}</dt><dd>${d.used_pct.toFixed(0)} % used · ${fmtBytes(d.free_b)} free${
      d.used_pct >= 90 ? ' <span class="fail">— almost full!</span>' : ""}</dd>`).join("");
  const smart = (sys.smart || []).map((s) => {
    const state = s.passed === true ? `<span class="ok">healthy</span>`
      : s.passed === false ? `<span class="fail">FAILING — replace!</span>` : "unknown";
    const extra = [s.temp_c ? `${s.temp_c} °C` : "", s.wear_pct ? `${s.wear_pct}% worn` : "",
      s.power_on_hours ? `${Math.round(s.power_on_hours / 24)} days on` : ""].filter(Boolean).join(" · ");
    return `<dt>SMART ${esc(s.device)}</dt><dd>${state}${extra ? " · " + extra : ""} <span style="color:var(--muted)">${esc(s.model || "")}</span></dd>`;
  }).join("");
  el.innerHTML = `
    <dl class="kv">
      <dt>CPU load</dt><dd>${sys.load1.toFixed(2)} / ${sys.load5.toFixed(2)} / ${sys.load15.toFixed(2)} <span style="color:var(--muted)">(${sys.cpu_count} cores)</span></dd>
      <dt>CPU temp</dt><dd>${sys.cpu_temp_c ? sys.cpu_temp_c.toFixed(0) + " °C" : "–"}</dd>
      <dt>Memory</dt><dd>${mem}</dd>
      <dt>Uptime</dt><dd>${fmtUptime(sys.uptime_s)}</dd>
      ${disks}
      ${smart || `<dt>SMART</dt><dd>no report yet (updates every 30 min)</dd>`}
    </dl>`;
}

// ---------- system performance chart ----------

let sysChartPoints = []; // for the tooltip: [{t, cpu, mem, x}]

async function loadSysHistory() {
  try {
    const hist = await api("GET", "/api/system/history");
    drawPerfChart(hist || []);
  } catch { /* chart is decoration; tiles still work */ }
}

function drawPerfChart(samples) {
  const svg = $("sys-chart");
  const wrap = $("sys-chart-wrap");
  const W = Math.max(wrap.clientWidth, 320), H = 240;
  const padL = 44, padR = 12, padT = 12, padB = 24;
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);

  if (samples.length < 2) {
    svg.innerHTML = `<text x="${W / 2}" y="${H / 2}" text-anchor="middle"
      fill="${C.muted}" font-size="13">Collecting samples… (every 15 s)</text>`;
    $("sys-legend").innerHTML = "";
    sysChartPoints = [];
    return;
  }

  const tMin = samples[0].t, tMax = samples[samples.length - 1].t;
  const x = (t) => padL + ((t - tMin) / Math.max(tMax - tMin, 1)) * (W - padL - padR);
  const y = (v) => padT + (1 - v / 100) * (H - padT - padB); // shared 0–100 % axis

  let grid = "";
  for (const v of [0, 25, 50, 75, 100]) {
    grid += `<line x1="${padL}" y1="${y(v)}" x2="${W - padR}" y2="${y(v)}" stroke="${C.grid}" stroke-width="1"/>
      <text x="${padL - 8}" y="${y(v) + 4}" text-anchor="end" fill="${C.muted}" font-size="11">${v}%</text>`;
  }
  let xlab = "";
  for (let i = 0; i <= 2; i++) {
    const t = tMin + ((tMax - tMin) / 2) * i;
    const d = new Date(t * 1000);
    const anchor = i === 0 ? "start" : i === 2 ? "end" : "middle";
    xlab += `<text x="${x(t)}" y="${H - 6}" text-anchor="${anchor}" fill="${C.muted}" font-size="11">
      ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}</text>`;
  }

  const cpuColor = C.series, memColor = "#d95926"; // categorical slots 1 + 2
  const line = (key) => samples.map((s, i) =>
    `${i ? "L" : "M"}${x(s.t).toFixed(1)},${y(Math.min(100, s[key])).toFixed(1)}`).join("");

  sysChartPoints = samples.map((s) => ({ t: s.t, cpu: s.cpu, mem: s.mem, x: x(s.t) }));

  svg.innerHTML = `
    ${grid}${xlab}
    <path d="${line("cpu")}" fill="none" stroke="${cpuColor}" stroke-width="2"
      stroke-linejoin="round" stroke-linecap="round"/>
    <path d="${line("mem")}" fill="none" stroke="${memColor}" stroke-width="2"
      stroke-linejoin="round" stroke-linecap="round"/>
    <line id="sys-cross" x1="0" y1="${padT}" x2="0" y2="${H - padB}"
      stroke="${C.muted}" stroke-width="1" stroke-dasharray="3,3" visibility="hidden"/>`;

  const last = samples[samples.length - 1];
  $("sys-legend").innerHTML = `
    <span class="lk"><i style="background:${cpuColor}"></i>CPU ${last.cpu.toFixed(0)} %</span>
    <span class="lk"><i style="background:${memColor}"></i>RAM ${last.mem.toFixed(0)} %</span>`;
}

$("sys-chart-wrap").addEventListener("mousemove", (e) => {
  if (!sysChartPoints.length) return;
  const svg = $("sys-chart");
  const rect = svg.getBoundingClientRect();
  const vb = svg.viewBox.baseVal;
  const mx = ((e.clientX - rect.left) / rect.width) * vb.width;
  let best = sysChartPoints[0];
  for (const p of sysChartPoints) if (Math.abs(p.x - mx) < Math.abs(best.x - mx)) best = p;
  const cross = $("sys-cross");
  if (!cross) return;
  cross.setAttribute("x1", best.x); cross.setAttribute("x2", best.x);
  cross.setAttribute("visibility", "visible");
  const tip = $("sys-tooltip");
  tip.innerHTML = `<b>CPU ${best.cpu.toFixed(0)} % · RAM ${best.mem.toFixed(0)} %</b><br>
    <span class="t">${new Date(best.t * 1000).toLocaleTimeString()}</span>`;
  tip.style.display = "block";
  const px = (best.x / vb.width) * rect.width;
  tip.style.left = Math.min(px + 12, rect.width - 170) + "px";
  tip.style.top = "16px";
});
$("sys-chart-wrap").addEventListener("mouseleave", () => {
  $("sys-tooltip").style.display = "none";
  const c = $("sys-cross");
  if (c) c.setAttribute("visibility", "hidden");
});

setInterval(() => {
  if (document.querySelector("#tab-system.active") && $("auth-overlay").hidden && !document.hidden) {
    loadSysHistory();
  }
}, 20000);

// ---------- work engine ----------

const WORK_CHIP = {
  running:      ["online",  "running"],
  disabled:     ["offline", "off"],
  waiting_node: ["warn",    "waiting for node"],
  starting:     ["warn",    "starting"],
  backoff:      ["warn",    "restarting"],
  error:        ["offline", "error"],
};

function renderWork() {
  const w = snapshot.work;
  if (!w) return;
  const s = w.settings || {};
  if (!workFormLoaded) {
    $("w-address").value = s.payout_address || "";
    $("w-mode").value = s.mode || "solo";
    $("w-autoswitch").checked = s.auto_switch !== false;
    workFormLoaded = true;
  }
  const [cls, label] = WORK_CHIP[w.state] || ["offline", w.state || "?"];
  $("w-state").innerHTML =
    `<span class="status-chip ${cls}"><span class="dot"></span>${esc(label)}</span>` +
    (w.mock ? ` <span class="badge demo">simulated</span>` : "") +
    (!w.binary_found && !w.mock
      ? ` <span class="badge" style="color:var(--warning)">datum_gateway not installed</span>` : "");
  $("w-detail").textContent = w.detail || "";
  $("w-endpoint").textContent = w.endpoint ? `stratum+tcp://${w.endpoint}` : "–";
  $("w-switched").innerHTML = w.switched
    ? `<span class="ok">yes — fleet mines on your node</span>` : "no";
  $("w-uptime").textContent = w.uptime_s ? fmtUptime(w.uptime_s) : "–";
  $("w-restarts").textContent = w.restarts || 0;
  $("w-enable").hidden = !!s.enabled;
  $("w-save").hidden = !s.enabled;
  $("w-disable").hidden = !s.enabled;
  $("w-point").disabled = w.state !== "running" || w.switched;
  $("w-back").disabled = !w.switched;
}

function readWorkForm(enabled) {
  return {
    enabled,
    payout_address: $("w-address").value.trim(),
    mode: $("w-mode").value,
    auto_switch: $("w-autoswitch").checked,
  };
}

async function saveWork(enabled, okMsg) {
  try {
    await api("PUT", "/api/work", readWorkForm(enabled));
    $("w-result").innerHTML = `<span class="ok">${okMsg}</span>`;
  } catch (err) {
    $("w-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
}

$("w-enable").addEventListener("click", () =>
  saveWork(true, "Enabled — the engine starts as soon as the node is synced."));
$("w-save").addEventListener("click", () => saveWork(true, "Saved."));
$("w-disable").addEventListener("click", () => {
  if (!confirm("Disable the work engine? The fleet switches back to the external pool.")) return;
  saveWork(false, "Disabled — fleet switching back to the external pool.");
});

$("w-point").addEventListener("click", async () => {
  try {
    await api("POST", "/api/work/switch", { target: "engine" });
    $("w-result").innerHTML = `<span class="ok">Switching the fleet to your node…</span>`;
  } catch (err) {
    $("w-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});
$("w-back").addEventListener("click", async () => {
  try {
    await api("POST", "/api/work/switch", { target: "external" });
    $("w-result").innerHTML = `<span class="ok">Switching the fleet back to the pool…</span>`;
  } catch (err) {
    $("w-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

// gateway log: refresh while the node tab is visible
setInterval(async () => {
  if (!document.querySelector("#tab-node.active")) return;
  try {
    const res = await api("GET", "/api/work");
    $("w-log").textContent = (res.log || []).join("\n") || "– no output yet –";
  } catch {}
}, 5000);

// ---------- node software (Core/Knots, pruning) ----------

let nodeSetupLoaded = false;
let nodeVersions = { core: "", knots: "" };

async function refreshNodeSetup() {
  try {
    const d = await api("GET", "/api/node/setup");
    nodeVersions = { core: d.core_version, knots: d.knots_version };
    $("n-current").textContent = d.subversion || "not running";
    $("n-pruned").textContent = d.subversion ? (d.pruned ? "pruned" : "full node") : "–";
    $("n-helper").textContent = d.helper_available
      ? "available" : "not installed (use deploy/install.sh)";
    if (!nodeSetupLoaded) {
      $("n-impl").value = d.impl === "knots" ? "knots" : "core";
      $("n-version").placeholder = nodeVersions[$("n-impl").value] || "";
      nodeSetupLoaded = true;
    }
    const job = d.job;
    const details = $("n-job-details");
    if (job) {
      details.hidden = false;
      $("n-job-log").textContent = (job.log || []).join("\n") || "…";
      $("n-apply").disabled = job.running;
      if (job.running) {
        $("n-result").innerHTML = `Installing (${esc(job.name)})…`;
      } else if (job.error) {
        $("n-result").innerHTML = `<span class="fail">${esc(job.error)}</span>`;
      } else if (job.ok) {
        $("n-result").innerHTML = `<span class="ok">Done.</span>`;
      }
    }
  } catch {}
}

$("n-impl").addEventListener("change", () => {
  $("n-version").placeholder = nodeVersions[$("n-impl").value] || "";
});

$("n-apply").addEventListener("click", async () => {
  const impl = $("n-impl").value;
  const version = $("n-version").value.trim();
  const prune = parseInt($("n-prune").value, 10) || 0;
  const name = impl === "knots" ? "Bitcoin Knots" : "Bitcoin Core";
  if (!confirm(`Install ${name} ${version || nodeVersions[impl]} (prune: ${prune || "full node"})?\n` +
      `bitcoind restarts; chain data is kept.`)) return;
  try {
    await api("POST", "/api/node/setup", { impl, version, prune });
    $("n-result").innerHTML = "Started — see install log below.";
    setTimeout(refreshNodeSetup, 1500);
  } catch (err) {
    $("n-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

setInterval(() => {
  if (document.querySelector("#tab-node.active") && $("auth-overlay").hidden) refreshNodeSetup();
}, 5000);

// ---------- self-update ----------

$("u-check").addEventListener("click", async () => {
  $("u-result").innerHTML = "Checking…";
  try {
    const d = await api("GET", "/api/update");
    $("u-latest").textContent = d.latest ? "v" + d.latest : "–";
    if (d.newer) {
      $("u-apply").hidden = false;
      $("u-result").innerHTML = `<span class="ok">Update available: v${esc(d.latest)}</span>`;
    } else {
      $("u-apply").hidden = true;
      $("u-result").innerHTML = `<span class="ok">You are up to date.</span>`;
    }
  } catch (err) {
    $("u-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

$("u-apply").addEventListener("click", async () => {
  if (!confirm("Download, verify and install the update? nodeosd restarts briefly.")) return;
  $("u-apply").disabled = true;
  $("u-result").innerHTML = "Downloading and verifying…";
  try {
    await api("POST", "/api/update/apply");
    $("u-result").innerHTML = "Installing — the service restarts, this page reloads automatically…";
    const current = snapshot ? snapshot.version : "";
    const poll = setInterval(async () => {
      try {
        const st = await api("GET", "/api/status");
        if (st.version && st.version !== current) { clearInterval(poll); location.reload(); }
      } catch {}
    }, 3000);
  } catch (err) {
    $("u-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
    $("u-apply").disabled = false;
  }
});

// ---------- security ----------

$("s-pw-btn").addEventListener("click", async () => {
  try {
    await api("POST", "/api/auth/password", {
      current: $("s-pw-cur").value, new: $("s-pw-new").value,
    });
    $("s-pw-cur").value = $("s-pw-new").value = "";
    $("s-pw-result").innerHTML = `<span class="ok">Password changed.</span>`;
  } catch (err) {
    $("s-pw-result").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

$("s-logout").addEventListener("click", async () => {
  try { await api("POST", "/api/auth/logout"); } catch {}
  location.reload();
});

// ---------- alerts ----------

const LEVEL_ICON = { info: "ℹ", warning: "⚠", critical: "✖", party: "🎉" };

function alertLi(a) {
  return `<li class="lvl-${esc(a.level)}">
    <span class="alert-ico">${LEVEL_ICON[a.level] || "•"}</span>
    <span class="msg">${esc(a.msg)}</span>
    <span class="when" title="${esc(new Date(a.time).toLocaleString())}">${timeAgo(a.time)}</span>
  </li>`;
}

function renderAlerts() {
  const list = $("alert-list"), dash = $("dash-alerts");
  if (!alertsLog.length) {
    list.innerHTML = dash.innerHTML = `<li class="empty">No events yet</li>`;
    $("nav-alert-count").textContent = "";
    return;
  }
  list.innerHTML = alertsLog.map(alertLi).join("");
  dash.innerHTML = alertsLog.slice(0, 6).map(alertLi).join("");
  $("nav-alert-count").textContent = `(${alertsLog.length})`;
}

// ---------- settings ----------

function fillPoolForm(p) {
  $("p-url").value = p.stratum_url || "";
  $("p-port").value = p.stratum_port || "";
  $("p-user").value = p.stratum_user || "";
  $("p-furl").value = p.fallback_url || "";
  $("p-fport").value = p.fallback_port || "";
  $("p-fuser").value = p.fallback_user || "";
}

function readPoolForm() {
  return {
    stratum_url: $("p-url").value.trim(),
    stratum_port: parseInt($("p-port").value, 10) || 0,
    stratum_user: $("p-user").value.trim(),
    fallback_url: $("p-furl").value.trim(),
    fallback_port: parseInt($("p-fport").value, 10) || 0,
    fallback_user: $("p-fuser").value.trim(),
  };
}

$("btn-pool-save").addEventListener("click", async () => {
  try {
    await api("PUT", "/api/pool", readPoolForm());
    $("apply-results").innerHTML = `<span class="ok">Saved.</span>`;
  } catch (err) {
    $("apply-results").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  }
});

$("btn-pool-apply").addEventListener("click", async () => {
  const btn = $("btn-pool-apply");
  try {
    await api("PUT", "/api/pool", readPoolForm());
  } catch (err) {
    $("apply-results").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
    return;
  }
  btn.disabled = true;
  btn.textContent = "Applying…";
  $("apply-results").innerHTML = "Pushing settings and restarting miners one by one…";
  try {
    const results = await api("POST", "/api/pool/apply", {});
    $("apply-results").innerHTML = results.map((r) =>
      `<div>${esc(r.host)} — ${r.ok ? `<span class="ok">OK</span>` : `<span class="fail">${esc(r.error)}</span>`}</div>`
    ).join("") || "No online miners.";
  } catch (err) {
    $("apply-results").innerHTML = `<span class="fail">${esc(err.message)}</span>`;
  } finally {
    btn.disabled = false;
    btn.textContent = "Save & apply to fleet";
  }
});

function renderAbout() {
  $("about-version").textContent = "v" + snapshot.version;
  $("about-uptime").textContent = fmtUptime(snapshot.uptime_s);
  $("about-mode").textContent = snapshot.demo ? "demo (simulated fleet)" : "production";
}

// ---------- miners tab actions ----------

$("btn-add").addEventListener("click", async () => {
  const host = $("add-host").value.trim();
  if (!host) return;
  try {
    await api("POST", "/api/miners", { host });
    $("add-host").value = "";
    $("scan-status").textContent = `Added ${host} — polling…`;
  } catch (err) {
    $("scan-status").textContent = "Error: " + err.message;
  }
});

$("btn-scan").addEventListener("click", async () => {
  try {
    const res = await api("POST", "/api/scan", { cidr: $("scan-cidr").value.trim() });
    $("scan-status").textContent = `Scanning ${res.scanning}…`;
    if (scanPoller) clearInterval(scanPoller);
    scanPoller = setInterval(async () => {
      try {
        const st = await api("GET", "/api/scan");
        if (st.running) {
          $("scan-status").textContent =
            `Scanning ${st.cidr}: ${st.scanned}/${st.total} hosts, ${st.found.length} miner(s) found`;
        } else {
          clearInterval(scanPoller);
          scanPoller = null;
          $("scan-status").textContent =
            `Scan finished: ${st.found.length} miner(s) found${st.found.length ? " — " + st.found.join(", ") : ""}`;
        }
      } catch {}
    }, 1000);
  } catch (err) {
    $("scan-status").textContent = "Error: " + err.message;
  }
});

// ---------- go ----------

bootstrap();
