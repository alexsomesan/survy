# 04 History store, rollups, retention, /api/history
Status: closed
Blocked by: 02

Spec user stories 15, 50–53, 55–56. Implementation Decisions › History store and rollups. API › History.

- SQLite (modernc.org/sqlite) in the data dir.
- Every 10 s store interface byte deltas and Router Resource gauges. Roll up to 1 min and 1 h (sum counters, average gauges). Retention: 48 h, 30 d and 2 y.
- 8 GB guard cap (configurable, so tests can use a tiny one) that trims the oldest rows first.
- /api/history?range=1h|24h|7d|30d|1y returns the series (WAN down/up rates, CPU, memory, tracked connections) and the Resolution it was served from.
- Today's WAN totals go into the Live event.
- Restart survival: the same data dir reopens and keeps its History.
