# 10 End-to-end verification
Status: closed
Blocked by: 02–09

- The full test suite passes, and `go vet` passes.
- The image builds and the container runs locally against the fixture root, with the API returning sane data and the UI rendering in a browser (screenshots, wide and phone).
- A read-only smoke run on the router: the binary bound to 127.0.0.1, with no config changes. It reads real data and checks CPU and memory usage against the budget.
- Report any gaps against the spec.

## Results (2026-11-07)

- **Tests:** `make test` passes (Go seam tests with `-race`, plus the deploy script tests). `go vet` is clean.
- **Image:** `make image` → `dist/dash.tar` (about 5 MB, busybox + static binary). Built without a daemon, because the local docker socket isn't accessible to this user.
- **Router, read-only checks** (binary bound to 127.0.0.1, temporary data dir, no config changes, cleaned up afterwards):
  - The adapters read real conntrack, neighbour and address tables.
  - The commit hook turned `show configuration json` into an inventory with no secrets.
  - Privilege drop:
    - uid 65534 and groups [102] (Kea leases).
    - CapEff, CapPrm and CapBnd are 0x1000 (CAP_NET_ADMIN only) on all threads; no ambient caps; NoNewPrivs 1.
    - `/proc/1/root` is unreachable, and conntrack dumps still work.
  - **Footprint:** about 0.5% CPU steady-state at nice 19; 24 MB RSS without the ASN database, 63 MB with it.
  - **UI** (through an SSH tunnel), all rendering real data:
    - live WAN chart, autoscaled to kb/s
    - interfaces and Router Resources
    - containers with their memory limits
    - disks: system 5.2 GB (p3) and the data dir
    - History at 1 h / 10 s
    - Top Talkers empty state
    - no console errors
- **Expected gap:** Top Talkers stays empty (all Unattributed) until the owner applies `nf_conntrack_acct=1`. Ranking, drill-down and endpoint labels are covered by the seam tests and the mock-API screenshots, but not yet by real traffic.
- **Not verified:**
  - the phone layout on real data (the browser window wouldn't go below desktop width; it was verified at 390 px against the mock API)
  - `podman load` and running inside the real container
  - firewall rule numbering
