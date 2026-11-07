# The binary drops root itself and keeps only CAP_NET_ADMIN

VyOS runs containers as host root with no user namespace, and the dashboard mounts the host's `/proc`. As root, `/host/proc/1/root` reaches the whole host filesystem, including `config.boot` (spec story 67). So `cmd/dash` opens everything that needs root first (conntrack netlink sockets, the :80 listeners, the data directory's ownership, the Kea lease file's group), then `internal/privdrop` turns the process into uid/gid 65534 with only `CAP_NET_ADMIN`, no ambient capabilities and `no_new_privs`. It then checks that `/host/proc/1/root` can't be opened, and exits if it can.

`CAP_NET_ADMIN` has to stay. The first version dropped every capability, assuming the kernel checks netlink permissions only against whoever opened the socket. On the router every conntrack dump then failed with "operation not permitted": the kernel (`__netlink_ns_capable`) also checks the current task. A uid other than root with `CAP_NET_ADMIN` still can't open other users' `/proc/<pid>/root`, because that needs `CAP_SYS_PTRACE`.

## Considered Options

- **Start the container as a non-root user** (VyOS `uid`/`gid` options or podman `--user`): not tried. Doing the drop in the binary makes it hold however the container is started, and lets the binary verify the result and fail closed. Revisit if VyOS gains user-namespace support for containers.
- **Don't mount the host's `/proc`**: then there is no CPU, memory, pppd uptime or container stats without another privileged helper.
- **Run as root with a read-only root filesystem**: still reaches `/proc/1/root`, which is the threat.

## Consequences

- Capabilities, keepcaps and the bounding set are per-thread, so every change uses `syscall.AllThreadsSyscall`. That needs a binary built without cgo, which we already require.
- Sockets are opened once and never reopened. A broken conntrack socket means empty Top Talkers until a restart.
- None of this runs in Go tests (it needs root). After any change to `internal/privdrop` or the startup order in `cmd/dash`, verify on the router: `/proc/<pid>/status` shows CapEff, CapPrm and CapBnd of `0x1000` on every thread and NoNewPrivs 1, the log says `/host/proc/1/root is not reachable`, and conntrack dumps work (`docs/operations.md`, "Privileges").
