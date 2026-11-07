# 09 Real adapters
Status: closed
Blocked by: 01

- Kernel implemented with netlink: conntrack dump plus a destroy-event listener buffering into the drain (github.com/ti-mo/conntrack), neighbours and addresses (github.com/vishvananda/netlink), and statfs.
- Real Clock (Every on a ticker goroutine) and a Resolver that uses the router's DNS forwarder.
- cmd wiring: flags/env for listen addrs, root, data dir, inventory/alias/ASN paths, and the CPU-cheap defaults.
- Not covered by the seam tests: verified by a smoke run (ticket 10).
