import { test } from "node:test";
import assert from "node:assert/strict";
import { kindLabel, deviceSubtitle, deviceIds, talkersSummary } from "../src/device.ts";
import type { Device } from "../src/api.ts";

const mac: Device = {
  id: "mac:3c:22:fb:1a:0d:a3", name: "work-laptop", kind: "lan", mac: "3c:22:fb:1a:0d:a3", vendor: "Apple",
  addresses: ["192.168.2.20", "2001:db8:1:1::1a", "fe80::1", "2001:db8:1:1::2b"], rx: 0, tx: 0,
};

test("kinds read as words", () => {
  assert.equal(kindLabel("lan"), "LAN");
  assert.equal(kindLabel("wg"), "WireGuard peer");
  assert.equal(kindLabel("container"), "Container");
  assert.equal(kindLabel("router"), "Router");
  assert.equal(kindLabel("wg-tunnels"), "Tunnel transport");
});

test("a LAN Device's subtitle is its IPv4 and vendor", () => {
  assert.equal(deviceSubtitle(mac), "192.168.2.20 · Apple");
  assert.equal(deviceSubtitle({ ...mac, vendor: null }), "192.168.2.20");
  assert.equal(deviceSubtitle({ ...mac, addresses: [] }), "Apple");
});

test("other kinds explain themselves in the subtitle", () => {
  assert.equal(deviceSubtitle({ ...mac, kind: "wg", mac: null, vendor: null, addresses: ["10.0.0.21"] }), "10.0.0.21");
  assert.equal(deviceSubtitle({ ...mac, kind: "container", mac: null, vendor: null, addresses: ["172.31.254.3"] }), "Container · 172.31.254.3");
  assert.equal(deviceSubtitle({ ...mac, kind: "router", addresses: [] }), "The router itself");
  assert.equal(deviceSubtitle({ ...mac, kind: "wg-tunnels", addresses: [] }), "Encrypted WireGuard transport");
});

test("the drill-down identifier line counts global IPv6 addresses (not link-local)", () => {
  assert.equal(deviceIds(mac), "MAC 3c:22:fb:1a:0d:a3 · 192.168.2.20 · 2 IPv6 addresses");
  assert.equal(deviceIds({ ...mac, addresses: ["192.168.2.20", "fe80::1", "2003::5"] }), "MAC 3c:22:fb:1a:0d:a3 · 192.168.2.20 · 1 IPv6 address");
  assert.equal(deviceIds({ ...mac, kind: "wg", name: "Laptop", mac: null, addresses: ["10.0.0.21"] }), "Peer Laptop · 10.0.0.21");
  assert.equal(deviceIds({ ...mac, kind: "container", name: "unifi-db", mac: null, addresses: ["172.31.254.4"] }), "Container unifi-db · 172.31.254.4");
});

test("the Top Talkers summary gives WAN total, Device count and Unattributed share", () => {
  const live = { window: "live", unit: "bps" as const, wanRx: 360_000_000, wanTx: 40_000_000,
    devices: [{ ...mac, rx: 300_000_000, tx: 30_000_000 }, { ...mac, id: "x", rx: 0, tx: 0 }],
    unattributed: { rx: 60_000_000, tx: 10_000_000 } };
  assert.equal(talkersSummary(live), "WAN now 400 Mb/s · 1 Device with WAN Traffic · Unattributed 17.5%");
  const day = { ...live, window: "24h", unit: "bytes" as const, wanRx: 58_400_000_000, wanTx: 4_100_000_000,
    devices: [live.devices[0], { ...mac, id: "y", rx: 5, tx: 0 }], unattributed: { rx: 0, tx: 0 } };
  assert.equal(talkersSummary(day), "WAN total 62.5 GB · 2 Devices with WAN Traffic · Unattributed 0.0%");
});
