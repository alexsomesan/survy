// Pure formatting helpers. Units follow docs/api.md: rates are bits/s, volumes are bytes.

export const DASH = "—";

export interface NumUnit {
  n: string;
  u: string;
}

function sig3(v: number): string {
  if (v >= 100) return v.toFixed(0);
  if (v >= 10) return v.toFixed(1);
  return v.toFixed(2);
}

/** A rate in bits per second, split into number and unit. */
export function rate(bps: number): NumUnit {
  const v = Math.max(0, bps);
  if (v >= 1e9) return { n: (v / 1e9).toFixed(2), u: "Gb/s" };
  if (v >= 1e6) return { n: sig3(v / 1e6), u: "Mb/s" };
  if (v >= 1e3) return { n: (v / 1e3).toFixed(0), u: "kb/s" };
  return { n: v.toFixed(0), u: "b/s" };
}

export function rateStr(bps: number | null | undefined): string {
  if (bps == null || !Number.isFinite(bps)) return DASH;
  const r = rate(bps);
  return `${r.n} ${r.u}`;
}

/** A compact rate for chart axes: trailing zeros dropped ("1 Gb/s", "2.5 Mb/s"). */
export function rateAxis(bps: number): string {
  const r = rate(bps);
  return `${r.n.includes(".") ? r.n.replace(/\.?0+$/, "") : r.n} ${r.u}`;
}

/** A traffic volume in bytes, decimal units. */
export function bytesStr(b: number | null | undefined): string {
  if (b == null || !Number.isFinite(b)) return DASH;
  const v = Math.max(0, b);
  if (v >= 1e12) return `${(v / 1e12).toFixed(2)} TB`;
  if (v >= 1e9) {
    const g = v / 1e9;
    return `${g >= 100 ? g.toFixed(0) : g >= 10 ? g.toFixed(1) : g.toFixed(2)} GB`;
  }
  if (v >= 1e6) {
    const m = v / 1e6;
    return `${m >= 10 ? m.toFixed(0) : m.toFixed(1)} MB`;
  }
  if (v >= 1e3) return `${(v / 1e3).toFixed(0)} KB`;
  return `${v.toFixed(0)} B`;
}

/** Memory and disk sizes in binary units (GiB, MiB), as `free -h` and
 *  `df -h` show them. Traffic volumes stay decimal (bytesStr), like the
 *  bit rates they go with. */
export function binStr(b: number | null | undefined): string {
  if (b == null || !Number.isFinite(b)) return DASH;
  const v = Math.max(0, b);
  if (v >= 2 ** 40) return `${(v / 2 ** 40).toFixed(2)} TiB`;
  if (v >= 2 ** 30) {
    const g = v / 2 ** 30;
    return `${g >= 100 ? g.toFixed(0) : g >= 10 ? g.toFixed(1) : g.toFixed(2)} GiB`;
  }
  if (v >= 2 ** 20) {
    const m = v / 2 ** 20;
    return `${m >= 100 ? m.toFixed(0) : m.toFixed(1)} MiB`;
  }
  if (v >= 1024) return `${(v / 1024).toFixed(0)} KiB`;
  return `${v.toFixed(0)} B`;
}

export function uptimeStr(sec: number | null | undefined): string {
  if (sec == null || !Number.isFinite(sec)) return DASH;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d} d ${h} h`;
  if (h > 0) return `${h} h ${m} min`;
  if (m > 0) return `${m} min`;
  return "<1 min";
}

/** Rounds an axis maximum up to 1, 2, 2.5, 5 or 10 × 10^k. */
export function niceMax(v: number): number {
  if (!(v > 0)) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const m = v / p;
  const n = m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10;
  return n * p;
}

export function pctStr(v: number | null | undefined, digits = 0): string {
  if (v == null || !Number.isFinite(v)) return DASH;
  return `${v.toFixed(digits)}%`;
}

function spanWords(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s % 3600 === 0) return `${s / 3600}-hour`;
  if (s % 60 === 0) return `${s / 60}-minute`;
  return `${s}-second`;
}

const RES_MS: Record<string, number> = { "10s": 10_000, "1m": 60_000, "1h": 3_600_000 };

/** "10-second samples · 24 h range", noting server-side averaging when stepMs is coarser. */
export function resolutionNote(resolution: string, stepMs: number, range: string): string {
  const resMs = RES_MS[resolution] ?? stepMs;
  let s = `${spanWords(resMs)} samples`;
  if (stepMs > resMs) s += `, shown as ${spanWords(stepMs)} averages`;
  const r = range.replace(/^(\d+)([a-z])$/, "$1 $2");
  return `${s} · ${r} range`;
}

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const pad = (n: number) => (n < 10 ? `0${n}` : `${n}`);

/** Local-time label: s = 14:03:07, m = 14:03, d = Tue 14:03, D = Nov 3 14:03, Y = Nov 3, 2026 14:00. */
export function timeLabel(ms: number, kind: "s" | "m" | "d" | "D" | "Y"): string {
  const d = new Date(ms);
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  switch (kind) {
    case "s":
      return `${hm}:${pad(d.getSeconds())}`;
    case "m":
      return hm;
    case "d":
      return `${DOW[d.getDay()]} ${hm}`;
    case "D":
      return `${MON[d.getMonth()]} ${d.getDate()} ${hm}`;
    case "Y":
      return `${MON[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()} ${hm}`;
  }
}

export function intStr(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return DASH;
  return Math.round(v).toLocaleString("en-US");
}
