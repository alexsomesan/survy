# survy

survy is a dashboard for the owner's VyOS router: one Go binary in a podman container on the router. It collects traffic and Router Resources, keeps History in SQLite, and serves a single page to the Trusted LAN. Use the [GLOSSARY.md](GLOSSARY.md) terms (Device, WAN Traffic, Top Talker, Resolution, …) in code, docs and commits.

## Guardrails

- **The owner applies every router change.** Write config changes as `set` commands in `deploy/vyos.commands`, or as scripts under `deploy/` with their runbook steps in `docs/operations.md`, then hand them over. On the router (`ssh vyos`), run only read-only checks, keep anything temporary under a temp dir, and remove it afterwards.
- **Router secrets stay on the router.** The container holds no VyOS API key and never reads `config.boot` ([ADR 0004](docs/adr/0004-router-inventory-from-a-commit-hook.md)). Treat `show configuration` output as secret: pipe it straight into its consumer and never write it to a file.
- **Routing comes first.** The dashboard lives within 10% of one core and 512 MB; keep per-packet work at zero ([ADR 0002](docs/adr/0002-top-talkers-from-conntrack-counters.md)).

## Read before changing

- [docs/architecture.md](docs/architecture.md): **always**. It covers the data flow, the module map (where each change goes), the invariants, and the router facts the code depends on.
- [docs/api.md](docs/api.md): before touching an endpoint, a Live field or the frontend.
- [docs/adr/](docs/adr/README.md): before reversing or working around a design choice.
- [docs/operations.md](docs/operations.md): before changing `deploy/`, the image, startup or privileges.
- [docs/spec/0001-router-dashboard.md](docs/spec/0001-router-dashboard.md): the original user stories, for the intent behind a feature.

## Working

- **Test-first through the one seam** ([ADR 0003](docs/adr/0003-one-test-seam-at-the-http-api.md)): a failing test in `internal/app/*_test.go` via `internal/testkit`, then the code. `make test` must pass; run `cd web && npm test` too when you change `web/`.
- **Code with no fake behind it** (`internal/adapters`, `internal/privdrop`, `cmd/survy`) is verified on the router with `cmd/survy-probe` or a smoke run. Say in your report that it is unverified until that's done.
- **New features:** write a spec in `docs/spec/` and tickets in `docs/tickets/` ([conventions](docs/tickets/README.md)). Record a hard-to-reverse, surprising trade-off as an ADR. Update `docs/architecture.md` in the same change whenever the module map, an invariant or a router fact changes.
- **Commits:** small and descriptive, on a branch, never pushed without the owner asking.
