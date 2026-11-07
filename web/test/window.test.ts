import { test } from "node:test";
import assert from "node:assert/strict";
import { RollingWindow } from "../src/window.ts";

const T = 1_794_012_000_000;
const ramp = (n: number, from: number) => Array.from({ length: n }, (_, i) => from + i);

test("the backfill fills the window", () => {
  const w = new RollingWindow(60, 1000);
  w.merge({ start: T, stepMs: 1000, a: ramp(60, 0) });
  const v = w.view(["a"]);
  assert.equal(v.times.length, 60);
  assert.equal(v.times[0], T);
  assert.equal(v.cols.a[59], 59);
});

test("a later event carrying the full 60 s slides the window by one", () => {
  const w = new RollingWindow(60, 1000);
  w.merge({ start: T, stepMs: 1000, a: ramp(60, 0) });
  w.merge({ start: T + 1000, stepMs: 1000, a: ramp(60, 1) });
  const v = w.view(["a"]);
  assert.equal(v.times[0], T + 1000);
  assert.equal(v.cols.a[0], 1);
  assert.equal(v.cols.a[59], 60);
});

test("a later event carrying only the newest sample appends it", () => {
  const w = new RollingWindow(60, 1000);
  w.merge({ start: T, stepMs: 1000, a: ramp(60, 0) });
  w.merge({ start: T + 60_000, stepMs: 1000, a: [99] });
  const v = w.view(["a"]);
  assert.equal(v.times.at(-1), T + 60_000);
  assert.equal(v.cols.a.at(-1), 99);
  assert.equal(v.cols.a[0], 1);
});

test("missed seconds show as gaps, not as stretched lines", () => {
  const w = new RollingWindow(60, 1000);
  w.merge({ start: T, stepMs: 1000, a: [1, 2, 3] });
  w.merge({ start: T + 5000, stepMs: 1000, a: [6] });
  const v = w.view(["a"]);
  assert.equal(v.times.length, 60);
  assert.deepEqual(v.cols.a.slice(-6), [1, 2, 3, null, null, 6]);
});
