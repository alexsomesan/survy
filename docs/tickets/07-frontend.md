# 07 Frontend
Status: closed
Blocked by: 01

Spec user stories 11–14, 18–30, 41–52, 57–62. UI section. Mockup: (a design mockup, not published) (layout, colours, responsive rules, copy).

- Plain TypeScript and uPlot, bundled with esbuild into web/dist, embedded in the Go binary with go:embed.
- Consumes the API contract in the spec (SSE /api/live, /api/top-talkers, /api/devices/{id}, /api/history). It reconnects the Live stream automatically.
- Follows prefers-color-scheme. Reflows at about 1000 px and 640 px. Touch targets of at least 44 px on phones. Hover crosshair and tooltip on every chart.
- Develop against a mock API (a small dev server or fixture JSON), so the frontend doesn't wait for the backend tickets.

Done: `cd web && npm ci && npm run build` writes web/dist (index.html, app.js, app.css). `npm test` runs the formatting/window tests (node --test), `npm run typecheck` runs tsc. `npm run dev` serves dist with a mock API on :5173 (`/__drop?sec=10` cuts the Live stream to show the stale indicator).
