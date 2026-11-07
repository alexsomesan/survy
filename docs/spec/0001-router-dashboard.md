# Spec 0001: Router dashboard

Status: implemented (merged to `main` 2026-11-07; tickets in [docs/tickets](../tickets/README.md)). This spec records what was agreed before building and is not kept in sync with the code. Current sources of truth: [architecture](../architecture.md), [API contract](../api.md), [ADRs](../adr/README.md).

Where the implementation departs from this spec:
- **Byte counting** is turned on with `set system conntrack flow-accounting`, not a sysctl parameter: VyOS overwrites `nf_conntrack_acct` on firewall commits.
- **Container identity** comes from cgroup names (`vyos-container-<name>.service`) plus the commit hook's inventory, not from podman's state metadata. That resolves the cgroup-mapping risk in Further Notes.
- **Privilege drop** (story 67): the container starts as root and the binary drops to uid 65534, keeping only `CAP_NET_ADMIN` ([ADR 0005](../adr/0005-in-process-privilege-drop-keeping-net-admin.md)). The router's `/boot` is also mounted read-only, to measure the system disk.
- **Tests outside the seam:** the frontend helpers (`web/test`) and the deploy scripts (`deploy/test`) have automated tests, despite "Not tested automatically" below ([ADR 0003](../adr/0003-one-test-seam-at-the-http-api.md)).
- **UI:** the open mockup questions in Further Notes were settled by the shipped frontend (inline drill-down, containers beside Router Resources). The code in `web/src` is now the reference, not the mockup.

Glossary: [GLOSSARY.md](../../GLOSSARY.md) · Decisions: [ADR 0001](../adr/0001-bespoke-app-over-grafana-stack.md), [ADR 0002](../adr/0002-top-talkers-from-conntrack-counters.md) · Mockup: (a design mockup, not published)

## Problem Statement

My home router runs VyOS on a small box (Pentium 4417U, 2 cores / 4 threads, 7.9 GB RAM) behind a 1 Gbps PPPoE fibre line. I can't easily see what it's doing:
- how much of my line is in use right now
- which machines are using it
- how traffic has trended over days and months
- whether the router's own hardware (CPU, memory, disk, temperature) or the containers it already runs (UniFi, vymanager, airprint) are under strain

The VyOS CLI gives me point-in-time numbers, no History, and no per-Device view. Off-the-shelf options either inspect every packet (ntopng), which competes with routing on this CPU, or don't model what I care about (Grafana stacks): a 1-second Live View, History that coarsens as it ages, and a Top Talkers ranking by Device.

## Solution

A small dashboard that runs in one podman container on the router and is opened from a browser at `http://dash.home.arpa`, from the Trusted LAN only (home LAN plus WireGuard peers), with no login.

It shows:
- a 1-second **Live View** of WAN throughput and **Router Resources**
- a **Top Talkers** table ranking **Devices** by their **WAN Traffic** over a window I choose. A Device opens into its own History and its top **Remote Endpoints**.
- **History** of interfaces and Router Resources at three **Resolutions**: 10 seconds for 48 hours, 1 minute for 30 days, and 1 hour for 2 years

It does all of this within a hard limit of 10% of one CPU core and 512 MB of RAM, at the lowest scheduling priority, without inspecting packets, so routing is never affected.

## User Stories

### Access and deployment

1. As the router owner, I want to open the dashboard at `http://dash.home.arpa` from any browser on my home LAN, so that I don't need any client software.
2. As the router owner, I want to open the same dashboard while connected over WireGuard, so that I can check my home network while travelling.
3. As the router owner, I want the dashboard to be unreachable from the WAN and from any network other than the Trusted LAN, so that my household's traffic patterns aren't exposed.
4. As the router owner, I want no login, so that the dashboard opens instantly on any of my devices.
5. As the router owner, I want the dashboard to be view-only, so that nothing anyone can do in the page can change the router or its data.
6. As the router owner, I want every router configuration change the dashboard needs to be written down as reviewable VyOS `set` commands, so that I can apply them myself with `commit-confirm` and roll back if something goes wrong.
7. As the router owner, I want to deploy a new version with one command from my laptop, so that updating the dashboard is effortless.
8. As the router owner, I want the dashboard container to start automatically after a reboot or a VyOS image upgrade, so that I never have to think about it.
9. As the router owner, I want the dashboard's data on its own partition, so that it can't fill the system partition or the container image storage.
10. As the router owner, I want the data partition to still be mounted after a VyOS image upgrade, so that History survives upgrades.

### Live View

11. As the router owner, I want a chart of WAN download and upload over the last 60 seconds that updates every second, so that I can see what my line is doing right now.
12. As the router owner, I want the 1 Gbps line rate marked on the live chart, so that I can see at a glance how close I am to saturating the line.
13. As the router owner, I want the current download and upload rates shown as large numbers, with the percentage of line rate in use, so that I get the answer without reading a chart.
14. As the router owner, I want current in and out rates for the WAN, LAN and WireGuard interfaces, so that I can tell where traffic is flowing.
15. As the router owner, I want today's total download and upload volume, so that I have a sense of daily usage.
16. As the router owner, I want the Live View to keep updating while the page is open, without reloading, so that I can leave it on a screen.
17. As the router owner, I want the Live View to resume automatically after a network blip or a container restart, so that a long-open tab doesn't go stale.

### Router Resources

18. As the router owner, I want current CPU usage with a short sparkline, so that I can see whether routing load is rising.
19. As the router owner, I want memory used and available, so that I know whether the containers are crowding the router.
20. As the router owner, I want CPU temperature, so that I can spot cooling problems in the fanless box.
21. As the router owner, I want the number of tracked connections against the conntrack limit, so that I can spot connection floods or table exhaustion.
22. As the router owner, I want the system partition and the dashboard data partition shown as used against capacity, so that I notice before either fills.
23. As the router owner, I want load averages (1, 5 and 15 minutes), so that I have the familiar Unix overview.
24. As the router owner, I want CPU and memory for each container, with memory against its configured limit, so that I can see which of UniFi, MongoDB, vymanager, airprint or the dashboard itself is using the router's resources.
25. As the router owner, I want the dashboard to show its own CPU, memory and data size, so that I can confirm it stays within its limits.
26. As the router owner, I want WAN uptime and the current public IP in the header, so that I can see reconnects and address changes at a glance.

### Top Talkers

27. As the router owner, I want a table of Devices ranked by WAN Traffic, highest first, so that I know who is using my internet connection.
28. As the router owner, I want to choose the ranking window (Live, 1 h, 24 h, 7 d, 30 d), so that I can tell a momentary spike from habitual heavy use.
29. As the router owner, I want the Live window to show current rates, and the other windows to show volumes (bytes received and sent), so that each window answers its natural question.
30. As the router owner, I want received and sent shown separately, plus the total and a proportional bar, so that I can tell downloaders from uploaders.
31. As the router owner, I want each Device named by its DHCP hostname, so that I recognise it.
32. As the router owner, I want a Device without a DHCP hostname named by its network-card manufacturer plus the end of its MAC (for example "Apple 7d:2f"), so that it is still recognisable.
33. As the router owner, I want to give Devices friendly names in an alias file, so that I can name unnamed or badly named Devices without the UI being editable.
34. As the router owner, I want all of a Device's IPv4 and IPv6 addresses, including rotating privacy addresses, counted as one Device, so that one laptop doesn't appear as five rows.
35. As the router owner, I want each WireGuard peer counted as a Device named by its peer name, so that remote use of my connection is attributed to the right peer.
36. As the router owner, I want peer names kept current automatically when I change the WireGuard config, so that a new peer appears by name without redeploying.
37. As the router owner, I want the router itself counted as a Device, so that its own DNS, NTP and update traffic is visible.
38. As the router owner, I want each container on the router counted as a Device, so that traffic from the UniFi controller and others is visible.
39. As the router owner, I want the encrypted WireGuard transport shown as one pseudo-Device, "WireGuard tunnels", so that the WAN bandwidth used by remote access is visible without being counted twice against the peers.
40. As the router owner, I want only WAN Traffic counted, never traffic between two Devices, so that the ranking reflects use of my internet connection.
41. As the router owner, I want an "Unattributed" row showing the WAN total minus the sum of all Devices, so that the table always adds up and I can see how much traffic couldn't be attributed.
42. As the router owner, I want a summary of the window's WAN total, the number of Devices and the Unattributed share above the table, so that I get the headline first.
43. As the router owner, I want the Live window to refresh every second while I'm watching, so that the ranking follows what's happening.

### Device drill-down

44. As the router owner, I want to click a Device in Top Talkers to see its details, so that I can find out why it's using so much traffic.
45. As the router owner, I want the drill-down to show the Device's identifiers (MAC, IPv4, number of IPv6 addresses, or its peer or container name), so that I know exactly which machine it is.
46. As the router owner, I want a chart of the Device's received and sent WAN Traffic over the selected window, so that I can see when it was active.
47. As the router owner, I want the Device's top Remote Endpoints over the selected window, so that I can see which services it talks to.
48. As the router owner, I want each Remote Endpoint shown with its IP address, reverse-DNS name and owning organisation, so that "192.0.2.140" reads as "Netflix".
49. As the router owner, I want to close the drill-down and go back to the table, so that I can look at another Device.

### History

50. As the router owner, I want WAN throughput History with a range selector (1 h, 24 h, 7 d, 30 d, 1 y), so that I can see trends over different time scales.
51. As the router owner, I want CPU, memory and tracked-connections History over the same range, so that I can correlate load with traffic.
52. As the router owner, I want to be told which Resolution I'm looking at, so that I read short spikes correctly when samples are coarse.
53. As the router owner, I want History kept for 2 years, so that I can compare this year with last year.
54. As the router owner, I want per-Device History at the 1-minute and 1-hour Resolutions, so that I can chart one Device over the past month or year.
55. As the router owner, I want History to survive dashboard restarts, router reboots and VyOS upgrades, so that I don't lose trends.
56. As the router owner, I want a guard cap on storage that trims the oldest data first, so that a bug can never fill the partition.

### Charts and interaction

57. As the router owner, I want a crosshair and tooltip with exact values and time when I hover any chart, so that I can read precise numbers.
58. As the router owner, I want download and upload in consistent colours on every chart and bar, so that I learn the encoding once.
59. As the router owner, I want the dashboard to follow my system's dark or light setting, so that it fits the rest of my screen.
60. As the router owner, I want a dense layout that uses a wide screen well, so that everything fits on one monitor.
61. As the router owner, I want the same page to work on my phone, with sections stacked, less important columns hidden and touch-sized controls, so that I can check it from the couch.
62. As the router owner, I want readable text contrast and keyboard-reachable controls in both themes, so that the dashboard is comfortable to use.

### Impact on the router

63. As the router owner, I want the dashboard capped at 10% of one CPU core and 512 MB of RAM, so that it can never starve routing.
64. As the router owner, I want it to run at the lowest CPU and I/O priority, so that routing and the other containers always win.
65. As the router owner, I want no packet inspection, so that the cost doesn't grow with traffic volume.
66. As the router owner, I want expensive 1-second collection to run only while someone is watching, so that the dashboard costs almost nothing when nobody is looking.
67. As the router owner, I want the dashboard never to hold credentials for the router or read its full configuration, so that a compromised dashboard can't take over the router.

## Implementation Decisions

### Shape (see ADR 0001)

- One Go binary in one container. It collects data, stores History in embedded SQLite (a pure-Go driver, no cgo) and serves the web UI from files compiled into the binary.
- The frontend is plain TypeScript with uPlot for charts and a small hand-written stylesheet, built ahead of time. There's no frontend framework, no CSS framework and no Node at runtime.
- The image is built on the developer's laptop, copied over SSH and loaded with `podman load`, then run as a local image (like airprint). One make target does build, copy and load.

### Router integration (applied by the owner, written as reviewable `set` commands and scripts)

- Turn on per-connection byte counting (`net.netfilter.nf_conntrack_acct = 1`) as a VyOS sysctl parameter. Top Talkers depends on the existing `counter` flag on flowtable FT01 (ADR 0002). Removing it silently undercounts offloaded traffic.
- Add a secondary address, 192.168.2.4/24, on `br0`, and a static host mapping `dash.home.arpa` → 192.168.2.4.
- The container runs on host networking and listens on port 80 at 192.168.2.4 and at 10.0.0.1 (`wg0`) only.
- Firewall input rules allow TCP 80 to those addresses from `br0` and `wg0` only.
- Container limits: 10% of one core, 512 MB of memory, lowest CPU and I/O priority. Capabilities limited to what reading conntrack and neighbour tables needs (`CAP_NET_ADMIN`). Read-only mounts for the DHCP leases directory and cgroup statistics. Read-write only for the data volume.
- **Data partition:** a new 10 GiB GPT partition (p5, ext4, label `vyos-dash-data`) created in the 133 GiB of free space after p4. It is mounted at `/mnt/dash-data` (noatime) by the VyOS pre-config boot script, which lives under `/config` so the mount survives image upgrades and happens before containers start. The partitioning script only creates p5 and refuses to touch p1–p4.
- **Router inventory without secrets:** a VyOS commit post-hook regenerates a small inventory file on the data partition after every commit. It contains WireGuard peer names with their public keys and allowed IPs, and container names with their addresses. The dashboard never receives an API key and never sees `config.boot`, since a VyOS API key has full admin rights.

### Domain modules (deep modules behind the single test seam)

- **Device identification:** turns an address into a Device.
  - LAN addresses map to MAC addresses via the neighbour tables. IPv4 and IPv6 addresses that share a MAC are merged.
  - Recently seen address-to-MAC pairs are cached, so an IPv6 privacy address that has dropped out of the neighbour table still resolves.
  - WireGuard addresses map to the peer via the inventory's allowed IPs.
  - Container network addresses map to the container via the inventory.
  - The router's own WAN address maps to the router Device.
  - Traffic between the router's WAN address and remote addresses on the WireGuard listen port maps to the "WireGuard tunnels" pseudo-Device.
  - An address that can't be resolved is counted under Unattributed.
- **Device naming:** in priority order: the alias file, the DHCP hostname from the Kea leases, the peer or container name, then the network-card manufacturer (from an embedded OUI table) plus the last two octets of the MAC.
- **WAN Traffic accounting:**
  - Each poll, the module reads conntrack entries and computes byte deltas per entry against its previous reading.
  - It also listens for conntrack destroy events, to capture final counters of connections that closed between polls.
  - Only entries with exactly one side outside the local prefixes count as WAN Traffic. Local prefixes are the LAN v4/v6, WireGuard, container networks, link-local and the router's own non-WAN addresses.
  - Deltas are attributed to the local side's Device as received and sent, and to the remote side as a Remote Endpoint.
  - Unattributed for any period is the WAN interface byte delta minus the sum of attributed Device deltas, floored at zero.
- **Sampling cadence:**
  - Interface counters and Router Resources are read every second, always. They are cheap, and the 10-second Resolution is built from them.
  - Conntrack is polled every second while at least one Live stream client is connected, otherwise every 10 seconds.
  - Destroy events are always listened to.
- **Live state:** held in memory, holding the last 60 one-second samples, and never stored.
- **History store and rollups:**

  | Series | Resolutions kept |
  |---|---|
  | Interfaces and Router Resources | 10 s for 48 h, 1 min for 30 d, 1 h for 2 y |
  | Per-Device WAN Traffic | 1 min for 30 d, 1 h for 2 y. Rows are written only for Devices with non-zero traffic in that slot. |
  | Per-Device Remote Endpoints | 1 h for 30 d, top 50 per Device per hour |

  - Coarser Resolutions are built from finer ones by a rollup that sums byte counters and averages gauges.
  - Expiry deletes rows past their retention.
  - A guard cap of 8 GB trims the oldest rows first if it's ever reached.
- **Top Talkers query:** for a window, the module sums each Device's received and sent bytes from the finest Resolution that covers the window. Live sums the current one-second rates. It sorts by total descending, and the Unattributed row is computed for the same window.
- **Remote Endpoint labelling:** reverse DNS is looked up through the router's DNS forwarder (the router's own address on the LAN) and cached with a time-to-live. Owning organisation comes from an offline IP-to-ASN/organisation database file on the data partition, refreshed monthly. Its licence attribution is shown in the footer.
- **Container stats:** container CPU and memory come from cgroup v2 statistics, with configured memory limits. Container identity comes from podman's read-only state metadata. The dashboard never runs podman.

### API contract (JSON over HTTP, all read-only)

- A Live stream (Server-Sent Events). It emits one event per second with:
  - current interface rates (`pppoe0`, `br0`, `wg0`)
  - Router Resources (CPU, memory, temperature, tracked connections and limit, disks, load, WAN uptime and IP)
  - container stats
  - the Live Top Talkers with Unattributed

  Connecting a client switches conntrack to 1-second polling; when the last client disconnects, polling drops back to 10 seconds.
- **Top Talkers** for a window (`1h`, `24h`, `7d`, `30d`): Device rows (id, name, kind, received, sent, total), the Unattributed row, the WAN total and the Device count.
- **Device detail** for a Device id and window: identifiers, a received/sent time series for the window at the appropriate Resolution, and the top Remote Endpoints (IP, reverse-DNS name, organisation, received, sent).
- **History** for a range (`1h`, `24h`, `7d`, `30d`, `1y`): time series for WAN download and upload, CPU, memory and tracked connections. Each response names the Resolution it was served from.
- **Device ids** are stable strings, prefixed by kind: LAN Devices by MAC, WireGuard peers by public key, containers by name, plus fixed ids for the router and for "WireGuard tunnels".

### UI (from the mockup; still open to revision)

The page is a single scrolling page. Top to bottom:
1. **Header:** host name, Live indicator, WAN uptime, public IP, clock.
2. The live WAN chart, next to a "Right now" panel (large rates, line-rate meter, per-interface rates, today's totals).
3. Router Resources tiles with sparklines, next to the containers table.
4. Top Talkers with the window selector. The drill-down opens inline below the table.
5. History, with a range selector and a Resolution note: one large WAN chart and three small charts.
6. **Footer:** dashboard self-usage, data size, the Resolutions, attributions.

Download and upload are always categorical slots 1 and 2 (blue and orange, each with a separate dark-mode value). The theme follows `prefers-color-scheme` with no toggle. The layout reflows at about 1000 px and 640 px: columns are hidden on phones, and touch targets are at least 44 px.

## Testing Decisions

- **One seam:** tests drive the whole app through its public HTTP API (JSON endpoints and the Live stream) and assert only on what the API returns. Nothing below the API is tested directly, and no test reaches into domain modules, SQLite tables or internal structs.
- **Inputs at the seam:** the app is built with three injected dependencies:
  1. **A root directory.** All file-based router inputs are read relative to it: `/proc` stat/meminfo/loadavg, `/sys/class/net` counters, hwmon temperature, cgroup statistics, the Kea DHCP lease CSV, the inventory file, the alias file and the IP-to-organisation database. Tests point it at a fixture tree copied from the real router.
  2. **A kernel-tables source:** conntrack snapshots, conntrack destroy events and neighbour tables, the only inputs that come from netlink rather than files. Tests use a fake fed with entries recorded from the real router.
  3. **A clock**, which tests advance by hand to trigger 1-second and 10-second polls, rollups and expiry.

  Reverse DNS is also an injected resolver, faked in tests. SQLite runs for real in a temporary directory.
- **Coverage through the seam:**
  - file parsing (each fixture format)
  - Device identification and merging (IPv4 plus several rotating IPv6 addresses resolving to one MAC; cached privacy addresses)
  - naming priority (alias, then DHCP hostname, then peer or container name, then manufacturer)
  - WAN-only filtering (LAN↔WireGuard traffic excluded)
  - router, container and "WireGuard tunnels" attribution
  - counter deltas across polls and destroy events
  - Unattributed arithmetic
  - Top Talkers ordering per window
  - each Resolution's rollup and retention boundary
  - the guard cap trimming oldest-first
  - per-Device Remote Endpoint top-50 truncation
  - Live-stream-driven switching between 1-second and 10-second polling
  - restart survival (re-open the same data directory)
- **What makes a good test here:** it states a scenario in domain terms ("work-laptop downloads 3 GB from a Remote Endpoint over 70 seconds, partly over IPv6") and checks the observable API result. It is not tied to table layouts, polling internals or function names. Fixtures are real data from the router, trimmed and anonymised where needed.
- **Approach:** test-first (red-green-refactor) on the domain behaviour.
- **Not tested automatically:**
  - the netlink decoding inside the conntrack and neighbour sources (a thin layer over a mature library, checked on the router during deploy)
  - the frontend (compared by eye with the mockup)
  - the VyOS commit hook, the boot-mount script and the partition script (reviewed by the owner and applied with `commit-confirm`)
- **Prior art:** none at the time of writing. Now see `internal/app/*_test.go`.

## Out of Scope

- Alerting or notifications of any kind.
- Authentication, user accounts, HTTPS, and access from outside the Trusted LAN.
- Any write action from the UI: renaming Devices, changing router config, blocking Devices.
- Packet capture, deep packet inspection, application or protocol classification, and NetFlow/sFlow/IPFIX export.
- Traffic between two Devices, including LAN↔WireGuard traffic and traffic switched inside the LAN that never reaches the router.
- Backups of History.
- A Grafana, Prometheus or other external metrics integration.
- Multiple routers, and running the dashboard anywhere other than the router itself.
- Remote Endpoint History beyond 30 days.
- A manual theme toggle.

## Further Notes

- **Facts measured on the router (2026-11-07):**
  - VyOS rolling 2026.09.28, kernel 6.18, podman 5.8
  - about 810 tracked connections against a 262,144 limit; about 57 IPv4 and 60 IPv6 neighbours on `br0`
  - WAN on `pppoe0` over `eth0.7`; LAN is a single bridge `br0` (192.168.2.0/24 plus an IPv6 /64)
  - WireGuard `wg0` on 10.0.0.0/24 with 4 peers
  - container networks 172.31.254.0/24 (UniFi) and 172.31.255.0/24 (vymanager); airprint on a macvlan at 192.168.2.2
  - DHCPv4 by Kea, with leases at `/config/dhcp/dhcp4-leases.csv`
  - about 250 GB of WAN download over 4 days of uptime
- Per-connection byte counting was off at the time of measurement. Connections opened before it's enabled never get counters, so expect Top Talkers to under-report briefly after enabling, and Unattributed to be high at first.
- **Risk:** mapping cgroups to container names without the podman socket depends on podman's on-disk state layout. If that proves fragile, the commit hook's inventory can be extended instead.
- **Risk:** temperature sensor paths vary by board. Check which hwmon device carries package temperature on this box during the first deploy.
- **Licensing:** the IP-to-organisation database and the OUI table must have licences that allow redistribution inside the image or download on the router. Record the attribution in the footer.
- The mockup is still being iterated. Open questions: the hero chart choice, the number of Top Talker rows, inline drill-down vs. a side panel, and where the container table goes. Update the UI section of this spec when they're settled.
