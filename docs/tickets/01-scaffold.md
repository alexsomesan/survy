# 01 Scaffold, ports and test harness
Status: closed

Spec: Implementation Decisions › Shape; Testing Decisions (the single seam).

- Go module and binary entry point.
- The app is constructed from a Config holding the root dir, data dir, Kernel, Clock and Resolver. It exposes an http.Handler and a way to start and stop.
- Ports: Kernel (conntrack dump, destroyed flows drained since last call, neighbours, interface addresses, statfs), Clock (Now plus Every(d, fn)), Resolver.
- Test harness: a fake kernel, a fake clock whose Advance runs scheduled functions synchronously, a fake resolver, the fixture root tree recorded from the router, and helpers to GET JSON and read SSE events.
- Route skeleton for /api/live (SSE), /api/top-talkers, /api/devices/{id}, /api/history and / (static files).
