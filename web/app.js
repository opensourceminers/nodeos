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

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

// ---------- tabs ----------

$("nav").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  document.querySelectorAll("nav button").forEach((b) => b.classList.toggle("active", b === btn));
  document.querySelectorAll(".tab").forEach((t) =>
    t.classList.toggle("active", t.id === "tab-" + btn.dataset.tab));
  if (snapshot) render();
});

// ---------- live updates ----------

function connectSSE() {
  const es = new EventSource("/api/events");
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
    setTimeout(connectSSE, 3000);
  };
}

async function bootstrap() {
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
  connectSSE();
}

// ---------- render ----------

function render() {
  if (!snapshot) return;
  renderHeader();
  renderTiles();
  renderFleetChart();
  renderSolo();
  renderMiners();
  renderNode();
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

function renderTiles() {
  const { fleet, solo } = snapshot;
  const tiles = [
    { v: fmtHashParts(fleet.total_hash_gh), l: "Fleet hashrate" },
    { v: `${fleet.online} <small>/ ${fleet.count}</small>`, l: "Miners online" },
    { v: `${fleet.total_power_w.toFixed(0)} <small>W</small>`, l: "Power draw" },
    { v: fleet.efficiency_j_th ? `${fleet.efficiency_j_th.toFixed(1)} <small>J/TH</small>` : "–",
      l: "Fleet efficiency" },
    { v: fleet.best_diff ? esc(fleet.best_diff_str) : "–", l: "Best difficulty (all time)" },
    { v: solo.expected_seconds ? fmtDur(solo.expected_seconds) : "–",
      l: "Expected time to block", s: solo.expected_seconds ? "statistical average" : "needs node + hashrate" },
    workTile(snapshot.work),
  ];
  $("tiles").innerHTML = tiles.map((t) => `
    <div class="tile">
      <div class="value">${t.v}</div>
      <div class="label">${t.l}</div>
      ${t.s ? `<div class="sub">${t.s}</div>` : ""}
    </div>`).join("");
}

function workTile(w) {
  if (!w || w.state === "disabled" || !w.state) {
    return { v: "off", l: "Work engine", s: "solo mining via your own node" };
  }
  if (w.state === "running" && w.switched) {
    return { v: "SOLO", l: "Work engine", s: "fleet mines on YOUR node" };
  }
  if (w.state === "running") return { v: "ready", l: "Work engine", s: w.endpoint || "" };
  return { v: esc(w.state.replace("_", " ")), l: "Work engine", s: "" };
}

// ---------- fleet chart ----------

let chartPoints = []; // [{t, gh, x, y}] in pixel space, for the tooltip

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
      stroke="#2c2c2a" stroke-width="1"/>
      <text x="${padL - 8}" y="${yy + 4}" text-anchor="end" fill="#898781"
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
      fill="#898781" font-size="11">${hh}:${mm}</text>`;
  }

  const line = chartPoints.map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join("");
  const area = line + `L${chartPoints[chartPoints.length - 1].x.toFixed(1)},${y(0)}L${chartPoints[0].x.toFixed(1)},${y(0)}Z`;

  svg.innerHTML = `
    ${grid}
    <line x1="${padL}" y1="${y(0)}" x2="${W - padR}" y2="${y(0)}" stroke="#383835" stroke-width="1"/>
    ${xlab}
    <path d="${area}" fill="#3987e5" fill-opacity="0.12"/>
    <path d="${line}" fill="none" stroke="#3987e5" stroke-width="2"
      stroke-linejoin="round" stroke-linecap="round"/>
    <line id="crosshair" x1="0" y1="${padT}" x2="0" y2="${H - padB}"
      stroke="#898781" stroke-width="1" stroke-dasharray="3,3" visibility="hidden"/>
    <circle id="hoverdot" r="4" fill="#3987e5" stroke="#1a1a19" stroke-width="2"
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
  if (!solo.expected_seconds) {
    el.innerHTML = `<div class="empty">Waiting for hashrate and difficulty…</div>`;
    return;
  }
  const oddsDay = solo.odds_per_day;
  const oneIn = oddsDay > 0 ? Math.round(1 / oddsDay) : 0;
  el.innerHTML = `
    <dl class="kv">
      <dt>Network difficulty</dt><dd>${fmtDiff(solo.network_difficulty)}</dd>
      <dt>Your fleet</dt><dd>${fmtHash(fleet.total_hash_gh)}</dd>
      <dt>Expected time to block</dt><dd><b>${fmtDur(solo.expected_seconds)}</b></dd>
      <dt>Chance in next 24 h</dt><dd>${(oddsDay * 100).toPrecision(2)} % — about 1 in ${oneIn.toLocaleString()}</dd>
      <dt>Block reward today</dt><dd>3.125 BTC + fees</dd>
    </dl>`;
}

// ---------- miners ----------

function sparkline(history) {
  const hs = (history || []).slice(-60);
  if (hs.length < 2) return `<div class="spark" style="height:28px"></div>`;
  const vals = hs.map((s) => s.h);
  const max = Math.max(...vals) * 1.1 || 1;
  const min = Math.min(...vals) * 0.9;
  const W = 120, H = 28;
  const pts = hs.map((s, i) =>
    `${(i / (hs.length - 1)) * W},${H - ((s.h - min) / Math.max(max - min, 0.001)) * (H - 4) - 2}`
  ).join(" ");
  return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none"
    style="width:100%;height:28px" aria-hidden="true">
    <polyline points="${pts}" fill="none" stroke="#3987e5" stroke-width="2"
      stroke-linejoin="round" stroke-linecap="round"/></svg>`;
}

function renderMiners() {
  const miners = snapshot.miners || [];
  $("miners-empty").hidden = miners.length > 0;
  const grid = $("miner-grid");
  grid.innerHTML = miners.map((m) => {
    const i = m.info || {};
    const status = m.online
      ? `<span class="status-chip online"><span class="dot"></span>online</span>`
      : `<span class="status-chip offline"><span class="dot"></span>offline</span>`;
    const pool = i.stratumURL
      ? `${esc(i.stratumURL)}:${i.stratumPort}${i.isUsingFallbackStratum ? " (FALLBACK!)" : ""}`
      : "–";
    return `
    <div class="miner-card" data-host="${esc(m.host)}">
      <div class="top">
        <span class="name">${esc(m.label)}</span>
        <span class="model">${esc(i.ASICModel || m.source)}</span>
        ${status}
      </div>
      <div class="hash">${m.online ? fmtHashParts(i.hashRate || 0) : "–"}</div>
      ${sparkline(m.history)}
      <div class="meta">
        <span>Temp <b>${i.temp ? i.temp.toFixed(0) + " °C" : "–"}</b></span>
        <span>Power <b>${i.power ? i.power.toFixed(1) + " W" : "–"}</b></span>
        <span>Fan <b>${i.fanrpm ? i.fanrpm.toFixed(0) + " rpm" : "–"}</b></span>
        <span>Freq <b>${i.frequency ? i.frequency + " MHz" : "–"}</b></span>
        <span>Shares <b>${(i.sharesAccepted ?? 0).toLocaleString()}</b>${i.sharesRejected ? ` <span style="color:var(--muted)">(${i.sharesRejected} rej)</span>` : ""}</span>
        <span>Best <b>${esc(i.bestDiff || "–")}</b></span>
        <span>Uptime <b>${fmtUptime(i.uptimeSeconds)}</b></span>
        <span>FW <b>${esc(i.version || "–")}</b></span>
      </div>
      <div class="pool-line" title="${esc(i.stratumUser || "")}">→ ${pool}</div>
      ${m.last_error && !m.online ? `<div class="pool-line" style="color:var(--critical)">${esc(m.last_error)}</div>` : ""}
      <div class="actions">
        <button class="btn small" data-act="restart">Restart</button>
        <button class="btn small danger" data-act="remove">Remove</button>
      </div>
    </div>`;
  }).join("");
}

$("miner-grid").addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-act]");
  if (!btn) return;
  const host = btn.closest(".miner-card").dataset.host;
  try {
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
    alert(err.message);
    btn.disabled = false;
  }
});

// ---------- node ----------

function renderNode() {
  const n = snapshot.node;
  const el = $("node-body");
  if (!n.available) {
    el.innerHTML = `
      <div class="empty">
        Bitcoin Core is not reachable${n.error ? `: <code>${esc(n.error)}</code>` : ""}.<br><br>
        Install it with the NodeOS installer (<code>install.sh --with-bitcoind</code>)
        or point <code>/etc/nodeos/config.json</code> at an existing node's RPC.
      </div>`;
    return;
  }
  const pct = (n.progress * 100);
  el.innerHTML = `
    ${n.ibd || n.progress < 0.9999 ? `
      <div class="progress"><div style="width:${pct.toFixed(2)}%"></div></div>
      <div class="note">Initial sync: ${pct.toFixed(2)} % — miners can already mine
      against a fallback pool; solo templates need a synced node.</div>` : ""}
    <dl class="kv" style="margin-top:10px">
      <dt>Implementation</dt><dd>${esc(n.subversion || "Bitcoin Core")}</dd>
      <dt>Chain</dt><dd>${esc(n.chain)}</dd>
      <dt>Blocks</dt><dd>${n.blocks.toLocaleString()} / ${n.headers.toLocaleString()} headers</dd>
      <dt>Verification</dt><dd>${(n.progress * 100).toFixed(3)} %</dd>
      <dt>Difficulty</dt><dd>${fmtDiff(n.difficulty)}</dd>
      <dt>Network hashrate</dt><dd>${n.network_hashps ? fmtHash(n.network_hashps / 1e9) : "–"}</dd>
      <dt>Peers</dt><dd>${n.connections} (${n.connections_in} inbound)</dd>
      <dt>Mempool</dt><dd>${n.mempool_txs.toLocaleString()} tx · ${fmtBytes(n.mempool_bytes)}</dd>
      <dt>Chain size</dt><dd>${fmtBytes(n.size_on_disk)}${n.pruned ? " (pruned)" : ""}</dd>
    </dl>`;
}

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

// ---------- alerts ----------

const LEVEL_ICON = { info: "ℹ", warning: "⚠", critical: "✖", party: "🎉" };

function alertLi(a) {
  return `<li class="lvl-${esc(a.level)}">
    <span class="when">${new Date(a.time).toLocaleString()}</span>
    <span class="alert-ico">${LEVEL_ICON[a.level] || "•"}</span>
    <span>${esc(a.msg)}</span>
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
