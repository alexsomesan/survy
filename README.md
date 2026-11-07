# survy

A lightweight traffic and health dashboard that runs on a [VyOS](https://vyos.io) router itself, in a single podman container, and is viewable from any browser on your LAN.

- **Live view:** WAN download and upload every second, per-interface rates, today's totals.
- **Router health:** CPU, memory, temperature, load, tracked connections, disks, and the containers the router runs.
- **Top talkers:** which devices on your network use the most internet traffic, live and over the last hour, day, week or month. Drill into any device to see the internet hosts it talks to, with reverse DNS and owning organisation.
- **History:** throughput and router health for up to two years, kept at coarser resolution as it ages (10 s for 48 h, 1 min for 30 days, 1 h for 2 years).

Devices are recognised by MAC address, with their IPv4 and IPv6 addresses merged. They are named from DHCP leases, WireGuard peer names or the network card's manufacturer, and you can set your own aliases.

## Designed not to get in the way of routing

survy never inspects packets. Per-device traffic comes from the byte counters the kernel's connection tracking already keeps, read every 1–10 seconds, and interface and system figures come from `/proc` and `/sys`. It runs at the lowest CPU and I/O priority, within 10% of one core and 512 MB of memory. In practice it uses well under 1% CPU and about 65 MB.

It is read-only. It holds no VyOS API key and never reads the router's configuration file: the few configuration facts it needs (WireGuard peer names, container addresses) are written for it by a commit hook. After start-up it drops root and keeps only the one capability it needs to read connection tracking.

## What you need

- VyOS with container support (built and tested on VyOS rolling 2026.09, kernel 6.18, podman 5.8)
- per-connection byte counting turned on (`set system conntrack flow-accounting`)
- a few GiB of disk for History; the guide adds a dedicated partition
- Go, Node.js and SSH access to the router to build and install it

There is no login and no HTTPS: survy is meant for a trusted home or office LAN, and the provided firewall rules only allow it from there.

## Getting started

[docs/operations.md](docs/operations.md) walks through building the image, preparing the router (data partition, scripts, configuration) and checking the result. The router configuration is in [deploy/vyos.commands](deploy/vyos.commands); adjust interface names and addresses to your network first.

## Documentation

- [docs/architecture.md](docs/architecture.md): how it works and where to change things
- [docs/api.md](docs/api.md): the JSON API the page uses
- [docs/adr/](docs/adr/README.md): the main design decisions and why
- [GLOSSARY.md](GLOSSARY.md): the terms used throughout

## Data sources

Organisation names come from the [iptoasn.com](https://iptoasn.com/) database (public domain, PDDL), downloaded monthly on the router. Network card manufacturers come from the IEEE MA-L registry. Charts use [uPlot](https://github.com/leeoniya/uPlot).

## License

MIT. See [LICENSE](LICENSE).
