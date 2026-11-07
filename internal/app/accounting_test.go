package app_test

import (
	"strconv"
	"testing"
	"time"

	"survy/internal/testkit"
)

func TestCountersAreCountedAsDeltasAcrossPolls(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	k.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	tk.second()
	k.SetFlow(snat(1, copper4, google, 1_500, 350_000))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 250_000*8 || d.Tx != 500*8 {
		t.Fatalf("rx/tx = %v/%v, want 2000000/4000", d.Rx, d.Tx)
	}
	k.SetFlow(snat(1, copper4, google, 1_500, 350_000))
	if ev := tk.second(); ev.has("mac:" + copperMAC) {
		t.Fatalf("idle flow still shows traffic: %+v", ev.TopTalkers.Devices)
	}
}

func TestFlowsAlreadyOpenAtStartupCountOnlyNewBytes(t *testing.T) {
	h := testkit.New(t)
	h.Kernel.ClearFlows()
	h.Kernel.SetFlow(snat(1, copper4, google, 5_000_000, 900_000_000))
	tk := startTalkers(t, h) // baseline poll sees the flow's lifetime counters
	h.Kernel.SetFlow(snat(1, copper4, google, 5_000_000, 900_001_000))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 8_000 || d.Tx != 0 {
		t.Fatalf("rx/tx = %v/%v, want 8000/0", d.Rx, d.Tx)
	}
}

func TestDestroyedFlowsAreCountedOnceWithTheirFinalCounters(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	k.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	tk.second()
	// Closes after another 50 kB, between two polls.
	k.DestroyFlow(snat(1, copper4, google, 1_000, 150_000))
	// A short connection that opened and closed between two polls.
	k.DestroyFlow(snat(2, copper4, google, 2_000, 20_000))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 70_000*8 || d.Tx != 2_000*8 {
		t.Fatalf("rx/tx = %v/%v, want 560000/16000", d.Rx, d.Tx)
	}
	if ev := tk.second(); len(ev.TopTalkers.Devices) != 0 {
		t.Fatalf("destroyed flows counted again: %+v", ev.TopTalkers.Devices)
	}
}

func TestDestroyedFlowSeenInTheSamePollIsNotDoubleCounted(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	k.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	tk.second()
	k.SetFlow(snat(1, copper4, google, 1_000, 130_000))
	tk.second()
	// The destroy event arrives with final counters; the last poll had
	// already counted up to 130 kB.
	k.DestroyFlow(snat(1, copper4, google, 1_000, 140_000))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 10_000*8 {
		t.Fatalf("rx = %v, want 80000", d.Rx)
	}
}

func writeWAN(h *testkit.Harness, rx, tx uint64) {
	h.WriteFile("/sys/class/net/pppoe0/statistics/rx_bytes", strconv.FormatUint(rx, 10)+"\n")
	h.WriteFile("/sys/class/net/pppoe0/statistics/tx_bytes", strconv.FormatUint(tx, 10)+"\n")
}

func TestUnattributedIsWANDeltaMinusAttributedFlooredAtZero(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	writeWAN(h, 1_000_000_000, 500_000_000)
	tk.second()

	// WAN moved 1 MB down / 100 kB up; Devices account for 600 kB / 120 kB.
	writeWAN(h, 1_001_000_000, 500_100_000)
	h.Kernel.SetFlow(snat(1, copper4, google, 120_000, 600_000))
	ev := tk.second()

	tt := ev.TopTalkers
	if tt.WanRx != 8_000_000 || tt.WanTx != 800_000 {
		t.Fatalf("wan rx/tx = %v/%v", tt.WanRx, tt.WanTx)
	}
	if tt.Unattributed.Rx != 400_000*8 || tt.Unattributed.Tx != 0 {
		t.Fatalf("unattributed = %+v, want rx 3200000, tx 0", tt.Unattributed)
	}
}

func TestDevicesAreSortedByTotalTraffic(t *testing.T) {
	tk := newTalkers(t)
	tk.h.Kernel.SetFlow(snat(1, copper4, google, 10, 1_000))
	tk.h.Kernel.SetFlow(snat(2, oma4, google, 50_000, 50_000))
	tk.h.Kernel.SetFlow(snat(3, "192.168.2.107", google, 1, 1))

	ev := tk.second()

	var ids []string
	for _, d := range ev.TopTalkers.Devices {
		ids = append(ids, d.ID)
	}
	want := []string{"mac:" + guestMAC, "mac:" + copperMAC, "mac:8c:30:66:a1:5f:9a"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestConntrackIsPolledEverySecondOnlyWhileSomeoneWatches(t *testing.T) {
	h := testkit.New(t)
	calls := func(d time.Duration) int {
		before := h.Kernel.ConntrackCallCount()
		h.Advance(d)
		return h.Kernel.ConntrackCallCount() - before
	}

	h.Advance(time.Second) // first poll
	if n := calls(30 * time.Second); n != 3 {
		t.Fatalf("idle: %d polls in 30 s, want 3", n)
	}

	s := h.OpenLive()
	if n := calls(5 * time.Second); n != 5 {
		t.Fatalf("watched: %d polls in 5 s, want 5", n)
	}

	s.Close()
	h.WaitLiveClients(0)
	if n := calls(30 * time.Second); n != 3 {
		t.Fatalf("after disconnect: %d polls in 30 s, want 3", n)
	}
}

func TestIdlePollRatesCoverTheWholeInterval(t *testing.T) {
	h := testkit.New(t)
	h.Kernel.ClearFlows()
	h.Advance(time.Second) // baseline
	h.Kernel.SetFlow(snat(1, copper4, google, 0, 1_000_000))
	h.Advance(10 * time.Second) // one idle poll

	s := h.OpenLive()
	var ev liveTopTalkers
	s.Next(&ev)

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 800_000 {
		t.Fatalf("rx = %v, want 800000 (1 MB over 10 s)", d.Rx)
	}
}

func TestFirstLiveRatesAfterConnectingCoverOneSecond(t *testing.T) {
	h := testkit.New(t)
	h.Kernel.ClearFlows()
	h.Advance(time.Second) // baseline
	h.Kernel.SetFlow(snat(1, copper4, google, 0, 1_000_000))
	h.Advance(10 * time.Second) // one idle poll
	h.Advance(5 * time.Second)  // nothing new, not yet polled

	s := h.OpenLive()
	var ev liveTopTalkers
	s.Next(&ev)
	h.Kernel.SetFlow(snat(1, copper4, google, 0, 1_250_000))
	h.Advance(time.Second)
	s.Next(&ev)

	if d := ev.device(t, "mac:"+copperMAC); d.Rx != 2_000_000 {
		t.Fatalf("rx = %v, want 2000000 (250 kB in the second after connecting)", d.Rx)
	}
}
