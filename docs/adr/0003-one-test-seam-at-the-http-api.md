# Tests drive the whole app through its HTTP API, and nothing else

Every Go test builds the complete app with a fixture root directory, a fake kernel, a fake clock and a fake resolver (`internal/testkit`), then asserts only on HTTP responses and the Live stream. No test calls a domain function, reads a SQLite table or inspects an internal struct. The owner chose this ("only use seam 1") when the spec was written.

A test states a scenario in **Glossary** terms ("a Device downloads 3 GB over IPv4 and two IPv6 addresses") and checks what the API returns. Internals (how **Devices** are merged, how **Resolutions** are rolled up, how often conntrack is polled) can be rewritten freely as long as the API answers stay the same.

## Considered Options

- **Unit tests per module** (Device identification, store, accounting): faster to localise failures, but they pin internal shapes and make every refactor a test rewrite.
- **Two seams** (HTTP API plus the store): the store is the most algorithmic part, but its behaviour (rollup, retention, guard cap) is fully visible through `/api/history` and the Top Talkers windows.

## Consequences

- When a test needs a new input, extend `internal/testkit` (a new fake knob or fixture file), not the app's exports.
- Code with no fake behind it is not covered by Go tests: the real adapters (`internal/adapters`), the privilege drop (`internal/privdrop`) and `cmd/dash`. They are checked on the router with `cmd/dash-probe` and the smoke run in `docs/tickets/10-verification.md`. Keep that code thin.
- Two test suites sit outside the seam and were kept as a judgement call, pending the owner's decision: the frontend helper tests (`web/test`, `npm test`) and the deploy script tests (`deploy/test`, part of `make test`). The partition script tests are the only guard against a script that touches the router's disk.
