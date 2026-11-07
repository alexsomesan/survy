import { test } from "node:test";
import assert from "node:assert/strict";
import { rate, rateStr, rateAxis, bytesStr, binStr, uptimeStr, niceMax } from "../src/format.ts";

test("axis labels drop trailing zeros", () => {
  assert.equal(rateAxis(1_000_000_000), "1 Gb/s");
  assert.equal(rateAxis(250_000_000), "250 Mb/s");
  assert.equal(rateAxis(2_500_000), "2.5 Mb/s");
  assert.equal(rateAxis(5_000_000), "5 Mb/s");
});

test("rates are bits per second, shown in decimal units with ~3 significant digits", () => {
  assert.deepEqual(rate(1_234_000_000), { n: "1.23", u: "Gb/s" });
  assert.deepEqual(rate(360_400_000), { n: "360", u: "Mb/s" });
  assert.deepEqual(rate(24_560_000), { n: "24.6", u: "Mb/s" });
  assert.deepEqual(rate(2_345_000), { n: "2.35", u: "Mb/s" });
  assert.deepEqual(rate(612_000), { n: "612", u: "kb/s" });
  assert.deepEqual(rate(450), { n: "450", u: "b/s" });
  assert.deepEqual(rate(0), { n: "0", u: "b/s" });
});

test("unknown rate renders as a dash", () => {
  assert.equal(rateStr(null), "—");
  assert.equal(rateStr(4_100_000), "4.10 Mb/s");
});

test("volumes are bytes, shown in decimal units", () => {
  assert.equal(bytesStr(58_400_000_000), "58.4 GB");
  assert.equal(bytesStr(1_440_000_000), "1.44 GB");
  assert.equal(bytesStr(312_000_000_000), "312 GB");
  assert.equal(bytesStr(2_310_000_000_000), "2.31 TB");
  assert.equal(bytesStr(668_000_000), "668 MB");
  assert.equal(bytesStr(4_200_000), "4.2 MB");
  assert.equal(bytesStr(15_300), "15 KB");
  assert.equal(bytesStr(512), "512 B");
  assert.equal(bytesStr(null), "—");
});

test("memory and disks read in binary units, like free -h and df -h", () => {
  assert.equal(binStr(8_066_224 * 1024), "7.69 GiB"); // the router's MemTotal
  assert.equal(binStr(536_870_912), "512 MiB"); // a 512 MiB container limit
  assert.equal(binStr(2 * 2 ** 30), "2.00 GiB");
  assert.equal(binStr(65_409_024), "62.4 MiB");
  assert.equal(binStr(5 * 2 ** 20), "5.0 MiB");
  assert.equal(binStr(300 * 1024), "300 KiB");
  assert.equal(binStr(5_217_234_944), "4.86 GiB"); // the 5 GiB system partition, ext4 overhead off
  assert.equal(binStr(10_464_022_528), "9.75 GiB"); // the 10 GiB data partition
  assert.equal(binStr(256 * 2 ** 30), "256 GiB");
  assert.equal(binStr(2 * 2 ** 40), "2.00 TiB");
  assert.equal(binStr(null), "—");
});

test("WAN uptime reads in the two largest units", () => {
  assert.equal(uptimeStr(360_000), "4 d 4 h");
  assert.equal(uptimeStr(3 * 3600 + 12 * 60), "3 h 12 min");
  assert.equal(uptimeStr(300), "5 min");
  assert.equal(uptimeStr(20), "<1 min");
  assert.equal(uptimeStr(null), "—");
});

test("axis maxima round up to 1, 2, 2.5, 5 or 10 times a power of ten", () => {
  assert.equal(niceMax(0), 1);
  assert.equal(niceMax(7), 10);
  assert.equal(niceMax(180), 200);
  assert.equal(niceMax(230), 250);
  assert.equal(niceMax(3100), 5000);
  assert.equal(niceMax(1_000_000_000), 1_000_000_000);
});
