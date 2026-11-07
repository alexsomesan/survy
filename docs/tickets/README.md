# Tickets for spec 0001

There's no issue tracker, so tickets live here. A ticket is **closed** when its `Status:` line reads `closed` and its work is merged into `integration/spec-0001`.

| # | Ticket | Blocked by | Status |
|---|--------|-----------|--------|
| 01 | [Scaffold, ports and test harness](01-scaffold.md) | none | closed |
| 02 | [Live interfaces and Router Resources](02-live-resources.md) | 01 | closed |
| 03 | [Live Top Talkers (Devices, WAN Traffic, Unattributed)](03-live-top-talkers.md) | 01 | closed |
| 04 | [History store, rollups, retention, /api/history](04-history.md) | 02 | closed |
| 05 | [Top Talkers over windows and Device detail](05-device-history.md) | 03, 04 | closed |
| 06 | [Remote Endpoint labelling (rDNS + organisation)](06-endpoint-labels.md) | 05 | closed |
| 07 | [Frontend](07-frontend.md) | 01 | closed |
| 08 | [Deployment and router integration](08-deploy.md) | 01 | closed |
| 09 | [Real adapters (netlink, statfs, clock, resolver)](09-real-adapters.md) | 01 | closed |
| 11 | [Review fixes](11-review-fixes.md) | 01–09 | closed |
| 10 | [End-to-end verification](10-verification.md) | 02–09, 11 | closed |

## Conventions

- Each ticket file has `Status:` and `Blocked by:` lines, then its scope and acceptance criteria.
- New work: number new tickets after the last one, under a new spec in `docs/spec/`, and add them to a table like the one above.
- To close a ticket, set `Status: closed` in its file and in this table in the ticket's final commit, and record anything left unverified (see ticket 10's Results).
