package app_test

import (
	"net/http"
	"testing"
	"time"

	"survy/internal/app"
	"survy/internal/testkit"
)

type historyResp struct {
	Range      string
	Resolution string
	Start      int64
	StepMs     int64
	WanRxBps   []*float64
	WanTxBps   []*float64
	CPUPct     []*float64
	MemUsedPct []*float64
	Conntrack  []*float64
}

func getHistory(h *testkit.Harness, rng string) historyResp {
	h.T().Helper()
	var r historyResp
	h.GetJSON("/api/history?range="+rng, &r)
	n := len(r.WanRxBps)
	if len(r.WanTxBps) != n || len(r.CPUPct) != n || len(r.MemUsedPct) != n || len(r.Conntrack) != n {
		h.T().Fatalf("history %s: series lengths differ", rng)
	}
	return r
}

// at returns the index of the sample covering t, failing if out of range.
func (r historyResp) at(t *testing.T, at time.Time) int {
	t.Helper()
	i := int((at.UnixMilli() - r.Start) / r.StepMs)
	if at.UnixMilli() < r.Start || i >= len(r.WanRxBps) {
		t.Fatalf("%s: %v outside series [%d, +%d×%d)", r.Range, at, r.Start, len(r.WanRxBps), r.StepMs)
	}
	return i
}

func TestHistoryOneHourServesTenSecondRatesFromStoredBytes(t *testing.T) {
	h := testkit.New(t)
	// 10 s of traffic: 1.25 MB down and 250 kB up each second -> 10 / 2 Mbit/s.
	for i := 0; i < 10; i++ {
		addCounter(h, "/sys/class/net/pppoe0/statistics/rx_bytes", 1_250_000)
		addCounter(h, "/sys/class/net/pppoe0/statistics/tx_bytes", 250_000)
		h.Advance(time.Second)
	}
	h.Advance(10 * time.Second) // a quiet slot

	r := getHistory(h, "1h")
	if r.Range != "1h" || r.Resolution != "10s" || r.StepMs != 10_000 || len(r.WanRxBps) != 360 {
		t.Fatalf("1h: range %q resolution %q step %d len %d", r.Range, r.Resolution, r.StepMs, len(r.WanRxBps))
	}
	now := testkit.Start.Add(20 * time.Second)
	if want := now.Add(-359 * 10 * time.Second).UnixMilli(); r.Start != want {
		t.Errorf("start = %d, want %d", r.Start, want)
	}
	i := r.at(t, testkit.Start)
	if v := r.WanRxBps[i]; v == nil || *v != 10e6 {
		t.Errorf("rx at first slot = %v, want 10e6", deref(v))
	}
	if v := r.WanTxBps[i]; v == nil || *v != 2e6 {
		t.Errorf("tx at first slot = %v, want 2e6", deref(v))
	}
	if v := r.WanRxBps[i+1]; v == nil || *v != 0 {
		t.Errorf("rx at quiet slot = %v, want 0", deref(v))
	}
	if v := r.WanRxBps[i-1]; v != nil {
		t.Errorf("rx before the app started = %v, want null", *v)
	}
	// conntrack fixture is 240; memory used% from meminfo (8066224 total, 5352732 avail).
	if v := r.Conntrack[i]; v == nil || *v != 240 {
		t.Errorf("conntrack = %v, want 240", deref(v))
	}
	wantMem := 100 * float64(8066224-5352732) / 8066224
	if v := r.MemUsedPct[i]; v == nil || !near(*v, wantMem) {
		t.Errorf("memUsedPct = %v, want %v", deref(v), wantMem)
	}
}

func TestHistoryUnknownRangeIs400(t *testing.T) {
	h := testkit.New(t)
	for _, q := range []string{"/api/history?range=2h", "/api/history"} {
		if code, b := h.Get(q); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d %s, want 400", q, code, b)
		}
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-6 && d > -1e-6
}

// advanceTo moves the clock to t in steps of at most step (one of
// 10s, 1m, 10m, 1h), using finer steps only to reach a multiple of step and
// to land on t. Keeping the coarse steps aligned keeps each sample inside one
// 10 s slot.
func advanceTo(h *testkit.Harness, t time.Time, step time.Duration) {
	h.T().Helper()
	now := h.Clock.Now()
	if t.Before(now) {
		h.T().Fatalf("advanceTo %v: already at %v", t, now)
	}
	finer := map[time.Duration]time.Duration{
		10 * time.Second: time.Second, time.Minute: 10 * time.Second,
		10 * time.Minute: time.Minute, time.Hour: 10 * time.Minute,
	}[step]
	if finer == 0 {
		h.T().Fatalf("advanceTo: unsupported step %v", step)
	}
	fine := func(to time.Time) {
		if finer == time.Second {
			h.Advance(to.Sub(h.Clock.Now()))
		} else {
			advanceTo(h, to, finer)
		}
	}
	ms := step.Milliseconds()
	from := time.UnixMilli((now.UnixMilli() + ms - 1) / ms * ms)
	to := time.UnixMilli(t.UnixMilli() / ms * ms)
	if to.Before(from) {
		fine(t)
		return
	}
	fine(from)
	h.FastForward(to.Sub(from), step)
	fine(t)
}

// boundaryTraffic puts x bytes in the 10 s slot just before b and y in the
// one starting at b, fast-forwarding there in steps of step.
func boundaryTraffic(h *testkit.Harness, b time.Time, x, y int64, step time.Duration) {
	h.T().Helper()
	advanceTo(h, b.Add(-10*time.Second), step)
	wanTraffic(h, x, 0)
	h.Advance(10 * time.Second)
	wanTraffic(h, y, 0)
	h.Advance(10 * time.Second)
}

func TestHistoryRollsUpTenSecondSlotsIntoTheirMinute(t *testing.T) {
	h := testkit.New(t)
	// A 7 min sample boundary (minute-aligned) well after Start, so the
	// samples either side were fully covered by the running app.
	const step = 7 * time.Minute
	b := time.UnixMilli((testkit.Start.Add(2*step).UnixMilli() / step.Milliseconds()) * step.Milliseconds())
	boundaryTraffic(h, b, 4_200_000, 840_000, time.Minute)
	advanceTo(h, b.Add(8*time.Minute), time.Minute) // the minute after b is rolled up

	r := getHistory(h, "7d")
	before, after := r.at(t, b.Add(-time.Second)), r.at(t, b)
	if after != before+1 {
		t.Fatalf("b is not a sample boundary: %d, %d", before, after)
	}
	// 4.2 MB over the 420 s sample = 80 kbit/s; 840 kB = 16 kbit/s.
	if v := r.WanRxBps[before]; v == nil || !near(*v, 80_000) {
		t.Errorf("rx before b = %v, want 80000", deref(v))
	}
	if v := r.WanRxBps[after]; v == nil || !near(*v, 16_000) {
		t.Errorf("rx from b = %v, want 16000", deref(v))
	}
}

func TestHistoryRollsUpMinutesIntoTheirHour(t *testing.T) {
	h := testkit.New(t)
	// 12:00 UTC is a 6 h sample boundary of the 1y range; the app runs
	// through all of 06:00-12:00 and 12:00-18:00.
	b := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	boundaryTraffic(h, b, 21_600_000, 2_160_000, time.Hour)
	advanceTo(h, b.Add(6*time.Hour+time.Minute), time.Hour)

	r := getHistory(h, "1y")
	before, after := r.at(t, b.Add(-time.Second)), r.at(t, b)
	// 21.6 MB over 6 h = 8 kbit/s; 2.16 MB = 800 bit/s.
	if v := r.WanRxBps[before]; v == nil || !near(*v, 8_000) {
		t.Errorf("rx 06-12 = %v, want 8000", deref(v))
	}
	if v := r.WanRxBps[after]; v == nil || !near(*v, 800) {
		t.Errorf("rx 12-18 = %v, want 800", deref(v))
	}
}

func TestHistoryIsNullWhileTheAppWasDown(t *testing.T) {
	h := testkit.New(t)
	h.Advance(time.Minute)
	h.RestartAfter(10 * time.Minute)
	h.Advance(time.Minute)

	r := getHistory(h, "1h")
	down := testkit.Start.Add(time.Minute)
	for at := down; at.Before(down.Add(10 * time.Minute)); at = at.Add(10 * time.Second) {
		i := r.at(t, at)
		if r.WanRxBps[i] != nil || r.CPUPct[i] != nil || r.Conntrack[i] != nil {
			t.Fatalf("sample at %v = %v, want null while down", at, deref(r.WanRxBps[i]))
		}
	}
	for _, at := range []time.Time{down.Add(-10 * time.Second), down.Add(10 * time.Minute)} {
		if v := r.WanRxBps[r.at(t, at)]; v == nil || *v != 0 {
			t.Errorf("sample at %v = %v, want 0 while up", at, deref(v))
		}
	}
}

func TestHistorySurvivesRestartIncludingAPartSlot(t *testing.T) {
	h := testkit.New(t)
	wanTraffic(h, 1_000_000, 0)
	h.Advance(10 * time.Second) // slot 01:00:00 complete
	wanTraffic(h, 250_000, 0)
	h.Advance(5 * time.Second) // half of slot 01:00:10
	h.Restart()
	wanTraffic(h, 250_000, 0)
	h.Advance(5 * time.Second)

	r := getHistory(h, "1h")
	if v := r.WanRxBps[r.at(t, testkit.Start)]; v == nil || *v != 800_000 {
		t.Errorf("rx before restart = %v, want 800000", deref(v))
	}
	if v := r.WanRxBps[r.at(t, testkit.Start.Add(10*time.Second))]; v == nil || *v != 400_000 {
		t.Errorf("rx in the slot spanning the restart = %v, want 400000", deref(v))
	}
}

type liveToday struct {
	Wan  struct{ TodayRxBytes, TodayTxBytes int64 }
	Self struct{ DataBytes int64 }
}

func live(h *testkit.Harness) liveToday {
	var ev liveToday
	s := h.OpenLive()
	s.Next(&ev)
	s.Close()
	return ev
}

func TestLiveTodayWANTotalsResetAtLocalMidnight(t *testing.T) {
	h := testkit.New(t)
	wanTraffic(h, 1000, 300)
	h.Advance(3 * time.Second)
	wanTraffic(h, 500, 200)
	h.Advance(20 * time.Second)
	if ev := live(h); ev.Wan.TodayRxBytes != 1500 || ev.Wan.TodayTxBytes != 500 {
		t.Errorf("today = %d/%d, want 1500/500", ev.Wan.TodayRxBytes, ev.Wan.TodayTxBytes)
	}

	h.Restart()
	wanTraffic(h, 5, 0)
	h.Advance(time.Second)
	if ev := live(h); ev.Wan.TodayRxBytes != 1505 || ev.Wan.TodayTxBytes != 500 {
		t.Errorf("after restart today = %d/%d, want 1505/500", ev.Wan.TodayRxBytes, ev.Wan.TodayTxBytes)
	}

	// Tests run in UTC. The day's last second counts for that day, and at
	// midnight the totals start again from zero.
	midnight := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	advanceTo(h, midnight.Add(-2*time.Second), time.Hour)
	wanTraffic(h, 7, 0)
	h.Advance(time.Second)
	if ev := live(h); ev.Wan.TodayRxBytes != 1512 {
		t.Errorf("before midnight today rx = %d, want 1512", ev.Wan.TodayRxBytes)
	}
	wanTraffic(h, 3, 0) // arrives during 23:59:59
	h.Advance(time.Second)
	if ev := live(h); ev.Wan.TodayRxBytes != 0 || ev.Wan.TodayTxBytes != 0 {
		t.Errorf("at midnight today = %d/%d, want 0/0", ev.Wan.TodayRxBytes, ev.Wan.TodayTxBytes)
	}
	wanTraffic(h, 42, 9)
	h.Advance(time.Second)
	if ev := live(h); ev.Wan.TodayRxBytes != 42 || ev.Wan.TodayTxBytes != 9 {
		t.Errorf("new day today = %d/%d, want 42/9", ev.Wan.TodayRxBytes, ev.Wan.TodayTxBytes)
	}
}

// wanTraffic adds rx/tx bytes to the WAN counters (seen at the next tick).
func wanTraffic(h *testkit.Harness, rx, tx int64) {
	addCounter(h, "/sys/class/net/pppoe0/statistics/rx_bytes", rx)
	addCounter(h, "/sys/class/net/pppoe0/statistics/tx_bytes", tx)
}

func TestHistoryEachRangeServesItsResolution(t *testing.T) {
	h := testkit.New(t)
	wanTraffic(h, 12_000_000, 0)
	h.Advance(10 * time.Second)
	// Past the hour so the 1 h slot is rolled up; 1 min steps so every
	// downsampled sample boundary (e.g. 29 min) falls between samples.
	advanceTo(h, testkit.Start.Add(2*time.Hour+10*time.Second), time.Minute)
	now := h.Clock.Now()

	cases := []struct {
		rng, res string
		stepMs   int64
		n        int
	}{
		{"1h", "10s", 10_000, 360},
		{"24h", "10s", 60_000, 1440},      // 8640 slots averaged 6:1
		{"7d", "1m", 7 * 60_000, 1440},    // 10080 slots averaged 7:1
		{"30d", "1m", 29 * 60_000, 1490},  // 43200 slots averaged 29:1
		{"1y", "1h", 6 * 3_600_000, 1460}, // 8760 slots averaged 6:1
	}
	for _, c := range cases {
		r := getHistory(h, c.rng)
		if r.Range != c.rng || r.Resolution != c.res || r.StepMs != c.stepMs || len(r.WanRxBps) != c.n {
			t.Errorf("%s: resolution %q step %d len %d, want %s %d %d", c.rng, r.Resolution, r.StepMs, len(r.WanRxBps), c.res, c.stepMs, c.n)
			continue
		}
		// The last sample is the one holding now; samples are step-aligned.
		if last := r.Start + int64(c.n-1)*c.stepMs; last > now.UnixMilli() || now.UnixMilli()-last >= c.stepMs || last%c.stepMs != 0 {
			t.Errorf("%s: last sample at %d does not hold now %d", c.rng, last, now.UnixMilli())
		}
		if c.rng == "1h" {
			continue // the traffic is older than an hour
		}
		// 12 MB arrived in the app's first 10 s. The app ran from Start on, so
		// the sample's rate is the bytes over the seconds from Start to the
		// sample's end, or to the end of the last complete slot of the
		// Resolution (now is 03:00:10; 1 h slots are complete up to 03:00).
		i := r.at(t, testkit.Start)
		end := time.UnixMilli(r.Start + int64(i+1)*r.StepMs)
		if c.res == "1h" {
			end = testkit.Start.Add(2 * time.Hour)
		}
		want := 12_000_000 * 8 / end.Sub(testkit.Start).Seconds()
		if v := r.WanRxBps[i]; v == nil || !near(*v, want) {
			t.Errorf("%s: rx at start = %v, want %v", c.rng, deref(v), want)
		}
		if v := r.Conntrack[i]; v == nil || *v != 240 {
			t.Errorf("%s: conntrack at start = %v, want 240", c.rng, deref(v))
		}
		if i > 0 && r.WanRxBps[i-1] != nil {
			t.Errorf("%s: sample before the app started = %v, want null", c.rng, *r.WanRxBps[i-1])
		}
	}
}

// emptyDataBytes is the size of a History database holding no rows.
func emptyDataBytes(t *testing.T) int64 {
	return live(testkit.New(t)).Self.DataBytes
}

func TestHistoryExpiresEachResolutionAfterItsRetention(t *testing.T) {
	empty := emptyDataBytes(t)
	if empty <= 0 {
		t.Fatalf("empty dataBytes = %d, want > 0", empty)
	}
	h := testkit.New(t)
	wanTraffic(h, 3_600_000, 0)
	// Three days in hourly steps: 72 rows at each Resolution, more than fit
	// in a table's first page, so each expiry shows in the database size.
	advanceTo(h, testkit.Start.Add(72*time.Hour), time.Hour)
	h.Restart()
	full := live(h).Self.DataBytes
	if full <= empty {
		t.Fatalf("dataBytes with three days of History = %d, want > empty %d", full, empty)
	}
	hasStart := func(rng string) bool {
		r := getHistory(h, rng)
		return r.WanRxBps[r.at(t, testkit.Start.Add(time.Hour-time.Second))] != nil // the first hourly sample
	}

	// After 48 h the 10 s rows are gone but the 1 min and 1 h rows remain.
	h.RestartAfter(48*time.Hour + time.Minute)
	noTenSec := live(h).Self.DataBytes
	if noTenSec >= full {
		t.Errorf("dataBytes after 48 h = %d, want < %d", noTenSec, full)
	}
	if !hasStart("7d") {
		t.Errorf("7d lost the 1 min History before 30 days")
	}

	// After 30 d only the 1 h rows remain.
	h.RestartAfter(30 * 24 * time.Hour)
	noMinute := live(h).Self.DataBytes
	if noMinute >= noTenSec {
		t.Errorf("dataBytes after 30 d = %d, want < %d", noMinute, noTenSec)
	}
	if !hasStart("1y") {
		t.Errorf("1y lost the 1 h History before 2 years")
	}

	// After 2 years nothing is left.
	h.RestartAfter(2 * 365 * 24 * time.Hour)
	if got := live(h).Self.DataBytes; got != empty || got >= noMinute {
		t.Errorf("dataBytes after 2 y = %d, want empty %d (and < %d)", got, empty, noMinute)
	}
}

func TestGuardCapTrimsTheOldestHistoryFirst(t *testing.T) {
	const page = 4096
	limit := emptyDataBytes(t) + 6*page
	h := testkit.New(t, testkit.WithSettings(app.Settings{StorageCapBytes: limit}))
	advanceTo(h, testkit.Start.Add(3*time.Hour), time.Minute) // ~13 pages of rows
	h.Restart()

	if got := live(h).Self.DataBytes; got > limit {
		t.Errorf("dataBytes = %d, want <= cap %d", got, limit)
	}
	r := getHistory(h, "24h")
	if v := r.Conntrack[r.at(t, testkit.Start)]; v != nil {
		t.Errorf("oldest minute = %v, want trimmed", *v)
	}
	if v := r.Conntrack[r.at(t, testkit.Start.Add(3*time.Hour-time.Minute))]; v == nil {
		t.Errorf("newest minute was trimmed")
	}
}
