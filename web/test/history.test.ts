import { test } from "node:test";
import assert from "node:assert/strict";
import { resolutionNote } from "../src/format.ts";

test("the History note names the Resolution served", () => {
  assert.equal(resolutionNote("10s", 10_000, "1h"), "10-second samples · 1 h range");
  assert.equal(resolutionNote("1m", 60_000, "30d"), "1-minute samples · 30 d range");
  assert.equal(resolutionNote("1h", 3_600_000, "1y"), "1-hour samples · 1 y range");
});

test("the History note says when the server averaged samples down", () => {
  assert.equal(resolutionNote("1m", 420_000, "7d"), "1-minute samples, shown as 7-minute averages · 7 d range");
  assert.equal(resolutionNote("10s", 60_000, "24h"), "10-second samples, shown as 1-minute averages · 24 h range");
  assert.equal(resolutionNote("1m", 1_800_000, "30d"), "1-minute samples, shown as 30-minute averages · 30 d range");
  assert.equal(resolutionNote("1h", 21_600_000, "1y"), "1-hour samples, shown as 6-hour averages · 1 y range");
});
