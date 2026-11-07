# API contract (v1)

This is the API between the Go backend and the frontend. Change it only together with both sides. Terms are from [GLOSSARY.md](../GLOSSARY.md).

## Conventions

- **Format:** JSON with camelCase keys. Every documented key is always present. A value that is unknown is `null`, not an omitted key.
- **Units:**
  - Times are unix **milliseconds**.
  - Rates are **bits per second** (`…Bps` suffix, a float).
  - Volumes are **bytes** (integers).
  - Percentages are 0–100 floats.
- **Direction:** always from the point of view of the thing named.
  - Interface `rxBps` is traffic arriving at the router on that interface.
  - WAN `rx` is download.
  - Device `rx` is what the Device **received** from the internet, and `tx` is what it **sent**.
- **Series:** `{start, stepMs, …arrays}`, oldest first, with sample `i` at `start + i*stepMs`. Missing samples are `null`.
- **Device kinds:** `lan`, `wg` (WireGuard peer), `container`, `router`, `wg-tunnels`.
- **Device ids:** `mac:<aa:bb:..>`, `wg:<base64 pubkey>`, `ctr:<name>`, `router`, `wg-tunnels`. Clients URL-encode them in paths.

### Device object

```json
{"id":"mac:3c:22:fb:1a:0d:a3","name":"work-laptop","kind":"lan",
 "mac":"3c:22:fb:1a:0d:a3","vendor":"Apple","addresses":["192.168.2.20","2001:db8:1:1::1a"],
 "rx":123,"tx":45}
```

- `mac` and `vendor` are `null` when not applicable.
- `addresses` lists the Device's local addresses currently known, IPv4 first.
- `rx`/`tx` are rates (bits/s) in Live contexts and bytes in window contexts. The enclosing object's `unit` says which: `"bps"` or `"bytes"`.

## `GET /api/live`: Server-Sent Events

Each message is `event: live`, with `data:` set to one LiveEvent, sent every second. The first message after connecting is sent immediately and carries full 60-second backfill in every series.

```json
{
  "time": 1794012000000,
  "lineRateBps": 1000000000,
  "interfaces": [{"name":"pppoe0","role":"wan","rxBps":0,"txBps":0},
                 {"name":"br0","role":"lan","rxBps":0,"txBps":0},
                 {"name":"wg0","role":"wireguard","rxBps":0,"txBps":0}],
  "wan": {"ipv4":"203.0.113.176","ipv6":"2003:…","uptimeSec":360000,
          "todayRxBytes":0,"todayTxBytes":0},
  "wanSeries": {"start":0,"stepMs":1000,"rxBps":[/*60*/],"txBps":[/*60*/]},
  "resources": {
    "cpuPct": 7.5, "memUsedBytes":0, "memTotalBytes":0, "memAvailBytes":0,
    "tempC": 48.0, "conntrackCount": 810, "conntrackMax": 262144,
    "load": [0.18,0.11,0.09],
    "disks": [{"name":"system","path":"/","usedBytes":0,"totalBytes":0},
              {"name":"data","path":"/mnt/dash-data","usedBytes":0,"totalBytes":0}],
    "series": {"start":0,"stepMs":1000,"cpuPct":[],"memUsedBytes":[],"tempC":[],"conntrack":[],"load1":[]}
  },
  "containers": [{"name":"unifi-db","cpuPct":3.5,"memBytes":0,"memLimitBytes":0}],
  "self": {"cpuPct":0.8,"memBytes":0,"dataBytes":0},
  "topTalkers": {"window":"live","unit":"bps","wanRx":0,"wanTx":0,
                 "devices":[/* Device objects, sorted by rx+tx desc */],
                 "unattributed":{"rx":0,"tx":0}}
}
```

- `disks[].path` is the router-side path: `/` for the system partition and the router's mount point of the data directory for `data` (`DASH_DATA_HOST_PATH`, default `/mnt/dash-data`). The system partition is measured through the router's `/boot`, which is on the same partition.
- `memLimitBytes` is 0 when the container has no limit. When the container's service cgroup has no limit, the limit podman sets on its `libpod-payload-*` child is used.
- `self` is the dashboard's own usage. `dataBytes` is the size of the History database.

## `GET /api/top-talkers?window=1h|24h|7d|30d`

```json
{"window":"24h","unit":"bytes","from":0,"to":0,"wanRx":0,"wanTx":0,
 "devices":[/* Device objects, sorted by rx+tx desc */],"unattributed":{"rx":0,"tx":0}}
```

- The window is exactly the last span: `from` = `to` − span (`to` is now). Every window is summed from 1-minute data (kept 30 days), counting the minutes that start at or after `from`.

Errors: 400 for an unknown window.

## `GET /api/devices/{id}?window=live|1h|24h|7d|30d`

```json
{"device":{/* Device object, rx/tx totals for the window */},
 "window":"24h","unit":"bytes","resolution":"1m",
 "series":{"start":0,"stepMs":60000,"rxBps":[],"txBps":[]},
 "endpoints":[{"ip":"192.0.2.140","host":"ipv4-c071-….nflxvideo.net","org":"Netflix","asn":2906,"rx":0,"tx":0}],
 "endpointsNote":"hourly, kept 30 days"}
```

- For `live`: `unit` is `"bps"`, the series covers the last 60 s at 1 s, and the endpoints are current rates.
- For stored windows, `resolution` is `"1m"`: the series is 1-minute data, summed into steps of a whole number of minutes so there are at most about 1,500 points (`1h` and `24h` at 1 min, `7d` at 7 min, `30d` at 29 min; `stepMs` is the step served). Its last step holds now. The `device` totals cover exactly `now − span` to now, like Top Talkers.
- `host`, `org` and `asn` are `null` until known. `host` is the reverse DNS name (looked up in the background after a Device detail first shows the endpoint, so a later request fills it in; public addresses only). `org` is the organisation part of the ip2asn AS description (`"CLOUDFLARENET - Cloudflare, Inc."` → `"Cloudflare, Inc."`); both are `null` for unrouted space or when the database file is missing.
- Endpoints are sorted by rx+tx descending, with at most 50.

Errors: 404 for an unknown Device, 400 for an unknown window.

## `GET /api/history?range=1h|24h|7d|30d|1y`

```json
{"range":"24h","resolution":"10s","start":0,"stepMs":10000,
 "wanRxBps":[],"wanTxBps":[],"cpuPct":[],"memUsedPct":[],"conntrack":[]}
```

| Range | Resolution |
|---|---|
| `1h`, `24h` | 10 s |
| `7d`, `30d` | 1 min |
| `1y` | 1 h |

The server may downsample to at most about 1,500 points by averaging; `stepMs` reflects the step actually served.

Errors: 400 for an unknown range.

## Router inventory file

The router's VyOS commit post-hook writes `inventory.json` into the data directory (`DataDir`, `/mnt/dash-data` in production) after every commit. It holds no secrets. The dashboard re-reads it whenever it changes, so new peers and containers appear without a restart. A missing or unreadable file means "no WireGuard peers and no containers".

```json
{"wireguard":{"interface":"wg0","listenPort":51820,
              "peers":[{"name":"Laptop","publicKey":"…","allowedIps":["10.0.0.21/32"]}]},
 "containers":[{"name":"unifi-db","addresses":["172.31.254.2"]}]}
```

- `peers[].allowedIps` are CIDR prefixes (a bare address means a single host). An address inside a peer's allowed IPs is that peer's Device, `wg:<publicKey>`.
- `listenPort` identifies the encrypted WireGuard transport: UDP between the router's WAN address on this port and a Remote Endpoint is the `wg-tunnels` pseudo-Device.
- `containers[].addresses` are the container's own addresses (bridge or macvlan). An address listed here is that container's Device, `ctr:<name>`, even when it sits on the LAN.

### Alias file

`aliases.json`, also in the data directory, gives Devices friendly names. Keys are a MAC address (any case) or a Device id; values are the name. A missing file means no aliases.

```json
{"3c:22:fb:1a:0d:a3":"Work laptop","wg:…":"Laptop (travel)","router":"Gateway"}
```

### Device naming

In priority order: the alias file; the DHCP hostname from Kea's lease file (`/config/dhcp/dhcp4-leases.csv`, last row per address wins, only state 0 counts); the WireGuard peer or container name; then the network-card manufacturer from the embedded IEEE OUI table plus the last two MAC octets (`"Apple 58:0b"`). A locally administered (randomised) MAC has no manufacturer and is named `"Private 58:0b"`; an unknown prefix gives `"Unknown 58:0b"`. The router is `"Router"` and the pseudo-Device is `"WireGuard tunnels"`.
