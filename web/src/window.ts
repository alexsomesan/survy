// A fixed-length rolling window of 1-second samples, fed by Live events.
// It accepts either a full backfill or only the newest samples in each event,
// so it doesn't depend on how much history each event repeats.
import type { Num } from "./api.ts";

type SeriesIn = { start: number; stepMs: number };

export interface WindowView {
  times: number[];
  cols: Record<string, Num[]>;
}

export class RollingWindow {
  private samples = new Map<number, Record<string, Num>>();
  private latest = -Infinity;

  readonly size: number;
  readonly stepMs: number;

  constructor(size: number, stepMs: number) {
    this.size = size;
    this.stepMs = stepMs;
  }

  merge<T extends SeriesIn>(s: T): void {
    for (const [key, val] of Object.entries(s as object)) {
      if (!Array.isArray(val)) continue;
      val.forEach((v, i) => {
        const t = s.start + i * s.stepMs;
        let row = this.samples.get(t);
        if (!row) this.samples.set(t, (row = {}));
        row[key] = typeof v === "number" ? v : null;
        if (t > this.latest) this.latest = t;
      });
    }
    const oldest = this.latest - (this.size - 1) * this.stepMs;
    for (const t of this.samples.keys()) if (t < oldest) this.samples.delete(t);
  }

  view(keys: string[]): WindowView {
    const times: number[] = [];
    const cols: Record<string, Num[]> = {};
    for (const k of keys) cols[k] = [];
    if (!Number.isFinite(this.latest)) return { times, cols };
    for (let i = 0; i < this.size; i++) {
      const t = this.latest - (this.size - 1 - i) * this.stepMs;
      times.push(t);
      const row = this.samples.get(t);
      for (const k of keys) cols[k].push(row?.[k] ?? null);
    }
    return { times, cols };
  }
}
