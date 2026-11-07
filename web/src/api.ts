// Types for the JSON contract in docs/api.md (v1).

export type DeviceKind = "lan" | "wg" | "container" | "router" | "wg-tunnels";
export type Unit = "bps" | "bytes";
export type TalkerWindow = "live" | "1h" | "24h" | "7d" | "30d";
export type HistoryRange = "1h" | "24h" | "7d" | "30d" | "1y";
export type Num = number | null;

export interface Device {
  id: string;
  name: string;
  kind: DeviceKind;
  mac: string | null;
  vendor: string | null;
  addresses: string[];
  rx: number;
  tx: number;
}

export interface TopTalkers {
  window: string;
  unit: Unit;
  from?: number;
  to?: number;
  wanRx: number;
  wanTx: number;
  devices: Device[];
  unattributed: { rx: number; tx: number };
}

export interface Series {
  start: number;
  stepMs: number;
}

export interface Iface {
  name: string;
  role: "wan" | "lan" | "wireguard" | string;
  rxBps: Num;
  txBps: Num;
}

export interface LiveEvent {
  time: number;
  lineRateBps: number;
  interfaces: Iface[];
  wan: { ipv4: string | null; ipv6: string | null; uptimeSec: Num; todayRxBytes: Num; todayTxBytes: Num };
  wanSeries: Series & { rxBps: Num[]; txBps: Num[] };
  resources: {
    cpuPct: Num;
    memUsedBytes: Num;
    memTotalBytes: Num;
    memAvailBytes: Num;
    tempC: Num;
    conntrackCount: Num;
    conntrackMax: Num;
    load: [Num, Num, Num] | null;
    disks: { name: string; path: string; usedBytes: Num; totalBytes: Num }[];
    series: Series & { cpuPct: Num[]; memUsedBytes: Num[]; tempC: Num[]; conntrack: Num[]; load1: Num[] };
  };
  containers: { name: string; cpuPct: Num; memBytes: Num; memLimitBytes: Num }[];
  self: { cpuPct: Num; memBytes: Num; dataBytes: Num };
  topTalkers: TopTalkers;
}

export interface Endpoint {
  ip: string;
  host: string | null;
  org: string | null;
  asn: number | null;
  rx: number;
  tx: number;
}

export interface DeviceDetail {
  device: Device;
  window: TalkerWindow;
  unit: Unit;
  resolution: string;
  series: Series & { rxBps: Num[]; txBps: Num[] };
  endpoints: Endpoint[];
  endpointsNote: string;
}

export interface History extends Series {
  range: HistoryRange;
  resolution: string;
  wanRxBps: Num[];
  wanTxBps: Num[];
  cpuPct: Num[];
  memUsedPct: Num[];
  conntrack: Num[];
}

export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return (await res.json()) as T;
}
