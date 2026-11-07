# Top Talkers come from conntrack byte counters, not packet capture or flow export

Per-**Device** traffic is measured by periodically reading byte counters from the router's connection-tracking table. We don't capture packets (pcap/ntopng) or export flows (VyOS flow-accounting/uacctd, NFLOG), because those cost work on every packet. On a 2-core router routing 1 Gbps, that work competes with forwarding. Reading about 1,000 connection entries every 1–10 s costs almost nothing.

## Consequences

- The router must have `net.netfilter.nf_conntrack_acct=1`, set through `system conntrack flow-accounting` (a `system sysctl` value is overwritten by VyOS). It is off by default, and only connections opened after it is enabled have counters.
- Traffic sent through the software flowtable (FT01) is counted only because that flowtable has `counter` enabled. Removing that flag silently undercounts most LAN↔WAN traffic.
- Traffic that never enters conntrack (for example, traffic exempted with `notrack`) is invisible to Top Talkers but still appears in interface throughput.
- The figures are accurate to the sampling interval. A connection that opens and closes between two reads is counted only if we also listen for conntrack destroy events.
