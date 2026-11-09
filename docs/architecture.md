# Architecture

How the dashboard fits together, where each kind of change goes, and the rules the code relies on. Terms come from [GLOSSARY.md](../GLOSSARY.md). The reasons behind the big choices are in [adr/](adr/README.md). The wire format is in [api.md](api.md).

## Names

The app is **survy**: the Go module, the binary and process (`survy`), the commands in `cmd/` and the UI all use that name. Its first deployment predates the name, so everything on the router keeps the original `dash` name, by the owner's choice (renaming them needs a migration on the router):
- the container `dash`, the image `localhost/dash`, the `DASH_*` environment variables
- the firewall group `DASH-ADDRS`, the rule descriptions, and the task `dash-fetch-asn`
- the commit hook `dash-inventory`, `/config/scripts/dash/`, the partition label `vyos-dash-data` and the mount `/mnt/dash-data`

The page answers on `survy.home.arpa`, with `dash.home.arpa` as an alias. Keep the router-side names unless the owner asks for that migration.

## Shape

survy is one static Go binary (`cmd/survy`) in a podman container on the router, on host networking. The image is busybox plus the binary, run at `nice 19` and idle I/O priority.

```
 router files, read-only            netlink (CAP_NET_ADMIN)            data partition /mnt/dash-data
 /host/proc /host/sys               conntrack dump + destroy events    inventory.json  (commit hook)
 /host/config/dhcp /host/boot       neighbours, addresses              aliases.json    (owner)
        │                                   │                          ip2asn …tsv.gz  (monthly fetch)
        │  procFS (Root)                    │  ports.Kernel                    │  watched files
        └───────────────┬───────────────────┴──────────────────────────────────┘
                        ▼
            internal/app  ── ticks every 1 s on ports.Clock ──►  internal/store (SQLite History)
                        │
                        ▼
            HTTP: /api/live (SSE), /api/top-talkers, /api/devices/{id}, /api/history, static web/
                        │
                        ▼
            web/ (TypeScript + uPlot, embedded in the binary)
```

## Lifecycle (`cmd/survy`)

1. **As root:** open the conntrack sockets (`adapters.Kernel.Open`, not part of `ports.Kernel`) and the :80 and (when `DASH_TLS_LISTEN` is set) :443 listeners, chown the data directory, and read the Kea lease file's group.
2. **Drop privileges** (`internal/privdrop`, [ADR 0005](adr/0005-in-process-privilege-drop-keeping-net-admin.md)): become uid 65534 keeping only `CAP_NET_ADMIN`, then self-check that `/host/proc/1/root` is unreachable. Any failure exits.
3. **`app.New`, then `Start`:** take a baseline reading, open History, start the labeller, and schedule `tickSecond` (1 s), `hist.maintain` (hourly expiry and guard cap) and the ASN file check.
4. **Shutdown:** stop the HTTP server first, then `app.Close`. `Close` stops the ticks (the real clock waits for an in-flight tick), flushes Device History and closes the store. Keep this order: the reverse closes the store under a running request.

## The per-second tick (`internal/app/app.go`, `tickSecond`)

In order:
1. `res.collect`: interface counters, CPU, memory, temperature, load, disks, containers, WAN info.
2. `hist.record`: the 10 s Resolution of the interfaces and Router Resources family.
3. `traffic.tick`: polls conntrack every second while a Live client is connected, otherwise every 10 s. It also drains destroy events, computes per-flow byte deltas, keeps only **WAN Traffic**, and attributes it to Devices and Remote Endpoints.
4. `devHist.tick`: per-Device and per-Remote-Endpoint minute rows.
5. `live.broadcast(buildLive)`: one `LiveEvent` to every SSE client.

The Live View is the last 60 one-second samples, held in memory only.

## Module map

| Where | What it owns | Change it when |
|---|---|---|
| `internal/app/app.go` | `Config`, `Settings`, lifecycle, `tickSecond` | adding a collector or a schedule |
| `internal/app/http.go` | routes and handlers | adding or changing an endpoint (with `docs/api.md` and `web/src/api.ts`) |
| `internal/app/live.go` | `LiveEvent`, `buildLive`, the SSE hub | adding a Live field |
| `internal/app/resources.go`, `procfs.go` | Router Resources, interfaces, containers, WAN uptime; all `/proc` and `/sys` parsing | reading a new router file |
| `internal/app/devices.go` | Device identification (address → MAC, peer, container, router, `wg-tunnels`), naming, kinds and ids | changing how traffic maps to a Device or how it is named |
| `internal/app/inventory.go` | `inventory.json` and `aliases.json`, re-read on change | the inventory format changes (with the commit hook) |
| `internal/app/traffic.go` | conntrack polling cadence, per-flow deltas, WAN-only filter, Unattributed | accounting rules |
| `internal/app/toptalkers.go` | the Top Talkers and Device objects, Live Top Talkers | ranking or the row shape |
| `internal/app/windows.go` | the table of windows and ranges, and which Resolution serves each | adding a window or range |
| `internal/app/history.go` | the interfaces and Router Resources family; `/api/history` | adding a History chart series |
| `internal/app/devicehistory.go` | Device, Remote Endpoint and talkers families; windowed Top Talkers and Device detail | per-Device History |
| `internal/app/labels.go`, `rdns.go` | Remote Endpoint host, org and ASN; background reverse DNS | labelling |
| `internal/store` | SQLite families, rollups, retention, guard cap, records ([ADR 0006](adr/0006-history-in-sqlite-on-its-own-partition.md)) | storage mechanics only; app code declares families |
| `internal/asndb` | ip2asn file → AS number and organisation | ASN source format |
| `internal/oui` | embedded IEEE MA-L table (regenerate with `gen/`) | refreshing manufacturer names |
| `internal/ports` | the interfaces to the outside world: `Kernel`, `Clock`, `Resolver` | a new non-file input |
| `internal/adapters` | real `Kernel` (netlink via ti-mo/conntrack and vishvananda/netlink), `Clock`, `Resolver` | kernel or DNS plumbing; check with `cmd/dash-probe` on the router |
| `internal/tlscert` | the HTTPS certificate: two PEM files re-read when they change, last good pair kept on a bad write | the certificate source or reload rules change |
| `internal/privdrop` | the privilege drop | only with a router re-check (ADR 0005) |
| `internal/testkit` | the test seam: harness, fakes, fixtures recorded from the router | a test needs a new knob |
| `cmd/survy-probe` | prints one reading of each real adapter as JSON | smoke-checking adapters on the router |
| `web/src` | the single-page UI: `main.ts` (DOM), `chart.ts` (uPlot wrapper), `stream.ts` (SSE with reconnect), `window.ts` (1 s ring), `format.ts`, `device.ts`, `api.ts` (types), `app.css` | UI work; run `npm run dev` for the mock API in `web/dev/server.mjs` |
| `deploy/` | VyOS `set` commands, the partition, boot-mount, commit-hook and ASN-fetch scripts, and their tests | router integration ([operations.md](operations.md)) |
| `tools/mkimage` | builds the image without a container daemon (separate `go.mod`) | image layout (keep it in step with `Dockerfile`) |

## Invariants

- **One test seam** ([ADR 0003](adr/0003-one-test-seam-at-the-http-api.md)). New behaviour starts as a failing test in `internal/app/*_test.go` (package `app_test`), driven through `testkit.New(t)`:
  - read responses with `h.GetJSON` and `h.OpenLive()`
  - move time with `h.Advance(d)`
  - change inputs with `h.WriteFile("/proc/stat", …)`, `h.Kernel.SetFlow(…)` and `h.WriteData("inventory.json", …)`

  The recorded conntrack fixture has zero byte counters, so synthesise flows with bytes.
- **Inputs:** files are read through `Config.Root`, so tests can point it at a fixture tree. Anything that isn't a file goes through `internal/ports`. Time comes only from `ports.Clock`; never call `time.Now()` in `internal/app`.
- **Locks:** never hold a module's mutex across a store query, DNS, file I/O or an HTTP write. Copy what you need under the lock and work outside it. Otherwise one slow client stalls the per-second tick (`concurrency_test.go` guards this).
- **API:** every documented key is always present, and an unknown value is `null`. The units and directions in `docs/api.md` are fixed. The frontend relies on both, so change the backend, `docs/api.md` and `web/src/api.ts` together.
- **Display units:** traffic (rates and volumes) is decimal: Mb/s, GB. Memory and disk sizes are binary: MiB, GiB, matching `free -h` and `df -h`. Use `rateStr`/`bytesStr` for traffic and `binStr` for memory and disk in `web/src/format.ts`.
- **Storage:** declare a family instead of writing tables. Every stored value must be summable (ADR 0006). Device ids are stored keys, so treat their format as permanent.
- **Accounting:** only WAN Traffic is attributed. LAN↔LAN and LAN↔WireGuard traffic are never counted. Unattributed is the WAN interface delta minus attributed traffic, floored at zero.
- **No cgo.** The binary is static on busybox, and the privilege drop depends on `AllThreadsSyscall`.
- **Budget:** at most 10% of one core and 512 MB (container limits). The steady state measured on the router was about 0.5% CPU and 63 MB. Anything that scales with connections or Devices must stay bounded: for example, Remote Endpoints are the top 50 per Device per hour, and reverse lookups happen only for endpoints a Device detail shows.

## Router facts the code depends on

Measured on the router on 2026-11-07 (VyOS rolling 2026.09.28, kernel 6.18, podman 5.8). If any of these changes, the code needs to change with it.

- **Interfaces:**
  - WAN is `pppoe0` (over `eth0.7`).
  - LAN is the bridge `br0`: 192.168.2.0/24 plus one IPv6 /64.
  - WireGuard is `wg0` on 10.0.0.0/24.
  - Container networks are 172.31.254.0/24 and 172.31.255.0/24; airprint is on a macvlan at 192.168.2.2.
- **WAN uptime:** `/run` is not mounted in the container (it would expose the podman socket), so it comes from finding the `pppd call pppoe0` process in `/proc` rather than from a pid file.
- **Containers:** each container's cgroup is `system.slice/vyos-container-<name>.service`. Podman puts the memory limit on its `libpod-payload-*` child, not on the service cgroup.
- **Temperature:** read from the hwmon device named `coretemp`, using the "Package id" sensor. hwmon numbering isn't stable, so it is found by name.
- **System disk:** the router's `/boot` is on the persistence partition (p3), so it is measured through the read-only `/host/boot` mount.
- **DHCP:** Kea leases are `/config/dhcp/dhcp4-leases.csv`, mode 0640, gid 102. That group is the only supplementary group kept after the privilege drop.
- **DNS:** reverse lookups go to the router's forwarder (pdns_recursor) at 192.168.2.1.
- **Conntrack counters:**
  - `nf_conntrack_acct` must be 1; until it is, Top Talkers are empty (ADR 0002). Turn it on with `set system conntrack flow-accounting`. VyOS's conntrack script owns this sysctl and overwrites a `system sysctl` setting on every firewall commit.
  - Flowtable FT01 must keep its `counter` flag, or offloaded traffic goes uncounted.

## Build and deploy

- **Tests:**
  - `make test`: Go vet and tests, the deploy script tests and shellcheck.
  - `cd web && npm test`: the frontend helper tests. These are not part of `make test`.
- **Image:** `make image` produces `dist/survy.tar`, tagged `localhost/dash:<sha>` and `:latest`. It uses docker if a daemon is available, otherwise `tools/mkimage`.
- **Router:** `make load` / `make deploy` copy the image over SSH into `podman load`. Each step, and everything else that touches the router, is in [operations.md](operations.md).

## Where the decisions are

- **Why:** the [ADRs](adr/README.md).
- **What was asked for:** [spec 0001](spec/0001-router-dashboard.md): problem, 67 user stories, and implementation and testing decisions as agreed before building.
- **How it was built:** [tickets](tickets/README.md), one per slice, each listing its acceptance criteria. Ticket 11 lists the review fixes; ticket 10 holds the router verification results and what remains unverified.
- **Where the code differs from the spec:** the spec's Status note.
