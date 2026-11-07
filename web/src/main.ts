import type { Device, DeviceDetail, History, HistoryRange, LiveEvent, Num, TalkerWindow, TopTalkers } from "./api.ts";
import { getJSON } from "./api.ts";
import { Chart } from "./chart.ts";
import { deviceIds, deviceSubtitle, kindLabel, talkersSummary } from "./device.ts";
import { DASH, bytesStr, intStr, binStr, niceMax, pctStr, rate, rateAxis, rateStr, resolutionNote, timeLabel, uptimeStr } from "./format.ts";
import { openLive } from "./stream.ts";
import { RollingWindow } from "./window.ts";

// ---------- DOM helpers ----------

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  ...children: (Node | string)[]
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else e.setAttribute(k, v);
  }
  e.append(...children);
  return e;
}

const setText = (id: string, s: string) => {
  const e = $(id);
  if (e.textContent !== s) e.textContent = s;
};

const pctOf = (a: Num, b: Num) => (a != null && b != null && b > 0 ? (a / b) * 100 : null);
const lineRateName = (bps: number) => (bps % 1e9 === 0 ? `${bps / 1e9} Gbps` : bps % 1e6 === 0 ? `${bps / 1e6} Mbps` : rateStr(bps));

function segmented<T extends string>(host: HTMLElement, items: { id: T; label: string }[], initial: T, onPick: (id: T) => void) {
  const btns = items.map((it) => {
    const b = h("button", { type: "button", "aria-pressed": String(it.id === initial) }, it.label);
    b.addEventListener("click", () => {
      for (const o of btns) o.setAttribute("aria-pressed", String(o === b));
      onPick(it.id);
    });
    return b;
  });
  host.append(...btns);
}

// ---------- Header ----------

setText("host", location.hostname || "localhost");
const tickClock = () => setText("clock", timeLabel(Date.now(), "s"));
tickClock();
setInterval(tickClock, 1000);

// ---------- Live View ----------

const wanWin = new RollingWindow(60, 1000);
const resWin = new RollingWindow(60, 1000);
let lineRate = 1e9;
const LIVE_Y_FLOOR = 1e6;

const liveChart = new Chart($("chart-live"), {
  label: "WAN throughput, last 60 seconds",
  height: 250,
  series: [
    { label: "Download", slot: "s1", fill: true },
    { label: "Upload", slot: "s2" },
  ],
  fmt: rateStr,
  axisFmt: rateAxis,
  timeFmt: (t) => timeLabel(t, "s"),
  // Autoscale to the traffic (at least 1 Mb/s) so kb/s rates stay visible;
  // the line rate is drawn only when the axis reaches it, else labelled.
  yFloor: () => LIVE_Y_FLOOR,
  refLine: () => lineRate,
  refLabel: () => `${lineRateName(lineRate)} line rate`,
  xAxis: "live",
});

const ROLE: Record<string, string> = { wan: "WAN", lan: "LAN", wireguard: "WireGuard" };

interface Tile {
  v: HTMLElement;
  u: HTMLElement;
  note: HTMLElement;
  chart?: Chart;
  meter?: HTMLElement;
}

function makeTile(label: string, spark?: { label: string; fmt: (v: number) => string; floor?: number; max?: number }): Tile {
  const v = h("span");
  const u = h("small");
  const note = h("span", { class: "sub num" });
  const el = h("div", { class: "tile" }, h("span", { class: "sub" }, label), h("span", { class: "v num" }, v, u), note);
  $("tiles").append(el);
  const t: Tile = { v, u, note };
  if (spark) {
    const c = h("div");
    el.append(c);
    t.chart = new Chart(c, {
      label: `${label}, last 60 seconds`,
      height: 30,
      series: [{ label: spark.label, slot: "k" }],
      fmt: spark.fmt,
      timeFmt: (x) => timeLabel(x, "s"),
      yFloor: spark.floor != null ? () => spark.floor! : undefined,
      yMax: spark.max != null ? () => spark.max! : undefined,
      xAxis: "none",
    });
  } else {
    const i = h("i");
    el.append(h("div", { class: "meter", role: "presentation" }, i));
    t.meter = i;
  }
  return t;
}

let memTotal: Num = null;
const tiles = {
  cpu: makeTile("CPU", { label: "CPU", fmt: (v) => pctStr(v, 1), floor: 10 }),
  mem: makeTile("Memory", { label: "Used", fmt: binStr }),
  temp: makeTile("Temperature", { label: "Temperature", fmt: (v) => `${v.toFixed(0)} °C`, floor: 60 }),
  ct: makeTile("Tracked connections", { label: "Connections", fmt: intStr, floor: 100 }),
  disk: makeTile("Disk · system"),
  load: makeTile("Load average", { label: "1 min", fmt: (v) => v.toFixed(2), floor: 1 }),
};
tiles.mem.chart?.configure({ yMax: () => memTotal });

function setTile(t: Tile, v: string, u: string, note: string) {
  if (t.v.textContent !== v) t.v.textContent = v;
  if (t.u.textContent !== u) t.u.textContent = u;
  if (t.note.textContent !== note) t.note.textContent = note;
}

const fixed = (v: Num, d: number) => (v == null ? DASH : v.toFixed(d));
const gib = (v: Num) => (v == null ? DASH : (v / 2 ** 30).toFixed(2));

function renderLive(e: LiveEvent) {
  lineRate = e.lineRateBps || 1e9;
  wanWin.merge(e.wanSeries);
  resWin.merge(e.resources.series);

  // Header
  setText("wan-up", `WAN up ${uptimeStr(e.wan.uptimeSec)}`);
  setText("wan-ip", e.wan.ipv4 ?? e.wan.ipv6 ?? DASH);
  $("wan-ip").title = [e.wan.ipv4, e.wan.ipv6].filter(Boolean).join("\n");

  // WAN chart
  const wv = wanWin.view(["rxBps", "txBps"]);
  liveChart.setData(wv.times, [wv.cols.rxBps, wv.cols.txBps]);
  const wanIf = e.interfaces.find((i) => i.role === "wan");
  setText("wan-sub", `${wanIf?.name ?? "WAN"} · last 60 seconds, 1-second samples`);
  setText("line-rate-label", `${lineRateName(lineRate)} line rate`);
  // The chart's y maximum is niceMax of the data (see chart.ts); the legend
  // shows the dashed line only when it is on the chart.
  const peak = Math.max(LIVE_Y_FLOOR, ...[...wv.cols.rxBps, ...wv.cols.txBps].map((v) => v ?? 0));
  $("line-rate-key").style.display = lineRate > niceMax(peak) ? "none" : "";

  // Right now
  const down = wanIf?.rxBps ?? wv.cols.rxBps.at(-1) ?? null;
  const up = wanIf?.txBps ?? wv.cols.txBps.at(-1) ?? null;
  const fd = down == null ? null : rate(down);
  const fu = up == null ? null : rate(up);
  setText("now-down", fd?.n ?? DASH);
  setText("now-down-u", fd?.u ?? "");
  setText("now-up", fu?.n ?? DASH);
  setText("now-up-u", fu?.u ?? "");
  const p = pctOf(down, lineRate);
  setText("now-pct", `${pctStr(p)} of the ${lineRateName(lineRate)} download line rate`);
  $("now-meter").style.width = `${Math.min(100, p ?? 0)}%`;

  const kv = $("ifaces");
  while (kv.children.length > 4) kv.lastElementChild!.remove();
  for (const i of e.interfaces) {
    kv.append(
      h("span", { class: "mono" }, i.name),
      h("span", { class: "sub" }, ROLE[i.role] ?? i.role),
      h("span", { class: "r" }, rateStr(i.rxBps)),
      h("span", { class: "r" }, rateStr(i.txBps)),
    );
  }
  setText("today-down", bytesStr(e.wan.todayRxBytes));
  setText("today-up", bytesStr(e.wan.todayTxBytes));

  // Router Resources
  const r = e.resources;
  memTotal = r.memTotalBytes;
  setText("res-sub", r.memTotalBytes ? `${binStr(r.memTotalBytes)} memory` : "");
  const rv = resWin.view(["cpuPct", "memUsedBytes", "tempC", "conntrack", "load1"]);
  setTile(tiles.cpu, fixed(r.cpuPct, 1), "%", r.load ? `load ${fixed(r.load[0], 2)} · all cores` : "all cores");
  tiles.cpu.chart?.setData(rv.times, [rv.cols.cpuPct]);
  setTile(
    tiles.mem,
    gib(r.memUsedBytes),
    `/ ${binStr(r.memTotalBytes)}`,
    `${pctStr(pctOf(r.memUsedBytes, r.memTotalBytes))} · ${binStr(r.memAvailBytes)} available`,
  );
  tiles.mem.chart?.setData(rv.times, [rv.cols.memUsedBytes]);
  const temps = rv.cols.tempC.filter((x): x is number => x != null);
  setTile(tiles.temp, fixed(r.tempC, 0), "°C", temps.length ? `peak ${Math.max(...temps).toFixed(0)} °C in the last minute` : "");
  tiles.temp.chart?.setData(rv.times, [rv.cols.tempC]);
  setTile(tiles.ct, intStr(r.conntrackCount), "", `of ${intStr(r.conntrackMax)} · ${pctStr(pctOf(r.conntrackCount, r.conntrackMax), 1)}`);
  tiles.ct.chart?.setData(rv.times, [rv.cols.conntrack]);
  const sys = r.disks.find((d) => d.path === "/") ?? r.disks[0];
  const data = r.disks.find((d) => d !== sys);
  if (sys) {
    const sp = pctOf(sys.usedBytes, sys.totalBytes);
    setTile(
      tiles.disk,
      gib(sys.usedBytes),
      `/ ${binStr(sys.totalBytes)}`,
      `${pctStr(sp)}${data ? ` · data partition ${pctStr(pctOf(data.usedBytes, data.totalBytes))} of ${binStr(data.totalBytes)}` : ""}`,
    );
    tiles.disk.meter!.style.width = `${Math.min(100, sp ?? 0)}%`;
  }
  setTile(tiles.load, fixed(r.load?.[0] ?? null, 2), "", r.load ? `1 min · 5 min ${fixed(r.load[1], 2)} · 15 min ${fixed(r.load[2], 2)}` : "");
  tiles.load.chart?.setData(rv.times, [rv.cols.load1]);

  // Containers
  const tb = $("containers");
  tb.replaceChildren(
    ...e.containers.map((c) => {
      const lim = c.memLimitBytes ? binStr(c.memLimitBytes) : "no limit";
      const pct = c.memLimitBytes ? pctOf(c.memBytes, c.memLimitBytes) ?? 0 : 0;
      const meter = h("i");
      meter.style.width = `${Math.min(100, pct)}%`;
      return h(
        "tr",
        {},
        h("td", {}, h("span", { class: "mono" }, c.name)),
        h("td", { class: "r" }, pctStr(c.cpuPct, 1)),
        h("td", { class: "r nowrap" }, `${binStr(c.memBytes)} `, h("span", { class: "sub" }, `/ ${lim}`)),
        h("td", { class: "hide-sm" }, h("div", { class: "meter", role: "presentation" }, meter)),
      );
    }),
  );

  // Footer
  setText("foot-self", `This dashboard: ${pctStr(e.self.cpuPct, 1)} CPU · ${binStr(e.self.memBytes)} RAM`);
  setText("foot-data", `History: ${binStr(e.self.dataBytes)}${data ? ` on ${data.path}` : ""}`);

  // Live Top Talkers
  if (ttWindow === "live") {
    renderTalkers(e.topTalkers);
    if (selected) loadDetail();
  }
}

openLive("/api/live", {
  onEvent: renderLive,
  onState: (s, lastAt) => {
    const pill = $("live-pill");
    pill.className = `pill ${s}`;
    document.body.classList.toggle("stale", s === "stale");
    setText(
      "live-text",
      s === "live" ? "Live · 1 s" : s === "stale" ? `Disconnected · reconnecting${lastAt ? ` · last update ${timeLabel(lastAt, "s")}` : ""}` : "Connecting…",
    );
  },
});

// ---------- Top Talkers ----------

const WINDOWS: { id: TalkerWindow; label: string; span: string }[] = [
  { id: "live", label: "Live", span: "last 60 seconds" },
  { id: "1h", label: "1 h", span: "last hour" },
  { id: "24h", label: "24 h", span: "last 24 hours" },
  { id: "7d", label: "7 d", span: "last 7 days" },
  { id: "30d", label: "30 d", span: "last 30 days" },
];
let ttWindow: TalkerWindow = "24h";
let ttAbort: AbortController | null = null;
let selected: Device | null = null;

segmented($("tt-windows"), WINDOWS, ttWindow, (w) => {
  ttWindow = w;
  setText("tt-summary", "Loading…");
  loadTalkers();
  if (selected) {
    detailSeq++;
    ddChart.setData([], [[], []]);
    loadDetail();
  }
});

function colHeads(unit: "bps" | "bytes", ids: [string, string, string]) {
  const live = unit === "bps";
  setText(ids[0], live ? "↓ Rate" : "↓ Received");
  setText(ids[1], live ? "↑ Rate" : "↑ Sent");
  setText(ids[2], live ? "Total rate" : "Total");
}

function renderTalkers(t: TopTalkers) {
  const fmt = t.unit === "bps" ? rateStr : bytesStr;
  colHeads(t.unit, ["h-down", "h-up", "h-total"]);
  setText("tt-summary", talkersSummary(t));
  const focusedId = (document.activeElement as HTMLElement | null)?.dataset?.dev;
  const max = Math.max(1, ...t.devices.map((d) => d.rx + d.tx));
  const rows = t.devices.map((d, i) => {
    const sel = selected?.id === d.id;
    const btn = h(
      "button",
      { type: "button", class: "devbtn", "aria-expanded": String(sel), "aria-controls": "dd", "data-dev": d.id },
      h("span", { class: "n" }, d.name),
      h("span", { class: "sub" }, deviceSubtitle(d)),
    );
    btn.addEventListener("click", () => select(sel ? null : d));
    const di = h("i", { class: "d" });
    const ui = h("i", { class: "u" });
    di.style.width = `${(d.rx / max) * 100}%`;
    ui.style.width = `${(d.tx / max) * 100}%`;
    return h(
      "tr",
      sel ? { class: "sel" } : {},
      h("td", { class: "rank" }, String(i + 1)),
      h("td", {}, btn),
      h("td", { class: "hide-sm" }, h("span", { class: "kind" }, kindLabel(d.kind))),
      h("td", { class: "r hide-sm" }, fmt(d.rx)),
      h("td", { class: "r hide-sm" }, fmt(d.tx)),
      h("td", { class: "r strong600" }, fmt(d.rx + d.tx)),
      h("td", {}, h("div", { class: "bar", "aria-hidden": "true" }, di, ui)),
    );
  });
  const u = t.unattributed;
  rows.push(
    h(
      "tr",
      { class: "unat" },
      h("td", { class: "rank" }),
      h("td", {}, h("span", {}, "Unattributed"), h("div", { class: "sub" }, "WAN total minus all Devices (headers, short-lived flows)")),
      h("td", { class: "hide-sm" }),
      h("td", { class: "r hide-sm" }, fmt(u.rx)),
      h("td", { class: "r hide-sm" }, fmt(u.tx)),
      h("td", { class: "r" }, fmt(u.rx + u.tx)),
      h("td"),
    ),
  );
  $("tt-rows").replaceChildren(...rows);
  const empty = $("tt-empty");
  empty.hidden = t.devices.length > 0;
  empty.textContent = "No Device had WAN Traffic in this window.";
  if (focusedId) $("tt-rows").querySelector<HTMLElement>(`[data-dev="${CSS.escape(focusedId)}"]`)?.focus();
}

async function loadTalkers() {
  ttAbort?.abort();
  if (ttWindow === "live") return; // filled by the Live stream
  const ac = (ttAbort = new AbortController());
  const w = ttWindow;
  try {
    const t = await getJSON<TopTalkers>(`/api/top-talkers?window=${w}`, ac.signal);
    if (w === ttWindow) renderTalkers(t);
  } catch (err) {
    if (ac.signal.aborted) return;
    setText("tt-summary", `Couldn't load Top Talkers (${(err as Error).message})`);
  }
}
setInterval(() => {
  if (ttWindow === "live") return;
  loadTalkers();
  if (selected) loadDetail();
}, 60_000);
loadTalkers();

// ---------- Device drill-down ----------

let ddTimeKind: "s" | "m" | "d" | "D" | "Y" = "s";
const ddChart = new Chart($("chart-dd"), {
  label: "Device WAN Traffic",
  height: 190,
  series: [
    { label: "Received", slot: "s1", fill: true },
    { label: "Sent", slot: "s2" },
  ],
  fmt: rateStr,
  axisFmt: rateAxis,
  timeFmt: (t) => timeLabel(t, ddTimeKind),
  ySteps: 4,
  xAxis: "live",
});
let ddMode: "live" | "time" = "live";
let detailSeq = 0;
let detailBusy = false;

function select(d: Device | null) {
  const prev = selected;
  selected = d;
  for (const b of $("tt-rows").querySelectorAll<HTMLElement>(".devbtn")) {
    const on = b.dataset.dev === d?.id;
    b.setAttribute("aria-expanded", String(on));
    b.closest("tr")!.classList.toggle("sel", on);
  }
  const dd = $("dd");
  if (!d) {
    dd.hidden = true;
    detailSeq++;
    if (prev) $("tt-rows").querySelector<HTMLElement>(`[data-dev="${CSS.escape(prev.id)}"]`)?.focus();
    return;
  }
  dd.hidden = false;
  showDeviceHead(d);
  $("dd-eps").replaceChildren();
  $("dd-empty").hidden = true;
  ddChart.setData([], [[], []]);
  detailSeq++;
  loadDetail();
}

function showDeviceHead(d: Device) {
  setText("dd-name", d.name);
  setText("dd-kind", kindLabel(d.kind));
  setText("dd-ids", deviceIds(d));
}

$("dd-close").addEventListener("click", () => select(null));
$("dd").addEventListener("keydown", (e) => {
  if (e.key === "Escape") select(null);
});

const TIME_KIND: Record<string, "s" | "m" | "d" | "D" | "Y"> = { live: "s", "1h": "m", "24h": "m", "7d": "d", "30d": "D", "1y": "Y" };

async function loadDetail() {
  if (!selected || detailBusy) return;
  const seq = detailSeq;
  const w = ttWindow;
  const span = WINDOWS.find((x) => x.id === w)!.span;
  detailBusy = true;
  try {
    const d = await getJSON<DeviceDetail>(`/api/devices/${encodeURIComponent(selected.id)}?window=${w}`);
    if (seq !== detailSeq) return;
    showDeviceHead(d.device);
    setText("dd-span", `WAN Traffic · ${span}${d.resolution && w !== "live" ? ` · ${d.resolution} samples` : ""}`);
    setText("dd-ep-span", `Top Remote Endpoints · ${span}`);
    setText("dd-ep-note", d.endpointsNote);
    const mode = w === "live" ? "live" : "time";
    if (mode !== ddMode) {
      ddMode = mode;
      ddChart.configure({ xAxis: mode });
    }
    ddTimeKind = TIME_KIND[w];
    ddChart.el.setAttribute("aria-label", `${d.device.name} WAN Traffic, ${span}`);
    const n = d.series.rxBps.length;
    ddChart.setData(
      Array.from({ length: n }, (_, i) => d.series.start + i * d.series.stepMs),
      [d.series.rxBps, d.series.txBps],
    );
    const fmt = d.unit === "bps" ? rateStr : bytesStr;
    colHeads(d.unit, ["eh-down", "eh-up", "eh-total"]);
    $("dd-eps").replaceChildren(
      ...d.endpoints.map((ep) =>
        h(
          "tr",
          {},
          h("td", {}, h("div", { class: "mono" }, ep.ip), h("div", { class: "sub ellip", title: ep.host ?? "" }, ep.host ?? "(no reverse DNS yet)")),
          h("td", ep.asn != null ? { title: `AS${ep.asn}` } : {}, ep.org ?? DASH),
          h("td", { class: "r hide-sm" }, fmt(ep.rx)),
          h("td", { class: "r hide-sm" }, fmt(ep.tx)),
          h("td", { class: "r strong600" }, fmt(ep.rx + ep.tx)),
        ),
      ),
    );
    const empty = $("dd-empty");
    empty.hidden = d.endpoints.length > 0;
    empty.textContent = "No Remote Endpoints in this window.";
  } catch (err) {
    if (seq !== detailSeq) return;
    const empty = $("dd-empty");
    empty.hidden = false;
    empty.textContent = `Couldn't load this Device (${(err as Error).message}).`;
  } finally {
    detailBusy = false;
    if (seq !== detailSeq && selected) loadDetail();
  }
}

// ---------- History ----------

const RANGES: { id: HistoryRange; label: string }[] = [
  { id: "1h", label: "1 h" },
  { id: "24h", label: "24 h" },
  { id: "7d", label: "7 d" },
  { id: "30d", label: "30 d" },
  { id: "1y", label: "1 y" },
];
let range: HistoryRange = "24h";
let histAbort: AbortController | null = null;
const histTime = (t: number) => timeLabel(t, range === "1h" ? "s" : TIME_KIND[range]);

const hWan = new Chart($("chart-h-wan"), {
  label: "WAN throughput history",
  height: 200,
  series: [
    { label: "Download", slot: "s1", fill: true },
    { label: "Upload", slot: "s2" },
  ],
  fmt: rateStr,
  axisFmt: rateAxis,
  timeFmt: histTime,
  xAxis: "time",
});
const small = (label: string, name: string, fmt: (v: number) => string, extra: { yMax?: () => number; yFloor?: () => number } = {}) =>
  ({ label, height: 120, series: [{ label: name, slot: "k" as const }], fmt, timeFmt: histTime, ySteps: 2, xAxis: "time" as const, ...extra });
const hCpu = new Chart($("chart-h-cpu"), small("CPU history", "CPU", (v) => pctStr(v, 1), { yFloor: () => 10 }));
const hMem = new Chart($("chart-h-mem"), small("Memory history", "Memory", (v) => pctStr(v), { yMax: () => 100 }));
const hCt = new Chart($("chart-h-ct"), small("Tracked connections history", "Connections", intStr, { yFloor: () => 100 }));

segmented($("hist-ranges"), RANGES, range, (r) => {
  range = r;
  setText("hist-note", "Loading…");
  loadHistory();
});

async function loadHistory() {
  histAbort?.abort();
  const ac = (histAbort = new AbortController());
  const r = range;
  try {
    const d = await getJSON<History>(`/api/history?range=${r}`, ac.signal);
    if (r !== range) return;
    setText("hist-note", resolutionNote(d.resolution, d.stepMs, d.range));
    $("hist-error").hidden = true;
    const n = d.wanRxBps.length;
    const times = Array.from({ length: n }, (_, i) => d.start + i * d.stepMs);
    hWan.setData(times, [d.wanRxBps, d.wanTxBps]);
    hCpu.setData(times, [d.cpuPct]);
    hMem.setData(times, [d.memUsedPct]);
    hCt.setData(times, [d.conntrack]);
  } catch (err) {
    if (ac.signal.aborted) return;
    setText("hist-note", " ");
    const e = $("hist-error");
    e.hidden = false;
    e.textContent = `Couldn't load History (${(err as Error).message}).`;
  }
}
setInterval(() => loadHistory(), 30_000);
loadHistory();
