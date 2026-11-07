# 03 Live Top Talkers
Status: closed
Blocked by: 01

Spec user stories 27, 29–43, 66. Implementation Decisions › Device identification, Device naming, WAN Traffic accounting, Sampling cadence. ADR 0002.

- Poll conntrack every 1 s while ≥1 Live client is connected, else every 10 s. Drain destroyed flows each poll.
- Per-flow byte deltas, the WAN-only filter, attribution to Devices and Remote Endpoints, and Unattributed (WAN iface delta minus attributed, floored at 0).
- Device identification: neighbours (with an address→MAC cache), DHCP leases (Kea CSV, last valid row per address wins), and the inventory file (WireGuard peers with allowed IPs and listen port; containers with addresses). Also the router WAN address and the "WireGuard tunnels" pseudo-Device.
- Naming: aliases file, then DHCP hostname, then peer or container name, then OUI vendor plus the last two MAC octets. Embed a trimmed OUI table.
- Stable Device ids: `mac:<mac>`, `wg:<pubkey>`, `ctr:<name>`, `router`, `wg-tunnels`.
- The Live SSE event gains topTalkers (rates per Device, Unattributed, WAN total).
- Expose an internal accounting feed (per-second or per-poll deltas per Device and per Device×Remote Endpoint) for ticket 05 to persist.

Follow-ups: the embedded OUI table (internal/oui/oui.tsv.gz) is generated from the IEEE MA-L registry CSV by internal/oui/gen/main.go; confirm its redistribution terms and add the attribution to the footer (ticket 07). The accounting feed is `a.traffic.subscribe(func(TrafficDelta))` in internal/app/traffic.go.
