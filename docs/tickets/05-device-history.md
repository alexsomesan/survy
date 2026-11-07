# 05 Top Talkers over windows and Device detail
Status: closed
Blocked by: 03, 04

Spec user stories 28, 44–47, 54. API › Top Talkers, Device detail.

- Persist per-Device received/sent at 1 min (30 d) and 1 h (2 y), writing only non-zero rows. Persist per-Device×Remote Endpoint at 1 h (30 d), keeping the top 50 per Device per hour.
- Persist the Unattributed and WAN totals per slot so window queries can compute Unattributed.
- /api/top-talkers?window=1h|24h|7d|30d (live is served by the SSE stream).
- /api/devices/{id}?window=live|1h|24h|7d|30d returns identifiers, a series, and the top Remote Endpoints (IP only; labels come in 06).
- Device metadata (name, kind, identifiers) persists, so a Device that went offline still shows its name.
