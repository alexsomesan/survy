# 02 Live interfaces and Router Resources
Status: closed
Blocked by: 01

Spec user stories 11–26 (except 15's daily total, which is stored data: do it in 04), 63–66. API contract › Live stream.

- Every 1 s: read interface counters for pppoe0, br0 and wg0, and compute rates (bits/s) from deltas.
- Router Resources: CPU % from /proc/stat deltas; memory from /proc/meminfo; package temperature from the hwmon device named coretemp; tracked connections (count and max); load averages; disks via Kernel statfs (system "/" and the data dir); WAN uptime from the pppd pid file and /proc/<pid>/stat start time; WAN IP from Kernel addresses on pppoe0.
- Containers: CPU % and memory against the limit from the cgroup v2 dirs `system.slice/vyos-container-<name>.service`. The name comes from the dir.
- The Live SSE event carries all of the above plus the last 60 one-second WAN samples on connect (backfill). It emits once per second.
- Track connected Live clients (ticket 03 uses this to switch polling cadence).
