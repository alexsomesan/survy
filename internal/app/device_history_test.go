package app_test

import (
	"net/http"
	"testing"
	"time"

	"survy/internal/testkit"
)

type windowTalkers struct {
	Window       string     `json:"window"`
	Unit         string     `json:"unit"`
	From         int64      `json:"from"`
	To           int64      `json:"to"`
	WanRx        float64    `json:"wanRx"`
	WanTx        float64    `json:"wanTx"`
	Devices      []ttDevice `json:"devices"`
	Unattributed struct {
		Rx float64 `json:"rx"`
		Tx float64 `json:"tx"`
	} `json:"unattributed"`
}

func getTalkers(h *testkit.Harness, window string) windowTalkers {
	h.T().Helper()
	var r windowTalkers
	h.GetJSON("/api/top-talkers?window="+window, &r)
	return r
}

func (w windowTalkers) ids() []string {
	var out []string
	for _, d := range w.Devices {
		out = append(out, d.ID)
	}
	return out
}

func (w windowTalkers) device(t *testing.T, id string) ttDevice {
	t.Helper()
	for _, d := range w.Devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("%s: device %q not in %v", w.Window, id, w.ids())
	return ttDevice{}
}

func TestTopTalkersOneHourSumsBytesPerDeviceSortedByTotal(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	k.SetFlow(snat(1, copper4, google, 1_000, 100_000))
	k.SetFlow(snat(2, oma4, google, 50_000, 400_000))
	tk.second()
	k.SetFlow(snat(1, copper4, google, 2_000, 900_000))
	tk.h.Advance(3 * time.Minute)

	r := getTalkers(tk.h, "1h")

	if r.Window != "1h" || r.Unit != "bytes" {
		t.Fatalf("window/unit = %q/%q", r.Window, r.Unit)
	}
	now := tk.h.Clock.Now().UnixMilli()
	// Exactly the last hour.
	if r.To != now || r.From != now-3_600_000 {
		t.Fatalf("from/to = %d/%d, now %d", r.From, r.To, now)
	}
	want := []string{"mac:" + copperMAC, "mac:" + guestMAC}
	if ids := r.ids(); len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	if d := r.device(t, "mac:"+copperMAC); d.Rx != 900_000 || d.Tx != 2_000 || d.Name != "copper" {
		t.Fatalf("copper = %+v", d)
	}
	if d := r.device(t, "mac:"+guestMAC); d.Rx != 400_000 || d.Tx != 50_000 {
		t.Fatalf("guest = %+v", d)
	}
}

func TestTopTalkersUnknownWindowIs400(t *testing.T) {
	h := testkit.New(t)
	for _, q := range []string{"/api/top-talkers?window=live", "/api/top-talkers?window=2h", "/api/top-talkers"} {
		if code, b := h.Get(q); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d %s, want 400", q, code, b)
		}
	}
}

func TestTopTalkersWindowsCountOnlyTrafficInsideThem(t *testing.T) {
	tk := newTalkers(t)
	h, k := tk.h, tk.h.Kernel
	k.SetFlow(snat(1, copper4, google, 10_000, 1_000_000)) // at the start
	tk.second()
	h.FastForward(2*time.Hour, time.Minute)
	k.SetFlow(snat(2, oma4, google, 5_000, 300_000)) // two hours later
	h.FastForward(time.Minute, time.Second)

	only := func(window string, want ...string) {
		t.Helper()
		r := getTalkers(h, window)
		if r.Window != window || r.Unit != "bytes" {
			t.Fatalf("%s: window/unit = %q/%q", window, r.Window, r.Unit)
		}
		ids := r.ids()
		if len(ids) != len(want) {
			t.Fatalf("%s: devices = %v, want %v", window, ids, want)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Fatalf("%s: devices = %v, want %v", window, ids, want)
			}
		}
		for _, d := range r.Devices {
			if d.Rx+d.Tx == 0 {
				t.Fatalf("%s: zero row %+v", window, d)
			}
		}
	}
	lr, guest := "mac:"+copperMAC, "mac:"+guestMAC
	only("1h", guest)
	only("24h", lr, guest)
	if d := getTalkers(h, "24h").device(t, lr); d.Rx != 1_000_000 || d.Tx != 10_000 {
		t.Fatalf("24h copper = %+v", d)
	}

	h.FastForward(3*24*time.Hour, time.Hour)
	only("24h")
	only("7d", lr, guest)
	if d := getTalkers(h, "7d").device(t, guest); d.Rx != 300_000 || d.Tx != 5_000 {
		t.Fatalf("7d guest = %+v", d)
	}

	h.FastForward(7*24*time.Hour, time.Hour)
	only("7d")
	only("30d", lr, guest)
	if d := getTalkers(h, "30d").device(t, lr); d.Rx != 1_000_000 || d.Tx != 10_000 {
		t.Fatalf("30d copper = %+v", d)
	}

	h.FastForward(21*24*time.Hour, time.Hour)
	only("30d")
}

func TestTopTalkersWindowUnattributedSumsWhatEachPollCouldNotAttribute(t *testing.T) {
	h := testkit.New(t)
	h.Kernel.ClearFlows()
	writeWAN(h, 1_000_000_000, 500_000_000)
	tk := startTalkers(t, h) // baseline poll
	// WAN 1 MB / 100 kB; Devices 600 kB / 120 kB: Unattributed 400 kB / 0.
	writeWAN(h, 1_001_000_000, 500_100_000)
	h.Kernel.SetFlow(snat(1, copper4, google, 120_000, 600_000))
	tk.second()
	// Later, 500 kB / 50 kB that no Device accounts for.
	h.FastForward(20*time.Minute, time.Minute)
	writeWAN(h, 1_001_500_000, 500_150_000)
	h.FastForward(10*time.Minute, time.Minute)

	for _, w := range []string{"1h", "24h", "7d", "30d"} {
		r := getTalkers(h, w)
		if r.WanRx != 1_500_000 || r.WanTx != 150_000 {
			t.Errorf("%s: wan = %v/%v, want 1500000/150000", w, r.WanRx, r.WanTx)
		}
		if r.Unattributed.Rx != 900_000 || r.Unattributed.Tx != 50_000 {
			t.Errorf("%s: unattributed = %+v, want 900000/50000", w, r.Unattributed)
		}
	}
}
