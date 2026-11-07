# Router configuration reaches the dashboard through a commit hook, not the VyOS API

The dashboard needs a few facts that live only in the router configuration: WireGuard peer names, public keys and allowed IPs, the WireGuard listen port, and container names with their addresses. A VyOS commit post-hook (`deploy/commit-hook/dash-inventory`) reads the running config after every commit, copies those fields through an allow-list, and writes `inventory.json` to the data partition. The dashboard re-reads the file when it changes. Its format is in `docs/api.md`.

We rejected the VyOS HTTP API because a VyOS API key carries full admin rights: a key inside a LAN-facing container would turn any dashboard bug into control of the router. For the same reason the container never mounts or reads `config.boot`, which holds private keys and secrets.

## Considered Options

- **VyOS HTTP API at runtime**: always current, but needs an all-powerful key in the container.
- **Read `config.boot` directly**: no key, but exposes every secret to the container.
- **Manual inventory file**: no automation; peer names go stale.

## Consequences

- A new piece of router configuration the dashboard needs means: add the field to the hook's allow-list (never copy a subtree wholesale), document it in the inventory format in `docs/api.md`, and have the owner reinstall the hook.
- The inventory updates only on commit. A config changed without a commit (impossible in normal VyOS use) would not show.
- The hook runs as the committing user and re-runs itself with `sudo -n` to write the file. A hook failure never undoes a commit; the previous inventory stays.
- Container identity on the dashboard side comes from cgroup names (`vyos-container-<name>.service`) plus this inventory, so the dashboard never needs the podman socket or podman's state files.
