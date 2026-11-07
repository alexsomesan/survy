// How Devices and Top Talkers read in the UI.
import type { Device, DeviceKind, TopTalkers } from "./api.ts";
import { bytesStr, rateStr } from "./format.ts";

const KINDS: Record<DeviceKind, string> = {
  lan: "LAN",
  wg: "WireGuard peer",
  container: "Container",
  router: "Router",
  "wg-tunnels": "Tunnel transport",
};

export function kindLabel(k: DeviceKind): string {
  return KINDS[k] ?? k;
}

const isV6 = (a: string) => a.includes(":");
const isLinkLocal = (a: string) => a.toLowerCase().startsWith("fe80:");

function primaryAddr(d: Device): string | undefined {
  return d.addresses.find((a) => !isV6(a)) ?? d.addresses.find((a) => !isLinkLocal(a));
}

function join(parts: (string | null | undefined)[]): string {
  return parts.filter((p): p is string => !!p).join(" · ");
}

export function deviceSubtitle(d: Device): string {
  const ip = primaryAddr(d);
  switch (d.kind) {
    case "lan":
      return join([ip, d.vendor]);
    case "wg":
      return join([ip]);
    case "container":
      return join(["Container", ip]);
    case "router":
      return "The router itself";
    case "wg-tunnels":
      return "Encrypted WireGuard transport";
  }
}

export function deviceIds(d: Device): string {
  const v4 = d.addresses.find((a) => !isV6(a));
  const v6 = d.addresses.filter((a) => isV6(a) && !isLinkLocal(a)).length;
  const v6s = v6 ? `${v6} IPv6 address${v6 === 1 ? "" : "es"}` : null;
  switch (d.kind) {
    case "lan":
      return join([d.mac ? `MAC ${d.mac}` : null, v4, v6s]);
    case "wg":
      return join([`Peer ${d.name}`, ...d.addresses]);
    case "container":
      return join([`Container ${d.name}`, d.mac ? `MAC ${d.mac}` : null, v4, v6s]);
    case "router":
      return join(["Router · DNS, NTP, updates and other traffic of the router itself"]);
    case "wg-tunnels":
      return "WireGuard UDP transport over the WAN, all peers";
  }
}

export function talkersSummary(t: TopTalkers): string {
  const live = t.unit === "bps";
  const fmt = live ? rateStr : bytesStr;
  const wan = t.wanRx + t.wanTx;
  const n = t.devices.filter((d) => d.rx + d.tx > 0).length;
  const un = t.unattributed.rx + t.unattributed.tx;
  const share = wan > 0 ? (un / wan) * 100 : 0;
  return join([
    `${live ? "WAN now" : "WAN total"} ${fmt(wan)}`,
    `${n} Device${n === 1 ? "" : "s"} with WAN Traffic`,
    `Unattributed ${share.toFixed(1)}%`,
  ]);
}
