# Bespoke single-container app instead of a Grafana stack

We build our own small app (collector, storage, and web UI in one container) instead of running Grafana with Prometheus or VictoriaMetrics. Two requirements don't fit a time-series database plus Grafana:
- a 1-second **Live View** that is never stored
- **History** that drops to coarser **Resolutions** as it ages (10 s for 48 h, 1 min for 30 d, 1 h for 2 y)

Grafana can only chart what the database stores. Neither open-source Prometheus nor VictoriaMetrics downsamples old data. Per-**Device** drill-downs into **Remote Endpoints** need an unbounded number of metric labels, which time-series databases handle badly.

A custom collector was needed in every option anyway, to read conntrack and to merge each Device's IPv4 and IPv6 addresses by MAC. So the Grafana stack would have added about 300 MB of RAM and extra moving parts without removing any of the hard work.

## Considered Options

- **Grafana + Prometheus/VictoriaMetrics + node_exporter**: fails on the two requirements above.
- **Hybrid** (Grafana for Router Resources, custom exporter for traffic): two UIs, same mismatches for traffic.
- **ntopng**: inspects every packet, which conflicts with the no-impact-on-routing constraint; long-term history is a paid feature.
- **Netdata**: heavy at 1-second collection and weak on per-Device Top Talkers.

## Consequences

We own the charts, the storage that rolls data into coarser Resolutions, and the maintenance. Revisit if alerting or many more data sources become requirements.
