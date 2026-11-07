# 08 Deployment and router integration
Status: closed
Blocked by: 01

Spec Implementation Decisions › Router integration. User stories 1–10, 36, 63–64, 67.

- Multi-stage Dockerfile (node build, go build, scratch/distroless runtime). A Makefile with test, build and image targets, plus a deploy target (docker save | ssh vyos sudo podman load).
- deploy/vyos.commands: sysctl, br0 address, static host mapping, firewall input rules, and the container definition (host network, cpu/memory limits, capabilities, volumes, restart, environment).
- deploy/partition-p5.sh: refuses unless p5 is absent and the target range is free space after p4.
- deploy/vyos-preconfig-bootup snippet that mounts by label at /mnt/dash-data.
- deploy/commit-hook: a /config/scripts/commit/post-hooks.d script that writes inventory.json (WireGuard peers and listen port, containers and addresses) without secrets.
- A script to fetch and refresh the ASN database monthly. docs/operations.md is the runbook.
- Do NOT apply anything to the router. The owner applies it.
