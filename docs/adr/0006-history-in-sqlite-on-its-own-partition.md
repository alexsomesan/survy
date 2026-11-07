# History lives in SQLite on a dedicated partition, as sum-only families

**History** is one SQLite database (`modernc.org/sqlite`, pure Go, WAL, a single writer) in `/mnt/dash-data`, a 10 GiB ext4 partition (p5) created for it in the router's free disk space. It is mounted by the VyOS pre-config boot script, so the mount survives VyOS image upgrades and happens before containers start.

Every stored series is a **family** (`internal/store`): one table per **Resolution**, where every value is a sum. Byte counters are stored as deltas and gauges as a (sum, count) pair, so every rollup is the same `GROUP BY slot, SUM(...)` query, and averages stay exact through any number of rollups. Retention and a guard cap (8 GB by default, oldest rows first) apply to every table without per-family code.

## Considered Options

- **The persistence partition (p3, 5 GB)**: shared with VyOS images and config; filling it can break upgrades or boot.
- **The container storage partition (p4)**: tied to podman's lifecycle; a `podman system reset` would wipe History.
- **A time-series database or round-robin files**: another process or a cgo dependency, and per-Device Remote Endpoint rows fit them badly (ADR 0001).

## Consequences

- To store a new series, declare a family (keys, values, Resolutions) and write rows at its finest Resolution. Never add hand-written tables or a rollup of your own. Values must be summable, so store a gauge as (sum, count).
- Device ids are stored keys. Changing their format (`docs/api.md`) orphans existing History.
- There are no backups (owner's decision). Losing p5 loses History; the dashboard starts empty and carries on.
- `deploy/partition-p5.sh` only ever creates p5 and refuses to touch p1–p4.
