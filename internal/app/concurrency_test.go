package app_test

import (
	"testing"
	"time"
)

// A client that stops reading a window response must not hold up collection
// or the Live View.
func TestSlowTopTalkersClientDoesNotDelayLiveEvents(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	h.FastForward(2*time.Minute, time.Second)

	release := h.StalledGet("/api/top-talkers?window=24h")
	h.AdvanceWithin(2*time.Minute, 2*time.Second)
	var ev liveTopTalkers
	tk.s.Latest(&ev)
	release()
}

func TestSlowDeviceDetailClientDoesNotDelayLiveEvents(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	h.FastForward(2*time.Minute, time.Second)

	for _, w := range []string{"live", "1h"} {
		release := h.StalledGet(devicePath("mac:"+copperMAC, w))
		h.AdvanceWithin(2*time.Minute, 2*time.Second)
		var ev liveTopTalkers
		tk.s.Latest(&ev)
		release()
	}
}
