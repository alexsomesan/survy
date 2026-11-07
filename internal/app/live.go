package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// LiveEvent is one Live View update, sent once per second on /api/live.
// Tickets add their sections as fields; keep them omitempty-free so the
// frontend can rely on every key being present.
type LiveEvent struct {
	Time int64 `json:"time"` // unix milliseconds

	LineRateBps float64         `json:"lineRateBps"`
	Interfaces  []IfaceRate     `json:"interfaces"`
	WAN         WANInfo         `json:"wan"`
	WANSeries   WANSeries       `json:"wanSeries"`
	Resources   RouterResources `json:"resources"`
	Containers  []ContainerStat `json:"containers"`
	Self        SelfUsage       `json:"self"`
	TopTalkers  TopTalkers      `json:"topTalkers"`
}

// buildLive assembles the Live event from every collector's current state.
func (a *App) buildLive(now time.Time) LiveEvent {
	ev := LiveEvent{Time: now.UnixMilli()}
	a.res.fillLive(&ev, now)
	if a.hist != nil {
		a.hist.fillLive(&ev, now)
	}
	ev.TopTalkers = a.liveTalkers.snapshot()
	return ev
}

// liveHub fans Live events out to connected SSE clients.
type liveHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newLiveHub() *liveHub { return &liveHub{clients: map[chan []byte]struct{}{}} }

// count is the number of connected Live clients. Collection cadence depends
// on it (spec: 1-second conntrack polling only while someone is watching).
func (h *liveHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *liveHub) broadcast(ev any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- b:
		default: // slow client: drop this event rather than block collection
		}
	}
}

func (h *liveHub) add() chan []byte {
	c := make(chan []byte, 8)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	return c
}

func (h *liveHub) remove(c chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c)
	}
}

func (h *liveHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		delete(h.clients, c)
		close(c)
	}
}

// handleLive streams Live events as Server-Sent Events. A new client first
// receives a snapshot built for "now" (which carries the 60-second backfill),
// then one event per second.
func (a *App) handleLive(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	c := a.live.add()
	defer a.live.remove(c)

	// Re-baseline conntrack now (unless it was polled within the second), so
	// the first per-second rates cover one second, not an idle interval.
	now := a.cfg.Clock.Now()
	a.traffic.tick(now, true)
	first, err := json.Marshal(a.buildLive(now))
	if err == nil {
		fmt.Fprintf(w, "event: live\ndata: %s\n\n", first)
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case b, ok := <-c:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: live\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}
