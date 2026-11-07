// The Live stream: EventSource('/api/live') with reconnect and staleness.
import type { LiveEvent } from "./api.ts";

export type StreamState = "connecting" | "live" | "stale";

export interface StreamHandlers {
  onEvent: (e: LiveEvent) => void;
  onState: (s: StreamState, lastAt: number | null) => void;
}

const STALE_AFTER_MS = 3500;

export function openLive(url: string, h: StreamHandlers): void {
  let es: EventSource | null = null;
  let lastAt: number | null = null;
  let state: StreamState | null = null;
  let retryMs = 1000;
  let retryTimer: number | undefined;
  let reopenedAt = 0;

  const setState = (s: StreamState) => {
    if (s !== state) {
      state = s;
      h.onState(s, lastAt);
    }
  };

  const connect = () => {
    clearTimeout(retryTimer);
    es?.close();
    es = new EventSource(url);
    es.addEventListener("live", (m) => {
      let ev: LiveEvent;
      try {
        ev = JSON.parse((m as MessageEvent<string>).data) as LiveEvent;
      } catch {
        return;
      }
      lastAt = Date.now();
      retryMs = 1000;
      setState("live");
      h.onEvent(ev);
    });
    es.onerror = () => {
      if (lastAt === null) setState("connecting");
      // The browser retries by itself while readyState is CONNECTING; when it
      // gives up (CLOSED, e.g. after a non-200 reply) we retry with backoff.
      if (es?.readyState === EventSource.CLOSED) {
        retryTimer = window.setTimeout(connect, retryMs);
        retryMs = Math.min(retryMs * 2, 5000);
      }
    };
  };

  // Staleness watchdog: a stream that stops delivering is stale even if the
  // socket still looks open, and gets reopened.
  window.setInterval(() => {
    if (lastAt === null) return;
    const age = Date.now() - lastAt;
    if (age > STALE_AFTER_MS) {
      setState("stale");
      const sinceReopen = Date.now() - reopenedAt;
      if (age > STALE_AFTER_MS * 3 && sinceReopen > 10000 && es?.readyState !== EventSource.CONNECTING) {
        reopenedAt = Date.now();
        connect();
      }
    }
  }, 1000);

  setState("connecting");
  connect();
}
