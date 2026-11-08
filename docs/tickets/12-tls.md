# 12: HTTPS listener

Status: closed
Blocked by: none
Spec: [0002](../spec/0002-tls.md)

## Scope

- `internal/tlscert`: a `Reloader` whose `GetCertificate` serves a certificate and key from two PEM files, re-read when their size or mtime changes. A mismatched or half-written pair keeps the last good certificate. Missing files at startup are not fatal.
- `cmd/survy`: `DASH_TLS_LISTEN`, `DASH_TLS_CERT`, `DASH_TLS_KEY` (defaults under `DASH_DATA/tls/`). The listeners are bound as root with the :80 ones, served with `ServeTLS`, shut down with the others. A startup log line reports the certificate's subject, names and expiry, or why it cannot be served.
- `deploy/vyos.commands`: optional firewall rules 903–905 for TCP 443 and the `DASH_TLS_LISTEN` variable.
- `docs/operations.md`: an "HTTPS" section (files, ownership, reload behaviour, names, verify); `docs/architecture.md`: lifecycle and module map.

## Acceptance criteria

- [x] A handshake serves the certificate on disk.
- [x] Replacing both files changes the certificate served by the next handshake, with no restart.
- [x] A new certificate with the old key keeps the old certificate in service until the pair is consistent.
- [x] With no files present, handshakes fail, then succeed once the files appear.
- [x] With `DASH_TLS_LISTEN` unset, behaviour is unchanged (no 443 listener, no TLS log lines).
- [x] `/api/live` works over HTTPS (HTTP/2).
- [x] The key is read after the privilege drop.

## Results

Verified with `make test` (tlscert tests under `-race`) and a smoke run of the real binary in a linux/arm64 container: HTTP 200 on :80, HTTPS 200 over HTTP/2 on :443, the `/api/live` stream over TLS, a live certificate swap served on the next handshake, and, with the privilege drop on, a mode 0600 key read as uid 65534.

**Unverified on the router** (no fake behind it): binding 443 on 192.168.2.4 and 10.0.0.1 under podman's capability set, the new firewall rules, and the renewal writer's file ownership. Check with the steps in operations, "HTTPS".
