// Mock API for frontend development: serves web/dist and synthetic data that
// follows docs/api.md. Not shipped. Usage: npm run dev  (PORT=5173 by default)
//
//   GET /__drop?sec=10   close all Live streams and refuse new ones for N s
//                        (to see the stale indicator and the reconnect)
import http from "node:http";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const PORT = Number(process.env.PORT || 5173);
const LINE = 1e9;
const MB = 1e6; // Mb/s in bits/s

let seed = 42;
const rnd = () => ((seed = (seed * 16807) % 2147483647) - 1) / 2147483646;
const jitter = (v, f = 0.25) => v * (1 - f + rnd() * 2 * f);

// [id, name, kind, mac, vendor, addresses, liveMbps[rx,tx], avgMbps[rx,tx], endpoint profile]
const DEVICES = [
  ["mac:00:d9:d1:95:b4:3f", "ps5", "lan", "00:d9:d1:95:b4:3f", "Sony", ["192.168.2.55", "2001:db8:1:1::55", "2001:db8:1:1:a1::7"], [310, 3.2], [1.2, 0.05], "game"],
  ["mac:a8:23:fe:2c:53:95", "living-room-tv", "lan", "a8:23:fe:2c:53:95", "LG Electronics", ["192.168.2.41", "2001:db8:1:1::41"], [24, 0.4], [1.9, 0.05], "stream"],
  ["mac:3c:22:fb:1a:0d:a3", "work-laptop", "lan", "3c:22:fb:1a:0d:a3", "Apple", ["192.168.2.20", "2001:db8:1:1::1a", "2001:db8:1:1:9c1::2", "2001:db8:1:1:77::3", "2001:db8:1:1:1f::9"], [18, 4.1], [1.1, 0.25], "work"],
  ["mac:8c:16:45:4c:95:b1", "work-laptop", "lan", "8c:16:45:4c:95:b1", "Lenovo", ["192.168.2.24", "2001:db8:1:1::24"], [6.2, 2.1], [0.7, 0.2], "work"],
  ["mac:5e:a1:07:2d:70:f1", "iphone", "lan", "5e:a1:07:2d:70:f1", null, ["192.168.2.31", "2001:db8:1:1::31"], [2.3, 0.5], [0.3, 0.06], "mobile"],
  ["wg-tunnels", "WireGuard tunnels", "wg-tunnels", null, null, [], [1.8, 0.6], [0.1, 0.03], "tunnel"],
  ["wg:Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=", "Laptop", "wg", null, null, ["10.0.0.21"], [1.4, 0.3], [0.08, 0.02], "work"],
  ["mac:48:a6:b8:b0:61:64", "kitchen-speaker", "lan", "48:a6:b8:b0:61:64", "Sonos", ["192.168.2.62"], [1.1, 0.05], [0.12, 0.01], "audio"],
  ["mac:f0:18:98:3c:86:64", "Apple 7d:2f", "lan", "f0:18:98:3c:86:64", "Apple", ["192.168.2.77", "2001:db8:1:1::77"], [0.6, 0.1], [0.15, 0.02], "mobile"],
  ["ctr:unifi-network-application", "unifi-network-application", "container", null, null, ["172.31.254.3"], [0.2, 0.1], [0.03, 0.02], "infra"],
  ["router", "vyos", "router", null, null, [], [0.1, 0.05], [0.03, 0.015], "router"],
  ["ctr:airprint", "airprint", "container", "02:37:fe:15:63:d1", null, ["192.168.2.2"], [0.004, 0.002], [0.0003, 0.0001], "infra"],
].map(([id, name, kind, mac, vendor, addresses, live, avg, ep]) => ({ id, name, kind, mac, vendor, addresses, live, avg, ep }));

const EPS = {
  netflix: ["192.0.2.140", "ipv4-c071-fra002-dtag-isp.1.oca.nflxvideo.net", "Netflix", 2906],
  youtube: ["2001:db8:eeee::2", "fra24s12-in-x0e.1e100.net", "Google", 15169],
  akamai: ["192.0.2.1", "a23-55-208-74.deploy.static.akamaitechnologies.com", "Akamai", 20940],
  akamai2: ["192.0.2.2", "a2-16-186-11.deploy.static.akamaitechnologies.com", "Akamai", 20940],
  sony: ["192.0.2.3", "ec2-54-93-122-18.eu-central-1.compute.amazonaws.com", "Amazon", 16509],
  cloudfront: ["192.0.2.4", "server-18-66-147-12.fra56.r.cloudfront.net", "Amazon", 16509],
  cloudflare: ["192.0.2.5", null, "Cloudflare", 13335],
  apple: ["192.0.2.6", null, "Apple", 714],
  apple6: ["2001:db8:eeee::3", null, "Apple", 714],
  github: ["192.0.2.7", "lb-140-82-121-4-fra.github.com", "GitHub", 36459],
  ms: ["2001:db8:eeee::1", null, "Microsoft", 8075],
  hetzner: ["192.0.2.8", "static.34.12.198.88.clients.your-server.de", "Hetzner Online", 24940],
  spotify: ["192.0.2.9", "25.224.186.35.bc.googleusercontent.com", "Google", 15169],
  sonos: ["192.0.2.10", "ec2-3-120-8-45.eu-central-1.compute.amazonaws.com", "Amazon", 16509],
  ubnt: ["192.0.2.11", "ec2-52-29-73-110.eu-central-1.compute.amazonaws.com", "Amazon", 16509],
  gtld: ["192.0.2.12", "a.gtld-servers.net", "VeriSign", 26415],
  vyosrepo: ["192.0.2.13", null, "Cloudflare", 13335],
  ntp: ["192.0.2.14", "static.230.24.216.95.clients.your-server.de", "Hetzner Online", 24940],
  mob1: ["192.0.2.15", null, "Telefónica Germany", 6805],
  mob2: ["192.0.2.16", null, null, null],
};
const PROFILES = {
  game: [["akamai", 0.48], ["cloudfront", 0.22], ["akamai2", 0.12], ["sony", 0.08], ["cloudflare", 0.06], ["youtube", 0.04]],
  stream: [["netflix", 0.62], ["youtube", 0.24], ["cloudfront", 0.08], ["akamai", 0.04], ["cloudflare", 0.02]],
  work: [["github", 0.22], ["youtube", 0.19], ["ms", 0.16], ["apple", 0.13], ["cloudflare", 0.11], ["hetzner", 0.09], ["cloudfront", 0.06], ["akamai", 0.04]],
  mobile: [["apple", 0.31], ["apple6", 0.22], ["youtube", 0.18], ["cloudfront", 0.13], ["akamai2", 0.1], ["cloudflare", 0.06]],
  audio: [["spotify", 0.71], ["sonos", 0.21], ["cloudfront", 0.08]],
  infra: [["ubnt", 0.64], ["cloudfront", 0.26], ["cloudflare", 0.1]],
  router: [["gtld", 0.38], ["vyosrepo", 0.34], ["ntp", 0.28]],
  tunnel: [["mob1", 0.6], ["mob2", 0.4]],
};
const WIN_SEC = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };

function diurnal(ms) {
  const d = new Date(ms);
  const h = d.getHours() + d.getMinutes() / 60;
  if (h < 7) return 0.12 + 0.04 * Math.cos(h);
  return 0.2 + 0.8 * Math.pow(Math.max(0, Math.sin((Math.PI * (h - 7)) / 17.5)), 1.6);
}

// ---------- Live state, advanced once per second ----------

const N = 60;
const live = { rx: [], tx: [], cpu: [], mem: [], temp: [], ct: [], load: [], devs: new Map(), unat: [0, 0] };
let nowSec = Math.floor(Date.now() / 1000);
let today = { rx: 58.4e9, tx: 4.1e9 };

function step() {
  let srx = 0, stx = 0;
  for (const d of DEVICES) {
    const rx = jitter(d.live[0] * MB, d.id.includes("ps5") ? 0.12 : 0.25);
    const tx = jitter(d.live[1] * MB, 0.2);
    live.devs.set(d.id, [rx, tx]);
    srx += rx; stx += tx;
  }
  live.unat = [jitter(3.2 * MB, 0.3), jitter(0.6 * MB, 0.3)];
  srx += live.unat[0]; stx += live.unat[1];
  const push = (a, v) => { a.push(v); if (a.length > N) a.shift(); };
  push(live.rx, srx); push(live.tx, stx);
  const cpu = 3 + (srx / MB) * 0.018 + rnd() * 2.5;
  push(live.cpu, cpu);
  push(live.mem, 2.64e9 + rnd() * 3e7);
  push(live.temp, 49 + rnd() * 2 + cpu * 0.1);
  push(live.ct, 790 + Math.round(rnd() * 45));
  push(live.load, 0.12 + rnd() * 0.12 + cpu * 0.01);
  today.rx += srx / 8; today.tx += stx / 8;
}
for (let i = 0; i < N; i++) step();
setInterval(() => { nowSec = Math.floor(Date.now() / 1000); step(); }, 1000);

const series = (a) => a.map((v) => v);
function device(d, rx, tx) {
  return { id: d.id, name: d.name, kind: d.kind, mac: d.mac, vendor: d.vendor, addresses: d.addresses, rx, tx };
}
function liveTalkers() {
  const devices = DEVICES.map((d) => device(d, ...live.devs.get(d.id))).sort((a, b) => b.rx + b.tx - (a.rx + a.tx));
  return { window: "live", unit: "bps", wanRx: live.rx.at(-1), wanTx: live.tx.at(-1), devices, unattributed: { rx: live.unat[0], tx: live.unat[1] } };
}

function liveEvent() {
  const start = (nowSec - N + 1) * 1000;
  const rx = live.rx.at(-1), tx = live.tx.at(-1);
  const wgDev = live.devs.get(DEVICES[6].id);
  return {
    time: nowSec * 1000,
    lineRateBps: LINE,
    interfaces: [
      { name: "pppoe0", role: "wan", rxBps: rx, txBps: tx },
      { name: "br0", role: "lan", rxBps: tx * 0.93, txBps: rx * 0.97 },
      { name: "wg0", role: "wireguard", rxBps: wgDev[1] + 2e5, txBps: wgDev[0] },
    ],
    wan: { ipv4: "203.0.113.176", ipv6: "2001:db8:1:0::1", uptimeSec: 360000 + (nowSec % 100000), todayRxBytes: Math.round(today.rx), todayTxBytes: Math.round(today.tx) },
    wanSeries: { start, stepMs: 1000, rxBps: series(live.rx), txBps: series(live.tx) },
    resources: {
      cpuPct: live.cpu.at(-1), memUsedBytes: Math.round(live.mem.at(-1)), memTotalBytes: 7.9e9, memAvailBytes: 5.2e9,
      tempC: live.temp.at(-1), conntrackCount: live.ct.at(-1), conntrackMax: 262144,
      load: [live.load.at(-1), 0.11, 0.09],
      disks: [
        { name: "system", path: "/", usedBytes: 2.1e9, totalBytes: 4.9e9 },
        { name: "data", path: "/mnt/dash-data", usedBytes: 0.31e9, totalBytes: 10e9 },
      ],
      series: { start, stepMs: 1000, cpuPct: series(live.cpu), memUsedBytes: live.mem.map(Math.round), tempC: series(live.temp), conntrack: series(live.ct), load1: series(live.load) },
    },
    containers: [
      { name: "unifi-network-application", cpuPct: 0.3, memBytes: 1.44e9, memLimitBytes: 2e9 },
      { name: "unifi-db", cpuPct: 3.5, memBytes: 668e6, memLimitBytes: 2e9 },
      { name: "vymanager-frontend", cpuPct: 0.0, memBytes: 168e6, memLimitBytes: 512e6 },
      { name: "vymanager-backend", cpuPct: 0.1, memBytes: 130e6, memLimitBytes: 512e6 },
      { name: "vymanager-postgres", cpuPct: 0.0, memBytes: 43e6, memLimitBytes: 512e6 },
      { name: "dash", cpuPct: 0.8, memBytes: 38e6, memLimitBytes: 512e6 },
      { name: "airprint", cpuPct: 0.0, memBytes: 15e6, memLimitBytes: 0 },
    ],
    self: { cpuPct: 0.8, memBytes: 38e6, dataBytes: 312e6 },
    topTalkers: liveTalkers(),
  };
}

// ---------- Window endpoints ----------

function windowTalkers(win) {
  const sec = WIN_SEC[win];
  const f = (i) => 0.65 + ((i * 97 + sec) % 70) / 100;
  const devices = DEVICES.map((d, i) => device(d, Math.round(((d.avg[0] * MB) / 8) * sec * f(i)), Math.round(((d.avg[1] * MB) / 8) * sec * f(i + 3))));
  devices.sort((a, b) => b.rx + b.tx - (a.rx + a.tx));
  const unattributed = { rx: Math.round((0.08 * MB / 8) * sec), tx: Math.round((0.02 * MB / 8) * sec) };
  const wanRx = devices.reduce((s, d) => s + d.rx, unattributed.rx);
  const wanTx = devices.reduce((s, d) => s + d.tx, unattributed.tx);
  const to = Date.now();
  return { window: win, unit: "bytes", from: to - sec * 1000, to, wanRx, wanTx, devices, unattributed };
}

function deviceDetail(id, win) {
  const d = DEVICES.find((x) => x.id === id);
  if (!d) return null;
  const isLive = win === "live";
  const t = isLive ? liveTalkers() : windowTalkers(win);
  const dv = t.devices.find((x) => x.id === id);
  const sec = isLive ? 60 : WIN_SEC[win];
  const stepMs = isLive ? 1000 : sec <= 86400 ? 60000 : 3600000;
  const n = Math.round((sec * 1000) / stepMs);
  const start = Math.floor(Date.now() / stepMs) * stepMs - (n - 1) * stepMs;
  const rxBps = [], txBps = [];
  const meanRx = isLive ? dv.rx : (dv.rx * 8) / sec, meanTx = isLive ? dv.tx : (dv.tx * 8) / sec;
  for (let i = 0; i < n; i++) {
    const tt = start + i * stepMs;
    const dz = isLive || sec <= 3600 ? 1 : diurnal(tt) * 1.9;
    const burst = rnd() < 0.12 ? 2.4 : 1;
    rxBps.push(meanRx * dz * (0.5 + rnd()) * burst);
    txBps.push(meanTx * dz * (0.5 + rnd()));
  }
  const endpoints = PROFILES[d.ep].map(([k, share]) => {
    const [ip, host, org, asn] = EPS[k];
    return { ip, host, org, asn, rx: isLive ? dv.rx * share : Math.round(dv.rx * share), tx: isLive ? dv.tx * share : Math.round(dv.tx * share * (0.6 + rnd() * 0.8)) };
  });
  endpoints.sort((a, b) => b.rx + b.tx - (a.rx + a.tx));
  return {
    device: dv, window: win, unit: isLive ? "bps" : "bytes", resolution: isLive ? "1s" : stepMs === 60000 ? "1m" : "1h",
    series: { start, stepMs, rxBps, txBps }, endpoints, endpointsNote: isLive ? "current connections" : "hourly, kept 30 days",
  };
}

const RANGE = { "1h": [3600, 10000, "10s"], "24h": [86400, 60000, "10s"], "7d": [604800, 420000, "1m"], "30d": [2592000, 1800000, "1m"], "1y": [31536000, 21600000, "1h"] };
function history(range) {
  const [sec, stepMs, resolution] = RANGE[range];
  const n = Math.round((sec * 1000) / stepMs);
  const start = Math.floor(Date.now() / stepMs) * stepMs - (n - 1) * stepMs;
  const out = { range, resolution, start, stepMs, wanRxBps: [], wanTxBps: [], cpuPct: [], memUsedPct: [], conntrack: [] };
  const amp = sec >= 2592000 ? 0.25 : 1;
  for (let i = 0; i < n; i++) {
    const t = start + i * stepMs;
    const gap = range === "24h" && i > n * 0.3 && i < n * 0.3 + 12; // a dashboard restart: missing samples
    const dz = 1 - amp + amp * diurnal(t) * 1.9;
    let base = 5.8 * MB * dz * (0.7 + rnd() * 0.6);
    if (sec <= 3600 && i > n * 0.55) base = (330 + rnd() * 70) * MB;
    if (range === "1y") base = 5.8 * MB * (0.8 + 0.25 * Math.sin(i / 9) + rnd() * 0.2);
    out.wanRxBps.push(gap ? null : base);
    out.wanTxBps.push(gap ? null : base * 0.07 + 0.4 * MB + rnd() * 0.3 * MB);
    out.cpuPct.push(gap ? null : 2.5 + (base / MB) * 0.019 + rnd() * 1.8);
    out.memUsedPct.push(gap ? null : 32.5 + (i / n) * 1.2 + rnd() * 0.6);
    out.conntrack.push(gap ? null : Math.round(380 + dz * 520 + rnd() * 80 + (base > 100 * MB ? 120 : 0)));
  }
  return out;
}

// ---------- HTTP ----------

const clients = new Set();
let refuseUntil = 0;
setInterval(() => {
  const msg = `event: live\ndata: ${JSON.stringify(liveEvent())}\n\n`;
  for (const res of clients) res.write(msg);
}, 1000);

const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css" };

function json(res, code, body) {
  res.writeHead(code, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

http
  .createServer(async (req, res) => {
    const url = new URL(req.url, "http://x");
    const p = url.pathname;
    if (p === "/__drop") {
      refuseUntil = Date.now() + Number(url.searchParams.get("sec") || 10) * 1000;
      for (const c of clients) c.destroy();
      clients.clear();
      return json(res, 200, { dropped: true });
    }
    if (p === "/api/live") {
      if (Date.now() < refuseUntil) return json(res, 503, { error: "down" });
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" });
      res.write(`retry: 1000\nevent: live\ndata: ${JSON.stringify(liveEvent())}\n\n`);
      clients.add(res);
      req.on("close", () => clients.delete(res));
      return;
    }
    if (p === "/api/top-talkers") {
      const w = url.searchParams.get("window");
      return WIN_SEC[w] ? json(res, 200, windowTalkers(w)) : json(res, 400, { error: "unknown window" });
    }
    if (p.startsWith("/api/devices/")) {
      const id = decodeURIComponent(p.slice("/api/devices/".length));
      const w = url.searchParams.get("window") || "live";
      if (w !== "live" && !WIN_SEC[w]) return json(res, 400, { error: "unknown window" });
      const d = deviceDetail(id, w);
      return d ? json(res, 200, d) : json(res, 404, { error: "unknown device" });
    }
    if (p === "/api/history") {
      const r = url.searchParams.get("range");
      return RANGE[r] ? json(res, 200, history(r)) : json(res, 400, { error: "unknown range" });
    }
    const file = path.join(dist, p === "/" ? "index.html" : path.normalize(p).replace(/^(\.\.[/\\])+/, ""));
    try {
      const body = await readFile(file);
      res.writeHead(200, { "Content-Type": TYPES[path.extname(file)] || "application/octet-stream" });
      res.end(body);
    } catch {
      res.writeHead(404).end("not found");
    }
  })
  .listen(PORT, () => console.log(`mock dashboard on http://localhost:${PORT}`));
