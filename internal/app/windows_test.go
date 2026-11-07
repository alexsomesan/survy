package app_test

import (
	"testing"
	"time"

	"survy/internal/testkit"
)

// Seven- and thirty-day windows are served from one-minute data, so they
// cover exactly the span, to the minute.
func TestTopTalkersLongWindowsCoverExactlyTheSpan(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	advanceWatched(h, testkit.Start.Add(5*time.Minute+29*time.Second))
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 100_000)) // in minute 01:05
	advanceWatched(h, testkit.Start.Add(50*time.Minute+29*time.Second))
	h.Kernel.SetFlow(snat(2, oma4, google, 2_000, 200_000)) // in minute 01:50
	advanceWatched(h, testkit.Start.Add(51*time.Minute))

	for _, c := range []struct {
		window string
		span   time.Duration
	}{{"7d", 7 * 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}} {
		// Ten minutes past the hour, one span after the app started: the
		// window starts at 01:10, between the two Devices' traffic.
		end := testkit.Start.Add(c.span + 10*time.Minute)
		h.FastForward(end.Sub(h.Clock.Now()), time.Hour)

		r := getTalkers(h, c.window)
		now := h.Clock.Now().UnixMilli()
		if r.To != now || r.From != now-c.span.Milliseconds() {
			t.Fatalf("%s: from/to = %d/%d, want %d/%d", c.window, r.From, r.To, now-c.span.Milliseconds(), now)
		}
		if ids := r.ids(); len(ids) != 1 || ids[0] != "mac:"+guestMAC {
			t.Fatalf("%s: devices = %v, want only guest", c.window, ids)
		}
		if d := r.device(t, "mac:"+guestMAC); d.Rx != 200_000 || d.Tx != 2_000 {
			t.Fatalf("%s: guest = %+v", c.window, d)
		}
	}
}
