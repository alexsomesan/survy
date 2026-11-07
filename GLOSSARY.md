# survy: VyOS router dashboard

A dashboard that runs on the router itself and shows the router's traffic and its own resource usage to anyone on the trusted LAN.

## Language

### Time

**Live View**:
A rolling view of the router's state as it is right now, sampled every second and never stored.
_Avoid_: Real-time data, current stats

**History**:
Everything stored for later viewing, kept at progressively coarser **Resolutions** as it ages.
_Avoid_: Historical data, archive, logs

**Resolution**:
One level of **History**, defined by how often it is sampled and how long it is kept (for example, one sample per minute, kept for 30 days).
_Avoid_: Tier, granularity, bucket, rollup level

### Subjects

**Router Resources**:
The router's own hardware usage: CPU, memory, disk, and similar measures.
_Avoid_: System stats, host metrics, hardware data

**Device**:
One machine on the Trusted LAN. A LAN Device is identified by its MAC address and named by its DHCP hostname, and all its IPv4 and IPv6 addresses count as the same Device. A WireGuard peer is a Device identified and named by its peer name. The router itself and each container it runs are also Devices.
_Avoid_: Host, client, IP, node

**Remote Endpoint**:
An address on the internet, reached through the WAN, that a Device exchanges traffic with.
_Avoid_: Destination, peer, server

**WAN Traffic**:
Bytes a Device exchanges with Remote Endpoints. It is the only traffic attributed to Devices; traffic between two Devices is never counted.
_Avoid_: Internet traffic, external traffic, usage

**WireGuard tunnels**:
A pseudo-Device that holds the encrypted WireGuard transport between the router and its peers' internet addresses, so tunnel overhead isn't counted as Unattributed. The traffic inside the tunnels belongs to the peers' own Devices.
_Avoid_: VPN traffic

**Unattributed**:
WAN Traffic that can't be assigned to any Device: the WAN total minus everything attributed, never below zero. It is shown as its own row next to the Top Talkers.
_Avoid_: Unknown, other

**Top Talker**:
A Device ranked by its total WAN Traffic (sent plus received) during a chosen window. Top Talkers are always Devices, never Remote Endpoints or connections.
_Avoid_: Heavy hitter, bandwidth hog, top host

### Access

**Trusted LAN**:
The home LAN plus the WireGuard road-warrior peers: the only places the dashboard can be opened from. Being on it is the only access control; there is no login.
_Avoid_: Internal network, local network
