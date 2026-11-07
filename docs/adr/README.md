# Architecture decision records

Read the ADR before reversing what it decides. Add one only when a decision is hard to reverse, surprising without context, and the result of a real trade-off. Smaller decisions live in the spec's Implementation Decisions or in the ticket that made them.

| # | Decision |
|---|---|
| [0001](0001-bespoke-app-over-grafana-stack.md) | One bespoke Go container, not a Grafana stack |
| [0002](0002-top-talkers-from-conntrack-counters.md) | Top Talkers from conntrack byte counters, not packet capture or flow export |
| [0003](0003-one-test-seam-at-the-http-api.md) | Tests go through the HTTP API only |
| [0004](0004-router-inventory-from-a-commit-hook.md) | Router config reaches the app through a commit hook; no API key, no `config.boot` |
| [0005](0005-in-process-privilege-drop-keeping-net-admin.md) | The binary drops root itself and keeps only CAP_NET_ADMIN |
| [0006](0006-history-in-sqlite-on-its-own-partition.md) | History in SQLite on its own partition, as sum-only families |
