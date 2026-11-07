// A thin wrapper over uPlot: theme colours from CSS tokens, a crosshair with an
// HTML tooltip, resize-following, an optional dashed reference line, and a
// rebuild when the system colour scheme changes.
import uPlot from "uplot";
import type { Num } from "./api.ts";
import { niceMax, timeLabel } from "./format.ts";

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function axisTime(ms: number, kind: "M" | "D" | "d" | "m"): string {
  const d = new Date(ms);
  if (kind === "M") return d.getMonth() === 0 ? `${MON[0]} ${d.getFullYear()}` : MON[d.getMonth()];
  if (kind === "D") return `${MON[d.getMonth()]} ${d.getDate()}`;
  if (kind === "d") return timeLabel(ms, "d");
  return timeLabel(ms, "m");
}

export type Slot = "s1" | "s2" | "k";

export interface ChartSeries {
  label: string;
  slot: Slot;
  fill?: boolean;
}

export interface ChartSpec {
  label: string; // accessible name
  height: number;
  series: ChartSeries[];
  /** Formats a value for the tooltip. */
  fmt: (v: number) => string;
  /** Formats a value for the y axis; defaults to fmt. */
  axisFmt?: (v: number) => string;
  /** Formats a timestamp (ms) for the tooltip. */
  timeFmt: (t: number) => string;
  /** Lower bound for the y axis maximum (e.g. the line rate). */
  yFloor?: () => number;
  /** Fixed y maximum (e.g. 100 for percentages). */
  yMax?: () => number | null;
  /** Draw a dashed horizontal line at this value when the y axis reaches it. */
  refLine?: () => number | null;
  /** Text shown at the top of the plot instead when refLine is above the axis. */
  refLabel?: () => string;
  ySteps?: number;
  /** "live": −60 s … now labels. "time": clock/date labels. "none": spark. */
  xAxis: "live" | "time" | "none";
}

const charts = new Set<Chart>();

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function withAlpha(hex: string, a: number): string {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) return hex;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
}

const FONT = "11px Inter, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif";

export class Chart {
  readonly el: HTMLElement;
  private spec: ChartSpec;
  private plot: uPlot | null = null;
  private data: uPlot.AlignedData = [[]];
  private tip: HTMLDivElement;
  private ro: ResizeObserver;

  constructor(el: HTMLElement, spec: ChartSpec) {
    this.el = el;
    this.spec = spec;
    el.classList.add("chart");
    el.style.height = `${spec.height}px`;
    el.setAttribute("role", "img");
    el.setAttribute("aria-label", spec.label);
    this.tip = document.createElement("div");
    this.tip.className = "tip";
    this.tip.hidden = true;
    this.data = [[], ...spec.series.map(() => [])] as unknown as uPlot.AlignedData;
    this.build();
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(el);
    charts.add(this);
  }

  destroy(): void {
    this.ro.disconnect();
    this.plot?.destroy();
    this.plot = null;
    charts.delete(this);
  }

  /** times in ms; one column per series. */
  setData(times: number[], cols: Num[][]): void {
    this.data = [times.map((t) => t / 1000), ...cols] as unknown as uPlot.AlignedData;
    this.plot?.setData(this.data);
    this.updateTip();
  }

  /** Changes part of the spec (e.g. the x axis kind) and rebuilds. */
  configure(patch: Partial<ChartSpec>): void {
    this.spec = { ...this.spec, ...patch };
    this.el.setAttribute("aria-label", this.spec.label);
    this.rebuild();
  }

  rebuild(): void {
    this.plot?.destroy();
    this.build();
  }

  private resize(): void {
    const w = this.el.clientWidth;
    if (this.plot && w > 0 && w !== this.plot.width) this.plot.setSize({ width: w, height: this.spec.height });
  }

  private build(): void {
    const s = this.spec;
    const muted = token("--muted");
    const grid = token("--grid");
    const colour = (slot: Slot) => token(slot === "k" ? "--ink2" : `--${slot}`);
    const spark = s.xAxis === "none";
    const steps = s.ySteps ?? 4;
    const axisFmt = s.axisFmt ?? s.fmt;

    const yRange: uPlot.Range.Function = (_u, _min, max) => {
      const fixed = s.yMax?.();
      if (fixed != null && fixed > 0) return [0, fixed];
      const floor = s.yFloor?.() ?? 0;
      const m = Math.max(max ?? 0, floor);
      return [0, spark ? m * 1.15 || 1 : niceMax(m)];
    };

    const axes: uPlot.Axis[] = spark
      ? [{ show: false }, { show: false }]
      : [
          {
            stroke: muted,
            font: FONT,
            grid: { show: false },
            ticks: { show: false },
            size: 22,
            gap: 4,
            ...(s.xAxis === "live"
              ? {
                  splits: (u: uPlot) => {
                    const max = u.scales.x.max ?? 0;
                    return [-60, -45, -30, -15, 0].map((d) => max + d).filter((v) => v >= (u.scales.x.min ?? 0) - 1);
                  },
                  values: (_u: uPlot, splits: number[]) => {
                    const max = splits[splits.length - 1];
                    return splits.map((v) => (v === max ? "now" : `−${Math.round(max - v)} s`));
                  },
                }
              : {
                  // One-line, 24-hour labels chosen by tick spacing and visible span.
                  values: (u: uPlot, splits: number[], _ax: number, _space: number, incr: number) => {
                    const span = (u.scales.x.max ?? 0) - (u.scales.x.min ?? 0);
                    const kind = incr >= 28 * 86400 ? "M" : incr >= 86400 ? "D" : span > 2 * 86400 ? "d" : "m";
                    return splits.map((v) => axisTime(v * 1000, kind));
                  },
                }),
          },
          {
            stroke: muted,
            font: FONT,
            size: 64,
            gap: 6,
            ticks: { show: false },
            grid: { stroke: grid, width: 1 },
            splits: (u: uPlot) => {
              const max = u.scales.y.max ?? 1;
              return Array.from({ length: steps + 1 }, (_, i) => (max * i) / steps);
            },
            values: (_u: uPlot, splits: number[]) => splits.map((v) => (v === 0 ? "0" : axisFmt(v))),
          },
        ];

    const opts: uPlot.Options = {
      width: Math.max(this.el.clientWidth, 50),
      height: s.height,
      padding: spark ? [3, 0, 2, 0] : [8, 8, 0, 0],
      legend: { show: false },
      select: { show: false, left: 0, top: 0, width: 0, height: 0 },
      cursor: {
        y: false,
        drag: { x: false, y: false },
        points: { size: spark ? 5 : 7, width: 1.5, fill: token("--card") },
      },
      scales: { x: { time: s.xAxis === "time" }, y: { range: yRange } },
      axes,
      series: [
        {},
        ...s.series.map<uPlot.Series>((ser) => ({
          label: ser.label,
          stroke: colour(ser.slot),
          width: ser.slot === "k" ? 1.5 : 2,
          fill: ser.fill ? withAlpha(colour(ser.slot), 0.12) : undefined,
          spanGaps: false,
          points: { show: false },
        })),
      ],
      hooks: {
        setCursor: [() => this.updateTip()],
        draw: [
          (u) => {
            const v = s.refLine?.();
            if (v == null) return;
            const ctx = u.ctx;
            if (v > (u.scales.y.max ?? 0)) {
              const text = s.refLabel?.();
              if (!text) return;
              ctx.save();
              ctx.fillStyle = muted;
              ctx.font = FONT.replace("11px", `${11 * devicePixelRatio}px`);
              ctx.textAlign = "right";
              ctx.textBaseline = "top";
              ctx.fillText(`${text} ↑`, u.bbox.left + u.bbox.width - 4 * devicePixelRatio, u.bbox.top + 2 * devicePixelRatio);
              ctx.restore();
              return;
            }
            const y = Math.round(u.valToPos(v, "y", true)) + 0.5;
            ctx.save();
            ctx.strokeStyle = muted;
            ctx.lineWidth = devicePixelRatio;
            ctx.setLineDash([4 * devicePixelRatio, 4 * devicePixelRatio]);
            ctx.beginPath();
            ctx.moveTo(u.bbox.left, y);
            ctx.lineTo(u.bbox.left + u.bbox.width, y);
            ctx.stroke();
            ctx.restore();
          },
        ],
      },
    };
    this.plot = new uPlot(opts, this.data, this.el);
    this.plot.over.appendChild(this.tip);
  }

  private updateTip(): void {
    const u = this.plot;
    if (!u) return;
    const idx = u.cursor.idx;
    const left = u.cursor.left ?? -1;
    if (idx == null || left < 0) {
      this.tip.hidden = true;
      return;
    }
    const t = (this.data[0] as number[])[idx];
    if (t == null) {
      this.tip.hidden = true;
      return;
    }
    const rows = this.spec.series.map((ser, i) => {
      const v = (this.data[i + 1] as Num[])[idx];
      const sw = ser.slot === "k" ? "" : `<span class="sw bg${ser.slot === "s1" ? 1 : 2}"></span>`;
      return `<span>${sw}${ser.label} <b class="num">${v == null ? "no data" : this.spec.fmt(v)}</b></span>`;
    });
    this.tip.innerHTML = `<span class="sub">${this.spec.timeFmt(t * 1000)}</span>${rows.join("")}`;
    this.tip.hidden = false;
    const w = u.over.clientWidth;
    if (left > w * 0.6) {
      this.tip.style.left = "";
      this.tip.style.right = `${w - left + 10}px`;
    } else {
      this.tip.style.right = "";
      this.tip.style.left = `${left + 10}px`;
    }
  }
}

// Follow the system colour scheme: rebuild every chart with the new tokens.
if (typeof window !== "undefined" && window.matchMedia) {
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    for (const c of charts) c.rebuild();
  });
}
