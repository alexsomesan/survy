# 11 Review fixes
Status: closed
Blocked by: 01–09

Fixes from the two-axis code review of integration/spec-0001, and from the first real-UI check on the router.

## 1. Security (story 67): a compromised dashboard must not reach the host filesystem
The container runs as host root (no user namespace) with /proc mounted, so `/host/proc/1/root/…` probably reaches `/config/config.boot`. Fix it in the binary, independent of podman options:
- In cmd/dash, while still root:
  - open every privileged resource: the conntrack netlink sockets (dump and destroy listener) and the HTTP listeners (port 80)
  - chown the data dir tree to the target uid
  - stat the Kea lease file to learn its gid (it's 0640, gid 102 on the router)
- Then `setgroups([leaseGid])`, `setgid(65534)`, `setuid(65534)` and `prctl(PR_SET_NO_NEW_PRIVS)`.
- The kernel checks netlink CAP_NET_ADMIN against the socket's opening credentials (`file_ns_capable` on f_cred), so sockets opened before the drop keep working. **Wrong, found on the router:** the request is also checked against the current task, so CAP_NET_ADMIN must be kept across the uid change (commit f8c66ea, ADR 0005).
- Fail closed: if the process started as root and is still uid 0 after the drop, exit with an error.
- Make the target uid/gid configurable (DASH_UID/DASH_GID, default 65534), and allow DASH_DROP_PRIVS=0 for local development.
- The adapters must not reopen conntrack sockets after the drop. Reuse them; if the dump socket fails, log it and keep reporting errors rather than redialing.
- Update deploy/vyos.commands and docs/operations.md: why /host/proc is safe after the drop, and which capabilities are needed (net-admin plus podman's defaults: setuid/setgid/chown for the drop, net-bind-service for :80).
- Add a self-check at startup, after the drop: try to open /host/proc/1/root (Root + "/proc/1/root"). Log an error and exit if it succeeds. The router smoke run verifies this.

## 2. Concurrency
- handleTopTalkers and handleDevice hold devHist.mu across SQLite queries and writeJSON, so a slow client stalls tickSecond. Copy what's needed under the lock, then query and write without it, or use a read-only snapshot. Store reads must not block the per-second writer path.
- Shutdown: cmd/dash must `srv.Shutdown` first, then `app.Close`. The real Clock's stop must wait for any in-flight fn before returning, so Close never closes the store under a running tick.
- asnFile.check: an os.Open failure must not record present/modTime/size, so it retries next check.
- rdns.start: set ctx/cancel under the lock (or before publishing).

## 3. Spec fidelity
- Top Talkers and Device detail for 7d and 30d must use the 1-minute Resolution, the finest that covers the window (1-min data is kept 30 d). Windows cover exactly the span (from = to − span), not n hourly slots. The Device detail series may downsample to about 1,500 points by summing.
- Container memory limit: when the service cgroup's memory.max is "max", read memory.max from the `libpod-payload-*` child (that's where podman puts the limit; on the router unifi-db's payload says 2147483648).
- System disk: statfs a path on the router's system partition. Mount host `/boot` read-only at `/host/boot` (it's on p3, the persistence partition, and holds only kernels) and statfs Root+"/boot". The data disk keeps statfs(DataDir). Update docs/api.md: `disks[].path` is the router-side path (`/` for system, `/mnt/dash-data` for data, taken from a new DASH_DATA_HOST_PATH env with that default).
- Live Top Talkers right after a client connects: poll conntrack on the next 1-second tick after a client connects, so the first rates aren't 10-second idle averages.

## 4. Frontend
- Live WAN chart: autoscale the y-axis to the data (minimum about 1 Mb/s), and show the dashed 1 Gbps line only when the axis reaches it, or as a label "1 Gbps line rate" at the top when not. Real traffic in kb/s must be visible.

## 5. Cheap smells (only where trivial)
- One shared window/Resolution table for history.go and devicehistory.go.
- One bits-per-second helper.
- Named constants for the interface roles, the units ("bps"/"bytes") and the "live" window.
- Rename `tUnRx`, `nTalkers`, `cCtN`.
- Remove `liveHub.last` if it's unused.
- Persist `Device` directly instead of the duplicated `deviceMeta`, if that's easy.

Testing: TDD through the HTTP seam for 2–3. Item 1 is verified on the router in ticket 10, not by seam tests.
